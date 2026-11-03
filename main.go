package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"l2utx/internal/imgio"
	"l2utx/internal/l2tx"
	"l2utx/internal/recolor"
	"l2utx/internal/upkg"
	"l2utx/internal/win"
)

const version = "1.1.0"

var (
	useColor = false
	quiet    = false
)

func main() {
	win.SetUTF8Output()
	useColor = win.EnableANSI()

	code := run(os.Args[1:])
	if code != 0 || !win.LaunchedFromShell() {
		// Dragged onto the .exe (or a double click): keep the window open.
		if !containsArg("-nopause") && !containsArg("--nopause") {
			win.Pause()
		}
	}
	os.Exit(code)
}

type options struct {
	list         bool
	importTo     string
	images       []string
	utx          []string
	outDir       string
	writePNG     bool
	writeDDS     bool
	forceFmt     int
	noMips       bool
	overwrite    bool
	noBackup     bool
	help         bool
	version      bool
	hue          float64
	saturation   float64
	lightness    float64
	mode         recolor.Mode
	recolorDir   string
	recolorOnly  bool
	applyPackage bool
}

func run(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		errorf("%v", err)
		usage()
		return 2
	}
	if opts.version {
		fmt.Printf("l2utx %s\n", version)
		return 0
	}
	if opts.help {
		usage()
		return 0
	}
	if opts.applyPackage {
		return cmdApply(opts)
	}
	if opts.recolorOnly {
		return cmdRecolor(opts)
	}
	if len(opts.utx) == 0 && len(opts.images) == 0 && opts.importTo == "" {
		usage()
		return 0
	}
	if opts.list {
		return cmdList(opts)
	}
	return cmdMain(opts)
}

// params returns the colour adjustment and whether it would change anything.
func (o *options) params() (recolor.Params, bool) {
	p := recolor.Params{
		Hue:        o.hue,
		Saturation: o.saturation,
		Lightness:  o.lightness,
		Mode:       o.mode,
	}
	return p, !p.IsIdentity()
}

