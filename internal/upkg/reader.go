package upkg

import (
	"errors"
	"fmt"
	"strings"
)

// Package magic numbers.
const (
	TagUE2      = 0x9E2A83C1
	TagUE2Swap  = 0xC1832A9E // byte-swapped (big endian) packages, unsupported
	HeaderStart = 0
)

// Generation mirrors FGenerationInfo.
type Generation struct {
	ExportCount uint32
	NameCount   uint32
}

// NameEntry is one entry of the name table.
type NameEntry struct {
	Str   string
	Flags uint32
}

// ImportEntry mirrors FObjectImport (UE2 flavour).
type ImportEntry struct {
	ClassPackage int32 // name index
	ClassName    int32 // name index
	PackageIndex int32 // object index (typically 0 or negative)
	ObjectName   int32 // name index
}

// ExportEntry mirrors FObjectExport (UE2 flavour).
type ExportEntry struct {
	ClassIndex   int32
	SuperIndex   int32
	PackageIndex uint32
	ObjectName   int32 // name index
	ObjectFlags  uint32
	SerialSize   int32
	SerialOffset int32

	// Data is the serialized object data (tagged properties + native part).
	Data []byte

	// Pad holds alignment bytes that sat in front of this export's data in the
	// source file. It is written back verbatim so untouched packages keep their
	// exact original layout.
	Pad []byte

	// rebuild, when non-nil, re-serializes Data for a given absolute start
	// offset in the output file (needed for textures, which embed absolute
	// file offsets inside their mip records).
	rebuild func(absStart int64) []byte
	// editable holds the parsed texture, if Data is a UTexture payload.
	tex *Texture
}

// Package is a parsed Unreal Engine 2 package.
type Package struct {
	Version         uint16
	LicenseeVersion uint16
	Flags           uint32
	GUID            [16]byte
	Generations     []Generation

	Names   []NameEntry
	Imports []ImportEntry
	Exports []ExportEntry

	// Trailer holds any bytes that follow the export table in the original
	// file (some L2 packages carry a small watermark there). It is written
	// back verbatim so round-trips stay byte-exact.
	Trailer []byte
	// TailPad holds alignment bytes between the last object blob and the
	// import table.
	TailPad []byte

	// HeaderSize is the size of the file summary in the source file.
	HeaderSize int
	// HeaderExtra holds bytes between the file summary and the name table.
	HeaderExtra []byte
	// OriginalSize is the size of the source file.
	OriginalSize int
}

// ErrNotPackage reports data that does not start with the UE2 package tag.
var ErrNotPackage = errors.New("not an Unreal package (bad tag)")

// PackageInfo is a cheap classification of a file's contents.
type PackageInfo struct {
	IsPackage   bool
	Compressed  bool // starts with a compression/encryption wrapper
	Reason      string
	VersionText string
}

// Identify performs a cheap check of the first bytes of a file.
func Identify(data []byte) PackageInfo {
	if len(data) < 32 {
		return PackageInfo{Reason: "file too small"}
	}
	tag := getU32(data, 0)
	switch tag {
	case TagUE2:
		ver := uint16(data[4]) | uint16(data[5])<<8
		return PackageInfo{IsPackage: true, VersionText: fmt.Sprintf("version %d", ver)}
	case 0xCEFAEDFF, 0xFFEDAECE, 0xCEFAEDFE:
		return PackageInfo{
			Compressed: true,
			Reason: "this is a protected Lineage 2 client file (encrypted/compressed " +
				"wrapper, not a plain package) - use the unpacked .utx from your client " +
				"patch source or decompress it first",
		}
	case TagUE2Swap:
		return PackageInfo{Reason: "big-endian package (not supported)"}
	}
	// Deflate-compressed UE1/UE2 packages start with a small header; be helpful.
	if tag&0xFFFF0000 == 0 || len(data) > 8 && getU32(data, 4) == TagUE2 {
		return PackageInfo{Compressed: true, Reason: "compressed package wrapper"}
	}
	return PackageInfo{Reason: fmt.Sprintf("unknown header (%08X)", tag)}
}

