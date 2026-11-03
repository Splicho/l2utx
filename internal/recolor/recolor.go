// Package recolor adjusts the hue, saturation and lightness of decoded
// textures while leaving the alpha channel untouched.
//
// The default OKLCH mode rotates hue in a perceptually uniform colour space, so
// the perceived brightness of every pixel stays the same. That avoids the
// bright/dark swings a plain HSL rotation produces (dark blue becoming bright
// yellow, for example). ModeHSL reproduces the HSL rotation used by
// Photoshop's Hue/Saturation adjustment.
package recolor

import (
	"fmt"
	"math"
	"strings"

	"l2utx/internal/dxt"
)

// Mode selects the colour model used for the adjustment.
type Mode int

const (
	// ModeOKLCH rotates hue in OKLCH, keeping perceived lightness and chroma.
	ModeOKLCH Mode = iota
	// ModeHSL rotates hue in HSL, like Photoshop's Hue/Saturation.
	ModeHSL
)

// String returns the CLI name of the mode.
func (m Mode) String() string {
	if m == ModeHSL {
		return "hsl"
	}
	return "oklch"
}

// ParseMode parses a mode name.
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "oklch", "oklab", "ok":
		return ModeOKLCH, true
	case "hsl", "hsv", "photoshop", "ps":
		return ModeHSL, true
	}
	return 0, false
}

// Params describes one adjustment. Hue is in degrees; Saturation and Lightness
// are multipliers where 1 means "unchanged".
type Params struct {
	Hue        float64
	Saturation float64
	Lightness  float64
	Mode       Mode

	// HueFrom and HueTo restrict the adjustment to pixels whose original hue lies
	// in [HueFrom, HueTo) degrees. Both zero means "every pixel". The window may
	// wrap around 0, so 320 to 40 selects magenta through orange.
	HueFrom float64
	HueTo   float64
	// SatMin is the lowest HSV saturation (0..1) a pixel must have to be touched.
	// Armour shading and grey metal sit below it and stay bit-identical.
	SatMin float64
}

// HasWindow reports whether a source hue window is configured.
func (p Params) HasWindow() bool {
	return math.Abs(p.HueFrom) > 1e-9 || math.Abs(p.HueTo) > 1e-9
}

// matches reports whether a source colour falls inside the configured window. It
// is what keeps a "rotate the purple parts" job from repainting gold trim, dark
// shading or the specular maps.
func (p Params) matches(c dxt.RGBA) bool {
	if !p.HasWindow() && p.SatMin <= 0 {
		return true
	}
	h, s := hsvOf(c)
	if p.SatMin > 0 && s < p.SatMin {
		return false
	}
	if !p.HasWindow() {
		return true
	}
	from := math.Mod(p.HueFrom, 360)
	to := math.Mod(p.HueTo, 360)
	deg := h * 360
	if from <= to {
		return deg >= from && deg < to
	}
	return deg >= from || deg < to
}

// IsIdentity reports whether the adjustment would leave every pixel unchanged.
func (p Params) IsIdentity() bool {
	return math.Abs(math.Remainder(p.Hue, 360)) < 1e-9 &&
		math.Abs(p.Saturation-1) < 1e-9 &&
		math.Abs(p.Lightness-1) < 1e-9
}

// Describe returns a compact human readable description, for progress output.
func (p Params) Describe() string {
	var parts []string
	if h := math.Round(p.Hue*10) / 10; h != 0 {
		parts = append(parts, fmt.Sprintf("hue %g°", h))
	}
	if s := math.Round(p.Saturation*1000) / 10; s != 100 {
		parts = append(parts, fmt.Sprintf("saturation %g%%", s))
	}
	if l := math.Round(p.Lightness*1000) / 10; l != 100 {
		parts = append(parts, fmt.Sprintf("lightness %g%%", l))
	}
	if p.Mode == ModeHSL {
		parts = append(parts, "HSL")
	}
	if p.HasWindow() {
		parts = append(parts, fmt.Sprintf("only %g°-%g°", math.Mod(p.HueFrom, 360), math.Mod(p.HueTo, 360)))
	}
	if p.SatMin > 0 {
		parts = append(parts, fmt.Sprintf("sat ≥ %g%%", math.Round(p.SatMin*1000)/10))
	}
	if len(parts) == 0 {
		return "no change"
	}
	return strings.Join(parts, ", ")
}

