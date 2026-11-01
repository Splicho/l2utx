package main

import (
	"fmt"
	"os"

	"l2utx/internal/upkg"
)

// Compare export sizes between two packages (used to debug round-trip drift).
func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: cmpsize <a.utx> <b.utx>")
		return
	}
	a := mustParse(os.Args[1])
	var b *upkg.Package
	if os.Args[2] == "-self" {
		out, err := a.Marshal()
		if err != nil {
			fmt.Println("marshal error:", err)
			return
		}
		fmt.Printf("marshalled %d bytes (original %d)\n", len(out), a.OriginalSize)
		orig := mustRead(os.Args[1])
		for i := 0; i < len(out) && i < len(orig); i++ {
			if out[i] != orig[i] {
				fmt.Printf("first difference at %d (%#x)\n", i, i)
				lo := i - 16
				if lo < 0 {
					lo = 0
				}
				hi := i + 48
				if hi > len(orig) {
					hi = len(orig)
				}
				fmt.Printf("  orig: % x\n", orig[lo:hi])
				hi2 := i + 48
				if hi2 > len(out) {
					hi2 = len(out)
				}
				fmt.Printf("  mine: % x\n", out[lo:hi2])
				break
			}
		}
		_ = os.WriteFile(os.Getenv("TEMP")+`\marshal.bin`, out, 0o644)
		b, _ = upkg.Parse(out)
	} else {
		b = mustParse(os.Args[2])
	}
	fmt.Printf("names %d vs %d, exports %d vs %d\n", len(a.Names), len(b.Names), len(a.Exports), len(b.Exports))
	fmt.Printf("header %d vs %d, gens %d vs %d, headerextra %d vs %d\n",
		a.HeaderSize, b.HeaderSize, len(a.Generations), len(b.Generations), len(a.HeaderExtra), len(b.HeaderExtra))
	fmt.Printf("imports %d vs %d, trailer %d vs %d, tailpad %d vs %d\n",
		len(a.Imports), len(b.Imports), len(a.Trailer), len(b.Trailer), len(a.TailPad), len(b.TailPad))
	padA, padB := 0, 0
	for i := range a.Exports {
		padA += len(a.Exports[i].Pad)
	}
	for i := range b.Exports {
		padB += len(b.Exports[i].Pad)
	}
	fmt.Printf("export pads %d vs %d\n", padA, padB)
	dataA, dataB := 0, 0
	for i := range a.Exports {
		dataA += int(a.Exports[i].SerialSize)
	}
	for i := range b.Exports {
		dataB += int(b.Exports[i].SerialSize)
	}
	fmt.Printf("data bytes %d vs %d\n", dataA, dataB)
	if len(a.Exports) > 0 && len(b.Exports) > 0 {
		la := a.Exports[0].SerialOffset
		lb := b.Exports[0].SerialOffset
		fmt.Printf("first export at %d vs %d\n", la, lb)
	}
	fmt.Printf("sizes: a=%d b=%d\n", a.OriginalSize, b.OriginalSize)
	fmt.Printf("trailer hex a=% x\n", a.Trailer)
	fmt.Printf("trailer hex b=% x\n", b.Trailer)
	for i := 0; i < len(a.Exports) && i < len(b.Exports); i++ {
		ea, eb := &a.Exports[i], &b.Exports[i]
		if ea.SerialSize != eb.SerialSize {
			fmt.Printf("export %-4d %-28s size %d -> %d (%+d)\n", i, a.Name(ea.ObjectName),
				ea.SerialSize, eb.SerialSize, eb.SerialSize-ea.SerialSize)
		}
	}
}

func mustParse(path string) *upkg.Package {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	p, err := upkg.Parse(data)
	if err != nil {
		panic(err)
	}
	p.Textures()
	return p
}

func mustRead(path string) []byte {
	d, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return d
}
