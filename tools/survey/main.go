// Command survey walks a folder of packages and reports how many parse cleanly.
// It is a development aid for stress-testing the reader against real game files.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"l2utx/internal/upkg"
)

type failure struct {
	path string
	err  string
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	maxFiles := 0
	if len(os.Args) > 2 {
		fmt.Sscanf(os.Args[2], "%d", &maxFiles)
	}
	var (
		total, plain, parsed, withTex, texParsed, texFailed int
		fails                                               []failure
	)
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".utx" && ext != ".u" && ext != ".ukx" && ext != ".usx" && ext != ".unr" {
			return nil
		}
		total++
		if maxFiles > 0 && total > maxFiles {
			return filepath.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fails = append(fails, failure{path, err.Error()})
			return nil
		}
		info := upkg.Identify(data)
		if !info.IsPackage {
			plain++
			return nil
		}
		pkg, err := upkg.Parse(data)
		if err != nil {
			fails = append(fails, failure{path, err.Error()})
			return nil
		}
		parsed++
		// exercise the writer too
		if out, err := pkg.Marshal(); err != nil {
			fails = append(fails, failure{path, "marshal: " + err.Error()})
		} else if len(out) != len(data) {
			fails = append(fails, failure{path, fmt.Sprintf("marshal size %d != %d", len(out), len(data))})
		}
		for _, te := range pkg.Textures() {
			if te.Skipped {
				continue
			}
			withTex++
			if te.Err != nil {
				texFailed++
				fails = append(fails, failure{path, fmt.Sprintf("texture %s: %v", te.Name, te.Err)})
			} else {
				texParsed++
			}
		}
		return nil
	})
	fmt.Printf("packages seen      : %d\n", total)
	fmt.Printf("non-UE2 / wrapped  : %d\n", plain)
	fmt.Printf("parsed ok          : %d\n", parsed)
	fmt.Printf("textures seen      : %d (ok %d, failed %d)\n", withTex, texParsed, texFailed)
	fmt.Printf("failures           : %d\n", len(fails))
	sort.Slice(fails, func(i, j int) bool { return fails[i].path < fails[j].path })
	limit := 25
	for i, f := range fails {
		if i >= limit {
			fmt.Printf("... and %d more\n", len(fails)-limit)
			break
		}
		fmt.Printf("  %-60s %s\n", filepath.Base(f.path), f.err)
	}
}
