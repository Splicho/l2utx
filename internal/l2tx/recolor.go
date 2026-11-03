package l2tx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"l2utx/internal/dxt"
	"l2utx/internal/imgio"
	"l2utx/internal/recolor"
	"l2utx/internal/upkg"
)

// ApplyReport summarises an in-place recolour of a package.
type ApplyReport struct {
	Changed int
	Bytes   int
	// Skipped lists textures that were deliberately left alone (normal maps and
	// formats this tool cannot re-encode).
	Skipped []string
}

// ApplyRecolor rotates the hue of every encodable texture in the loaded
// package. It only changes memory; the caller decides where to save.
func (i *Info) ApplyRecolor(p recolor.Params) (*ApplyReport, error) {
	if p.IsIdentity() {
		return nil, errors.New("no colour adjustment given")
	}
	rep := &ApplyReport{}
	for k := range i.Textures {
		te := &i.Textures[k]
		if te.Skipped {
			continue
		}
		if te.Err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", te.Name, te.Err))
			continue
		}
		tex := te.Tex
		if tex == nil || len(tex.Mips) == 0 {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: no texture data", te.Name))
			continue
		}
		switch tex.Format {
		case upkg.FmtDXT1, upkg.FmtDXT3, upkg.FmtDXT5, upkg.FmtRGBA8:
			// Encodable without changing the stored format.
		case upkg.FmtDXT5N:
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: normal map (DXT5N) left unchanged", te.Name))
			continue
		default:
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %s cannot be re-encoded", te.Name, upkg.FormatName(tex.Format)))
			continue
		}
		if tex.Props == nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: properties could not be decoded", te.Name))
			continue
		}
		img, err := DecodeTexture(tex)
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", te.Name, err))
			continue
		}
		if img.W != tex.Mips[0].USize || img.H != tex.Mips[0].VSize {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: base mip level is empty", te.Name))
			continue
		}
		recolor.Apply(img, p)
		levels := dxt.GenerateMips(img)
		// Keep the original number of mip levels; some packages store fewer
		// than a full chain, and silently adding levels would change the asset.
		n := len(tex.Mips)
		if n > len(levels) {
			n = len(levels)
		}
		if n < 1 {
			n = 1
		}
		mips, err := EncodeMips(levels[:n], tex.Format)
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", te.Name, err))
			continue
		}
		// The format and dimensions are unchanged, so this only refreshes the
		// properties to the values they already hold. Do it before swapping the
		// mip chain so a failure leaves the texture untouched.
		if err := tex.SetFormatAndSize(i.Pkg, tex.Format, img.W, img.H); err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", te.Name, err))
			continue
		}
		tex.Mips = mips
		rep.Changed++
		for _, m := range mips {
			rep.Bytes += len(m.Data)
		}
	}
	return rep, nil
}

// RecolorOptions controls batch recolouring of an exported folder.
type RecolorOptions struct {
	OutDir    string
	Params    recolor.Params
	Overwrite bool
}

// RecolorReport summarises a folder recolour.
type RecolorReport struct {
	OutDir     string
	Written    []string
	Skipped    []string
	Failed     []string
	NormalMaps []string
	Manifest   bool
}

// RecolorFolder reads every PNG in dir, applies the adjustment and writes the
// results to OutDir. When dir contains an export manifest, a manifest with
// paths adjusted for OutDir is written as well, so the new folder can be
// dropped onto the tool to import it. Normal maps listed in the manifest are
// left out instead of being hue-rotated.
func RecolorFolder(dir string, opts RecolorOptions) (*RecolorReport, error) {
	if opts.Params.IsIdentity() {
		return nil, errors.New("no colour adjustment given")
	}
	dir = absPath(dir)
	outDir := opts.OutDir
	if outDir == "" {
		outDir = dir + ".recolored"
	}
	outDir = absPath(outDir)
	if samePath(dir, outDir) {
		return nil, errors.New("the output folder must be different from the input folder; use -o DIR")
	}

	man, pkgPath, manErr := LoadManifest(dir)
	hasManifest := manErr == nil
	if manErr != nil && !os.IsNotExist(manErr) {
		return nil, manErr
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var pngs []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".png") {
			continue
		}
		pngs = append(pngs, e.Name())
	}
	if len(pngs) == 0 {
		return nil, fmt.Errorf("%s: no PNG files (export with -png first)", dir)
	}
	sort.Strings(pngs)

	normal := map[string]bool{}
	if hasManifest {
		for _, t := range man.Textures {
			if t.PNG != "" && strings.EqualFold(t.Format, "DXT5N") {
				normal[strings.ToLower(t.PNG)] = true
			}
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	rep := &RecolorReport{OutDir: outDir}
	for _, name := range pngs {
		if normal[strings.ToLower(name)] {
			rep.NormalMaps = append(rep.NormalMaps, name)
			continue
		}
		dst := filepath.Join(outDir, name)
		if fileExists(dst) && !opts.Overwrite {
			rep.Skipped = append(rep.Skipped, name)
			continue
		}
		img, _, err := imgio.Load(filepath.Join(dir, name))
		if err != nil {
			rep.Failed = append(rep.Failed, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		recolor.Apply(img, opts.Params)
		if err := imgio.SavePNG(dst, img); err != nil {
			rep.Failed = append(rep.Failed, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		rep.Written = append(rep.Written, name)
	}

	if hasManifest {
		outMan := *man
		outMan.Textures = nil
		for _, t := range man.Textures {
			if t.PNG == "" || normal[strings.ToLower(t.PNG)] {
				continue
			}
			if !fileExists(filepath.Join(outDir, t.PNG)) {
				continue
			}
			t.DDS = "" // the recoloured folder only carries PNGs
			outMan.Textures = append(outMan.Textures, t)
		}
		sort.Slice(outMan.Textures, func(a, b int) bool {
			return outMan.Textures[a].Name < outMan.Textures[b].Name
		})
		outMan.Exported = time.Now().Format(time.RFC3339)
		outMan.Tool = "l2utx"
		if pkgPath != "" {
			if rel, err := filepath.Rel(outDir, pkgPath); err == nil {
				outMan.Package = rel
			} else {
				outMan.Package = pkgPath
			}
			outMan.PackageName = filepath.Base(pkgPath)
		}
		data, err := json.MarshalIndent(outMan, "", "  ")
		if err != nil {
			return rep, err
		}
		if err := os.WriteFile(filepath.Join(outDir, ManifestName), data, 0o644); err != nil {
			return rep, err
		}
		rep.Manifest = true
	}
	return rep, nil
}

func samePath(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}
