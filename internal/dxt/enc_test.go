package dxt

import (
	"math"
	"math/rand"
	"testing"
)

func psnr(a, b *Image) float64 {
	var sum float64
	n := 0
	for i := range a.Pix {
		for _, d := range []int{
			int(a.Pix[i].R) - int(b.Pix[i].R),
			int(a.Pix[i].G) - int(b.Pix[i].G),
			int(a.Pix[i].B) - int(b.Pix[i].B),
			int(a.Pix[i].A) - int(b.Pix[i].A),
		} {
			sum += float64(d * d)
			n++
		}
	}
	if n == 0 {
		return 99
	}
	mse := sum / float64(n)
	if mse == 0 {
		return 99
	}
	return 10 * math.Log10(255*255/mse)
}

// TestSmoothRoundTrip checks the encoders on a smooth gradient.
func TestSmoothRoundTrip(t *testing.T) {
	img := NewImage(64, 64)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	for name, enc := range map[string]func(*Image) []byte{
		"dxt1": EncodeDXT1, "dxt3": EncodeDXT3, "dxt5": EncodeDXT5,
	} {
		data := enc(img)
		var dec *Image
		switch name {
		case "dxt1":
			dec = DecodeDXT1(data, 64, 64)
		case "dxt3":
			dec = DecodeDXT3(data, 64, 64)
		default:
			dec = DecodeDXT5(data, 64, 64)
		}
		got := psnr(img, dec)
		t.Logf("%s gradient PSNR %.2f dB", name, got)
		if got < 32 {
			t.Errorf("%s gradient quality too low: %.2f dB", name, got)
		}
	}
}

// TestReencodeIsStable checks that re-encoding a decoded image keeps quality high.
func TestReencodeIsStable(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	src := NewImage(128, 128)
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			src.Set(x, y, RGBA{
				R: uint8((x*7 + rng.Intn(24)) & 0xFF),
				G: uint8((y*5 + rng.Intn(24)) & 0xFF),
				B: uint8((x + y + rng.Intn(24)) & 0xFF),
				A: uint8(255 - rng.Intn(40)),
			})
		}
	}
	first := DecodeDXT3(EncodeDXT3(src), 128, 128)
	second := DecodeDXT3(EncodeDXT3(first), 128, 128)
	got := psnr(first, second)
	t.Logf("re-encode PSNR %.2f dB", got)
	if got < 32 {
		t.Errorf("re-encode quality too low: %.2f dB", got)
	}
}
