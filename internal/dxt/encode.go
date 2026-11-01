package dxt

import (
	"encoding/binary"
	"math"
)

// EncodeDXT1 compresses an image to DXT1 (BC1) blocks, honouring 1-bit alpha.
func EncodeDXT1(img *Image) []byte {
	bw, bh := blocks(img.W), blocks(img.H)
	out := make([]byte, 0, bw*bh*8)
	var blk [16]RGBA
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			gather(img, bx, by, &blk)
			hasAlpha := false
			for _, c := range blk {
				if c.A < 128 {
					hasAlpha = true
					break
				}
			}
			c0, c1, idx := fitColorBlock(blk, hasAlpha)
			out = append(out, byte(c0), byte(c0>>8), byte(c1), byte(c1>>8))
			var v uint32
			for i := 0; i < 16; i++ {
				v |= uint32(idx[i]&3) << (2 * uint(i))
			}
			out = binary.LittleEndian.AppendUint32(out, v)
		}
	}
	return out
}

// EncodeDXT3 compresses an image to DXT3 (BC2) blocks.
func EncodeDXT3(img *Image) []byte {
	bw, bh := blocks(img.W), blocks(img.H)
	out := make([]byte, 0, bw*bh*16)
	var blk [16]RGBA
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			gather(img, bx, by, &blk)
			var alpha [8]byte
			for i := 0; i < 8; i++ {
				lo := uint8(blk[i*2].A) >> 4
				hi := uint8(blk[i*2+1].A) >> 4
				alpha[i] = lo | hi<<4
			}
			c0, c1, idx := fitColorBlock(blk, false)
			out = append(out, alpha[:]...)
			out = append(out, byte(c0), byte(c0>>8), byte(c1), byte(c1>>8))
			var v uint32
			for i := 0; i < 16; i++ {
				v |= uint32(idx[i]&3) << (2 * uint(i))
			}
			out = binary.LittleEndian.AppendUint32(out, v)
		}
	}
	return out
}

// EncodeDXT5 compresses an image to DXT5 (BC3) blocks.
func EncodeDXT5(img *Image) []byte {
	bw, bh := blocks(img.W), blocks(img.H)
	out := make([]byte, 0, bw*bh*16)
	var blk [16]RGBA
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			gather(img, bx, by, &blk)
			a0, a1, aidx := fitAlphaBlock(blk)
			var bits uint64
			for i := 0; i < 16; i++ {
				bits |= uint64(aidx[i]&7) << (3 * uint(i))
			}
			out = append(out, a0, a1)
			for i := 0; i < 6; i++ {
				out = append(out, byte(bits>>(8*uint(i))))
			}
			c0, c1, idx := fitColorBlock(blk, false)
			out = append(out, byte(c0), byte(c0>>8), byte(c1), byte(c1>>8))
			var v uint32
			for i := 0; i < 16; i++ {
				v |= uint32(idx[i]&3) << (2 * uint(i))
			}
			out = binary.LittleEndian.AppendUint32(out, v)
		}
	}
	return out
}

// gather copies a 4x4 pixel block, replicating edge pixels at image borders.
func gather(img *Image, bx, by int, blk *[16]RGBA) {
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			blk[4*y+x] = img.At(bx*4+x, by*4+y)
		}
	}
}

type candidate struct {
	c0, c1 uint16
	err    int
	idx    [16]uint8
}

// fitColorBlock picks the best colour endpoints for a block and returns the
// quantized palette indices.
func fitColorBlock(blk [16]RGBA, transparent bool) (uint16, uint16, [16]uint8) {
	// Candidate endpoint pairs: bounding box, inset box toward the mean, and
	// the principal axis of the colour distribution.
	var minC, maxC, mean RGBA
	minC = RGBA{255, 255, 255, 255}
	maxC = RGBA{0, 0, 0, 0}
	var sumR, sumG, sumB int
	for _, c := range blk {
		if c.R < minC.R {
			minC.R = c.R
		}
		if c.G < minC.G {
			minC.G = c.G
		}
		if c.B < minC.B {
			minC.B = c.B
		}
		if c.R > maxC.R {
			maxC.R = c.R
		}
		if c.G > maxC.G {
			maxC.G = c.G
		}
		if c.B > maxC.B {
			maxC.B = c.B
		}
		sumR += int(c.R)
		sumG += int(c.G)
		sumB += int(c.B)
	}
	mean = RGBA{uint8(sumR / 16), uint8(sumG / 16), uint8(sumB / 16), 255}

	cands := make([]candidate, 0, 6)
	add := func(a, b RGBA) {
		c := evalEndpoints(blk, to565(a), to565(b), transparent)
		cands = append(cands, c)
	}
	add(RGBA{minC.R, minC.G, minC.B, 255}, RGBA{maxC.R, maxC.G, maxC.B, 255})
	add(inset(minC, maxC), inset(maxC, minC))

	// Principal axis: approximate by the axis of largest variance.
	axis := principalAxis(blk, mean)
	if axis != (vec3{}) {
		lo, hi := projectExtremes(blk, mean, axis)
		add(lo, hi)
	}
	// Grayscale fallback for nearly flat blocks.
	add(mean, mean)

	best := cands[0]
	for _, c := range cands[1:] {
		if c.err < best.err {
			best = c
		}
	}
	return best.c0, best.c1, best.idx
}

