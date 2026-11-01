package upkg

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// UE2 ETextureFormat values (as used by Lineage 2 / UT2003).
const (
	FmtP8        = 0
	FmtRGBA7     = 1
	FmtRGB16     = 2
	FmtDXT1      = 3
	FmtRGB8      = 4
	FmtRGBA8     = 5
	FmtNoData    = 6
	FmtDXT3      = 7
	FmtDXT5      = 8
	FmtL8        = 9
	FmtG16       = 10
	FmtRRRGGGBBB = 11
	FmtCxV8U8    = 12
	FmtDXT5N     = 13
	Fmt3DC       = 14
)

// FormatNames maps ETextureFormat to a readable name.
var FormatNames = map[int]string{
	FmtP8:        "P8",
	FmtRGBA7:     "RGBA7",
	FmtRGB16:     "RGB16",
	FmtDXT1:      "DXT1",
	FmtRGB8:      "RGB8",
	FmtRGBA8:     "RGBA8",
	FmtNoData:    "NODATA",
	FmtDXT3:      "DXT3",
	FmtDXT5:      "DXT5",
	FmtL8:        "L8",
	FmtG16:       "G16",
	FmtRRRGGGBBB: "RRRGGGBBB",
	FmtCxV8U8:    "CxV8U8",
	FmtDXT5N:     "DXT5N",
	Fmt3DC:       "3DC",
}

// FormatName returns a human readable texture format name.
func FormatName(f int) string {
	if n, ok := FormatNames[f]; ok {
		return n
	}
	return fmt.Sprintf("fmt%d", f)
}

// IsDXT reports whether the format is a DXT block format.
func IsDXT(f int) bool { return f == FmtDXT1 || f == FmtDXT3 || f == FmtDXT5 }

// Mip is one mipmap level of a texture.
type Mip struct {
	USize int
	VSize int
	UBits byte
	VBits byte
	Data  []byte
}

// Texture is a parsed UTexture payload (tagged properties + native mip list).
type Texture struct {
	Props *Props // nil when the property list could not be decoded
	// PropsRaw holds the bytes preceding the mip list when Props is nil.
	PropsRaw []byte
	// Extra holds bytes that sit between the property list and the mip list.
	// Some UT2004-era builds write a 32-bit field there; it is preserved as-is.
	Extra []byte
	Mips  []Mip

	Format int
	// FormatKnown is false when the format had to be inferred from block sizes.
	FormatKnown bool
}

// Size returns the byte length of the serialized payload.
func (t *Texture) Size() int {
	n := len(t.propBytes()) + len(t.Extra) + 1
	for _, m := range t.Mips {
		n += 4 + CompactIndexSize(int32(len(m.Data))) + len(m.Data) + 10
	}
	return n
}

func (t *Texture) propBytes() []byte {
	if t.Props != nil {
		return t.Props.Raw
	}
	return t.PropsRaw
}

// Build serializes the payload. absStart is the absolute file offset at which
// the payload will be placed; each mip record stores the absolute position that
// follows its data, exactly like the engine's writer does.
func (t *Texture) Build(absStart int64) []byte {
	props := t.propBytes()
	out := make([]byte, 0, len(props)+len(t.Extra)+1+len(t.Mips)*16+1024)
	out = append(out, props...)
	out = append(out, t.Extra...)
	out = append(out, byte(len(t.Mips)))
	for _, m := range t.Mips {
		fixup := len(out)
		out = append(out, 0, 0, 0, 0)
		out = WriteCompact(out, int32(len(m.Data)))
		out = append(out, m.Data...)
		endAfterData := absStart + int64(len(out))
		binary.LittleEndian.PutUint32(out[fixup:], uint32(endAfterData))
		out = putU32(out, uint32(m.USize))
		out = putU32(out, uint32(m.VSize))
		out = append(out, m.UBits, m.VBits)
	}
	return out
}

// ExpectedDataSize returns the number of bytes a mip of the given format and
// dimensions must occupy (0 when the format/size rule is unknown).
func ExpectedDataSize(format, w, h int) int {
	if w <= 0 || h <= 0 {
		return 0
	}
	switch format {
	case FmtDXT1:
		return blocks(w) * blocks(h) * 8
	case FmtDXT3, FmtDXT5, FmtCxV8U8, FmtDXT5N, Fmt3DC:
		return blocks(w) * blocks(h) * 16
	case FmtP8, FmtL8, FmtRGB8:
		return w * h
	case FmtRGB16, FmtG16:
		return w * h * 2
	case FmtRGBA8:
		return w * h * 4
	case FmtRRRGGGBBB:
		return w * h * 3
	}
	return 0
}

func blocks(v int) int {
	if v < 4 {
		return 1
	}
	return (v + 3) / 4
}

