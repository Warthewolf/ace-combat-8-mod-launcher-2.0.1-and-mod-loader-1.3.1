package main

// A reader for the parts of an IoStore table of contents (.utoc) the launcher
// needs: which packages a container holds (to spot two mods changing the same
// game file) and the file names (to show what a mod changes).

import (
	"encoding/binary"
	"errors"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf16"
)

type utocInfo struct {
	chunkIDs []uint64
	types    []byte
	files    map[uint32]string // chunk index -> file path
}

var utocMagic = []byte("-==--==--==--==-")

func readUtoc(p string) (*utocInfo, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > 64<<20 {
		return nil, errors.New("too big")
	}
	d, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return parseUtoc(d)
}

func parseUtoc(d []byte) (*utocInfo, error) {
	if len(d) < 144 || string(d[:16]) != string(utocMagic) {
		return nil, errors.New("not a .utoc file")
	}
	le := binary.LittleEndian
	ver := d[16]
	hdrSize := le.Uint32(d[20:])
	entries := le.Uint32(d[24:])
	blocks := le.Uint32(d[28:])
	blockSize := le.Uint32(d[32:])
	methods := le.Uint32(d[36:])
	methodLen := le.Uint32(d[40:])
	dirIndexSize := le.Uint32(d[48:])
	flags := d[80]
	seeds := le.Uint32(d[84:])
	noHash := le.Uint32(d[96:])
	if hdrSize < 144 || entries > 1<<22 || blocks > 1<<26 {
		return nil, errors.New("unexpected .utoc header")
	}
	need := uint64(hdrSize) + uint64(entries)*12
	if need > uint64(len(d)) {
		return nil, errors.New("short .utoc")
	}
	u := &utocInfo{files: map[uint32]string{}}
	o := uint64(hdrSize)
	for i := uint32(0); i < entries; i++ {
		u.chunkIDs = append(u.chunkIDs, le.Uint64(d[o:]))
		u.types = append(u.types, d[o+11])
		o += 12
	}
	o += uint64(entries) * 10 // offsets and lengths
	if ver >= 4 {
		o += uint64(seeds) * 4
	}
	if ver >= 5 {
		o += uint64(noHash) * 4
	}
	o += uint64(blocks)*uint64(blockSize) + uint64(methods)*uint64(methodLen)
	const flagEncrypted, flagSigned, flagIndexed = 0x2, 0x4, 0x8
	if flags&flagSigned != 0 {
		if o+4 > uint64(len(d)) {
			return u, nil
		}
		hs := uint64(le.Uint32(d[o:]))
		o += 4 + 2*hs + uint64(blocks)*20
	}
	if flags&flagIndexed == 0 || flags&flagEncrypted != 0 || dirIndexSize == 0 || o+uint64(dirIndexSize) > uint64(len(d)) {
		return u, nil
	}
	u.readDirectoryIndex(d[o : o+uint64(dirIndexSize)])
	return u, nil
}

func (u *utocInfo) readDirectoryIndex(d []byte) {
	le := binary.LittleEndian
	o := 0
	ok := true
	u32 := func() uint32 {
		if o+4 > len(d) {
			ok = false
			return 0
		}
		v := le.Uint32(d[o:])
		o += 4
		return v
	}
	str := func() string {
		n := int32(u32())
		if !ok || n == 0 {
			return ""
		}
		if n > 1<<16 || n < -(1<<16) { // no real file name is this long: a damaged file
			ok = false
			return ""
		}
		if n > 0 {
			if o+int(n) > len(d) {
				ok = false
				return ""
			}
			s := string(d[o : o+int(n)-1])
			o += int(n)
			return s
		}
		n = -n
		if o+2*int(n) > len(d) {
			ok = false
			return ""
		}
		w := make([]uint16, n-1)
		for i := range w {
			w[i] = le.Uint16(d[o+2*i:])
		}
		o += 2 * int(n)
		return string(utf16.Decode(w))
	}
	mount := str()
	nd := int(u32())
	if !ok || nd > 1<<20 {
		return
	}
	type dirE struct{ name, child, sibling, file uint32 }
	dirs := make([]dirE, 0, nd)
	for i := 0; i < nd; i++ {
		dirs = append(dirs, dirE{u32(), u32(), u32(), u32()})
	}
	nf := int(u32())
	if !ok || nf > 1<<22 {
		return
	}
	type fileE struct{ name, next, data uint32 }
	files := make([]fileE, 0, nf)
	for i := 0; i < nf; i++ {
		files = append(files, fileE{u32(), u32(), u32()})
	}
	ns := int(u32())
	if !ok || ns > 1<<22 {
		return
	}
	strs := make([]string, 0, ns)
	for i := 0; i < ns && ok; i++ {
		strs = append(strs, str())
	}
	if !ok || len(dirs) == 0 {
		return
	}
	const none = 0xFFFFFFFF
	name := func(i uint32) string {
		if int(i) < len(strs) {
			return strs[i]
		}
		return ""
	}
	var walk func(i uint32, p string, depth int)
	walk = func(i uint32, p string, depth int) {
		if depth > 64 || int(i) >= len(dirs) {
			return
		}
		dn := dirs[i]
		if dn.name != none {
			p += name(dn.name) + "/"
		}
		for f, n := dn.file, 0; f != none && int(f) < len(files) && n < len(files); f, n = files[f].next, n+1 {
			u.files[files[f].data] = p + name(files[f].name)
		}
		for c, n := dn.child, 0; c != none && int(c) < len(dirs) && n < len(dirs); c, n = dirs[c].sibling, n+1 {
			walk(c, p, depth+1)
		}
	}
	walk(0, mount, 0)
}

// packageIDs are the packages whose data the container replaces or adds.
func (u *utocInfo) packageIDs() map[uint64]bool {
	ids := map[uint64]bool{}
	for i, t := range u.types {
		if t == 1 { // ExportBundleData: one per package
			ids[u.chunkIDs[i]] = true
		}
	}
	return ids
}

// assetNames lists the packages by name, like "Vehicles/Aircraft/PP/PP0021_su57/Textures/001/T_PP0021_su57_001_C".
func (u *utocInfo) assetNames() []string {
	var out []string
	seen := map[string]bool{}
	for i, t := range u.types {
		if t != 1 {
			continue
		}
		p, ok := u.files[uint32(i)]
		if !ok {
			continue
		}
		p = strings.TrimPrefix(p, "../../../")
		if j := strings.Index(p, "/Content/"); j >= 0 {
			p = p[j+len("/Content/"):]
		}
		p = strings.TrimSuffix(p, path.Ext(p))
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
