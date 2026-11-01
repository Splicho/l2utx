// Package imgio loads and stores the image formats the tool accepts: PNG, JPEG,
// GIF and BMP (via the standard library plus a small BMP reader), TGA and DDS.
package imgio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"l2utx/internal/dxt"
)

// Format identifies a container format.
type Format int

const (
	FormatPNG Format = iota
	FormatJPEG
	FormatGIF
	FormatBMP
	FormatTGA
	FormatDDS
)

// Name returns a human readable format name.
func (f Format) Name() string {
	switch f {
	case FormatPNG:
		return "PNG"
	case FormatJPEG:
		return "JPEG"
	case FormatGIF:
		return "GIF"
	case FormatBMP:
		return "BMP"
	case FormatTGA:
		return "TGA"
	case FormatDDS:
		return "DDS"
	}
	return "unknown"
}

// FormatForPath guesses the format from a file extension.
func FormatForPath(path string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return FormatPNG, true
	case ".jpg", ".jpeg":
		return FormatJPEG, true
	case ".gif":
		return FormatGIF, true
	case ".bmp", ".dib":
		return FormatBMP, true
	case ".tga":
		return FormatTGA, true
	case ".dds":
		return FormatDDS, true
	}
	return 0, false
}

// IsImagePath reports whether the path looks like a supported image.
func IsImagePath(path string) bool {
	_, ok := FormatForPath(path)
	return ok
}