// parseMipChain parses the native mip list starting at start. It requires that
// the list consumes the payload exactly, which makes it a reliable oracle for
// locating the start when the property block cannot be decoded.
func parseMipChain(payload []byte, start int) ([]Mip, error) {
	if start < 0 || start >= len(payload) {
		return nil, fmt.Errorf("mip list starts outside the payload")
	}
	r := &reader{d: payload, p: start}
	count := int(r.u8())
	if r.err != nil {
		return nil, r.err
	}
	if count < 1 || count > 32 {
		return nil, fmt.Errorf("implausible mip count %d", count)
	}
	mips := make([]Mip, 0, count)
	prevW, prevH := 0, 0
	for i := 0; i < count; i++ {
		r.bytes(4) // WidthOffset (absolute), validated on write only
		size := r.compact()
		if r.err != nil {
			return nil, r.err
		}
		// A size of zero means the mip was stripped (seen in UT2004 packages and
		// in server-side texture packages); the descriptor is still present.
		if size < 0 || int(size) > r.remaining() {
			return nil, fmt.Errorf("mip %d has implausible size %d", i, size)
		}
		var data []byte
		if size > 0 {
			data = r.bytes(int(size))
		}
		w := int(r.u32())
		h := int(r.u32())
		ub := r.u8()
		vb := r.u8()
		if r.err != nil {
			return nil, r.err
		}
		if w <= 0 || h <= 0 || w > 8192 || h > 8192 {
			return nil, fmt.Errorf("mip %d has implausible size %dx%d", i, w, h)
		}
		if i > 0 && (w != prevW/2 || h != prevH/2) && !(prevW <= 1 && prevH <= 1) {
			// Allow the engine's clamp-at-1 behaviour, reject jumps otherwise.
			if w != maxInt(1, prevW/2) || h != maxInt(1, prevH/2) {
				return nil, fmt.Errorf("mip %d dimensions %dx%d do not follow %dx%d", i, w, h, prevW, prevH)
			}
		}
		if ub != byte(Log2(w)) || vb != byte(Log2(h)) {
			return nil, fmt.Errorf("mip %d bit counts (%d,%d) disagree with %dx%d", i, ub, vb, w, h)
		}
		mips = append(mips, Mip{USize: w, VSize: h, UBits: ub, VBits: vb, Data: data})
		prevW, prevH = w, h
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.p != len(payload) {
		return nil, fmt.Errorf("mip list ends at %d but payload is %d bytes", r.p, len(payload))
	}
	return mips, nil
}

// ParseTexture decodes a UTexture payload.
func (p *Package) ParseTexture(payload []byte) (*Texture, error) {
	t := &Texture{}
	// Fast path: property list decodes cleanly and the mip list follows it.
	props, propsErr := p.ParseProps(payload)
	if propsErr == nil {
		if mips, err := parseMipChain(payload, props.End); err == nil {
			t.Props = props
			t.Mips = mips
			t.Format = int(propsIntOr(props, "Format", -1))
			t.FormatKnown = props.Has("Format")
			return t.finish()
		}
		// A few UT2004-era builds insert a small field between the property list
		// and the mip list; accept a gap only when the chain still ends exactly
		// at the end of the payload.
		for k := 1; k <= 8 && props.End+k < len(payload); k++ {
			mips, err := parseMipChain(payload, props.End+k)
			if err != nil {
				continue
			}
			t.Props = props
			t.Extra = append([]byte(nil), payload[props.End:props.End+k]...)
			t.Mips = mips
			t.Format = int(propsIntOr(props, "Format", -1))
			t.FormatKnown = props.Has("Format")
			return t.finish()
		}
	}
	// Fallback: locate the mip list by structural validation.
	for start := 1; start < len(payload) && start < 4096; start++ {
		mips, err := parseMipChain(payload, start)
		if err != nil {
			continue
		}
		t.PropsRaw = payload[:start]
		t.Mips = mips
		t.Format = inferFormat(mips)
		return t.finish()
	}
	if propsErr != nil {
		return nil, fmt.Errorf("not a texture payload (%v)", propsErr)
	}
	return nil, fmt.Errorf("not a texture payload (no mip list found)")
}

func (t *Texture) finish() (*Texture, error) {
	if len(t.Mips) == 0 {
		return nil, fmt.Errorf("texture has no mips")
	}
	if !t.FormatKnown {
		if t.Format < 0 {
			return nil, fmt.Errorf("texture format could not be determined")
		}
	}
	// Verify every mip's size matches the declared format (stripped levels skip).
	for i, m := range t.Mips {
		if len(m.Data) == 0 {
			continue
		}
		if want := ExpectedDataSize(t.Format, m.USize, m.VSize); want != 0 && want != len(m.Data) {
			return nil, fmt.Errorf("mip %d (%dx%d) is %d bytes, expected %d for %s",
				i, m.USize, m.VSize, len(m.Data), want, FormatName(t.Format))
		}
	}
	return t, nil
}

func propsIntOr(pr *Props, name string, def int32) int32 {
	if v, ok := pr.IntValue(name); ok {
		return v
	}
	if v, ok := pr.ByteValue(name); ok {
		return int32(v)
	}
	return def
}

func inferFormat(mips []Mip) int {
	m := mips[0]
	if m.USize*m.VSize == 0 {
		return -1
	}
	perPixel := float64(len(m.Data)) / float64(m.USize*m.VSize)
	switch {
	case perPixel == 0.5:
		return FmtDXT1
	case perPixel == 1:
		// DXT3/DXT5 use one byte per pixel as well; guess from the alpha layout.
		return guessDXT3or5(m.Data)
	case perPixel == 4:
		return FmtRGBA8
	case perPixel == 3:
		return FmtRRRGGGBBB
	case perPixel == 2:
		return FmtRGB16
	}
	return -1
}

// guessDXT3or5 inspects the alpha halves of the DXT blocks. DXT3 stores 4-bit
// alpha directly (opaque blocks are 8 bytes of 0xFF); DXT5 stores two 8-bit
// endpoints followed by 3-bit indices, which for fully opaque blocks looks like
// "FF FF 00 00 00 00 00 00".
func guessDXT3or5(data []byte) int {
	if len(data) < 16 {
		return FmtDXT3
	}
	allFF, dxt5ish := 0, 0
	total := len(data) / 16
	if total > 4096 {
		total = 4096
	}
	for i := 0; i < total; i++ {
		blk := data[i*16:]
		zero := true
		ff := true
		for k := 0; k < 8; k++ {
			if blk[k] != 0xFF {
				ff = false
			}
			if blk[k] != 0 {
				zero = false
			}
		}
		if ff {
			allFF++
		}
		if blk[0] == 0xFF && blk[1] == 0xFF && zero == false && blk[2] == 0 && blk[3] == 0 && blk[4] == 0 {
			dxt5ish++
		}
	}
	if dxt5ish*2 > total && allFF*2 < total {
		return FmtDXT5
	}
	return FmtDXT3
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TextureExport describes a texture object found in a package.
type TextureExport struct {
	Index  int
	Name   string
	Class  string
	Tex    *Texture
	Err    error
	Skipped bool
}

// IsTextureClass reports whether an export's class looks like a plain texture.
func (p *Package) IsTextureClass(e *ExportEntry) bool {
	return strings.EqualFold(p.ClassName(e), "Texture")
}

// SetFormatAndSize patches the tagged properties so they describe the new
// format and dimensions. It fails when the property block could not be decoded,
// because then only same-size replacements are safe.
func (t *Texture) SetFormatAndSize(p *Package, format, w, h int) error {
	if t.Props == nil {
		return fmt.Errorf("texture properties could not be decoded; only same-size replacement is possible")
	}
	t.Format = format
	t.FormatKnown = true
	if err := t.patchByte(p, "Format", byte(format)); err != nil {
		return err
	}
	if err := t.patchByte(p, "UBits", byte(Log2(w))); err != nil {
		return err
	}
	if err := t.patchByte(p, "VBits", byte(Log2(h))); err != nil {
		return err
	}
	if err := t.patchInt(p, "USize", int32(w)); err != nil {
		return err
	}
	if err := t.patchInt(p, "VSize", int32(h)); err != nil {
		return err
	}
	// Clamp dimensions usually mirror the size; only patch them when present so
	// we do not invent properties the class does not declare.
	if t.Props.Has("UClamp") {
		if err := t.patchInt(p, "UClamp", int32(w)); err != nil {
			return err
		}
	}
	if t.Props.Has("VClamp") {
		if err := t.patchInt(p, "VClamp", int32(h)); err != nil {
			return err
		}
	}
	return nil
}

func (t *Texture) patchByte(p *Package, name string, v byte) error {
	if t.Props.SetByte(name, v) {
		return nil
	}
	idx := p.AddName(name)
	if t.Props.AppendProp(idx, PropByte, int32(v)) {
		return nil
	}
	return fmt.Errorf("cannot set property %q", name)
}

func (t *Texture) patchInt(p *Package, name string, v int32) error {
	if t.Props.SetInt(name, v) {
		return nil
	}
	idx := p.AddName(name)
	if t.Props.AppendProp(idx, PropInt, v) {
		return nil
	}
	return fmt.Errorf("cannot set property %q", name)
}

// Textures parses every texture export in the package. Non-texture exports are
// reported with Skipped set (or Err when a texture payload fails to parse).
func (p *Package) Textures() []TextureExport {
	out := make([]TextureExport, 0, len(p.Exports))
	for i := range p.Exports {
		e := &p.Exports[i]
		te := TextureExport{Index: i, Name: p.Name(e.ObjectName), Class: p.ClassName(e)}
		if !p.IsTextureClass(e) {
			te.Skipped = true
			out = append(out, te)
			continue
		}
		if len(e.Data) == 0 {
			te.Err = fmt.Errorf("texture export has no data")
			out = append(out, te)
			continue
		}
		t, err := p.ParseTexture(e.Data)
		if err != nil {
			te.Err = err
		} else {
			te.Tex = t
			e.tex = t
			e.rebuild = func(absStart int64) []byte { return t.Build(absStart) }
		}
		out = append(out, te)
	}
	return out
}
