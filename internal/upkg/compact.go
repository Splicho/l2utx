// Package upkg implements reading and writing of Unreal Engine 2 packages
// (.utx/.u/.unr) as used by Lineage 2 (package version 126) and UT2003/2004
// (versions 118/123).
//
// Format notes (all little-endian):
//
//	header:   u32 tag(0x9E2A83C1) u16 ver u16 licensee u32 flags
//	          u32 nameCount u32 nameOffset
//	          u32 exportCount u32 exportOffset
//	          u32 importCount u32 importOffset
//	          u8 guid[16] u32 generationCount { u32 exportCount u32 nameCount }[n]
//	name:     u8 length(incl. NUL) char[len] u32 flags
//	import:   compact classPackage, compact className, u32 packageIndex, compact objectName
//	export:   compact classIndex, compact superIndex, u32 packageIndex,
//	          compact objectName, u32 objectFlags, compact serialSize,
//	          compact serialOffset  (offset only present when size != 0)
//
// Integers written as "compact index" occupy 1..5 bytes; see compact.go.
package upkg

import (
	"encoding/binary"
	"fmt"
	"math"
)

// CompactIndexSize returns the number of bytes WriteCompact would emit.
func CompactIndexSize(v int32) int {
	var mag uint64
	if v < 0 {
		mag = uint64(-int64(v))
	} else {
		mag = uint64(v)
	}
	n := 1
	mag >>= 6
	for i := 0; i < 3 && mag != 0; i++ {
		n++
		mag >>= 7
	}
	if mag != 0 {
		n++
	}
	return n
}

// WriteCompact appends the UE2 "compact index" encoding of v to dst.
func WriteCompact(dst []byte, v int32) []byte {
	neg := v < 0
	var mag uint64
	if neg {
		mag = uint64(-int64(v))
	} else {
		mag = uint64(v)
	}

	b0 := byte(mag & 0x3F)
	mag >>= 6
	if mag != 0 {
		b0 |= 0x40
	}
	if neg {
		b0 |= 0x80
	}
	dst = append(dst, b0)
	if mag == 0 {
		return dst
	}
	for i := 0; i < 3; i++ {
		b := byte(mag & 0x7F)
		mag >>= 7
		if mag != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if mag == 0 {
			return dst
		}
	}
	// Fifth byte carries a full 8 bits and has no continuation flag.
	dst = append(dst, byte(mag&0xFF))
	return dst
}

// reader is a bounds-checked little-endian cursor over a byte slice.
type reader struct {
	d   []byte
	p   int
	err error
}

func (r *reader) fail(format string, a ...any) {
	if r.err == nil {
		r.err = fmt.Errorf(format, a...)
	}
}

func (r *reader) remaining() int { return len(r.d) - r.p }

func (r *reader) need(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || r.p+n > len(r.d) {
		r.fail("unexpected end of data at offset %#x (need %d bytes, have %d)", r.p, n, r.remaining())
		return false
	}
	return true
}

func (r *reader) u8() byte {
	if !r.need(1) {
		return 0
	}
	v := r.d[r.p]
	r.p++
	return v
}

func (r *reader) u16() uint16 {
	if !r.need(2) {
		return 0
	}
	v := binary.LittleEndian.Uint16(r.d[r.p:])
	r.p += 2
	return v
}

func (r *reader) u32() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(r.d[r.p:])
	r.p += 4
	return v
}

func (r *reader) i32() int32 { return int32(r.u32()) }

func (r *reader) bytes(n int) []byte {
	if !r.need(n) {
		return nil
	}
	v := r.d[r.p : r.p+n]
	r.p += n
	return v
}

// compact reads a UE2 compact index.
func (r *reader) compact() int32 {
	var out uint64
	neg := false
	for i := 0; i < 5; i++ {
		x := r.u8()
		if r.err != nil {
			return 0
		}
		switch {
		case i == 0:
			if x&0x80 != 0 {
				neg = true
			}
			out |= uint64(x & 0x3F)
			if x&0x40 == 0 {
				goto done
			}
		case i == 4:
			out |= uint64(x) << 27
		default:
			out |= uint64(x&0x7F) << (6 + uint(i-1)*7)
			if x&0x80 == 0 {
				goto done
			}
		}
	}
done:
	// Wrap into 32 bits the same way the engine's int32 arithmetic does.
	v := int32(uint32(out))
	if neg {
		if v == math.MinInt32 {
			return v
		}
		return -v
	}
	return v
}

// nameString reads a name-table string: u8 length (incl. NUL) + chars.
func (r *reader) nameString() string {
	n := int(r.u8())
	if r.err != nil {
		return ""
	}
	if n == 0 {
		return ""
	}
	b := r.bytes(n)
	if b == nil {
		return ""
	}
	// Drop the trailing NUL (and any stray NULs, some packages pad).
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	return string(b[:end])
}

// putCompact appends a compact index to dst (helper for writers).
func putU32(dst []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(dst, tmp[:]...)
}

func getU32(d []byte, off int) uint32 { return binary.LittleEndian.Uint32(d[off:]) }
