package upkg

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// UE2 tagged-property type codes (low nibble of the info byte).
const (
	PropByte       = 0x01
	PropInt        = 0x02
	PropBool       = 0x03
	PropFloat      = 0x04
	PropObject     = 0x05
	PropName       = 0x06
	PropString     = 0x07
	PropClass      = 0x08
	PropArray      = 0x09
	PropStruct     = 0x0A
	PropVector     = 0x0B
	PropRotator    = 0x0C
	PropStr        = 0x0D
	PropMap        = 0x0E
	PropFixedArray = 0x0F

	propArrayFlag = 0x80
)

// structSizes holds the serialized size of the engine structs that show up in
// texture/actor property streams. Only the ones we can size safely are listed;
// anything else aborts property parsing and lets the caller fall back to a
// structural scan.
var structSizes = map[string]int{
	"Color":     4,
	"MaxColor":  4,
	"Vector":    12,
	"Rotator":   12,
	"Plane":     16,
	"Scale":     12,
	"Range":     8,
	"RangeVector": 24,
	"PointRegion": 20,
	"Box":       25,
	"IntPoint":  8,
	"Matrix":    64,
}

// Prop is one tagged property entry inside an object payload.
type Prop struct {
	NameIdx    int32
	Name       string
	Info       byte
	Type       byte
	HasArray   bool
	ArrayIndex int32
	StructName string

	Start    int // offset of the entry (name index) inside the payload
	ValueOff int // offset of the value bytes
	ValueLen int
	End      int
}

// Props is a parsed tagged-property block.
type Props struct {
	Raw  []byte // payload[0:End]; includes the terminating "None" entry
	List []Prop
	End  int
}

// IsNone reports whether a name index refers to "None".
func (p *Package) isNone(idx int32) bool {
	return idx >= 0 && int(idx) < len(p.Names) && strings.EqualFold(p.Names[idx].Str, "None")
}

// ParseProps decodes the tagged property list at the start of payload.
//
// UE2 property encoding:
//
//	compact nameIndex
//	if None -> end of list
//	byte info            (type = info & 0x0F, 0x80 = array element)
//	compact arrayIndex   (only when info & 0x80)
//	value                (layout depends on the property type)
func (p *Package) ParseProps(payload []byte) (*Props, error) {
	r := &reader{d: payload}
	props := &Props{}
	for {
		if r.remaining() <= 1 {
			return nil, fmt.Errorf("property list runs past end of object data")
		}
		start := r.p
		nameIdx := r.compact()
		if r.err != nil {
			return nil, r.err
		}
		if p.isNone(nameIdx) {
			props.End = r.p
			props.Raw = payload[:r.p]
			return props, nil
		}
		if nameIdx < 0 || int(nameIdx) >= len(p.Names) {
			return nil, fmt.Errorf("bad property name index %d at offset %d", nameIdx, start)
		}
		pi := Prop{
			NameIdx: nameIdx,
			Name:    p.Names[nameIdx].Str,
			Start:   start,
			Type:    InfoType(payload[r.p]),
			Info:    payload[r.p],
		}
		r.p++
		if pi.Info&propArrayFlag != 0 {
			pi.HasArray = true
			pi.ArrayIndex = r.compact()
			if r.err != nil {
				return nil, r.err
			}
		}
		pi.ValueOff = r.p

		switch pi.Type {
		case PropByte:
			r.bytes(1)
		case PropBool:
			// Booleans carry their value in bit 0x10 of the info byte; no payload.
			pi.ValueLen = 0
		case PropInt, PropFloat:
			r.bytes(4)
		case PropObject, PropName, PropClass:
			r.compact()
		case PropVector, PropRotator:
			r.bytes(12)
		case PropStr, PropString:
			n := r.compact()
			if r.err != nil {
				return nil, r.err
			}
			if n < 0 || int(n) > r.remaining() {
				return nil, fmt.Errorf("property %q has bad string length %d", pi.Name, n)
			}
			r.bytes(int(n))
		case PropStruct:
			sn := r.compact()
			if r.err != nil {
				return nil, r.err
			}
			structName := p.Name(sn)
			size, ok := structSizes[structName]
			if !ok {
				return nil, fmt.Errorf("property %q uses unsized struct %q", pi.Name, structName)
			}
			pi.StructName = structName
			pi.ValueOff = r.p
			r.bytes(size)
		default:
			return nil, fmt.Errorf("property %q has unsupported type %#x", pi.Name, pi.Type)
		}
		if r.err != nil {
			return nil, r.err
		}
		pi.ValueLen = r.p - pi.ValueOff
		pi.End = r.p
		props.List = append(props.List, pi)
	}
}

// InfoType extracts the property type from an info byte.
func InfoType(info byte) byte { return info & 0x0F }

// Find returns the first property with the given (case-insensitive) name.
func (pr *Props) Find(name string) *Prop {
	for i := range pr.List {
		if strings.EqualFold(pr.List[i].Name, name) {
			return &pr.List[i]
		}
	}
	return nil
}

// ByteValue returns the value of a byte/bool property.
func (pr *Props) ByteValue(name string) (byte, bool) {
	p := pr.Find(name)
	if p == nil || p.ValueLen < 1 {
		return 0, false
	}
	return pr.Raw[p.ValueOff], true
}

// IntValue returns the value of an int/float property.
func (pr *Props) IntValue(name string) (int32, bool) {
	p := pr.Find(name)
	if p == nil || p.ValueLen < 4 {
		return 0, false
	}
	return int32(binary.LittleEndian.Uint32(pr.Raw[p.ValueOff:])), true
}

// SetByte patches a byte property in place.
func (pr *Props) SetByte(name string, v byte) bool {
	p := pr.Find(name)
	if p == nil || p.ValueLen != 1 {
		return false
	}
	pr.Raw[p.ValueOff] = v
	return true
}

// SetInt patches an int property in place.
func (pr *Props) SetInt(name string, v int32) bool {
	p := pr.Find(name)
	if p == nil || p.ValueLen != 4 {
		return false
	}
	binary.LittleEndian.PutUint32(pr.Raw[p.ValueOff:], uint32(v))
	return true
}

// Has reports whether a property with that name exists.
func (pr *Props) Has(name string) bool { return pr.Find(name) != nil }

// AppendProp appends a simple (byte or int) property to the block, keeping the
// "None" terminator last. It returns false for unsupported types.
func (pr *Props) AppendProp(nameIdx int32, typ byte, value int32) bool {
	if pr.End == 0 || pr.End != len(pr.Raw) {
		return false
	}
	body := append([]byte(nil), pr.Raw[:pr.End-1]...) // drop the None entry byte
	body = WriteCompact(body, nameIdx)
	body = append(body, typ)
	switch typ {
	case PropByte, PropBool:
		body = append(body, byte(value))
	case PropInt, PropFloat:
		body = putU32(body, uint32(value))
	default:
		return false
	}
	body = append(body, pr.Raw[pr.End-1]) // re-append the None name index
	pr.Raw = body
	pr.End = len(body)
	// The new entry is not added to List: offsets of existing entries are
	// untouched, and callers that need the new value re-parse the block.
	return true
}

// Log2 returns ceil(log2(v)) with a floor of 0, as used for UBits/VBits.
func Log2(v int) int {
	if v <= 1 {
		return 0
	}
	n := 0
	for x := v - 1; x > 0; x >>= 1 {
		n++
	}
	return n
}

// FloatBits converts a float32 to its int32 bit pattern.
func FloatBits(f float32) int32 { return int32(math.Float32bits(f)) }