// Apply adjusts img in place. The alpha channel is copied through unchanged and
// fully neutral pixels stay neutral, so grey metal and masks are not tinted.
func Apply(img *dxt.Image, p Params) {
	if img == nil || img.W <= 0 || img.H <= 0 || len(img.Pix) == 0 {
		return
	}
	p = p.sanitized()
	if p.IsIdentity() {
		return
	}
	if p.Mode == ModeHSL {
		applyHSL(img, p)
		return
	}
	applyOKLCH(img, p)
}

func (p Params) sanitized() Params {
	if math.IsNaN(p.Hue) || math.IsInf(p.Hue, 0) {
		p.Hue = 0
	}
	if math.IsNaN(p.Saturation) || math.IsInf(p.Saturation, 0) || p.Saturation < 0 {
		p.Saturation = 1
	}
	if math.IsNaN(p.Lightness) || math.IsInf(p.Lightness, 0) || p.Lightness < 0 {
		p.Lightness = 1
	}
	return p
}

// --- HSL -------------------------------------------------------------------

func applyHSL(img *dxt.Image, p Params) {
	hue := p.Hue / 360
	for i := range img.Pix {
		c := &img.Pix[i]
		if !p.matches(*c) {
			continue
		}
		h, s, l := rgbToHSL(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
		h = math.Mod(h+hue, 1)
		if h < 0 {
			h++
		}
		s = clamp01(s * p.Saturation)
		l = clamp01(l * p.Lightness)
		r, g, b := hslToRGB(h, s, l)
		*c = dxt.RGBA{R: to8(r), G: to8(g), B: to8(b), A: c.A}
	}
}

func rgbToHSL(r, g, b float64) (h, s, l float64) {
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l = (max + min) / 2
	if max == min {
		return 0, 0, l
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h /= 6
	return h, s, l
}

func hslToRGB(h, s, l float64) (r, g, b float64) {
	if s <= 0 {
		return l, l, l
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return hueToRGB(p, q, h+1.0/3), hueToRGB(p, q, h), hueToRGB(p, q, h-1.0/3)
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 1.0/2:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}

// --- OKLCH -----------------------------------------------------------------

// srgbToLinear is a lookup table for the 8-bit sRGB -> linear sRGB transfer
// function, which is the expensive part of the forward conversion.
var srgbToLinear = func() (tab [256]float64) {
	for i := range tab {
		c := float64(i) / 255
		if c <= 0.04045 {
			tab[i] = c / 12.92
		} else {
			tab[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return tab
}()

func applyOKLCH(img *dxt.Image, p Params) {
	hue := p.Hue * math.Pi / 180
	sat := p.Saturation
	light := p.Lightness
	for i := range img.Pix {
		c := &img.Pix[i]
		if !p.matches(*c) {
			continue
		}
		rl, gl, bl := srgbToLinear[c.R], srgbToLinear[c.G], srgbToLinear[c.B]
		l := 0.4122214708*rl + 0.5363325363*gl + 0.0514459929*bl
		m := 0.2119034982*rl + 0.6806995451*gl + 0.1073969566*bl
		s := 0.0883024619*rl + 0.2817188376*gl + 0.6299787005*bl
		l_ := math.Cbrt(l)
		m_ := math.Cbrt(m)
		s_ := math.Cbrt(s)

		L := 0.2104542553*l_ + 0.7936177850*m_ - 0.0040720468*s_
		A := 1.9779984951*l_ - 2.4285922050*m_ + 0.4505937099*s_
		B := 0.0259040371*l_ + 0.7827717662*m_ - 0.8086757660*s_

		C0 := math.Hypot(A, B)
		// A neutral pixel has no hue to rotate; without a lightness change it
		// must come out exactly as it went in (this keeps grey metal and
		// greyscale masks bit-identical).
		if C0 < 1e-9 && light == 1 {
			continue
		}
		C := C0 * sat

		L *= light
		if L <= 0 {
			*c = dxt.RGBA{A: c.A}
			continue
		}
		if L >= 1 {
			*c = dxt.RGBA{R: 255, G: 255, B: 255, A: c.A}
			continue
		}
		h := math.Atan2(B, A) + hue
		r, g, b := oklchToSRGB(L, C, h)
		*c = dxt.RGBA{R: to8(r), G: to8(g), B: to8(b), A: c.A}
	}
}

// oklchToSRGB converts an OKLCH colour to sRGB. When the colour is outside the
// sRGB gamut its chroma is reduced (binary search) instead of clipping the
// channels, which would shift the hue.
func oklchToSRGB(L, C, h float64) (float64, float64, float64) {
	ca := C * math.Cos(h)
	sa := C * math.Sin(h)
	r, g, b := oklabToLinear(L, ca, sa)
	if !inGamut(r, g, b) {
		// C=0 is neutral and therefore always inside the gamut; use it as a
		// safe fallback and then look for the largest chroma that still fits.
		r, g, b = oklabToLinear(L, 0, 0)
		lo, hi := 0.0, C
		for i := 0; i < 14; i++ {
			mid := (lo + hi) / 2
			cr := mid * math.Cos(h)
			sr := mid * math.Sin(h)
			rr, gg, bb := oklabToLinear(L, cr, sr)
			if inGamut(rr, gg, bb) {
				lo = mid
				r, g, b = rr, gg, bb
			} else {
				hi = mid
			}
		}
	}
	return clamp01(linearToSRGB(r)), clamp01(linearToSRGB(g)), clamp01(linearToSRGB(b))
}

func oklabToLinear(L, a, b float64) (float64, float64, float64) {
	l_ := L + 0.3963377774*a + 0.2158037573*b
	m_ := L - 0.1055613458*a - 0.0638541728*b
	s_ := L - 0.0894841775*a - 1.2914855480*b
	l := l_ * l_ * l_
	m := m_ * m_ * m_
	s := s_ * s_ * s_
	r := +4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b2 := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	return r, g, b2
}

func inGamut(r, g, b float64) bool {
	const eps = 1e-7
	return r >= -eps && r <= 1+eps && g >= -eps && g <= 1+eps && b >= -eps && b <= 1+eps
}

func linearToSRGB(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	const steps = 4096
	x := v * steps
	i := int(x)
	f := x - float64(i)
	return srgbLUT[i]*(1-f) + srgbLUT[i+1]*f
}

// srgbLUT samples the linear -> sRGB transfer function densely enough that the
// 8-bit result is unchanged. Interpolating avoids three math.Pow calls per
// pixel, which makes the OKLCH path roughly twice as fast.
var srgbLUT = func() (tab [4097]float64) {
	for i := range tab {
		v := float64(i) / 4096
		if v <= 0.0031308 {
			tab[i] = 12.92 * v
		} else {
			tab[i] = 1.055*math.Pow(v, 1.0/2.4) - 0.055
		}
	}
	return tab
}()

// --- helpers ---------------------------------------------------------------

// hsvOf returns the hue (0..1) and HSV saturation (0..1) of a colour.
func hsvOf(c dxt.RGBA) (h, s float64) {
	r := float64(c.R) / 255
	g := float64(c.G) / 255
	b := float64(c.B) / 255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	if max <= 0 {
		return 0, 0
	}
	d := max - min
	s = d / max
	if d == 0 {
		return 0, s
	}
	switch max {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h /= 6
	if h < 0 {
		h++
	}
	return h, s
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func to8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(math.Round(v * 255))
}