func parseArgs(args []string) (*options, error) {
	o := &options{forceFmt: -1, saturation: 1, lightness: 1}
	positional := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-h" || a == "--help" || a == "/?" || strings.EqualFold(a, "-help"):
			o.help = true
		case a == "--version" || a == "-v":
			o.version = true
		case a == "-list" || a == "--list":
			o.list = true
		case a == "-i" || a == "-import" || a == "--import":
			if i+1 >= len(args) {
				return nil, errors.New("-import needs a target .utx")
			}
			i++
			o.importTo = args[i]
		case a == "-o" || a == "-out" || a == "--out":
			if i+1 >= len(args) {
				return nil, errors.New("-o needs a folder or file")
			}
			i++
			o.outDir = args[i]
		case a == "-png":
			o.writePNG = true
		case a == "-dds":
			o.writeDDS = true
		case a == "-both":
			o.writePNG, o.writeDDS = true, true
		case a == "-nomips" || a == "--nomips":
			o.noMips = true
		case a == "-y" || a == "--yes" || a == "-overwrite":
			o.overwrite = true
		case a == "-nobackup":
			o.noBackup = true
		case a == "-q" || a == "-quiet":
			quiet = true
		case a == "-nopause" || a == "--nopause":
			// handled in main
		case a == "-format" || a == "--format":
			if i+1 >= len(args) {
				return nil, errors.New("-format needs dxt1, dxt3, dxt5 or rgba8")
			}
			i++
			f, err := parseFormat(args[i])
			if err != nil {
				return nil, err
			}
			o.forceFmt = f
		case a == "-hue" || a == "--hue":
			v, err := floatArg(args, &i, "-hue")
			if err != nil {
				return nil, err
			}
			o.hue = v
		case a == "-sat" || a == "-saturation" || a == "--saturation":
			v, err := floatArg(args, &i, "-sat")
			if err != nil {
				return nil, err
			}
			if v < 0 || v > 1000 {
				return nil, errors.New("-sat must be between 0 and 1000 (percent)")
			}
			o.saturation = v / 100
		case a == "-light" || a == "-lightness" || a == "--lightness":
			v, err := floatArg(args, &i, "-light")
			if err != nil {
				return nil, err
			}
			if v < 0 || v > 1000 {
				return nil, errors.New("-light must be between 0 and 1000 (percent)")
			}
			o.lightness = v / 100
		case a == "-mode" || a == "-hue-mode" || a == "-colorspace":
			s, err := nextArg(args, &i, "-mode")
			if err != nil {
				return nil, err
			}
			m, ok := recolor.ParseMode(s)
			if !ok {
				return nil, fmt.Errorf("unknown mode %q (use oklch or hsl)", s)
			}
			o.mode = m
		case a == "-recolor":
			o.recolorOnly = true
		case a == "-apply":
			o.applyPackage = true
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("unknown option %q", a)
		default:
			positional = append(positional, a)
		}
		i++
	}

	if o.applyPackage && o.recolorOnly {
		return nil, errors.New("-apply and -recolor are mutually exclusive")
	}
	if o.applyPackage || o.recolorOnly {
		if o.list {
			return nil, errors.New("-list cannot be combined with -apply or -recolor")
		}
		if o.importTo != "" {
			return nil, errors.New("-i cannot be combined with -apply or -recolor")
		}
		if _, ok := o.params(); !ok {
			return nil, errors.New("-apply and -recolor need -hue, -sat or -light")
		}
	}
	if o.recolorOnly {
		if len(positional) != 1 {
			return nil, errors.New("-recolor needs one exported texture folder")
		}
		st, err := os.Stat(positional[0])
		if err != nil {
			return nil, fmt.Errorf("cannot open %q: %w", positional[0], err)
		}
		if !st.IsDir() {
			return nil, errors.New("-recolor takes an exported texture folder, not a file")
		}
		o.recolorDir = positional[0]
		return o, nil
	}
	if o.applyPackage {
		if len(positional) != 1 {
			return nil, errors.New("-apply needs one .utx file")
		}
		st, err := os.Stat(positional[0])
		if err != nil {
			return nil, fmt.Errorf("cannot open %q: %w", positional[0], err)
		}
		if st.IsDir() {
			return nil, errors.New("-apply takes one .utx file, not a folder")
		}
		switch strings.ToLower(filepath.Ext(positional[0])) {
		case ".utx", ".u":
			o.utx = append(o.utx, positional[0])
		default:
			return nil, fmt.Errorf("%s: not a texture package", positional[0])
		}
		return o, nil
	}

	// Split positionals into packages, images and folders.
	for _, p := range positional {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cannot open %q: %w", p, err)
		}
		if st.IsDir() {
			if _, _, err := l2tx.LoadManifest(p); err == nil {
				o.importTo = p
				continue
			}
			// A plain folder: treat every .utx inside as input.
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			found := 0
			for _, e := range entries {
				if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".utx") {
					o.utx = append(o.utx, filepath.Join(p, e.Name()))
					found++
				}
			}
			if found == 0 {
				return nil, fmt.Errorf("%s: no .utx files and no exported texture folder found", p)
			}
			continue
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".utx", ".u":
			o.utx = append(o.utx, p)
		default:
			if imgio.IsImagePath(p) {
				o.images = append(o.images, p)
			} else {
				return nil, fmt.Errorf("%s: not a texture package or image", p)
			}
		}
	}
	if o.importTo != "" && len(o.utx) > 0 {
		return nil, errors.New("give either a package to import into or packages to extract, not both")
	}
	if _, ok := o.params(); ok && (o.importTo != "" || len(o.images) > 0) {
		return nil, errors.New("-hue, -sat and -light apply when extracting a package; use -recolor for existing images")
	}
	return o, nil
}

