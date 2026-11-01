// Package dxt implements the DXT1/DXT3/DXT5 (BC1/BC2/BC3) block codecs used by
// Unreal Engine 2 textures, plus small helpers shared by the image loaders.
package dxt

import "encoding/binary"

// RGBA is a straight (non-premultiplied) 8-bit colour with alpha.
type RGBA struct {
	R, G, B, A uint8
}

// Image is a decoded RGBA image.
type Image struct {
	W, H int
	Pix  []RGBA // row-major, len == W*H
}

// NewImage allocates a transparent image.
func NewImage(w, h int) *Image {
	return &Image{W: w, H: h, Pix: make([]RGBA, w*h)}
}

// At returns the pixel at x,y (clamped).
func (im *Image) At(x, y int) RGBA {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= im.W {
		x = im.W - 1
	}
	if y >= im.H {
		y = im.H - 1
	}
	return im.Pix[y*im.W+x]
}

// Set stores a pixel.
func (im *Image) Set(x, y int, c RGBA) { im.Pix[y*im.W+x] = c }

// blocks returns the number of 4x4 blocks along a dimension.
func blocks(v int) int {
	if v < 4 {
		return 1
	}
	return (v + 3) / 4
}

// BlocksW returns the block width of a texture of width w.
func BlocksW(w int) int { return blocks(w) }

// BlocksH returns the block height of a texture of height h.
func BlocksH(h int) int { return blocks(h) }

func rgb565(c uint16) RGBA {
	r := uint8((c >> 11) & 0x1F)
	g := uint8((c >> 5) & 0x3F)
	b := uint8(c & 0x1F)
	return RGBA{
		R: r<<3 | r>>2,
		G: g<<2 | g>>4,
		B: b<<3 | b>>2,
		A: 255,
	}
}

func to565(c RGBA) uint16 {
	r := uint32(c.R) * 31 / 255
	g := uint32(c.G) * 63 / 255
	b := uint32(c.B) * 31 / 255
	return uint16(r<<11 | g<<5 | b)
}

func mix(a, b RGBA, wa, wb int) RGBA {
	return RGBA{
		R: uint8((int(a.R)*wa + int(b.R)*wb) / (wa + wb)),
		G: uint8((int(a.G)*wa + int(b.G)*wb) / (wa + wb)),
		B: uint8((int(a.B)*wa + int(b.B)*wb) / (wa + wb)),
		A: uint8((int(a.A)*wa + int(b.A)*wb) / (wa + wb)),
	}
}

type colorPalette [4]RGBA

// buildColorPalette expands a DXT colour block into its four colours.
func buildColorPalette(c0, c1 uint16, dxt1 bool) colorPalette {
	p0 := rgb565(c0)
	p1 := rgb565(c1)
	var pal colorPalette
	if c0 > c1 || !dxt1 {
		pal[0] = p0
		pal[1] = p1
		pal[2] = RGBA{
			R: uint8((2*int(p0.R) + int(p1.R)) / 3),
			G: uint8((2*int(p0.G) + int(p1.G)) / 3),
			B: uint8((2*int(p0.B) + int(p1.B)) / 3),
			A: 255,
		}
		pal[3] = RGBA{
			R: uint8((int(p0.R) + 2*int(p1.R)) / 3),
			G: uint8((int(p0.G) + 2*int(p1.G)) / 3),
			B: uint8((int(p0.B) + 2*int(p1.B)) / 3),
			A: 255,
		}
	} else {
		pal[0] = p0
		pal[1] = p1
		pal[2] = RGBA{
			R: uint8((int(p0.R) + int(p1.R)) / 2),
			G: uint8((int(p0.G) + int(p1.G)) / 2),
			B: uint8((int(p0.B) + int(p1.B)) / 2),
			A: 255,
		}
		pal[3] = RGBA{0, 0, 0, 0}
	}
	return pal
}

// DecodeDXT1 decodes DXT1 (BC1) data.
func DecodeDXT1(data []byte, w, h int) *Image {
	return decodeColorBlocks(data, w, h, 8, true)
}