// Parse decodes a package from memory.
func Parse(data []byte) (*Package, error) {
	if len(data) < 0x40 {
		return nil, errors.New("file is too small to be an Unreal package")
	}
	r := &reader{d: data}
	if tag := r.u32(); tag != TagUE2 {
		info := Identify(data)
		if info.Compressed {
			return nil, fmt.Errorf("%s - extract/decompress it first", info.Reason)
		}
		return nil, ErrNotPackage
	}
	p := &Package{
		Version:      r.u16(),
		OriginalSize: len(data),
	}
	p.LicenseeVersion = r.u16()
	p.Flags = r.u32()

	nameCount := r.u32()
	nameOffset := r.u32()
	exportCount := r.u32()
	exportOffset := r.u32()
	importCount := r.u32()
	importOffset := r.u32()

	copy(p.GUID[:], r.bytes(16))
	genCount := r.u32()
	if r.err != nil {
		return nil, r.err
	}
	if genCount > 256 {
		return nil, fmt.Errorf("implausible generation count %d", genCount)
	}
	for i := uint32(0); i < genCount; i++ {
		p.Generations = append(p.Generations, Generation{
			ExportCount: r.u32(),
			NameCount:   r.u32(),
		})
	}
	if r.err != nil {
		return nil, r.err
	}
	p.HeaderSize = r.p
	// Some builds keep extra bytes between the file summary and the name table.
	if int(nameOffset) > p.HeaderSize && int(nameOffset) <= len(data) {
		p.HeaderExtra = append([]byte(nil), data[p.HeaderSize:int(nameOffset)]...)
	}

	// --- name table
	if err := bounds(nameOffset, nameCount, 5, len(data), "name"); err != nil {
		return nil, err
	}
	r.p = int(nameOffset)
	p.Names = make([]NameEntry, nameCount)
	for i := range p.Names {
		str := r.nameString()
		flags := r.u32()
		if r.err != nil {
			return nil, fmt.Errorf("name table entry %d: %w", i, r.err)
		}
		p.Names[i] = NameEntry{Str: str, Flags: flags}
	}

	// --- import table
	if err := bounds(importOffset, importCount, 7, len(data), "import"); err != nil {
		return nil, err
	}
	r.p = int(importOffset)
	p.Imports = make([]ImportEntry, importCount)
	for i := range p.Imports {
		p.Imports[i] = ImportEntry{
			ClassPackage: r.compact(),
			ClassName:    r.compact(),
			PackageIndex: r.i32(),
			ObjectName:   r.compact(),
		}
		if r.err != nil {
			return nil, fmt.Errorf("import table entry %d: %w", i, r.err)
		}
	}

	// --- export table
	if err := bounds(exportOffset, exportCount, 15, len(data), "export"); err != nil {
		return nil, err
	}
	r.p = int(exportOffset)
	p.Exports = make([]ExportEntry, exportCount)
	for i := range p.Exports {
		e := &p.Exports[i]
		e.ClassIndex = r.compact()
		e.SuperIndex = r.compact()
		e.PackageIndex = r.u32()
		e.ObjectName = r.compact()
		e.ObjectFlags = r.u32()
		e.SerialSize = r.compact()
		if e.SerialSize > 0 {
			e.SerialOffset = r.compact()
		}
		if r.err != nil {
			return nil, fmt.Errorf("export table entry %d: %w", i, r.err)
		}
		if e.SerialSize < 0 || e.SerialSize > int32(len(data)) {
			return nil, fmt.Errorf("export %d has implausible size %d", i, e.SerialSize)
		}
		if e.SerialSize > 0 {
			if e.SerialOffset < 0 || int(e.SerialOffset)+int(e.SerialSize) > len(data) {
				return nil, fmt.Errorf("export %d points outside the file (offset %d, size %d)",
					i, e.SerialOffset, e.SerialSize)
			}
			e.Data = data[e.SerialOffset : int(e.SerialOffset)+int(e.SerialSize)]
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.p < len(data) {
		p.Trailer = data[r.p:]
	}
	p.captureLayout(data, importOffset)
	return p, nil
}

// captureLayout records the padding bytes that the source file has between the
// object blobs, so a package can be re-serialized without moving anything.
func (p *Package) captureLayout(data []byte, importOffset uint32) {
	if len(p.Exports) == 0 {
		return
	}
	cursor := p.HeaderSize + len(p.HeaderExtra)
	// Skip the name table: exports start after it.
	cursor = int(p.nameEndOffsetApprox())
	ordered := true
	last := -1
	for i := range p.Exports {
		e := &p.Exports[i]
		if e.SerialSize == 0 {
			continue
		}
		if last >= 0 && int(e.SerialOffset) < last {
			ordered = false
			break
		}
		last = int(e.SerialOffset) + int(e.SerialSize)
	}
	if !ordered {
		return
	}
	maxEnd := cursor
	for i := range p.Exports {
		e := &p.Exports[i]
		if e.SerialSize == 0 {
			continue
		}
		off := int(e.SerialOffset)
		if off < cursor || off > len(data) {
			return
		}
		if off > cursor {
			e.Pad = append([]byte(nil), data[cursor:off]...)
		}
		cursor = off + int(e.SerialSize)
		if cursor > maxEnd {
			maxEnd = cursor
		}
	}
	if int(importOffset) > maxEnd && int(importOffset) <= len(data) {
		p.TailPad = append([]byte(nil), data[maxEnd:importOffset]...)
	}
}

func (p *Package) nameEndOffsetApprox() int64 {
	n := int64(p.HeaderSize + len(p.HeaderExtra))
	for _, nm := range p.Names {
		n += int64(nameEntrySize(nm.Str))
	}
	return n
}

func bounds(offset, count uint32, minEntry int, fileLen int, what string) error {
	if count == 0 {
		return nil
	}
	if count > 1<<22 {
		return fmt.Errorf("implausible %s count %d", what, count)
	}
	end := uint64(offset) + uint64(count)*uint64(minEntry)
	if end > uint64(fileLen) || offset > uint32(fileLen) {
		return fmt.Errorf("%s table (offset %d, count %d) is outside the file", what, offset, count)
	}
	return nil
}

// Name returns the name-table string at index i (or a placeholder).
func (p *Package) Name(i int32) string {
	if i < 0 || int(i) >= len(p.Names) {
		return fmt.Sprintf("<bad name %d>", i)
	}
	return p.Names[i].Str
}

// NameIndex finds a name index by (case-insensitive) string.
func (p *Package) NameIndex(s string) int32 {
	for i := range p.Names {
		if strings.EqualFold(p.Names[i].Str, s) {
			return int32(i)
		}
	}
	return -1
}

// AddName appends a name-table entry and returns its index.
func (p *Package) AddName(s string) int32 {
	if i := p.NameIndex(s); i >= 0 {
		return i
	}
	p.Names = append(p.Names, NameEntry{Str: s, Flags: 0x00070010})
	return int32(len(p.Names) - 1)
}

// ObjectRef renders an object reference (positive = export, negative = import).
func (p *Package) ObjectRef(i int32) string {
	switch {
	case i == 0:
		return "None"
	case i < 0:
		idx := int(-i - 1)
		if idx < len(p.Imports) {
			imp := p.Imports[idx]
			return fmt.Sprintf("%s.%s'%s'", p.Name(imp.ClassPackage), p.Name(imp.ClassName), p.Name(imp.ObjectName))
		}
		return fmt.Sprintf("<bad import %d>", i)
	default:
		idx := int(i - 1)
		if idx < len(p.Exports) {
			return fmt.Sprintf("Export%d(%s)", idx, p.Name(p.Exports[idx].ObjectName))
		}
		return fmt.Sprintf("<bad export %d>", i)
	}
}

// ClassName returns the class name of an export.
func (p *Package) ClassName(e *ExportEntry) string {
	if e.ClassIndex < 0 {
		idx := int(-e.ClassIndex - 1)
		if idx < len(p.Imports) {
			return p.Name(p.Imports[idx].ObjectName)
		}
		return ""
	}
	return p.Name(e.ClassIndex)
}

// ClassPackage returns the package that defines the export's class (e.g. "Engine").
func (p *Package) ClassPackage(e *ExportEntry) string {
	if e.ClassIndex < 0 {
		idx := int(-e.ClassIndex - 1)
		if idx < len(p.Imports) {
			return p.Name(p.Imports[idx].ClassPackage)
		}
	}
	return ""
}