func nextArg(args []string, i *int, name string) (string, error) {
	if *i+1 >= len(args) {
		return "", fmt.Errorf("%s needs a value", name)
	}
	*i = *i + 1
	return args[*i], nil
}

func floatArg(args []string, i *int, name string) (float64, error) {
	s, err := nextArg(args, i, name)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s: %q is not a number", name, s)
	}
	return v, nil
}

func parseFormat(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dxt1", "bc1":
		return upkg.FmtDXT1, nil
	case "dxt3", "bc2":
		return upkg.FmtDXT3, nil
	case "dxt5", "bc3":
		return upkg.FmtDXT5, nil
	case "rgba8", "rgba":
		return upkg.FmtRGBA8, nil
	}
	return 0, fmt.Errorf("unknown format %q (use dxt1, dxt3, dxt5 or rgba8)", s)
}

// cmdMain decides between extraction and import based on what was dropped.
func cmdMain(o *options) int {
	// Import mode.
	if o.importTo != "" {
		return cmdImport(o, o.importTo)
	}
	if len(o.images) > 0 {
		// Images dropped without an explicit target: use a sibling manifest, or
		// the single dropped package.
		if len(o.utx) == 1 {
			return cmdImport(o, o.utx[0])
		}
		if len(o.utx) > 1 {
			errorf("several packages and images given - say which package to import into with -i")
			return 2
		}
		return importViaManifests(o)
	}
	// Extraction.
	code := 0
	for _, u := range o.utx {
		if err := extractOne(u, o); err != nil {
			errorf("%v", err)
			code = 1
		}
	}
	return code
}

func extractOne(path string, o *options) error {
	info, err := l2tx.Load(path)
	if err != nil {
		return err
	}
	textures := 0
	for _, te := range info.Textures {
		if !te.Skipped {
			textures++
		}
	}
	title("%s  %s", filepath.Base(path), dim(versionText(info)))
	if textures == 0 {
		warnf("no textures in this package")
		return nil
	}
	p, adjusting := o.params()
	var rec *recolor.Params
	suffix := ""
	if adjusting {
		rec = &p
		suffix = recolorSuffix(p)
	}
	rep, err := info.Export(l2tx.ExportOptions{
		OutDir:    o.outDir,
		WritePNG:  o.writePNG || !o.writeDDS,
		WriteDDS:  o.writeDDS,
		Overwrite: o.overwrite,
		Recolor:   rec,
		DirSuffix: suffix,
	})
	if err != nil {
		return err
	}
	printTextureTable(info)
	for _, f := range rep.Failed {
		warnf("%s", f)
	}
	if len(rep.Skipped) > 0 {
		warnf("%d file%s already exist and were left untouched (use -y to overwrite)",
			len(rep.Skipped), plural(len(rep.Skipped), "", "s"))
	}
	okf("extracted %d texture%s to %s", len(rep.Written), plural(len(rep.Written), "", "s"), dim(relPathTo(rep.OutDir)))
	if adjusting {
		okf("recoloured %d texture%s (%s)", rep.Recolored, plural(rep.Recolored, "", "s"), p.Describe())
		if len(rep.NormalMaps) > 0 {
			warnf("%d normal map%s left unchanged", len(rep.NormalMaps), plural(len(rep.NormalMaps), "", "s"))
		}
	}
	return nil
}

func versionText(info *l2tx.Info) string {
	p := info.Pkg
	return fmt.Sprintf("v%d/%d · %d textures · %d objects", p.Version, p.LicenseeVersion, countTextures(info), len(p.Exports))
}

func countTextures(info *l2tx.Info) int {
	n := 0
	for _, te := range info.Textures {
		if !te.Skipped {
			n++
		}
	}
	return n
}

