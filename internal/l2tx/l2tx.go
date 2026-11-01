// Package l2tx implements the high-level Lineage 2 texture import/export
// operations on top of the upkg and dxt packages.
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
	"l2utx/internal/upkg"
)

// ManifestName is the file dropped into an export folder so the tool can find
// its way back to the source package when the folder is dropped again.
const ManifestName = "_l2utx.json"

// Manifest is the export folder descriptor.
type Manifest struct {
	Package     string          `json:"package"`     // path to the source .utx (relative to the folder when possible)
	PackageName string          `json:"packageName"` // base name of the source .utx
	Exported    string          `json:"exported"`    // timestamp
	Tool        string          `json:"tool"`
	Textures    []ManifestEntry `json:"textures"`
}

// ManifestEntry describes one exported texture file.
type ManifestEntry struct {
	Index  int    `json:"index"` // export index, disambiguates duplicate names
	Name   string `json:"name"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Mips   int    `json:"mips"`
	PNG    string `json:"png,omitempty"`
	DDS    string `json:"dds,omitempty"`
}

// Info is a loaded package with its textures parsed.
type Info struct {
	Path     string
	Dir      string
	Base     string // file name without extension
	Pkg      *upkg.Package
	Textures []upkg.TextureExport
	Raw      []byte
}

// Load reads and parses a package, parsing texture payloads.
func Load(path string) (*Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info := upkg.Identify(data)
	if !info.IsPackage {
		if info.Compressed {
			return nil, fmt.Errorf("%s: %s", filepath.Base(path), info.Reason)
		}
		return nil, fmt.Errorf("%s: %s", filepath.Base(path), info.Reason)
	}
	pkg, err := upkg.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	i := &Info{
		Path: absPath(path),
		Dir:  filepath.Dir(absPath(path)),
		Base: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Pkg:  pkg,
		Raw:  data,
	}
	i.Textures = pkg.Textures()
	return i, nil
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// TexturesNamed returns the textures with the given name (case-insensitive).
func (i *Info) TextureIndex(name string) int {
	for k := range i.Textures {
		if strings.EqualFold(i.Textures[k].Name, name) {
			return k
		}
	}
	return -1
}

// ExportOptions controls extraction.
type ExportOptions struct {
	OutDir   string
	WritePNG bool
	WriteDDS bool
	Only     []string
	Overwrite bool
	Quiet    bool
}

// ExportReport summarises an extraction run.
type ExportReport struct {
	OutDir   string
	Written  []string
	Skipped  []string
	Failed   []string
	Textures int
}

// Export writes texture images next to (or into) OutDir.
func (i *Info) Export(opts ExportOptions) (*ExportReport, error) {
	outDir := opts.OutDir
	if outDir == "" {
		outDir = filepath.Join(i.Dir, i.Base+".textures")
	}
	outDir = absPath(outDir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	if !opts.WritePNG && !opts.WriteDDS {
		opts.WritePNG = true
	}
	only := map[string]bool{}
	for _, n := range opts.Only {
		only[strings.ToLower(n)] = true
	}

	rep := &ExportReport{OutDir: outDir}
	manifest := Manifest{
		Package:     relPath(outDir, i.Path),
		PackageName: filepath.Base(i.Path),
		Exported:    time.Now().Format(time.RFC3339),
		Tool:        "l2utx",
	}

	used := map[string]int{}
	for k := range i.Textures {
		te := &i.Textures[k]
		if te.Skipped {
			continue
		}
		if len(only) > 0 && !only[strings.ToLower(te.Name)] {
			continue
		}
		if te.Err != nil {
			rep.Failed = append(rep.Failed, fmt.Sprintf("%s: %v", te.Name, te.Err))
			continue
		}
		tex := te.Tex
		rep.Textures++
		entry := ManifestEntry{
			Index:  te.Index,
			Name:   te.Name,
			Format: upkg.FormatName(tex.Format),
			Width:  tex.Mips[0].USize,
			Height: tex.Mips[0].VSize,
			Mips:   len(tex.Mips),
		}
		img, err := DecodeTexture(tex)
		if err != nil {
			rep.Failed = append(rep.Failed, fmt.Sprintf("%s: %v", te.Name, err))
			continue
		}
		// Lineage 2 packages may contain several exports with the same name;
		// suffix them so each one keeps its own file and can be edited alone.
		safe := SafeFileName(te.Name)
		key := strings.ToLower(safe)
		used[key]++
		if n := used[key]; n > 1 {
			safe = fmt.Sprintf("%s_%d", safe, n)
		}

		if opts.WritePNG {
			name := safe + ".png"
			path := filepath.Join(outDir, name)
			entry.PNG = name
			if fileExists(path) && !opts.Overwrite {
				rep.Skipped = append(rep.Skipped, name)
			} else if err := imgio.SavePNG(path, img); err != nil {
				rep.Failed = append(rep.Failed, fmt.Sprintf("%s: %v", te.Name, err))
				continue
			} else {
				rep.Written = append(rep.Written, name)
			}
		}
		if opts.WriteDDS {
			name := safe + ".dds"
			path := filepath.Join(outDir, name)
			entry.DDS = name
			if fileExists(path) && !opts.Overwrite {
				rep.Skipped = append(rep.Skipped, name)
			} else if err := WriteTextureDDS(path, tex); err != nil {
				rep.Failed = append(rep.Failed, fmt.Sprintf("%s: %v", te.Name, err))
				continue
			} else {
				rep.Written = append(rep.Written, name)
			}
		}
		manifest.Textures = append(manifest.Textures, entry)
	}
	// Keep the manifest stable/diffable.
	sort.Slice(manifest.Textures, func(a, b int) bool { return manifest.Textures[a].Name < manifest.Textures[b].Name })
	if data, err := json.MarshalIndent(manifest, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(outDir, ManifestName), data, 0o644)
	}
	return rep, nil
}

func relPath(base, target string) string {
	if r, err := filepath.Rel(base, target); err == nil {
		return r
	}
	return target
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// nextPow2 returns the smallest power of two that is >= v (minimum 1).
func nextPow2(v int) int {
	if v <= 1 {
		return 1
	}
	n := 1
	for n < v {
		n <<= 1
	}
	return n
}

// SafeFileName strips characters that Windows does not allow in file names.
func SafeFileName(name string) string {
	repl := func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}
	out := strings.Map(repl, name)
	if out == "" {
		return "unnamed"
	}
	return out
}

// DecodeTexture decodes the largest mip level that still has pixel data.
func DecodeTexture(t *upkg.Texture) (*dxt.Image, error) {
	if len(t.Mips) == 0 {
		return nil, errors.New("texture has no mip levels")
	}
	var m upkg.Mip
	found := false
	for _, cand := range t.Mips {
		if len(cand.Data) > 0 {
			m = cand
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("all mip levels of this texture are empty")
	}
	switch t.Format {
	case upkg.FmtDXT1:
		return dxt.DecodeDXT1(m.Data, m.USize, m.VSize), nil
	case upkg.FmtDXT3:
		return dxt.DecodeDXT3(m.Data, m.USize, m.VSize), nil
	case upkg.FmtDXT5:
		return dxt.DecodeDXT5(m.Data, m.USize, m.VSize), nil
	case upkg.FmtDXT5N:
		return dxt.DecodeDXT5N(m.Data, m.USize, m.VSize), nil
	case upkg.FmtRGBA8:
		img := dxt.NewImage(m.USize, m.VSize)
		for k := 0; k < m.USize*m.VSize && k*4+3 < len(m.Data); k++ {
			img.Pix[k] = dxt.RGBA{R: m.Data[k*4+2], G: m.Data[k*4+1], B: m.Data[k*4+0], A: m.Data[k*4+3]}
		}
		return img, nil
	case upkg.FmtL8, upkg.FmtP8:
		img := dxt.NewImage(m.USize, m.VSize)
		for k := 0; k < m.USize*m.VSize && k < len(m.Data); k++ {
			v := m.Data[k]
			img.Pix[k] = dxt.RGBA{R: v, G: v, B: v, A: 255}
		}
		return img, nil
	case upkg.FmtRGB16:
		img := dxt.NewImage(m.USize, m.VSize)
		for k := 0; k < m.USize*m.VSize && k*2+1 < len(m.Data); k++ {
			v := uint16(m.Data[k*2]) | uint16(m.Data[k*2+1])<<8
			img.Pix[k] = dxt.RGBA{
				R: uint8((v>>11)&0x1F) << 3, G: uint8((v>>5)&0x3F) << 2, B: uint8(v&0x1F) << 3, A: 255,
			}
		}
		return img, nil
	}
	return nil, fmt.Errorf("unsupported texture format %s", upkg.FormatName(t.Format))
}

// WriteTextureDDS writes a DDS with the texture's full mip chain. For DXT
// formats the original blocks are written verbatim, which makes the file a
// lossless carrier for re-import.
func WriteTextureDDS(path string, t *upkg.Texture) error {
	var pf imgio.PixelFormat
	switch t.Format {
	case upkg.FmtDXT1:
		pf = imgio.PixelDXT1
	case upkg.FmtDXT3:
		pf = imgio.PixelDXT3
	case upkg.FmtDXT5:
		pf = imgio.PixelDXT5
	case upkg.FmtDXT5N:
		pf = imgio.PixelDXT5
	default:
		// Fall back to an uncompressed RGBA8 DDS.
		img, err := DecodeTexture(t)
		if err != nil {
			return err
		}
		raw := make([]byte, 0, img.W*img.H*4)
		for _, c := range img.Pix {
			raw = append(raw, c.R, c.G, c.B, c.A)
		}
		data := imgio.EncodeDDS(imgio.PixelRGBA8, img.W, img.H, [][]byte{raw})
		return os.WriteFile(path, data, 0o644)
	}
	mips := make([][]byte, len(t.Mips))
	for i, m := range t.Mips {
		mips[i] = m.Data
	}
	data := imgio.EncodeDDS(pf, t.Mips[0].USize, t.Mips[0].VSize, mips)
	return os.WriteFile(path, data, 0o644)
}

// ImportOptions controls import behaviour.
type ImportOptions struct {
	OutPath     string // where to write ("" = in place)
	Backup      bool
	ForceFormat int  // -1 keeps the texture's own format
	NoMips      bool // store only the base mip
}

// ImportResult reports what a single image did.
type ImportResult struct {
	Image    string
	Object   string
	OldW, OldH int
	NewW, NewH int
	OldFormat  string
	NewFormat  string
	Lossless   bool
	Bytes      int
}

// ImportImage replaces every texture object whose name matches the image's base
// file name. Lineage 2 packages sometimes hold several objects with the same
// name; a file called "name_2.png" targets the second one instead of all of
// them (that is how duplicate names are exported).
func (i *Info) ImportImage(imagePath string, opts ImportOptions) ([]*ImportResult, error) {
	base := strings.TrimSuffix(filepath.Base(imagePath), filepath.Ext(imagePath))
	targets := i.texturesNamed(base)
	if len(targets) == 0 {
		// "name_2" style suffix for packages with duplicate object names.
		if stem, n, ok := splitDuplicateSuffix(base); ok {
			if dups := i.texturesNamed(stem); len(dups) >= n {
				targets = dups[n-1 : n]
			}
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no texture named %q in %s", base, filepath.Base(i.Path))
	}
	var results []*ImportResult
	var lastErr error
	for _, te := range targets {
		res, err := i.importIntoTexture(te, imagePath, opts)
		if err != nil {
			lastErr = err
			continue
		}
		results = append(results, res)
	}
	if len(results) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no texture named %q in %s", base, filepath.Base(i.Path))
	}
	return results, nil
}

// texturesNamed returns all texture exports with the given object name.
func (i *Info) texturesNamed(name string) []*upkg.TextureExport {
	var out []*upkg.TextureExport
	for k := range i.Textures {
		te := &i.Textures[k]
		if te.Skipped || !strings.EqualFold(te.Name, name) {
			continue
		}
		out = append(out, te)
	}
	return out
}

// splitDuplicateSuffix recognises the "name_2" pattern used for duplicates.
func splitDuplicateSuffix(base string) (string, int, bool) {
	idx := strings.LastIndexByte(base, '_')
	if idx <= 0 || idx == len(base)-1 {
		return "", 0, false
	}
	n := 0
	for _, r := range base[idx+1:] {
		if r < '0' || r > '9' {
			return "", 0, false
		}
		n = n*10 + int(r-'0')
		if n > 9999 {
			return "", 0, false
		}
	}
	if n < 2 {
		return "", 0, false
	}
	return base[:idx], n, true
}

// ImportImageAt replaces one specific export (used for folders exported by this
// tool, where duplicate object names are distinguished by index).
func (i *Info) ImportImageAt(exportIndex int, imagePath string, opts ImportOptions) (*ImportResult, error) {
	for k := range i.Textures {
		te := &i.Textures[k]
		if te.Index != exportIndex || te.Skipped {
			continue
		}
		return i.importIntoTexture(te, imagePath, opts)
	}
	return nil, fmt.Errorf("export %d is not a texture", exportIndex)
}

// importIntoTexture is the shared replacement routine.
func (i *Info) importIntoTexture(te *upkg.TextureExport, imagePath string, opts ImportOptions) (*ImportResult, error) {
	if te.Tex == nil {
		return nil, fmt.Errorf("texture %q could not be parsed: %v", te.Name, te.Err)
	}
	tex := te.Tex
	res := &ImportResult{
		Image:     filepath.Base(imagePath),
		Object:    te.Name,
		OldW:      tex.Mips[0].USize,
		OldH:      tex.Mips[0].VSize,
		OldFormat: upkg.FormatName(tex.Format),
	}

	// DDS input can carry the exact original blocks for a lossless round-trip.
	img, fmtFromFile, err := imgio.Load(imagePath)
	if err != nil {
		return nil, err
	}

	format := tex.Format
	if opts.ForceFormat >= 0 {
		format = opts.ForceFormat
	}
	if format != upkg.FmtDXT1 && format != upkg.FmtDXT3 && format != upkg.FmtDXT5 && format != upkg.FmtRGBA8 {
		// Existing format cannot be produced by this tool; switch to DXT5 so the
		// replacement still works. The property list is patched accordingly.
		format = upkg.FmtDXT5
	}

	// Fast path: identical DDS that matches the current format and dimensions.
	if fmtFromFile == imgio.FormatDDS && opts.ForceFormat < 0 && len(img.Pix) > 0 &&
		img.W == tex.Mips[0].USize && img.H == tex.Mips[0].VSize {
		if dds, err := imgio.DecodeDDS(mustRead(imagePath)); err == nil {
			if ddsFormatMatches(dds.Format, tex.Format) && mipChainMatches(dds, tex) {
				for k := range tex.Mips {
					tex.Mips[k].Data = append([]byte(nil), dds.MipData[k]...)
				}
				res.Lossless = true
				res.NewW, res.NewH = tex.Mips[0].USize, tex.Mips[0].VSize
				res.NewFormat = upkg.FormatName(tex.Format)
				return res, nil
			}
		}
	}

	// Re-encode from pixels.
	// Unreal Engine 2 stores texture sizes as powers of two (UBits/VBits are the
	// log2 of the dimensions), so round unusual sizes up and say so.
	if w2, h2 := nextPow2(img.W), nextPow2(img.H); w2 != img.W || h2 != img.H {
		if w2 > 4096 || h2 > 4096 {
			return nil, fmt.Errorf("%dx%d is too large (limit 4096)", img.W, img.H)
		}
		img = dxt.ResizeTo(img, w2, h2)
	}
	res.NewW, res.NewH = img.W, img.H
	res.NewFormat = upkg.FormatName(format)
	levels := []*dxt.Image{img}
	if !opts.NoMips {
		levels = dxt.GenerateMips(img)
	}
	mips, err := EncodeMips(levels, format)
	if err != nil {
		return nil, err
	}
	tex.Mips = mips
	if err := tex.SetFormatAndSize(i.Pkg, format, img.W, img.H); err != nil {
		return nil, err
	}
	for _, m := range mips {
		res.Bytes += len(m.Data)
	}
	return res, nil
}

func mustRead(path string) []byte {
	d, _ := os.ReadFile(path)
	return d
}

func ddsFormatMatches(dds imgio.PixelFormat, format int) bool {
	switch format {
	case upkg.FmtDXT1:
		return dds == imgio.PixelDXT1
	case upkg.FmtDXT3:
		return dds == imgio.PixelDXT3
	case upkg.FmtDXT5:
		return dds == imgio.PixelDXT5
	case upkg.FmtRGBA8:
		return dds == imgio.PixelRGBA8
	}
	return false
}

func mipChainMatches(dds *imgio.DDS, t *upkg.Texture) bool {
	if len(dds.MipData) != len(t.Mips) {
		return false
	}
	for k := range dds.MipData {
		if len(dds.MipData[k]) != len(t.Mips[k].Data) {
			return false
		}
	}
	return true
}

// EncodeMips encodes a mip chain into upkg mip records.
func EncodeMips(levels []*dxt.Image, format int) ([]upkg.Mip, error) {
	out := make([]upkg.Mip, 0, len(levels))
	for _, img := range levels {
		var data []byte
		switch format {
		case upkg.FmtDXT1:
			data = dxt.EncodeDXT1(img)
		case upkg.FmtDXT3:
			data = dxt.EncodeDXT3(img)
		case upkg.FmtDXT5:
			data = dxt.EncodeDXT5(img)
		case upkg.FmtRGBA8:
			data = make([]byte, 0, img.W*img.H*4)
			for _, c := range img.Pix {
				data = append(data, c.B, c.G, c.R, c.A) // BGRA in memory
			}
		default:
			return nil, fmt.Errorf("cannot encode %s", upkg.FormatName(format))
		}
		want := upkg.ExpectedDataSize(format, img.W, img.H)
		if want != 0 && want != len(data) {
			return nil, fmt.Errorf("encoder produced %d bytes for %dx%d %s, expected %d",
				len(data), img.W, img.H, upkg.FormatName(format), want)
		}
		out = append(out, upkg.Mip{
			USize: img.W, VSize: img.H,
			UBits: byte(upkg.Log2(img.W)), VBits: byte(upkg.Log2(img.H)),
			Data:  data,
		})
	}
	return out, nil
}

// Save writes the package to disk (making a .bak copy unless disabled).
func (i *Info) Save(outPath string, backup bool) (string, error) {
	data, err := i.Pkg.Marshal()
	if err != nil {
		return "", err
	}
	if err := upkg.Validate(data); err != nil {
		return "", fmt.Errorf("internal check failed, refusing to write: %w", err)
	}
	if outPath == "" {
		outPath = i.Path
	}
	outPath = absPath(outPath)
	if backup {
		if _, err := os.Stat(i.Path); err == nil {
			bak := i.Path + ".bak"
			if _, err := os.Stat(bak); os.IsNotExist(err) {
				if err := copyFile(i.Path, bak); err != nil {
					return "", fmt.Errorf("cannot create backup: %w", err)
				}
			}
		}
	}
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, outPath); err != nil {
		os.Remove(tmp)
		return "", err
	}
	i.Path = outPath
	return outPath, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// LoadManifest reads the export manifest from a folder and resolves the package
// it points at. If the recorded path no longer exists (the folder was moved),
// the package is looked for next to the folder and in the working directory.
func LoadManifest(dir string) (*Manifest, string, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, "", fmt.Errorf("broken %s: %w", ManifestName, err)
	}
	candidates := []string{}
	if filepath.IsAbs(m.Package) {
		candidates = append(candidates, m.Package)
	} else {
		candidates = append(candidates, filepath.Join(dir, m.Package))
	}
	if m.PackageName != "" {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(filepath.Clean(dir)), m.PackageName))
		if wd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(wd, m.PackageName))
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return &m, c, nil
		}
	}
	resolved := m.Package
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(dir, m.Package)
	}
	return &m, resolved, nil
}
