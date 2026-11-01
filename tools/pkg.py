#!/usr/bin/env python3
"""Independent UE2/Lineage 2 package dumper.

This is a second, deliberately separate implementation of the reader used to
cross-check the Go tool's output while the format was being reverse engineered.
It reads the file summary, name table, imports and exports, verifies that export
blobs tile the data region and prints one line per texture.

usage: python tools/pkg.py file.utx [file2.utx ...]
"""
import os
import struct
import sys


def cidx(d, p):
    """Read a UE2 compact index."""
    out = 0
    sign = False
    for i in range(5):
        x = d[p]
        p += 1
        if i == 0:
            if x & 0x80:
                sign = True
            out |= x & 0x3F
            if not (x & 0x40):
                break
        elif i == 4:
            out |= (x & 0x1F) << 27
        else:
            out |= (x & 0x7F) << (6 + (i - 1) * 7)
            if not (x & 0x80):
                break
    return (-out if sign else out), p


class Pkg:
    def __init__(self, path):
        self.path = path
        self.d = d = open(path, 'rb').read()
        tag, ver, lic, flags, nc, no, ec, eo, ic, io = struct.unpack_from('<IHHIIIIIII', d, 0)
        if tag != 0x9E2A83C1:
            raise ValueError('not a UE2 package (tag %08X)' % tag)
        self.ver, self.lic, self.flags = ver, lic, flags
        self.nameCount, self.nameOffset = nc, no
        self.exportCount, self.exportOffset = ec, eo
        self.importCount, self.importOffset = ic, io

        p = no
        self.names, self.nameFlags = [], []
        for _ in range(nc):
            n = d[p]
            p += 1
            self.names.append(d[p:p + n - 1].decode('latin-1'))
            p += n
            self.nameFlags.append(struct.unpack_from('<I', d, p)[0])
            p += 4
        self.namesEnd = p

        p = io
        self.imports = []
        for _ in range(ic):
            cp, p = cidx(d, p)
            cn, p = cidx(d, p)
            pi = struct.unpack_from('<i', d, p)[0]
            p += 4
            on, p = cidx(d, p)
            self.imports.append((cp, cn, pi, on))
        self.importsEnd = p

        p = eo
        self.exports = []
        for _ in range(ec):
            ci, p = cidx(d, p)
            si, p = cidx(d, p)
            grp = struct.unpack_from('<I', d, p)[0]
            p += 4
            on, p = cidx(d, p)
            fl = struct.unpack_from('<I', d, p)[0]
            p += 4
            sz, p = cidx(d, p)
            off = 0
            if sz > 0:
                off, p = cidx(d, p)
            self.exports.append(dict(cls=ci, sup=si, grp=grp, name=on, flags=fl, size=sz, off=off))
        self.exportsEnd = p

    def nm(self, i):
        return self.names[i] if 0 <= i < len(self.names) else '<bad %d>' % i

    def clsname(self, i):
        if i < 0:
            cp, cn, pi, on = self.imports[-i - 1]
            return '%s.%s' % (self.nm(cp), self.nm(cn))
        return 'Class' if i == 0 else 'export:' + self.nm(self.exports[i - 1]['name'])

    def report(self):
        print('=' * 100)
        print(os.path.basename(self.path), 'version', self.ver, 'licensee', self.lic, 'size', len(self.d))
        print('  names %d @%#x end %#x' % (self.nameCount, self.nameOffset, self.namesEnd))
        print('  imports %d @%#x end %#x' % (self.importCount, self.importOffset, self.importsEnd))
        print('  exports %d @%#x end %#x' % (self.exportCount, self.exportOffset, self.exportsEnd))
        spans = sorted((e['off'], e['off'] + e['size'], e['name']) for e in self.exports if e['size'])
        cursor = self.namesEnd
        gaps = []
        for a, b, name in spans:
            if a > cursor:
                gaps.append((cursor, a))
            cursor = max(cursor, b)
        if gaps:
            print('  gaps between objects:', ['%#x..%#x' % g for g in gaps][:8])
        else:
            print('  object blobs tile the data region exactly')
        print('  trailer:', len(self.d) - self.exportsEnd, 'bytes')
        for e in self.exports:
            print('   %-32s %-14s %8d bytes @%#010x' % (
                self.nm(e['name']), self.clsname(e['cls']), e['size'], e['off']))


if __name__ == '__main__':
    for a in sys.argv[1:]:
        Pkg(a).report()