func printTextureTable(info *l2tx.Info) {
	if quiet {
		return
	}
	rows := [][2]string{}
	for _, te := range info.Textures {
		if te.Skipped {
			continue
		}
		if te.Tex == nil {
			rows = append(rows, [2]string{"  " + yellow("!") + " " + te.Name, red("unreadable")})
			continue
		}
		t := te.Tex
		m := t.Mips[0]
		desc := fmt.Sprintf("%-6s %4dx%-4d %2d mip%s %9s",
			upkg.FormatName(t.Format), m.USize, m.VSize, len(t.Mips),
			plural(len(t.Mips), "", "s"), humanBytes(int64(len(m.Data))))
		rows = append(rows, [2]string{"  " + te.Name, dim(desc)})
	}
	w := 0
	for _, r := range rows {
		if len(r[0]) > w {
			w = len(r[0])
		}
	}
	for _, r := range rows {
		fmt.Printf("%-*s  %s\n", w, r[0], r[1])
	}
}

// importTarget is one image to import. A negative index means "match by file name".
type importTarget struct {
	index int
	path  string
}

func (t importTarget) label() string {
	if t.index >= 0 {
		return fmt.Sprintf("%s [export %d]", filepath.Base(t.path), t.index)
	}
	return filepath.Base(t.path)
}

// cmdImport imports images into a package (or an exported folder).
func cmdImport(o *options, target string) int {
	// A folder target: read the manifest and import every image in it.
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		man, pkgPath, err := l2tx.LoadManifest(target)
		if err != nil {
			errorf("%s: %v", target, err)
			return 1
		}
		if _, err := os.Stat(pkgPath); err != nil {
			errorf("the package this folder was exported from is missing: %s", pkgPath)
			errorf("put %s back next to the folder, or drop the .utx together with the images", man.PackageName)
			return 1
		}
		return importInto(o, pkgPath, manifestTargets(target, man))
	}
	// Explicit package target: resolve images by name.
	if len(o.images) == 0 {
		errorf("nothing to import - drop the edited .png/.dds files together with the package")
		return 2
	}
	targets := make([]importTarget, 0, len(o.images))
	for _, img := range o.images {
		targets = append(targets, importTarget{index: -1, path: img})
	}
	return importInto(o, target, targets)
}

// importViaManifests resolves images to their source package through the
// manifest files that sit next to them.
func importViaManifests(o *options) int {
	type group struct {
		pkg     string
		targets []importTarget
	}
	byPkg := map[string]*group{}
	order := []string{}
	for _, img := range o.images {
		dir := filepath.Dir(img)
		man, pkgPath, err := l2tx.LoadManifest(dir)
		if err != nil {
			errorf("%s: no exported-texture folder here (drop the .utx first, or use -i <file.utx>)", dir)
			return 1
		}
		if _, err := os.Stat(pkgPath); err != nil {
			errorf("%s: source package missing (%s)", dir, man.PackageName)
			return 1
		}
		g := byPkg[pkgPath]
		if g == nil {
			g = &group{pkg: pkgPath}
			byPkg[pkgPath] = g
			order = append(order, pkgPath)
		}
		idx := -1
		base := filepath.Base(img)
		for _, te := range man.Textures {
			if strings.EqualFold(te.PNG, base) || strings.EqualFold(te.DDS, base) {
				idx = te.Index
				break
			}
		}
		g.targets = append(g.targets, importTarget{index: idx, path: img})
	}
	code := 0
	for _, pkg := range order {
		if importInto(o, pkg, byPkg[pkg].targets) != 0 {
			code = 1
		}
	}
	return code
}

// manifestTargets lists the images of an export folder that still exist.
func manifestTargets(dir string, man *l2tx.Manifest) []importTarget {
	var out []importTarget
	for _, t := range man.Textures {
		for _, name := range []string{t.DDS, t.PNG} {
			if name == "" {
				continue
			}
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				out = append(out, importTarget{index: t.Index, path: p})
			}
		}
	}
	return out
}

