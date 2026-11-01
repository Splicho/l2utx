package main

import (
	"fmt"
	"os"

	"l2utx/internal/upkg"
)

// Dump a texture payload: property list, then the raw tail, to reverse engineer
// layouts the generic parser does not understand yet.
func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	want := ""
	if len(os.Args) > 2 {
		want = os.Args[2]
	}
	pkg, err := upkg.Parse(data)
	if err != nil {
		panic(err)
	}
	for _, te := range pkg.Textures() {
		if te.Skipped || te.Err == nil {
			continue
		}
		if want != "" && te.Name != want {
			continue
		}
		e := &pkg.Exports[te.Index]
		fmt.Printf("=== %s  class=%s  size=%d  err=%v\n", te.Name, te.Class, len(e.Data), te.Err)
		if props, perr := pkg.ParseProps(e.Data); perr == nil {
			fmt.Printf("  props ok: %d entries, end=%d\n", len(props.List), props.End)
			for _, p := range props.List {
				fmt.Printf("    %-20s type=%#02x len=%d\n", p.Name, p.Type, p.ValueLen)
			}
			tail := e.Data[props.End:]
			n := len(tail)
			if n > 96 {
				n = 96
			}
			fmt.Printf("  tail[%d]: % x\n", len(tail), tail[:n])
		} else {
			fmt.Printf("  props error: %v\n", perr)
			n := len(e.Data)
			if n > 128 {
				n = 128
			}
			fmt.Printf("  head: % x\n", e.Data[:n])
			// Scan for a valid mip chain start.
			for start := 1; start < len(e.Data) && start < 512; start++ {
				if ok := tryChain(e.Data[start:]); ok {
					fmt.Printf("  valid chain at offset %d\n", start)
					break
				}
			}
		}
	}
}

func tryChain(p []byte) bool {
	_ = p
	return false
}