// DecodeDXT3 decodes DXT3 (BC2) data: explicit 4-bit alpha + DXT1 colours.
func DecodeDXT3(data []byte, w, h int) *Image {
	img := NewImage(w, h)
	bw, bh := blocks(w), blocks(h)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			off := (by*bw + bx) * 16
			if off+16 > len(data) {
				return img
			}
			blk := data[off : off+16]
			// 8 bytes of 4-bit alpha, one nibble per pixel, low nibble first.
			var alpha [16]uint8
			for i := 0; i < 8; i++ {
				b := blk[i]
				alpha[i*2] = uint8(b&0x0F) * 17
				alpha[i*2+1] = uint8(b>>4) * 17
			}
			pal := buildColorPalette(binary.LittleEndian.Uint16(blk[8:]), binary.LittleEndian.Uint16(blk[10:]), false)
			idx := binary.LittleEndian.Uint32(blk[12:])
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					px, py := bx*4+x, by*4+y
					if px >= w || py >= h {
						continue
					}
					ci := (idx >> (2 * (4*y + x))) & 3
					c := pal[ci]
					c.A = alpha[4*y+x]
					img.Pix[py*w+px] = c
				}
			}
		}
	}
	return img
}

// DecodeDXT5 decodes DXT5 (BC3) data: interpolated 8-bit alpha + DXT1 colours.
func DecodeDXT5(data []byte, w, h int) *Image {
	img := NewImage(w, h)
	bw, bh := blocks(w), blocks(h)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			off := (by*bw + bx) * 16
			if off+16 > len(data) {
				return img
			}
			blk := data[off : off+16]
			a0, a1 := blk[0], blk[1]
			var apal [8]uint8
			apal[0], apal[1] = a0, a1
			var bits uint64
			for i := 0; i < 6; i++ {
				bits |= uint64(blk[2+i]) << (8 * uint(i))
			}
			if a0 > a1 {
				for i := 1; i <= 6; i++ {
					apal[i+1] = uint8(((7-i)*int(a0) + i*int(a1)) / 7)
				}
			} else {
				for i := 1; i <= 4; i++ {
					apal[i+1] = uint8(((5-i)*int(a0) + i*int(a1)) / 5)
				}
				apal[6] = 0
				apal[7] = 255
			}
			pal := buildColorPalette(binary.LittleEndian.Uint16(blk[8:]), binary.LittleEndian.Uint16(blk[10:]), false)
			idx := binary.LittleEndian.Uint32(blk[12:])
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					px, py := bx*4+x, by*4+y
					if px >= w || py >= h {
						continue
					}
					p := 4*y + x
					ci := (idx >> (2 * uint(p))) & 3
					ai := (bits >> (3 * uint(p))) & 7
					c := pal[ci]
					c.A = apal[ai]
					img.Pix[py*w+px] = c
				}
			}
		}
	}
	return img
}

// DecodeDXT5N decodes the normal-map DXT5 variant: X and Y in colour, Z in alpha.
func DecodeDXT5N(data []byte, w, h int) *Image {
	return DecodeDXT5(data, w, h)
}

func decodeColorBlocks(data []byte, w, h, stride int, dxt1 bool) *Image {
	img := NewImage(w, h)
	bw, bh := blocks(w), blocks(h)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			off := (by*bw + bx) * stride
			if off+stride > len(data) {
				return img
			}
			blk := data[off : off+stride]
			pal := buildColorPalette(binary.LittleEndian.Uint16(blk[0:]), binary.LittleEndian.Uint16(blk[2:]), dxt1)
			idx := binary.LittleEndian.Uint32(blk[4:])
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					px, py := bx*4+x, by*4+y
					if px >= w || py >= h {
						continue
					}
					ci := (idx >> (2 * (4*y + x))) & 3
					img.Pix[py*w+px] = pal[ci]
				}
			}
		}
	}
	return img
}