func importInto(o *options, pkgPath string, targets []importTarget) int {
	info, err := l2tx.Load(pkgPath)
	if err != nil {
		errorf("%v", err)
		return 1
	}
	if len(targets) == 0 {
		errorf("no texture images found")
		return 1
	}
	title("%s", filepath.Base(pkgPath))
	any := false
	failed := 0
	for _, t := range targets {
		var results []*l2tx.ImportResult
		if t.index >= 0 {
			res, err := info.ImportImageAt(t.index, t.path, l2tx.ImportOptions{ForceFormat: o.forceFmt, NoMips: o.noMips})
			if err != nil {
				errorf("%s: %v", t.label(), err)
				failed++
				continue
			}
			results = []*l2tx.ImportResult{res}
		} else {
			results, err = info.ImportImage(t.path, l2tx.ImportOptions{ForceFormat: o.forceFmt, NoMips: o.noMips})
			if err != nil {
				errorf("%s: %v", t.label(), err)
				failed++
				continue
			}
		}
		for _, res := range results {
			any = true
			if res.Lossless {
				okf("%-30s %s  %dx%d  %s", res.Object, res.NewFormat, res.NewW, res.NewH, dim("copied unchanged"))
				continue
			}
			change := ""
			if res.OldW != res.NewW || res.OldH != res.NewH {
				change = " " + dim(fmt.Sprintf("(%dx%d -> %dx%d)", res.OldW, res.OldH, res.NewW, res.NewH))
			}
			if res.OldFormat != res.NewFormat {
				change += " " + yellow(fmt.Sprintf("format %s -> %s", res.OldFormat, res.NewFormat))
			}
			okf("%-30s %s  %dx%d%s", res.Object, res.NewFormat, res.NewW, res.NewH, change)
		}
	}
	if !any {
		errorf("nothing was imported")
		return 1
	}
	out, err := info.Save(pkgPath, !o.noBackup)
	if err != nil {
		errorf("%v", err)
		return 1
	}
	okf("saved %s", dim(filepath.Base(out)))
	if failed > 0 {
		warnf("%d image%s could not be imported", failed, plural(failed, "", "s"))
		return 1
	}
	return 0
}

// cmdList prints the texture inventory without writing files.
func cmdList(o *options) int {
	code := 0
	for _, u := range o.utx {
		info, err := l2tx.Load(u)
		if err != nil {
			errorf("%v", err)
			code = 1
			continue
		}
		title("%s  %s", filepath.Base(u), dim(versionText(info)))
		printTextureTable(info)
		// Also mention non-texture objects so users know what else is inside.
		var others []string
		for _, te := range info.Textures {
			if te.Skipped {
				others = append(others, fmt.Sprintf("%s (%s)", te.Name, te.Class))
			}
		}
		if len(others) > 0 {
			sort.Strings(others)
			if !quiet {
				fmt.Printf("  %s\n", dim(fmt.Sprintf("%d other object(s): %s", len(others), strings.Join(others, ", "))))
			}
		}
	}
	return code
}

// cmdApply recolours a package without going through image files.
func cmdApply(o *options) int {
	p, ok := o.params()
	if !ok {
		errorf("-apply needs -hue, -sat or -light")
		return 2
	}
	if len(o.utx) != 1 {
		errorf("-apply needs exactly one .utx file")
		return 2
	}
	pkg := o.utx[0]
	info, err := l2tx.Load(pkg)
	if err != nil {
		errorf("%v", err)
		return 1
	}
	title("%s  %s", filepath.Base(pkg), dim(versionText(info)))
	rep, err := info.ApplyRecolor(p)
	if err != nil {
		errorf("%v", err)
		return 1
	}
	for _, s := range rep.Skipped {
		warnf("%s", s)
	}
	if rep.Changed == 0 {
		errorf("nothing was recoloured")
		return 1
	}

	outPath := pkg
	backup := !o.noBackup
	if o.outDir != "" {
		outPath = o.outDir
		if !samePath(outPath, pkg) {
			backup = false
			if st, err := os.Stat(outPath); err == nil {
				if st.IsDir() {
					errorf("%s is a folder; -o needs a file name with -apply", outPath)
					return 2
				}
				if !o.overwrite {
					errorf("%s already exists (use -y to overwrite)", outPath)
					return 1
				}
			}
		}
	}
	out, err := info.Save(outPath, backup)
	if err != nil {
		errorf("%v", err)
		return 1
	}
	okf("recoloured %d texture%s (%s)", rep.Changed, plural(rep.Changed, "", "s"), p.Describe())
	okf("saved %s", dim(filepath.Base(out)))
	return 0
}