func inset(a, b RGBA) RGBA {
	// Move a slightly toward b (1/16) which usually improves the fit.
	return RGBA{
		R: uint8(int(a.R) + (int(b.R)-int(a.R))/16),
		G: uint8(int(a.G) + (int(b.G)-int(a.G))/16),
		B: uint8(int(a.B) + (int(b.B)-int(a.B))/16),
		A: 255,
	}
}

type vec3 struct{ x, y, z float64 }

func principalAxis(blk [16]RGBA, mean RGBA) vec3 {
	var xx, xy, xz, yy, yz, zz float64
	for _, c := range blk {
		r := float64(int(c.R) - int(mean.R))
		g := float64(int(c.G) - int(mean.G))
		b := float64(int(c.B) - int(mean.B))
		xx += r * r
		xy += r * g
		xz += r * b
		yy += g * g
		yz += g * b
		zz += b * b
	}
	// Power iteration on the covariance matrix.
	v := vec3{1, 1, 1}
	for i := 0; i < 8; i++ {
		nx := xx*v.x + xy*v.y + xz*v.z
		ny := xy*v.x + yy*v.y + yz*v.z
		nz := xz*v.x + yz*v.y + zz*v.z
		n := math.Sqrt(nx*nx + ny*ny + nz*nz)
		if n < 1e-6 {
			return vec3{}
		}
		v = vec3{nx / n, ny / n, nz / n}
	}
	return v
}

func projectExtremes(blk [16]RGBA, mean RGBA, axis vec3) (RGBA, RGBA) {
	loT, hiT := math.Inf(1), math.Inf(-1)
	for _, c := range blk {
		t := float64(int(c.R)-int(mean.R))*axis.x +
			float64(int(c.G)-int(mean.G))*axis.y +
			float64(int(c.B)-int(mean.B))*axis.z
		if t < loT {
			loT = t
		}
		if t > hiT {
			hiT = t
		}
	}
	clamp := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v + 0.5)
	}
	lo := RGBA{
		clamp(float64(mean.R) + loT*axis.x),
		clamp(float64(mean.G) + loT*axis.y),
		clamp(float64(mean.B) + loT*axis.z),
		255,
	}
	hi := RGBA{
		clamp(float64(mean.R) + hiT*axis.x),
		clamp(float64(mean.G) + hiT*axis.y),
		clamp(float64(mean.B) + hiT*axis.z),
		255,
	}
	return lo, hi
}

// evalEndpoints quantizes a block against a candidate endpoint pair and scores it.
func evalEndpoints(blk [16]RGBA, c0, c1 uint16, transparent bool) candidate {
	if transparent {
		// DXT1 3-colour mode requires c0 <= c1 so index 3 means transparent.
		if c0 > c1 {
			c0, c1 = c1, c0
		}
	} else {
		// The 4-colour mode requires c0 > c1; order the endpoints accordingly.
		if c0 < c1 {
			c0, c1 = c1, c0
		}
		if c0 == c1 {
			if c1 > 0 {
				c1--
			} else {
				c0 = 1
			}
		}
	}
	pal := buildColorPalette(c0, c1, transparent)
	res := candidate{c0: c0, c1: c1}
	for i, c := range blk {
		if transparent && c.A < 128 {
			res.idx[i] = 3
			continue
		}
		bestIdx, bestErr := 0, math.MaxInt
		for k := 0; k < 4; k++ {
			if transparent && k == 3 {
				continue // transparent slot holds black
			}
			e := colorErr(c, pal[k])
			if e < bestErr {
				bestErr, bestIdx = e, k
			}
		}
		res.idx[i] = uint8(bestIdx)
		res.err += bestErr
	}
	return res
}

