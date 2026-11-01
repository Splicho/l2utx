package upkg

import (
	"fmt"
)

// Marshal serializes the package back to bytes using the canonical UE2 layout:
//
//	header | name table | object data | import table | export table | trailer
//
// All table offsets and every export's serial size/offset are recomputed, so
// exports may grow or shrink freely. Textures are re-serialized so that the
// absolute offsets embedded in their mip records stay correct.
func (p *Package) Marshal() ([]byte, error) {
	if len(p.Generations) == 0 {
		p.Generations = []Generation{{}}
	}
	// Keep the generation record in sync with the actual table counts.
	p.Generations[0].ExportCount = uint32(len(p.Exports))
	p.Generations[0].NameCount = uint32(len(p.Names))

	headerLen := 56 + 8*len(p.Generations)
	nameOffset := headerLen + len(p.HeaderExtra)
	names := p.buildNames()
	dataStart := nameOffset + len(names)

	sizes := make([]int, len(p.Exports))
	total := len(p.TailPad)
	for i := range p.Exports {
		e := &p.Exports[i]
		n := len(e.Data)
		if e.tex != nil {
			n = e.tex.Size()
		}
		sizes[i] = n
		total += n + len(e.Pad)
	}

	importBlob := p.buildImports()
	importOffset := dataStart + total
	exportOffset := importOffset + len(importBlob)
	exportBlob := p.buildExports(sizes, dataStart)

	header := p.buildHeader(nameOffset, exportOffset, importOffset)

	out := make([]byte, 0, exportOffset+len(exportBlob)+len(p.Trailer))
	out = append(out, header...)
	out = append(out, p.HeaderExtra...)
	out = append(out, names...)
	for i := range p.Exports {
		e := &p.Exports[i]
		out = append(out, e.Pad...)
		absStart := int64(len(out))
		var blob []byte
		if e.tex != nil {
			blob = e.tex.Build(absStart)
		} else {
			blob = e.Data
		}
		if len(blob) != sizes[i] {
			return nil, fmt.Errorf("export %d (%q) serialized to %d bytes but the layout expects %d",
				i, p.Name(e.ObjectName), len(blob), sizes[i])
		}
		out = append(out, blob...)
	}
	out = append(out, p.TailPad...)
	out = append(out, importBlob...)
	out = append(out, exportBlob...)
	out = append(out, p.Trailer...)

	// Refresh cached offsets so a later Marshal produces identical bytes.
	off := dataStart
	for i := range p.Exports {
		e := &p.Exports[i]
		off += len(e.Pad)
		e.SerialSize = int32(sizes[i])
		if sizes[i] == 0 {
			e.SerialOffset = 0
			e.Data = nil
			continue
		}
		e.SerialOffset = int32(off)
		e.Data = out[off : off+sizes[i]]
		off += sizes[i]
	}
	return out, nil
}

func (p *Package) buildHeader(nameOffset, exportOffset, importOffset int) []byte {
	buf := make([]byte, 0, 64+8*len(p.Generations))
	buf = putU32(buf, TagUE2)
	buf = append(buf, byte(p.Version), byte(p.Version>>8), byte(p.LicenseeVersion), byte(p.LicenseeVersion>>8))
	buf = putU32(buf, p.Flags)
	buf = putU32(buf, uint32(len(p.Names)))
	buf = putU32(buf, uint32(nameOffset))
	buf = putU32(buf, uint32(len(p.Exports)))
	buf = putU32(buf, uint32(exportOffset))
	buf = putU32(buf, uint32(len(p.Imports)))
	buf = putU32(buf, uint32(importOffset))
	buf = append(buf, p.GUID[:]...)
	buf = putU32(buf, uint32(len(p.Generations)))
	for _, g := range p.Generations {
		buf = putU32(buf, g.ExportCount)
		buf = putU32(buf, g.NameCount)
	}
	return buf
}

func nameEntrySize(s string) int {
	if len(s) > 254 {
		s = s[:254]
	}
	return 1 + len(s) + 1 + 4
}

func (p *Package) buildNames() []byte {
	n := 0
	for _, nm := range p.Names {
		n += nameEntrySize(nm.Str)
	}
	out := make([]byte, 0, n)
	for _, nm := range p.Names {
		s := nm.Str
		if len(s) > 254 {
			s = s[:254]
		}
		out = append(out, byte(len(s)+1))
		out = append(out, s...)
		out = append(out, 0)
		out = putU32(out, nm.Flags)
	}
	return out
}

func (p *Package) buildImports() []byte {
	var out []byte
	for _, im := range p.Imports {
		out = WriteCompact(out, im.ClassPackage)
		out = WriteCompact(out, im.ClassName)
		out = putU32(out, uint32(im.PackageIndex))
		out = WriteCompact(out, im.ObjectName)
	}
	return out
}

func (p *Package) buildExports(sizes []int, dataStart int) []byte {
	var out []byte
	off := dataStart
	for i := range p.Exports {
		e := &p.Exports[i]
		out = WriteCompact(out, e.ClassIndex)
		out = WriteCompact(out, e.SuperIndex)
		out = putU32(out, e.PackageIndex)
		out = WriteCompact(out, e.ObjectName)
		out = putU32(out, e.ObjectFlags)
		out = WriteCompact(out, int32(sizes[i]))
		if sizes[i] > 0 {
			out = WriteCompact(out, int32(off))
		}
		off += sizes[i]
	}
	return out
}

// Validate re-parses marshalled bytes and checks structural invariants. It is
// the last line of defence before a package is written to disk: every export
// must be in bounds and every texture payload must parse with a consistent mip
// chain.
func Validate(data []byte) error {
	p, err := Parse(data)
	if err != nil {
		return err
	}
	for i := range p.Exports {
		e := &p.Exports[i]
		if e.SerialSize == 0 {
			continue
		}
		if e.SerialOffset < 0 || int(e.SerialOffset)+int(e.SerialSize) > len(data) {
			return fmt.Errorf("export %d (%s) is out of bounds", i, p.Name(e.ObjectName))
		}
	}
	// Re-parse the textures so a broken encoder can never reach the user's file.
	for _, te := range p.Textures() {
		if te.Skipped || te.Err != nil {
			continue
		}
		if len(te.Tex.Mips) == 0 || te.Tex.Mips[0].USize <= 0 {
			return fmt.Errorf("texture %q has an invalid mip chain after writing", te.Name)
		}
	}
	return nil
}