// cmdRecolor recolours the PNGs of an exported folder.
func cmdRecolor(o *options) int {
	p, ok := o.params()
	if !ok {
		errorf("-recolor needs -hue, -sat or -light")
		return 2
	}
	in := o.recolorDir
	out := o.outDir
	if out == "" {
		out = defaultRecolorDir(in, p)
	}
	title("%s  %s", filepath.Base(in), dim(p.Describe()))
	rep, err := l2tx.RecolorFolder(in, l2tx.RecolorOptions{
		OutDir:    out,
		Params:    p,
		Overwrite: o.overwrite,
	})
	if err != nil {
		errorf("%v", err)
		return 1
	}
	for _, f := range rep.Failed {
		warnf("%s", f)
	}
	for _, n := range rep.NormalMaps {
		warnf("%s: normal map left unchanged", n)
	}
	if len(rep.Skipped) > 0 {
		warnf("%d file%s already exist in the output folder (use -y to overwrite)",
			len(rep.Skipped), plural(len(rep.Skipped), "", "s"))
	}
	if len(rep.Written) == 0 {
		if len(rep.Failed) == 0 && len(rep.Skipped) == 0 {
			errorf("nothing was recoloured")
			return 1
		}
	} else {
		okf("recoloured %d image%s -> %s", len(rep.Written), plural(len(rep.Written), "", "s"), dim(relPathTo(rep.OutDir)))
	}
	if rep.Manifest {
		okf("manifest written - drop the folder to import it")
	} else {
		warnf("no %s in the source folder; the output cannot be imported on its own", l2tx.ManifestName)
	}
	if len(rep.Failed) > 0 {
		return 1
	}
	return 0
}

// recolorSuffix builds a short folder-name suffix such as "hue120-sat80".
func recolorSuffix(p recolor.Params) string {
	var parts []string
	if h := math.Round(p.Hue*10) / 10; h != 0 {
		parts = append(parts, fmt.Sprintf("hue%g", h))
	}
	if s := math.Round(p.Saturation*1000) / 10; s != 100 {
		parts = append(parts, fmt.Sprintf("sat%g", s))
	}
	if l := math.Round(p.Lightness*1000) / 10; l != 100 {
		parts = append(parts, fmt.Sprintf("light%g", l))
	}
	if p.Mode == recolor.ModeHSL {
		parts = append(parts, "hsl")
	}
	if len(parts) == 0 {
		return "recolored"
	}
	return strings.Join(parts, "-")
}

// defaultRecolorDir puts a recoloured copy next to the source folder:
// IntactL.textures -> IntactL.hue120.textures.
func defaultRecolorDir(dir string, p recolor.Params) string {
	if a, err := filepath.Abs(dir); err == nil {
		dir = a
	}
	parent := filepath.Dir(dir)
	base := filepath.Base(dir)
	suffix := recolorSuffix(p)
	const ext = ".textures"
	if strings.HasSuffix(strings.ToLower(base), ext) {
		return filepath.Join(parent, base[:len(base)-len(ext)]+"."+suffix+ext)
	}
	return filepath.Join(parent, base+"."+suffix)
}

