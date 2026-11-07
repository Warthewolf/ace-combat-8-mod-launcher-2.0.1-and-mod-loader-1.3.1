package main

// A reader for the parts of an IoStore table of contents (.utoc) the launcher
// needs: which packages a container holds (to spot two mods changing the same
// game file) and the file names (to show what a mod changes).
//
// The file comes from a download, so nothing in it is trusted: every count and
// offset is checked against the bytes really there before it is used, each
// directory and file entry is visited once at most, and a damaged file gives
// fewer names or none, never a hang, a crash or a huge allocation.

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

const (
	maxUtocSize    = 64 << 20 // no real mod's .utoc is near this
	maxUtocPathLen = 1024     // longer paths are damage, not file names
	maxUtocNameLen = 1 << 16  // one directory or file name
	maxUtocDepth   = 64       // folders inside folders
	maxUtocPathSum = 16 << 20 // all file paths together, in bytes
)

func readUtoc(p string) (*utocInfo, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxUtocSize {
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
	n := uint64(len(d))
	if need := uint64(hdrSize) + uint64(entries)*12; need > n {
		return nil, errors.New("short .utoc")
	}
	u := &utocInfo{files: map[uint32]string{}, chunkIDs: make([]uint64, 0, entries), types: make([]byte, 0, entries)}
	o := uint64(hdrSize)
	for i := uint32(0); i < entries; i++ {
		u.chunkIDs = append(u.chunkIDs, le.Uint64(d[o:]))
		u.types = append(u.types, d[o+11])
		o += 12
	}
	// skip moves past count items of size bytes each; false when they would run
	// past the end of the file. o <= n holds throughout, so n-o never wraps.
	skip := func(count, size uint32) bool {
		if size != 0 && uint64(count) > (n-o)/uint64(size) {
			return false
		}
		o += uint64(count) * uint64(size)
		return true
	}
	if !skip(entries, 10) { // offsets and lengths
		return u, nil
	}
	if ver >= 4 && !skip(seeds, 4) {
		return u, nil
	}
	if ver >= 5 && !skip(noHash, 4) {
		return u, nil
	}
	if !skip(blocks, blockSize) || !skip(methods, methodLen) {
		return u, nil
	}
	const flagEncrypted, flagSigned, flagIndexed = 0x2, 0x4, 0x8
	if flags&flagSigned != 0 {
		if n-o < 4 {
			return u, nil
		}
		hs := le.Uint32(d[o:])
		o += 4
		if !skip(2, hs) || !skip(blocks, 20) {
			return u, nil
		}
	}
	if flags&flagIndexed == 0 || flags&flagEncrypted != 0 || dirIndexSize == 0 || uint64(dirIndexSize) > n-o {
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
		if !ok || len(d)-o < 4 {
			ok = false
			return 0
		}
		v := le.Uint32(d[o:])
		o += 4
		return v
	}
	// fits: count items of size bytes each are really there
	fits := func(count uint32, size int) bool {
		return ok && uint64(count)*uint64(size) <= uint64(len(d)-o)
	}
	str := func() string {
		n := int32(u32())
		if !ok || n == 0 {
			return ""
		}
		if n > maxUtocNameLen || n < -maxUtocNameLen { // no real file name is this long: a damaged file
			ok = false
			return ""
		}
		if n > 0 {
			if int(n) > len(d)-o {
				ok = false
				return ""
			}
			s := string(d[o : o+int(n)-1])
			o += int(n)
			return s
		}
		n = -n
		if 2*int(n) > len(d)-o {
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
	nd := u32()
	if !fits(nd, 16) {
		return
	}
	type dirE struct{ name, child, sibling, file uint32 }
	dirs := make([]dirE, nd)
	for i := range dirs {
		dirs[i] = dirE{u32(), u32(), u32(), u32()}
	}
	nf := u32()
	if !fits(nf, 12) {
		return
	}
	type fileE struct{ name, next, data uint32 }
	files := make([]fileE, nf)
	for i := range files {
		files[i] = fileE{u32(), u32(), u32()}
	}
	ns := u32()
	if !fits(ns, 4) { // each string takes at least its 4-byte length
		return
	}
	strs := make([]string, 0, ns)
	for i := uint32(0); i < ns && ok; i++ {
		strs = append(strs, str())
	}
	if !ok || len(dirs) == 0 {
		return
	}
	const none = 0xFFFFFFFF
	name := func(i uint32) string {
		if uint64(i) < uint64(len(strs)) {
			return strs[i]
		}
		return ""
	}
	// Walk the tree with a stack of its own, not recursion. A damaged or crafted
	// index can link a folder back to itself, to a parent or to another branch,
	// so each folder is queued once at most and each file entry is read once at
	// most: the work is bounded by the counts, which are bounded by the file's
	// size. The paths built along the way have a budget too.
	queued := make([]bool, len(dirs))
	seenFile := make([]bool, len(files))
	type item struct {
		dir   uint32
		path  string
		depth int
	}
	queued[0] = true
	stack := []item{{0, mount, 0}}
	budget := maxUtocPathSum
	var kids []uint32
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		dn := dirs[it.dir]
		p := it.path
		if dn.name != none {
			p += name(dn.name) + "/"
		}
		if len(p) > maxUtocPathLen {
			continue
		}
		if budget -= len(p); budget < 0 {
			return
		}
		for f := dn.file; f != none && uint64(f) < uint64(len(files)) && !seenFile[f]; f = files[f].next {
			seenFile[f] = true
			fe := files[f]
			if uint64(fe.data) >= uint64(len(u.types)) {
				continue // names a chunk the container doesn't have
			}
			full := p + name(fe.name)
			if len(full) > maxUtocPathLen {
				continue
			}
			if budget -= len(full); budget < 0 {
				return
			}
			u.files[fe.data] = full
		}
		if it.depth >= maxUtocDepth {
			continue
		}
		// the children, in their own order: push the sibling chain in reverse
		kids = kids[:0]
		for c := dn.child; c != none && uint64(c) < uint64(len(dirs)) && !queued[c]; c = dirs[c].sibling {
			queued[c] = true
			kids = append(kids, c)
		}
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, item{kids[i], p, it.depth + 1})
		}
	}
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
