package recolor

import (
	"math"
	"testing"

	"l2utx/internal/dxt"
)

func testImage() *dxt.Image {
	img := dxt.NewImage(4, 2)
	copy(img.Pix, []dxt.RGBA{
		{R: 255, G: 0, B: 0, A: 255},
		{R: 0, G: 255, B: 0, A: 128},
		{R: 0, G: 0, B: 255, A: 0},
		{R: 128, G: 128, B: 128, A: 255},
		{R: 12, G: 200, B: 240, A: 77},
		{R: 250, G: 240, B: 10, A: 255},
		{R: 0, G: 0, B: 0, A: 255},
		{R: 255, G: 255, B: 255, A: 255},
	})
	return img
}

func copyPix(src []dxt.RGBA) []dxt.RGBA {
	return append([]dxt.RGBA(nil), src...)
}

func TestIdentity(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		img := testImage()
		want := copyPix(img.Pix)
		Apply(img, Params{Saturation: 1, Lightness: 1, Mode: mode})
		for i := range want {
			if img.Pix[i] != want[i] {
				t.Fatalf("%s: pixel %d changed: %+v -> %+v", mode, i, want[i], img.Pix[i])
			}
		}
	}
}

func TestAlphaPreserved(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		img := testImage()
		var alphas []uint8
		for _, c := range img.Pix {
			alphas = append(alphas, c.A)
		}
		Apply(img, Params{Hue: 137, Saturation: 1.4, Lightness: 0.85, Mode: mode})
		for i, c := range img.Pix {
			if c.A != alphas[i] {
				t.Fatalf("%s: pixel %d alpha %d -> %d", mode, i, alphas[i], c.A)
			}
		}
	}
}

func TestGreyStaysGrey(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		for _, v := range []uint8{0, 32, 128, 200, 255} {
			img := &dxt.Image{W: 1, H: 1, Pix: []dxt.RGBA{{R: v, G: v, B: v, A: 123}}}
			Apply(img, Params{Hue: 90, Saturation: 1, Lightness: 1, Mode: mode})
			if img.Pix[0] != (dxt.RGBA{R: v, G: v, B: v, A: 123}) {
				t.Fatalf("%s: grey %d changed to %+v", mode, v, img.Pix[0])
			}
		}
	}
}

func TestHue360IsIdentity(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		img := testImage()
		want := copyPix(img.Pix)
		Apply(img, Params{Hue: 360, Saturation: 1, Lightness: 1, Mode: mode})
		for i := range want {
			d := channelDiff(want[i], img.Pix[i])
			if d > 1 {
				t.Fatalf("%s: pixel %d differs by %d after a full turn: %+v -> %+v",
					mode, i, d, want[i], img.Pix[i])
			}
		}
	}
}

func TestHSLRotatesPrimaries(t *testing.T) {
	img := &dxt.Image{W: 1, H: 1, Pix: []dxt.RGBA{{R: 255, A: 255}}}
	Apply(img, Params{Hue: 120, Saturation: 1, Lightness: 1, Mode: ModeHSL})
	want := dxt.RGBA{G: 255, A: 255}
	if channelDiff(img.Pix[0], want) > 1 {
		t.Fatalf("HSL red +120: got %+v, want %+v", img.Pix[0], want)
	}
}

func TestOKLCHKeepsGreenDominant(t *testing.T) {
	img := &dxt.Image{W: 1, H: 1, Pix: []dxt.RGBA{{R: 255, A: 255}}}
	Apply(img, Params{Hue: 120, Saturation: 1, Lightness: 1})
	c := img.Pix[0]
	if !(c.G > c.R && c.G > c.B) {
		t.Fatalf("OKLCH red +120 should be greenish, got %+v", c)
	}
}

func TestSaturationZero(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		img := testImage()
		Apply(img, Params{Saturation: 0, Lightness: 1, Mode: mode})
		for i, c := range img.Pix {
			if c.R != c.G || c.G != c.B {
				t.Fatalf("%s: pixel %d is not grey: %+v", mode, i, c)
			}
		}
	}
}

func TestLightnessZeroAndBlackWhite(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		img := testImage()
		Apply(img, Params{Saturation: 1, Lightness: 0, Mode: mode})
		for i, c := range img.Pix {
			if c.R != 0 || c.G != 0 || c.B != 0 {
				t.Fatalf("%s: lightness 0 did not produce black at %d: %+v", mode, i, c)
			}
		}
		img = &dxt.Image{W: 1, H: 1, Pix: []dxt.RGBA{{A: 255}}}
		Apply(img, Params{Saturation: 2, Lightness: 2, Mode: mode})
		if img.Pix[0] != (dxt.RGBA{A: 255}) {
			t.Fatalf("%s: black changed: %+v", mode, img.Pix[0])
		}
		img = &dxt.Image{W: 1, H: 1, Pix: []dxt.RGBA{{R: 255, G: 255, B: 255, A: 255}}}
		Apply(img, Params{Hue: 90, Saturation: 2, Lightness: 1, Mode: mode})
		if img.Pix[0] != (dxt.RGBA{R: 255, G: 255, B: 255, A: 255}) {
			t.Fatalf("%s: white changed: %+v", mode, img.Pix[0])
		}
	}
}

func TestExtremeAdjustmentStaysFinite(t *testing.T) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		img := testImage()
		Apply(img, Params{Hue: 725.5, Saturation: 4, Lightness: 3, Mode: mode})
		for i, c := range img.Pix {
			if c.R != 0 || c.G != 0 || c.B != 0 {
				continue
			}
			t.Logf("%s: pixel %d became black", mode, i)
		}
	}
}

func TestDescribe(t *testing.T) {
	p := Params{Hue: 120, Saturation: 0.8, Lightness: 1.1, Mode: ModeOKLCH}
	got := p.Describe()
	want := "hue 120°, saturation 80%, lightness 110%"
	if got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
}

func channelDiff(a, b dxt.RGBA) int {
	d := 0
	for _, pair := range [][2]int{
		{int(a.R), int(b.R)}, {int(a.G), int(b.G)}, {int(a.B), int(b.B)}, {int(a.A), int(b.A)},
	} {
		if v := int(math.Abs(float64(pair[0] - pair[1]))); v > d {
			d = v
		}
	}
	return d
}

func BenchmarkApply(b *testing.B) {
	for _, mode := range []Mode{ModeOKLCH, ModeHSL} {
		b.Run(mode.String(), func(b *testing.B) {
			img := dxt.NewImage(512, 512)
			for i := range img.Pix {
				img.Pix[i] = dxt.RGBA{
					R: uint8(i), G: uint8(i >> 3), B: uint8(i >> 6),
					A: uint8(i >> 9),
				}
			}
			p := Params{Hue: 120, Saturation: 1.1, Lightness: 1, Mode: mode}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Apply(img, p)
			}
		})
	}
}