func colorErr(a, b RGBA) int {
	dr := int(a.R) - int(b.R)
	dg := int(a.G) - int(b.G)
	db := int(a.B) - int(b.B)
	return dr*dr + dg*dg + db*db
}

// fitAlphaBlock computes the DXT5 alpha endpoints and indices.
func fitAlphaBlock(blk [16]RGBA) (byte, byte, [16]uint8) {
	a0, a1 := uint8(0), uint8(255)
	// 8-value mode (a0 > a1) preserves detail best; find min/max.
	minA, maxA := uint8(255), uint8(0)
	for _, c := range blk {
		if c.A < minA {
			minA = c.A
		}
		if c.A > maxA {
			maxA = c.A
		}
	}
	if maxA == minA {
		a0, a1 = maxA, maxA
	} else {
		a0, a1 = maxA, minA
	}
	pal := alphaPalette(a0, a1)
	var idx [16]uint8
	for i, c := range blk {
		bestIdx, bestErr := 0, math.MaxInt
		for k := 0; k < 8; k++ {
			d := int(c.A) - int(pal[k])
			if d < 0 {
				d = -d
			}
			e := d * d
			if e < bestErr {
				bestErr, bestIdx = e, k
			}
		}
		idx[i] = uint8(bestIdx)
	}
	return a0, a1, idx
}

func alphaPalette(a0, a1 uint8) [8]uint8 {
	var pal [8]uint8
	pal[0], pal[1] = a0, a1
	if a0 > a1 {
		for i := 1; i <= 6; i++ {
			pal[i+1] = uint8(((7-i)*int(a0) + i*int(a1)) / 7)
		}
	} else {
		for i := 1; i <= 4; i++ {
			pal[i+1] = uint8(((5-i)*int(a0) + i*int(a1)) / 5)
		}
		pal[6] = 0
		pal[7] = 255
	}
	return pal
}

// GenerateMips builds a full mip chain (down to 1x1) by box filtering.
func GenerateMips(img *Image) []*Image {
	mips := []*Image{img}
	for img.W > 1 || img.H > 1 {
		w := img.W / 2
		h := img.H / 2
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		next := NewImage(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var sum [4]int
				n := 0
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						sx, sy := x*2+dx, y*2+dy
						if sx >= img.W || sy >= img.H {
							continue
						}
						c := img.Pix[sy*img.W+sx]
						sum[0] += int(c.R)
						sum[1] += int(c.G)
						sum[2] += int(c.B)
						sum[3] += int(c.A)
						n++
					}
				}
				if n == 0 {
					continue
				}
				next.Pix[y*w+x] = RGBA{
					uint8(sum[0] / n), uint8(sum[1] / n), uint8(sum[2] / n), uint8(sum[3] / n),
				}
			}
		}
		mips = append(mips, next)
		img = next
	}
	return mips
}

// ResizeTo scales an image to the given size with bilinear filtering.
func ResizeTo(img *Image, w, h int) *Image {
	if img.W == w && img.H == h {
		return img
	}
	out := NewImage(w, h)
	for y := 0; y < h; y++ {
		fy := (float64(y) + 0.5) * float64(img.H) / float64(h) - 0.5
		for x := 0; x < w; x++ {
			fx := (float64(x) + 0.5) * float64(img.W) / float64(w) - 0.5
			out.Pix[y*w+x] = sampleBilinear(img, fx, fy)
		}
	}
	return out
}

func sampleBilinear(img *Image, fx, fy float64) RGBA {
	x0 := int(math.Floor(fx))
	y0 := int(math.Floor(fy))
	tx := fx - float64(x0)
	ty := fy - float64(y0)
	c00 := img.At(x0, y0)
	c10 := img.At(x0+1, y0)
	c01 := img.At(x0, y0+1)
	c11 := img.At(x0+1, y0+1)
	lerp := func(a, b uint8, t float64) float64 { return float64(a) + (float64(b)-float64(a))*t }
	blend := func(c00, c10, c01, c11 uint8) uint8 {
		top := lerp(c00, c10, tx)
		bot := lerp(c01, c11, tx)
		return uint8(top + (bot-top)*ty + 0.5)
	}
	return RGBA{
		blend(c00.R, c10.R, c01.R, c11.R),
		blend(c00.G, c10.G, c01.G, c11.G),
		blend(c00.B, c10.B, c01.B, c11.B),
		blend(c00.A, c10.A, c01.A, c11.A),
	}
}
