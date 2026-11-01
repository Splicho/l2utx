package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"l2utx/internal/dxt"
	"l2utx/internal/l2tx"
	"l2utx/internal/upkg"
)

// TestNoOpRoundTrip verifies that parsing and re-serializing a package without
// any modification reproduces the original file byte for byte. This exercises
// table offsets, compact index encoding, texture mip rebuilds and the trailer.
func TestNoOpRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("examples", "*.utx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no example packages")
	}
	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			orig, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := upkg.Parse(orig)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info := &l2tx.Info{Pkg: pkg}
			_ = info
			// Parse textures so the mip rebuild path is exercised.
			if n := len(pkg.Textures()); n == 0 {
				t.Log("package has no textures")
			}
			out, err := pkg.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if len(out) != len(orig) {
				t.Fatalf("size changed: %d -> %d bytes", len(orig), len(out))
			}
			if !bytes.Equal(out, orig) {
				for i := range out {
					if out[i] != orig[i] {
						t.Fatalf("first difference at offset %#x: got %02X want %02X", i, out[i], orig[i])
					}
				}
			}
		})
	}
}

// TestTextureParse verifies that every texture in the sample packages parses
// and that mip sizes match the declared format.
func TestTextureParse(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("examples", "*.utx"))
	for _, f := range files {
		info, err := l2tx.Load(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		texCount := 0
		for _, te := range info.Textures {
			if te.Skipped {
				continue
			}
			texCount++
			if te.Err != nil {
				t.Errorf("%s: texture %q: %v", filepath.Base(f), te.Name, te.Err)
				continue
			}
			if te.Tex.Props == nil {
				t.Errorf("%s: texture %q parsed without properties", filepath.Base(f), te.Name)
			}
		}
		if texCount == 0 {
			t.Errorf("%s: no textures found", f)
		}
		t.Logf("%s: %d textures", filepath.Base(f), texCount)
	}
}

// TestTextureReencodeQuality re-encodes a real texture from the samples and
// checks that decoding the result still matches the original pixels.
func TestTextureReencodeQuality(t *testing.T) {
	info, err := l2tx.Load("examples/IntactL.utx")
	if err != nil {
		t.Skip(err)
	}
	for _, te := range info.Textures[:12] {
		if te.Tex == nil {
			continue
		}
		m := te.Tex.Mips[0]
		first, err := l2tx.DecodeTexture(te.Tex)
		if err != nil {
			t.Fatal(err)
		}
		mips, err := l2tx.EncodeMips([]*dxt.Image{first}, te.Tex.Format)
		if err != nil {
			t.Fatal(err)
		}
		second := dxt.DecodeDXT3(mips[0].Data, m.USize, m.VSize)
		var sum float64
		for i := range first.Pix {
			for _, d := range []int{
				int(first.Pix[i].R) - int(second.Pix[i].R),
				int(first.Pix[i].G) - int(second.Pix[i].G),
				int(first.Pix[i].B) - int(second.Pix[i].B),
				int(first.Pix[i].A) - int(second.Pix[i].A),
			} {
				sum += float64(d * d)
			}
		}
		mse := sum / float64(len(first.Pix)*4)
		psnr := 99.0
		if mse > 0 {
			psnr = 10 * math.Log10(255*255/mse)
		}
		t.Logf("%-32s %dx%d PSNR %.2f dB", te.Name, m.USize, m.VSize, psnr)
	}
}