// Load reads any supported image file into an RGBA image.
func Load(path string) (*dxt.Image, Format, error) {
	f, ok := FormatForPath(path)
	if !ok {
		return nil, 0, fmt.Errorf("unsupported image type %q", filepath.Ext(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	img, err := Decode(data, f)
	return img, f, err
}

// Decode decodes image bytes of the given format.
func Decode(data []byte, f Format) (*dxt.Image, error) {
	switch f {
	case FormatPNG, FormatJPEG, FormatGIF:
		var (
			src image.Image
			err error
		)
		r := bytes.NewReader(data)
		switch f {
		case FormatPNG:
			src, err = png.Decode(r)
		case FormatJPEG:
			src, err = jpeg.Decode(r)
		default:
			src, err = gif.Decode(r)
		}
		if err != nil {
			return nil, err
		}
		return fromGoImage(src), nil
	case FormatBMP:
		return decodeBMP(data)
	case FormatTGA:
		return decodeTGA(data)
	case FormatDDS:
		dds, err := DecodeDDS(data)
		if err != nil {
			return nil, err
		}
		return dds.Image(), nil
	}
	return nil, errors.New("unsupported image format")
}

func fromGoImage(src image.Image) *dxt.Image {
	b := src.Bounds()
	out := dxt.NewImage(b.Dx(), b.Dy())
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			// NRGBAModel conversion keeps colours straight (not premultiplied),
			// which matters for textures with soft alpha edges.
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			out.Pix[y*out.W+x] = dxt.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
		}
	}
	return out
}

// SavePNG writes an RGBA image as PNG.
func SavePNG(path string, img *dxt.Image) error {
	out := image.NewNRGBA(image.Rect(0, 0, img.W, img.H))
	for i, c := range img.Pix {
		out.Pix[i*4+0] = c.R
		out.Pix[i*4+1] = c.G
		out.Pix[i*4+2] = c.B
		out.Pix[i*4+3] = c.A
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	return enc.Encode(f, out)
}

// --- BMP -------------------------------------------------------------------

func decodeBMP(data []byte) (*dxt.Image, error) {
	if len(data) < 54 || data[0] != 'B' || data[1] != 'M' {
		return nil, errors.New("not a BMP file")
	}
	dataOff := int(binary.LittleEndian.Uint32(data[10:]))
	hdrSize := int(binary.LittleEndian.Uint32(data[14:]))
	if hdrSize < 12 {
		return nil, errors.New("unsupported BMP header")
	}
	var w, h int
	var bpp int
	topDown := false
	if hdrSize >= 40 {
		w = int(int32(binary.LittleEndian.Uint32(data[18:])))
		h = int(int32(binary.LittleEndian.Uint32(data[22:])))
		bpp = int(binary.LittleEndian.Uint16(data[28:]))
		if h < 0 {
			h = -h
			topDown = true
		}
	} else {
		w = int(binary.LittleEndian.Uint16(data[18:]))
		h = int(binary.LittleEndian.Uint16(data[20:]))
		bpp = int(binary.LittleEndian.Uint16(data[24:]))
	}
	if w <= 0 || h <= 0 || w > 16384 || h > 16384 {
		return nil, errors.New("bad BMP dimensions")
	}
	compression := uint32(0)
	if hdrSize >= 40 {
		compression = binary.LittleEndian.Uint32(data[30:])
	}
	if compression != 0 && compression != 3 {
		return nil, fmt.Errorf("compressed BMP (type %d) is not supported", compression)
	}
	img := dxt.NewImage(w, h)
	rowSize := ((bpp*w + 31) / 32) * 4
	if dataOff+rowSize*h > len(data) {
		return nil, errors.New("truncated BMP data")
	}
	for y := 0; y < h; y++ {
		srcY := h - 1 - y
		if topDown {
			srcY = y
		}
		row := data[dataOff+srcY*rowSize:]
		for x := 0; x < w; x++ {
			var c dxt.RGBA
			switch bpp {
			case 32:
				p := row[x*4:]
				c = dxt.RGBA{R: p[2], G: p[1], B: p[0], A: p[3]}
			case 24:
				p := row[x*3:]
				c = dxt.RGBA{R: p[2], G: p[1], B: p[0], A: 255}
			case 16:
				v := binary.LittleEndian.Uint16(row[x*2:])
				c = dxt.RGBA{
					R: uint8((v>>10)&0x1F) * 8,
					G: uint8((v>>5)&0x1F) * 8,
					B: uint8(v&0x1F) * 8,
					A: 255,
				}
			case 8:
				// Palette-based; read the palette from the header area.
				c = paletteColor(data, hdrSize, int(row[x]))
			default:
				return nil, fmt.Errorf("unsupported BMP depth %d", bpp)
			}
			img.Set(x, y, c)
		}
	}
	if bpp == 32 {
		// Some BMPs leave alpha at zero; treat fully-zero alpha as opaque.
		anyAlpha := false
		for _, c := range img.Pix {
			if c.A != 0 {
				anyAlpha = true
				break
			}
		}
		if !anyAlpha {
			for i := range img.Pix {
				img.Pix[i].A = 255
			}
		}
	}
	return img, nil
}

func paletteColor(data []byte, hdrSize, idx int) dxt.RGBA {
	base := 14 + hdrSize
	p := base + idx*4
	if p+3 >= len(data) {
		return dxt.RGBA{A: 255}
	}
	return dxt.RGBA{R: data[p+2], G: data[p+1], B: data[p], A: 255}
}

// --- TGA -------------------------------------------------------------------

func decodeTGA(data []byte) (*dxt.Image, error) {
	if len(data) < 18 {
		return nil, errors.New("not a TGA file")
	}
	idLen := int(data[0])
	cmapType := data[1]
	imgType := data[2]
	cmapFirst := int(binary.LittleEndian.Uint16(data[3:]))
	cmapLen := int(binary.LittleEndian.Uint16(data[5:]))
	cmapBits := int(data[7])
	w := int(binary.LittleEndian.Uint16(data[12:]))
	h := int(binary.LittleEndian.Uint16(data[14:]))
	bpp := int(data[16])
	desc := data[17]
	if w <= 0 || h <= 0 || w > 16384 || h > 16384 {
		return nil, errors.New("bad TGA dimensions")
	}
	off := 18 + idLen
	var palette []dxt.RGBA
	if cmapType == 1 {
		entrySize := (cmapBits + 7) / 8
		for i := 0; i < cmapLen; i++ {
			p := off + i*entrySize
			if p+entrySize > len(data) {
				return nil, errors.New("truncated TGA palette")
			}
			var c dxt.RGBA
			switch entrySize {
			case 4:
				c = dxt.RGBA{R: data[p+2], G: data[p+1], B: data[p], A: data[p+3]}
			case 3:
				c = dxt.RGBA{R: data[p+2], G: data[p+1], B: data[p], A: 255}
			case 2:
				v := binary.LittleEndian.Uint16(data[p:])
				c = dxt.RGBA{
					R: uint8((v>>10)&0x1F) * 8, G: uint8((v>>5)&0x1F) * 8, B: uint8(v&0x1F) * 8, A: 255,
				}
			default:
				return nil, errors.New("unsupported TGA palette entry size")
			}
			palette = append(palette, c)
		}
		off += cmapLen * entrySize
	}
	img := dxt.NewImage(w, h)
	switch imgType {
	case 1, 2, 3: // uncompressed
		bytesPP := (bpp + 7) / 8
		if off+w*h*bytesPP > len(data) {
			return nil, errors.New("truncated TGA data")
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p := off + (y*w+x)*bytesPP
				img.Set(x, y, tgaPixel(data[p:p+bytesPP], imgType, bpp, palette, cmapFirst))
			}
		}
	case 9, 10, 11: // RLE
		bytesPP := (bpp + 7) / 8
		px := 0
		total := w * h
		for px < total {
			if off >= len(data) {
				return nil, errors.New("truncated TGA RLE data")
			}
			hdr := data[off]
			off++
			count := int(hdr&0x7F) + 1
			if hdr&0x80 != 0 {
				if off+bytesPP > len(data) {
					return nil, errors.New("truncated TGA RLE packet")
				}
				c := tgaPixel(data[off:off+bytesPP], imgType-8, bpp, palette, cmapFirst)
				off += bytesPP
				for i := 0; i < count && px < total; i++ {
					img.Set(px%w, px/w, c)
					px++
				}
			} else {
				for i := 0; i < count && px < total; i++ {
					if off+bytesPP > len(data) {
						return nil, errors.New("truncated TGA RLE data")
					}
					c := tgaPixel(data[off:off+bytesPP], imgType-8, bpp, palette, cmapFirst)
					off += bytesPP
					img.Set(px%w, px/w, c)
					px++
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported TGA image type %d", imgType)
	}
	// Bit 5 of the descriptor selects top-down storage.
	if desc&0x20 == 0 {
		for y := 0; y < h/2; y++ {
			for x := 0; x < w; x++ {
				a := img.Pix[y*w+x]
				b := img.Pix[(h-1-y)*w+x]
				img.Pix[y*w+x], img.Pix[(h-1-y)*w+x] = b, a
			}
		}
	}
	// Bit 4 selects right-to-left storage.
	if desc&0x10 != 0 {
		for y := 0; y < h; y++ {
			for x := 0; x < w/2; x++ {
				a := img.Pix[y*w+x]
				b := img.Pix[y*w+(w-1-x)]
				img.Pix[y*w+x], img.Pix[y*w+(w-1-x)] = b, a
			}
		}
	}
	return img, nil
}

func tgaPixel(p []byte, imgType byte, bpp int, palette []dxt.RGBA, first int) dxt.RGBA {
	switch imgType {
	case 1:
		idx := 0
		if bpp == 16 {
			idx = int(binary.LittleEndian.Uint16(p))
		} else {
			idx = int(p[0])
		}
		idx -= first
		if idx >= 0 && idx < len(palette) {
			return palette[idx]
		}
		return dxt.RGBA{A: 255}
	case 3:
		v := p[0]
		return dxt.RGBA{R: v, G: v, B: v, A: 255}
	default:
		switch bpp {
		case 32:
			return dxt.RGBA{R: p[2], G: p[1], B: p[0], A: p[3]}
		case 24:
			return dxt.RGBA{R: p[2], G: p[1], B: p[0], A: 255}
		case 16:
			v := binary.LittleEndian.Uint16(p)
			return dxt.RGBA{
				R: uint8((v>>10)&0x1F) * 8, G: uint8((v>>5)&0x1F) * 8, B: uint8(v&0x1F) * 8, A: 255,
			}
		case 15:
			v := binary.LittleEndian.Uint16(p)
			return dxt.RGBA{
				R: uint8((v>>10)&0x1F) * 8, G: uint8((v>>5)&0x1F) * 8, B: uint8(v&0x1F) * 8, A: 255,
			}
		}
	}
	return dxt.RGBA{A: 255}
}