func samePath(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

// --- console helpers -------------------------------------------------------

func color(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func green(s string) string  { return color("32", s) }
func yellow(s string) string { return color("33", s) }
func red(s string) string    { return color("31", s) }
func dim(s string) string    { return color("90", s) }
func bold(s string) string   { return color("1", s) }

func title(format string, a ...any) {
	if !quiet {
		fmt.Println()
	}
	fmt.Println(bold(">> ") + fmt.Sprintf(format, a...))
}

func okf(format string, a ...any)   { fmt.Println(green("  ok  ") + fmt.Sprintf(format, a...)) }
func warnf(format string, a ...any) { fmt.Println(yellow("  !   ") + fmt.Sprintf(format, a...)) }
func errorf(format string, a ...any) {
	fmt.Fprintln(os.Stderr, red("error ")+fmt.Sprintf(format, a...))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func relPathTo(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	if r, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(r, "..") {
		return r + string(os.PathSeparator)
	}
	return p
}

func containsArg(s string) bool {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, s) {
			return true
		}
	}
	return false
}

func usage() {
	fmt.Println()
	fmt.Println(bold("L2 UTX Texture Tool ") + dim(version) + dim(" - Unreal Engine 2 / Lineage 2 texture import & export"))
	fmt.Println()
	fmt.Println(bold("Drop files onto the .exe:"))
	fmt.Println("   file.utx                 extract every texture to  <name>.textures\\")
	fmt.Println("   folder                   extract all .utx inside, or re-import an export folder")
	fmt.Println("   file.utx + image(s)      replace textures whose name matches the image file")
	fmt.Println("   image(s)                 re-import into the package named in _l2utx.json")
	fmt.Println()
	fmt.Println(bold("Options:"))
	fmt.Println("   -list file.utx           list textures without extracting")
	fmt.Println("   -o DIR|FILE              extraction folder, -recolor output folder, or -apply output .utx")
	fmt.Println("   -png | -dds | -both      image format (default -png; -dds is lossless)")
	fmt.Println("   -i file.utx img...       import images into a package")
	fmt.Println("   -format dxt1|dxt3|dxt5|rgba8   force the stored format on import")
	fmt.Println("   -nomips                  store only the base mip level")
	fmt.Println("   -nobackup                do not create <file>.utx.bak")
	fmt.Println("   -q                       quiet")
	fmt.Println("   -nopause                 do not wait for Enter when run by drag & drop")
	fmt.Println()
	fmt.Println(bold("Recolouring:"))
	fmt.Println("   -hue DEG                 rotate hue (for example 120, -45, 180)")
	fmt.Println("   -sat PCT                 saturation scale (100 = unchanged)")
	fmt.Println("   -light PCT               lightness scale (100 = unchanged)")
	fmt.Println("   -mode oklch|hsl          colour model (default oklch; hsl matches Photoshop)")
	fmt.Println("   -recolor                 recolour the PNGs of an exported folder")
	fmt.Println("   -apply                   recolour a package in place (or write -o file.utx)")
	fmt.Println()
	fmt.Println("   l2utx -hue 120 file.utx          extract to file.hue120.textures\\ already recoloured")
	fmt.Println("   l2utx -hue 120 -recolor folder   write folder.hue120.textures\\ from an existing export")
	fmt.Println("   l2utx -hue 120 -apply file.utx   rewrite the package (file.utx.bak is kept)")
	fmt.Println()
	fmt.Println(bold("How importing works:"))
	fmt.Println("   The name of the image selects the texture object: armor_t1004_b_i00.png")
	fmt.Println("   replaces the object armor_t1004_b_i00. Dimensions may change freely;")
	fmt.Println("   the mip chain and USize/VSize/UBits/VBits properties are updated for you.")
	fmt.Println()
	fmt.Println(dim("   Exit codes: 0 ok, 1 failure, 2 bad arguments"))
}
