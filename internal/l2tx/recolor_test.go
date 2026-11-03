package l2tx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"l2utx/internal/dxt"
	"l2utx/internal/imgio"
	"l2utx/internal/recolor"
	"l2utx/internal/upkg"
)

func TestApplyRecolorSample(t *testing.T) {
	src := filepath.Join("..", "..", "examples", "IntactIcons.utx")
	info, err := Load(src)
	if err != nil {
		t.Skip(err)
	}
	var target *upkg.TextureExport
	for i := range info.Textures {
		if !info.Textures[i].Skipped && info.Textures[i].Tex != nil {
			target = &info.Textures[i]
			break
		}
	}
	if target == nil {
		t.Fatal("no texture in sample package")
	}
	oldMips := len(target.Tex.Mips)
	oldFormat := target.Tex.Format
	before, err := DecodeTexture(target.Tex)
	if err != nil {
		t.Fatal(err)
	}
	p := recolor.Params{Hue: 120, Saturation: 1, Lightness: 1}
	rep, err := info.ApplyRecolor(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed != 18 {
		t.Fatalf("changed %d textures, want 18", rep.Changed)
	}
	after, err := DecodeTexture(target.Tex)
	if err != nil {
		t.Fatal(err)
	}
	if after.W != before.W || after.H != before.H {
		t.Fatalf("size changed: %dx%d -> %dx%d", before.W, before.H, after.W, after.H)
	}
	if len(target.Tex.Mips) != oldMips || target.Tex.Format != oldFormat {
		t.Fatalf("mip count/format changed: %d/%d -> %d/%d",
			oldMips, oldFormat, len(target.Tex.Mips), target.Tex.Format)
	}
	diff := 0
	for i := range before.Pix {
		if before.Pix[i].A != after.Pix[i].A {
			t.Fatalf("alpha changed at %d: %d -> %d", i, before.Pix[i].A, after.Pix[i].A)
		}
		if before.Pix[i] != after.Pix[i] {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("no pixel changed")
	}

	out := filepath.Join(t.TempDir(), "recoloured.utx")
	if _, err := info.Save(out, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := Load(out)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Textures) != len(info.Textures) {
		t.Fatalf("texture count changed: %d -> %d", len(info.Textures), len(reloaded.Textures))
	}
}

func TestApplyRecolorSkipsNormalMapsAndUnsupportedFormats(t *testing.T) {
	info := &Info{Textures: []upkg.TextureExport{
		{
			Name: "normal",
			Tex: &upkg.Texture{
				Format: upkg.FmtDXT5N,
				Mips:   []upkg.Mip{{USize: 4, VSize: 4, Data: make([]byte, 32)}},
			},
		},
		{
			Name: "palette",
			Tex: &upkg.Texture{
				Format: upkg.FmtP8,
				Mips:   []upkg.Mip{{USize: 4, VSize: 4, Data: make([]byte, 16)}},
			},
		},
	}}
	rep, err := info.ApplyRecolor(recolor.Params{Hue: 90, Saturation: 1, Lightness: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed != 0 {
		t.Fatalf("changed %d textures, want 0", rep.Changed)
	}
	if len(rep.Skipped) != 2 {
		t.Fatalf("skipped %d textures, want 2: %v", len(rep.Skipped), rep.Skipped)
	}
}

func TestExportRecolorSkipsNormalMaps(t *testing.T) {
	root := t.TempDir()
	info := &Info{
		Path: filepath.Join(root, "Pkg.utx"),
		Dir:  root,
		Base: "Pkg",
		Textures: []upkg.TextureExport{
			{
				Index: 0,
				Name:  "normal",
				Tex: &upkg.Texture{
					Format: upkg.FmtDXT5N,
					Mips:   []upkg.Mip{{USize: 4, VSize: 4, Data: make([]byte, 16)}},
				},
			},
			{
				Index: 1,
				Name:  "diffuse",
				Tex: &upkg.Texture{
					Format: upkg.FmtDXT3,
					Mips:   []upkg.Mip{{USize: 4, VSize: 4, Data: make([]byte, 16)}},
				},
			},
		},
	}
	outDir := filepath.Join(root, "Pkg.hue120.textures")
	p := recolor.Params{Hue: 120, Saturation: 1, Lightness: 1}
	rep, err := info.Export(ExportOptions{
		OutDir:    outDir,
		WritePNG:  true,
		Overwrite: true,
		Recolor:   &p,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.NormalMaps) != 1 || rep.NormalMaps[0] != "normal" {
		t.Fatalf("normal maps = %v, want [normal]", rep.NormalMaps)
	}
	if fileExists(filepath.Join(outDir, "normal.png")) {
		t.Fatal("normal map was exported")
	}
	if !fileExists(filepath.Join(outDir, "diffuse.png")) {
		t.Fatal("diffuse texture was not exported")
	}
	man, _, err := LoadManifest(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(man.Textures) != 1 || man.Textures[0].Name != "diffuse" {
		t.Fatalf("manifest entries = %+v", man.Textures)
	}
}

func TestRecolorFolder(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "Pkg.textures")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A dummy package so the manifest path resolves. It is not read.
	pkgPath := filepath.Join(root, "Pkg.utx")
	if err := os.WriteFile(pkgPath, []byte("not a real package"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(srcDir, "colour.png"), dxt.RGBA{R: 40, G: 80, B: 200, A: 255})
	writeTestPNG(t, filepath.Join(srcDir, "normal.png"), dxt.RGBA{R: 128, G: 128, B: 255, A: 255})
	man := Manifest{
		Package:     filepath.Join("..", "Pkg.utx"),
		PackageName: "Pkg.utx",
		Tool:        "l2utx",
		Textures: []ManifestEntry{
			{Index: 0, Name: "colour", Format: "DXT3", PNG: "colour.png"},
			{Index: 1, Name: "normal", Format: "DXT5N", PNG: "normal.png"},
			{Index: 2, Name: "dds-only", Format: "DXT3", DDS: "dds-only.dds"},
		},
	}
	data, _ := json.MarshalIndent(man, "", "  ")
	if err := os.WriteFile(filepath.Join(srcDir, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(root, "Pkg.hue120.textures")
	rep, err := RecolorFolder(srcDir, RecolorOptions{
		OutDir: outDir,
		Params: recolor.Params{Hue: 120, Saturation: 1, Lightness: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Written) != 1 || rep.Written[0] != "colour.png" {
		t.Fatalf("written = %v, want [colour.png]", rep.Written)
	}
	if len(rep.NormalMaps) != 1 || rep.NormalMaps[0] != "normal.png" {
		t.Fatalf("normal maps = %v, want [normal.png]", rep.NormalMaps)
	}
	if fileExists(filepath.Join(outDir, "normal.png")) {
		t.Fatal("normal map was written")
	}
	if !rep.Manifest {
		t.Fatal("manifest was not written")
	}
	outMan, resolved, err := LoadManifest(outDir)
	if err != nil {
		t.Fatalf("load output manifest: %v", err)
	}
	if resolved != pkgPath {
		t.Fatalf("manifest resolves to %q, want %q", resolved, pkgPath)
	}
	if len(outMan.Textures) != 1 || outMan.Textures[0].Name != "colour" || outMan.Textures[0].DDS != "" {
		t.Fatalf("output manifest entries = %+v", outMan.Textures)
	}
	img, _, err := imgio.Load(filepath.Join(outDir, "colour.png"))
	if err != nil {
		t.Fatal(err)
	}
	if img.Pix[0] == (dxt.RGBA{R: 40, G: 80, B: 200, A: 255}) {
		t.Fatal("image was not recoloured")
	}
	if img.Pix[0].A != 255 {
		t.Fatalf("alpha changed: %d", img.Pix[0].A)
	}
}

func TestRecolorFolderRejectsSameOutput(t *testing.T) {
	dir := t.TempDir()
	writeTestPNG(t, filepath.Join(dir, "a.png"), dxt.RGBA{R: 1, G: 2, B: 3, A: 255})
	_, err := RecolorFolder(dir, RecolorOptions{
		OutDir: dir,
		Params: recolor.Params{Hue: 10, Saturation: 1, Lightness: 1},
	})
	if err == nil {
		t.Fatal("expected an error for an output folder equal to the input")
	}
}

func TestExportRecolor(t *testing.T) {
	src := filepath.Join("..", "..", "examples", "IntactIcons.utx")
	info, err := Load(src)
	if err != nil {
		t.Skip(err)
	}
	p := recolor.Params{Hue: 120, Saturation: 1, Lightness: 1}
	outDir := t.TempDir()
	rep, err := info.Export(ExportOptions{
		OutDir:    outDir,
		WritePNG:  true,
		Overwrite: true,
		Recolor:   &p,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Recolored != 18 || len(rep.Written) != 18 {
		t.Fatalf("recoloured %d, written %d; want 18/18", rep.Recolored, len(rep.Written))
	}
	man, _, err := LoadManifest(outDir)
	if err != nil {
		t.Fatal(err)
	}
	entry := man.Textures[0]
	var tex *upkg.Texture
	for k := range info.Textures {
		if info.Textures[k].Index == entry.Index {
			tex = info.Textures[k].Tex
			break
		}
	}
	if tex == nil {
		t.Fatalf("no texture with index %d", entry.Index)
	}
	got, _, err := imgio.Load(filepath.Join(outDir, entry.PNG))
	if err != nil {
		t.Fatal(err)
	}
	want, err := DecodeTexture(tex)
	if err != nil {
		t.Fatal(err)
	}
	recolor.Apply(want, p)
	if got.W != want.W || got.H != want.H {
		t.Fatalf("size %dx%d, want %dx%d", got.W, got.H, want.W, want.H)
	}
	for i := range want.Pix {
		if got.Pix[i] != want.Pix[i] {
			t.Fatalf("pixel %d = %+v, want %+v", i, got.Pix[i], want.Pix[i])
		}
	}
}

func TestWriteImageDDS(t *testing.T) {
	img := dxt.NewImage(8, 8)
	for i := range img.Pix {
		img.Pix[i] = dxt.RGBA{R: uint8(i * 3), G: uint8(255 - i), B: uint8(i), A: 255}
	}
	path := filepath.Join(t.TempDir(), "image.dds")
	if err := WriteImageDDS(path, upkg.FmtDXT3, img, 4); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dds, err := imgio.DecodeDDS(data)
	if err != nil {
		t.Fatal(err)
	}
	if dds.Format != imgio.PixelDXT3 || dds.W != 8 || dds.H != 8 {
		t.Fatalf("DDS = %s %dx%d, want DXT3 8x8", dds.Format.Name(), dds.W, dds.H)
	}
	if len(dds.MipData) != 4 {
		t.Fatalf("DDS has %d mips, want 4", len(dds.MipData))
	}

	path2 := filepath.Join(t.TempDir(), "short.dds")
	if err := WriteImageDDS(path2, upkg.FmtDXT3, img, 2); err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	dds2, err := imgio.DecodeDDS(data2)
	if err != nil {
		t.Fatal(err)
	}
	if len(dds2.MipData) != 2 {
		t.Fatalf("truncated DDS has %d mips, want 2", len(dds2.MipData))
	}
}

func writeTestPNG(t *testing.T, path string, c dxt.RGBA) {
	t.Helper()
	img := dxt.NewImage(2, 2)
	for i := range img.Pix {
		img.Pix[i] = c
	}
	if err := imgio.SavePNG(path, img); err != nil {
		t.Fatal(err)
	}
}
