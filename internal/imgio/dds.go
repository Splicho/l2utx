package imgio

import (
	"encoding/binary"
	"errors"
	"fmt"

	"l2utx/internal/dxt"
)

// PixelFormat is the storage format of a DDS file.
type PixelFormat int

// Supported DDS storage formats.
const (
	PixelRGBA8 PixelFormat = iota
	PixelDXT1
	PixelDXT3
	PixelDXT5
)

// Name returns a readable format name.
func (f PixelFormat) Name() string {
	switch f {
	case PixelRGBA8:
		return "RGBA8"
	case PixelDXT1:
		return "DXT1"
	case PixelDXT3:
		return "DXT3"
	case PixelDXT5:
		return "DXT5"
	}
	return "unknown"
}

// DDS is a decoded DirectDraw Surface file.
type DDS struct {
	W, H    int
	Format  PixelFormat
	MipData [][]byte // MipData[0] is the largest level
}

// Image decodes the base mip level to RGBA.
func (d *DDS) Image() *dxt.Image {
	if len(d.MipData) == 0 {
		return dxt.NewImage(d.W, d.H)
	}
	return DecodeBlocks(d.Format, d.MipData[0], d.W, d.H)
}

// DecodeBlocks decodes raw block/pixel data of a DDS format.
func DecodeBlocks(f PixelFormat, data []byte, w, h int) *dxt.Image {
	switch f {
	case PixelDXT1:
		return dxt.DecodeDXT1(data, w, h)
	case PixelDXT3:
		return dxt.DecodeDXT3(data, w, h)
	case PixelDXT5:
		return dxt.DecodeDXT5(data, w, h)
	default:
		out := dxt.NewImage(w, h)
		for i := 0; i < w*h && i*4+3 < len(data); i++ {
			out.Pix[i] = dxt.RGBA{R: data[i*4+0], G: data[i*4+1], B: data[i*4+2], A: data[i*4+3]}
		}
		return out
	}
}

// BlockSize returns the stored size of one mip level.
func BlockSize(f PixelFormat, w, h int) int {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	switch f {
	case PixelDXT1:
		return dxt.BlocksW(w) * dxt.BlocksH(h) * 8
	case PixelDXT3, PixelDXT5:
		return dxt.BlocksW(w) * dxt.BlocksH(h) * 16
	default:
		return w * h * 4
	}
}

// DecodeDDS parses a DDS file.
func DecodeDDS(data []byte) (*DDS, error) {
	if len(data) < 128 || string(data[0:4]) != "DDS " {
		return nil, errors.New("not a DDS file")
	}
	hdr := data[4:128]
	h := int(binary.LittleEndian.Uint32(hdr[8:]))  // height
	w := int(binary.LittleEndian.Uint32(hdr[12:])) // width
	mipCount := int(binary.LittleEndian.Uint32(hdr[24:]))
	if mipCount == 0 {
		mipCount = 1
	}
	pfFlags := binary.LittleEndian.Uint32(hdr[76:])
	fourCC := string(hdr[80:84])
	rgbBits := binary.LittleEndian.Uint32(hdr[84:])

	var format PixelFormat
	switch {
	case pfFlags&0x4 != 0: // DDPF_FOURCC
		switch fourCC {
		case "DXT1":
			format = PixelDXT1
		case "DXT3":
			format = PixelDXT3
		case "DXT5":
			format = PixelDXT5
		default:
			return nil, fmt.Errorf("unsupported DDS compression %q", fourCC)
		}
	case pfFlags&0x40 != 0 && rgbBits == 32: // DDPF_RGB(A)
		format = PixelRGBA8
	default:
		return nil, errors.New("unsupported DDS pixel format")
	}
	if w <= 0 || h <= 0 || w > 16384 || h > 16384 {
		return nil, errors.New("bad DDS dimensions")
	}

	d := &DDS{W: w, H: h, Format: format}
	off := 128
	cw, ch := w, h
	for i := 0; i < mipCount; i++ {
		size := BlockSize(format, cw, ch)
		if off+size > len(data) {
			break
		}
		d.MipData = append(d.MipData, data[off:off+size])
		off += size
		cw /= 2
		ch /= 2
		if cw < 1 {
			cw = 1
		}
		if ch < 1 {
			ch = 1
		}
	}
	if len(d.MipData) == 0 {
		return nil, errors.New("DDS contains no mip data")
	}
	return d, nil
}

// EncodeDDS writes a DDS file with all supplied mip levels (largest first).
func EncodeDDS(format PixelFormat, w, h int, mips [][]byte) []byte {
	var fourCC [4]byte
	switch format {
	case PixelDXT1:
		copy(fourCC[:], "DXT1")
	case PixelDXT3:
		copy(fourCC[:], "DXT3")
	case PixelDXT5:
		copy(fourCC[:], "DXT5")
	}
	out := make([]byte, 0, 128+func() int {
		n := 0
		for _, m := range mips {
			n += len(m)
		}
		return n
	}())
	out = append(out, 'D', 'D', 'S', ' ')

	flags := uint32(0x1 | 0x2 | 0x4 | 0x1000) // CAPS|HEIGHT|WIDTH|PIXELFORMAT
	if len(mips) > 1 {
		flags |= 0x20000 // MIPMAPCOUNT
	}
	var hdr [124]byte
	binary.LittleEndian.PutUint32(hdr[0:], 124)
	binary.LittleEndian.PutUint32(hdr[4:], flags)
	binary.LittleEndian.PutUint32(hdr[8:], uint32(h))
	binary.LittleEndian.PutUint32(hdr[12:], uint32(w))
	pitch := 0
	switch format {
	case PixelRGBA8:
		pitch = w * 4
	case PixelDXT1:
		pitch = dxt.BlocksW(w) * 8
	default:
		pitch = dxt.BlocksW(w) * 16
	}
	binary.LittleEndian.PutUint32(hdr[16:], uint32(pitch))
	binary.LittleEndian.PutUint32(hdr[20:], 0) // depth
	binary.LittleEndian.PutUint32(hdr[24:], uint32(len(mips)))
	// reserved[11] at 28..71 stays zero

	// DDS_PIXELFORMAT at 72..103
	pfSize := uint32(32)
	var pfFlags uint32
	binary.LittleEndian.PutUint32(hdr[72:], pfSize)
	if format == PixelRGBA8 {
		pfFlags = 0x1 | 0x40 // ALPHAPIXELS | RGB
		binary.LittleEndian.PutUint32(hdr[80:], 32) // RGBBitCount
		binary.LittleEndian.PutUint32(hdr[84:], 0x000000FF)
		binary.LittleEndian.PutUint32(hdr[88:], 0x0000FF00)
		binary.LittleEndian.PutUint32(hdr[92:], 0x00FF0000)
		binary.LittleEndian.PutUint32(hdr[96:], 0xFF000000)
	} else {
		pfFlags = 0x4 // FOURCC
		copy(hdr[80:84], fourCC[:])
	}
	binary.LittleEndian.PutUint32(hdr[76:], pfFlags)

	// caps
	var caps uint32 = 0x1000 // TEXTURE
	if len(mips) > 1 {
		caps |= 0x8 | 0x400000 // COMPLEX|MIPMAP
	}
	binary.LittleEndian.PutUint32(hdr[104:], caps)
	out = append(out, hdr[:]...)
	for _, m := range mips {
		out = append(out, m...)
	}
	return out
}
