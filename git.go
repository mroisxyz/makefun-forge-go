package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// Git object / pack helpers. Ported 1:1 from forge-entry/git.ts.
// Only stdlib is used: crypto/sha1, compress/zlib, bytes, encoding/hex,
// regexp, strconv, strings.

const gitZero = "0000000000000000000000000000000000000000"

const (
	gitUPAdvertisement = "application/x-git-upload-pack-advertisement"
	gitRCAdvertisement = "application/x-git-receive-pack-advertisement"
	gitUPResult        = "application/x-git-upload-pack-result"
	gitRCResult        = "application/x-git-receive-pack-result"
)

var (
	gitReObject = regexp.MustCompile(`(?m)^object ([0-9a-f]{40})$`)
	gitReTree   = regexp.MustCompile(`(?m)^tree ([0-9a-f]{40})$`)
	gitReParent = regexp.MustCompile(`(?m)^parent ([0-9a-f]{40})$`)
	gitReCmd    = regexp.MustCompile(`^command=([a-z-]+)`)
	gitReOID    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

const gitB64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// OIDOf returns the git SHA-1 object id of a body of the given type.
func OIDOf(typ string, body []byte) string {
	h := sha1.New()
	h.Write([]byte(typ + " " + strconv.Itoa(len(body)) + "\x00"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// PutObject stores an object and, for commits, fills repo.Commits.
func PutObject(repo *Repo, typ string, body []byte) string {
	if repo.Objects == nil {
		repo.Objects = map[string]*GitObject{}
	}
	if repo.Commits == nil {
		repo.Commits = map[string]*GitCommit{}
	}
	oid := OIDOf(typ, body)
	if _, ok := repo.Objects[oid]; !ok {
		cp := make([]byte, len(body))
		copy(cp, body)
		repo.Objects[oid] = &GitObject{OID: oid, Type: typ, Body: cp}
	}
	if typ == "commit" {
		text := string(body)
		tree, parent := "", ""
		if m := gitReTree.FindStringSubmatch(text); m != nil {
			tree = m[1]
		}
		if m := gitReParent.FindStringSubmatch(text); m != nil {
			parent = m[1]
		}
		if tree != "" {
			start := strings.Index(text, "\n\n") + 2
			if start < 1 {
				start = 1
			}
			if start > len(text) {
				start = len(text)
			}
			msg := strings.TrimSuffix(text[start:], "\n")
			repo.Commits[oid] = &GitCommit{OID: oid, Tree: tree, Parent: parent, Message: msg}
		}
	}
	return oid
}

// MakeCommit builds and stores a commit object, returning its oid.
func MakeCommit(repo *Repo, tree, parent, message string) string {
	text := "tree " + tree + "\n"
	if parent != "" {
		text += "parent " + parent + "\n"
	}
	text += "author Forge <forge@localhost> 0 +0000\ncommitter Forge <forge@localhost> 0 +0000\n\n" + message + "\n"
	return PutObject(repo, "commit", []byte(text))
}

/* ----------------------------- small helpers ----------------------------- */

func gitConcat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func gitSortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort: avoid importing sort
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func gitContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func gitMaxDepth() int { return int(^uint(0) >> 1) }

func gitNAK(tip string, haves []string) string {
	for _, h := range haves {
		if h == tip {
			return "ACK " + tip + "\n"
		}
	}
	return "NAK\n"
}

func gitPrefixMatchAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func gitB64Encode(src []byte) string {
	var b strings.Builder
	b.Grow((len(src) + 2) / 3 * 4)
	for i := 0; i < len(src); i += 3 {
		remain := len(src) - i
		n := uint32(src[i]) << 16
		if remain > 1 {
			n |= uint32(src[i+1]) << 8
		}
		if remain > 2 {
			n |= uint32(src[i+2])
		}
		b.WriteByte(gitB64Alphabet[(n>>18)&0x3f])
		b.WriteByte(gitB64Alphabet[(n>>12)&0x3f])
		if remain > 1 {
			b.WriteByte(gitB64Alphabet[(n>>6)&0x3f])
		} else {
			b.WriteByte('=')
		}
		if remain > 2 {
			b.WriteByte(gitB64Alphabet[n&0x3f])
		} else {
			b.WriteByte('=')
		}
	}
	return b.String()
}

func gitHexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func gitPercentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, lo := gitHexVal(s[i+1]), gitHexVal(s[i+2])
			if hi >= 0 && lo >= 0 {
				b = append(b, byte(hi<<4|lo))
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

/* ------------------------------ pkt-lines ------------------------------- */

var (
	gitFlush = []byte("0000")
	gitDelim = []byte("0001")
)

func gitPkt(payload []byte) []byte {
	n := len(payload) + 4
	h := strconv.FormatInt(int64(n), 16)
	if len(h) < 4 {
		h = strings.Repeat("0", 4-len(h)) + h
	}
	out := make([]byte, 0, n)
	out = append(out, h...)
	out = append(out, payload...)
	return out
}

func gitPktString(s string) []byte { return gitPkt([]byte(s)) }

// gitParseHexPrefix emulates JS parseInt(str, 16): parse leading hex digits.
func gitParseHexPrefix(s string) (int64, bool) {
	i := 0
	for i < len(s) {
		if gitHexVal(s[i]) >= 0 {
			i++
		} else {
			break
		}
	}
	if i == 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(s[:i], 16, 64)
	return v, err == nil
}

func gitParsePktLines(buf []byte) [][]byte {
	var out [][]byte
	i := 0
	for i+4 <= len(buf) {
		n, ok := gitParseHexPrefix(string(buf[i : i+4]))
		i += 4
		if !ok || n < 0 || n > int64(int(^uint(0)>>1)) {
			break
		}
		if n == 0 {
			break
		}
		if n == 1 || n == 2 {
			continue
		}
		if n < 4 || i+int(n)-4 > len(buf) {
			break
		}
		line := make([]byte, int(n)-4)
		copy(line, buf[i:i+int(n)-4])
		out = append(out, line)
		i += int(n) - 4
	}
	return out
}

func gitFirstCommand(lines [][]byte) string {
	if len(lines) == 0 {
		return ""
	}
	if m := gitReCmd.FindSubmatch(lines[0]); m != nil {
		return string(m[1])
	}
	return ""
}

/* ----------------------------- advertisements --------------------------- */

func gitAdvertisement(service string, v2 bool, repo *Repo) []byte {
	var parts [][]byte
	if service == "upload-pack" && v2 {
		parts = append(parts, gitPktString("version 2\n"))
		parts = append(parts, gitPktString("agent=git/2.44.0\n"))
		parts = append(parts, gitPktString("ls-refs\n"))
		parts = append(parts, gitPktString("fetch=shallow wait-for-done filter\n"))
		parts = append(parts, gitPktString("object-format=sha1\n"))
		parts = append(parts, gitFlush)
		return gitConcat(parts...)
	}
	caps := "side-band-64k ofs-delta shallow agent=git/2.44.0 symref=HEAD:refs/heads/main"
	if service == "receive-pack" {
		caps = "report-status side-band-64k delete-refs ofs-delta agent=git/2.44.0"
	}
	parts = append(parts, gitPktString("# service=git-"+service+"\n"))
	parts = append(parts, gitFlush)
	first := true
	for _, ref := range gitSortedKeys(repo.Refs) {
		if ref == "HEAD" {
			continue
		}
		suffix := ""
		if first {
			suffix = "\x00" + caps
		}
		parts = append(parts, gitPktString(repo.Refs[ref]+" "+ref+suffix+"\n"))
		first = false
	}
	if first {
		parts = append(parts, gitPktString(gitZero+" capabilities^{}\x00"+caps+"\n"))
	}
	parts = append(parts, gitFlush)
	return gitConcat(parts...)
}

/** ls-refs response with optional prefix filtering, symrefs and peeled tags. */
func gitLsRefs(repo *Repo, lines [][]byte) []byte {
	args := make([]string, 0, len(lines))
	for _, l := range lines {
		args = append(args, strings.TrimSpace(string(l)))
	}
	var prefixes []string
	wantUnborn, wantSymrefs, wantPeel := false, false, false
	for _, a := range args {
		if strings.HasPrefix(a, "ref-prefix ") {
			prefixes = append(prefixes, a[11:])
		}
		if a == "unborn" {
			wantUnborn = true
		}
		if a == "symrefs" {
			wantSymrefs = true
		}
		if a == "peel" {
			wantPeel = true
		}
	}
	var parts [][]byte
	mainOID := repo.Refs["refs/heads/main"]
	if mainOID != "" && (len(prefixes) == 0 || gitPrefixMatchAny("HEAD", prefixes)) {
		line := mainOID + " HEAD"
		if wantSymrefs {
			line += " symref-target:refs/heads/main"
		}
		parts = append(parts, gitPktString(line+"\n"))
	}
	for _, ref := range gitSortedKeys(repo.Refs) {
		if ref == "HEAD" {
			continue
		}
		oid := repo.Refs[ref]
		if len(prefixes) > 0 && !gitPrefixMatchAny(ref, prefixes) {
			continue
		}
		line := oid + " " + ref
		if wantSymrefs && ref == "HEAD" {
			t := repo.Refs["HEAD"]
			if t == "" {
				t = "refs/heads/main"
			}
			line += " symref-target:" + t
		}
		if wantPeel && strings.HasPrefix(ref, "refs/tags/") {
			tag := repo.Objects[oid]
			if tag != nil && tag.Type == "tag" {
				if m := gitReObject.FindStringSubmatch(string(tag.Body)); m != nil {
					line += " peeled:" + m[1]
				}
			} else {
				line += " peeled:" + oid
			}
		}
		parts = append(parts, gitPktString(line+"\n"))
	}
	if wantUnborn {
		if _, ok := repo.Refs["HEAD"]; !ok {
			parts = append(parts, gitPktString("unborn HEAD\n"))
		}
	}
	parts = append(parts, gitFlush)
	return gitConcat(parts...)
}

/* -------------------------------- packs --------------------------------- */

func gitInflateAll(data []byte) ([]byte, bool) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		zr.Close()
		return nil, false
	}
	zr.Close()
	return out.Bytes(), true
}

func gitReadVarint(data []byte, offset int) (uint64, int, bool) {
	var value uint64
	var shift uint
	i := offset
	for {
		if i >= len(data) {
			return 0, i, false
		}
		b := data[i]
		i++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, i, true
		}
		shift += 7
		if shift > 63 {
			return 0, i, false
		}
	}
}

type gitEntry struct {
	offset     int
	typ        int
	size       uint64
	body       []byte
	baseOffset int
	baseOID    string
}

func gitReadEntry(data []byte, offset int) (*gitEntry, int, bool) {
	if offset < 0 || offset >= len(data) {
		return nil, offset, false
	}
	first := data[offset]
	typ := int((first >> 4) & 7)
	var size uint64 = uint64(first & 15)
	cursor := offset + 1
	var shift uint = 4
	b := first
	for b&0x80 != 0 {
		if cursor >= len(data) {
			return nil, cursor, false
		}
		b = data[cursor]
		cursor++
		size |= uint64(b&0x7f) << shift
		shift += 7
		if shift > 63 {
			return nil, cursor, false
		}
	}
	baseOffset := -1
	baseOID := ""
	if typ == 6 { // OFS_DELTA
		if cursor >= len(data) {
			return nil, cursor, false
		}
		c := data[cursor]
		cursor++
		var dist uint64 = uint64(c & 0x7f)
		for c&0x80 != 0 {
			if cursor >= len(data) {
				return nil, cursor, false
			}
			c = data[cursor]
			cursor++
			dist = ((dist + 1) << 7) | uint64(c&0x7f)
		}
		if dist > uint64(offset) {
			return nil, cursor, false
		}
		baseOffset = offset - int(dist)
	} else if typ == 7 { // REF_DELTA
		if cursor+20 > len(data) {
			return nil, cursor, false
		}
		baseOID = hex.EncodeToString(data[cursor : cursor+20])
		cursor += 20
	}
	if cursor > len(data) {
		return nil, cursor, false
	}
	rest := data[cursor:]
	body, ok := gitInflateAll(rest)
	if !ok {
		return nil, cursor, false
	}
	// Re-inflate the shortest prefix that still succeeds to find consumed length.
	low := 1
	target := 64
	if size < (1 << 30) {
		v := int(size)*2 + 64
		if v > target {
			target = v
		}
	}
	high := len(rest)
	if target < high {
		high = target
	}
	if high < low {
		high = low
	}
	for low < high {
		mid := (low + high) >> 1
		if _, ok := gitInflateAll(rest[:mid]); ok {
			high = mid
		} else {
			low = mid + 1
		}
	}
	cursor += low
	if cursor > len(data) {
		return nil, cursor, false
	}
	return &gitEntry{offset: offset, typ: typ, size: size, body: body, baseOffset: baseOffset, baseOID: baseOID}, cursor, true
}

func gitApplyDelta(base, delta []byte) ([]byte, bool) {
	baseSize, pos, ok := gitReadVarint(delta, 0)
	if !ok {
		return nil, false
	}
	outSize, pos, ok := gitReadVarint(delta, pos)
	if !ok {
		return nil, false
	}
	if baseSize != uint64(len(base)) || outSize > uint64(1<<28) {
		return nil, false
	}
	out := make([]byte, int(outSize))
	written := 0
	for pos < len(delta) {
		op := delta[pos]
		pos++
		if op&0x80 != 0 {
			// copy instruction
			if op&0x01 != 0 { // cp (ignored, but consumed)
				if pos >= len(delta) {
					return nil, false
				}
				pos++
			}
			off := 0
			if op&0x02 != 0 {
				if pos >= len(delta) {
					return nil, false
				}
				off = int(delta[pos])
				pos++
			}
			if op&0x04 != 0 {
				if pos >= len(delta) {
					return nil, false
				}
				off |= int(delta[pos]) << 8
				pos++
			}
			if op&0x08 != 0 {
				if pos >= len(delta) {
					return nil, false
				}
				off |= int(delta[pos]) << 16
				pos++
			}
			if op&0x10 != 0 {
				if pos >= len(delta) {
					return nil, false
				}
				off |= int(delta[pos]) << 24
				pos++
			}
			sz := 0
			if op&0x20 != 0 {
				if pos >= len(delta) {
					return nil, false
				}
				sz = int(delta[pos])
				pos++
			}
			if op&0x40 != 0 {
				if pos >= len(delta) {
					return nil, false
				}
				sz |= int(delta[pos]) << 8
				pos++
			}
			// TS: `if (op & 0x80) size |= delta[pos++] << 16;` — always true here.
			if pos >= len(delta) {
				return nil, false
			}
			sz |= int(delta[pos]) << 16
			pos++
			if sz == 0 {
				sz = 0x10000
			}
			if off < 0 || off+sz > len(base) || written+sz > len(out) {
				return nil, false
			}
			copy(out[written:written+sz], base[off:off+sz])
			written += sz
		} else if op != 0 {
			n := int(op)
			if pos+n > len(delta) || written+n > len(out) {
				return nil, false
			}
			copy(out[written:written+n], delta[pos:pos+n])
			pos += n
			written += n
		} else {
			return nil, false
		}
	}
	return out, true
}

func gitFindOffsetForOID(byOffset map[int]*GitObject, oid string) int {
	for off, obj := range byOffset {
		if obj.OID == oid {
			return off
		}
	}
	return -1
}

func gitParsePack(repo *Repo, raw []byte) ([]*GitObject, bool) {
	if len(raw) < 32 || !bytes.Equal(raw[:4], []byte("PACK")) {
		return nil, false
	}
	version := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
	count := int(raw[8])<<24 | int(raw[9])<<16 | int(raw[10])<<8 | int(raw[11])
	if version != 2 && version != 3 {
		return nil, false
	}
	if count < 0 || count > len(raw)/2+1 {
		return nil, false
	}
	entries := make([]*gitEntry, 0, count)
	cursor := 12
	for i := 0; i < count; i++ {
		e, next, ok := gitReadEntry(raw, cursor)
		if !ok {
			return nil, false
		}
		entries = append(entries, e)
		cursor = next
	}
	byOffset := map[int]*GitObject{}
	pending := append([]*gitEntry(nil), entries...)
	typeNames := [5]string{"", "commit", "tree", "blob", "tag"}
	guard := 0
	for len(pending) > 0 && guard <= count+2 {
		guard++
		for i := len(pending) - 1; i >= 0; i-- {
			e := pending[i]
			if e.typ >= 1 && e.typ <= 4 {
				name := typeNames[e.typ]
				byOffset[e.offset] = &GitObject{OID: OIDOf(name, e.body), Type: name, Body: e.body}
				pending = append(pending[:i], pending[i+1:]...)
				continue
			}
			var base *GitObject
			if e.baseOffset >= 0 {
				base = byOffset[e.baseOffset]
			} else if e.baseOID != "" {
				off := gitFindOffsetForOID(byOffset, e.baseOID)
				if o, ok := byOffset[off]; ok {
					base = o
				}
				if base == nil {
					base = repo.Objects[e.baseOID]
				}
			}
			if base == nil {
				continue
			}
			merged, ok := gitApplyDelta(base.Body, e.body)
			if !ok {
				return nil, false
			}
			byOffset[e.offset] = &GitObject{OID: OIDOf(base.Type, merged), Type: base.Type, Body: merged}
			pending = append(pending[:i], pending[i+1:]...)
		}
	}
	out := make([]*GitObject, 0, len(byOffset))
	for _, e := range entries {
		if obj, ok := byOffset[e.offset]; ok {
			out = append(out, obj)
		}
	}
	return out, true
}

/* --------------------------- receive-pack side -------------------------- */

type gitCommand struct{ old, new, ref string }

type gitNgRef struct{ ref, reason string }

func gitParseCommands(buf []byte) []gitCommand {
	var out []gitCommand
	for _, line := range gitParsePktLines(buf) {
		s := string(line)
		if i := strings.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
		p := strings.Split(s, " ")
		if len(p) >= 3 && gitReOID.MatchString(p[0]) && gitReOID.MatchString(p[1]) {
			out = append(out, gitCommand{old: p[0], new: p[1], ref: p[2]})
		}
	}
	return out
}

func gitExtractPack(buf []byte) []byte {
	i := bytes.Index(buf, []byte("PACK"))
	if i >= 0 {
		return buf[i:]
	}
	return nil
}

func gitIsAncestor(repo *Repo, old, tip string) bool {
	if old == tip {
		return true
	}
	seen := map[string]bool{}
	cur := tip
	for cur != "" && !seen[cur] {
		seen[cur] = true
		c := repo.Commits[cur]
		if c == nil {
			break
		}
		if c.Parent == old {
			return true
		}
		cur = c.Parent
	}
	return false
}

func gitWalkTree(repo *Repo, oid, prefix string, m map[string]*ContentEntry, seen map[string]bool) {
	if seen[oid] {
		return
	}
	seen[oid] = true
	tree := repo.Objects[oid]
	if tree == nil || tree.Type != "tree" {
		return
	}
	body := tree.Body
	p := 0
	for p < len(body) {
		space := bytes.IndexByte(body[p:], ' ')
		if space < 0 {
			break
		}
		space += p
		nul := bytes.IndexByte(body[space+1:], 0)
		if nul < 0 {
			break
		}
		nul += space + 1
		if nul+21 > len(body) {
			break
		}
		name := string(body[space+1 : nul])
		child := hex.EncodeToString(body[nul+1 : nul+21])
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}
		obj := repo.Objects[child]
		if obj != nil {
			if obj.Type == "tree" {
				gitWalkTree(repo, child, path, m, seen)
			} else if obj.Type == "blob" {
				content := gitB64Encode(obj.Body)
				m[path] = &ContentEntry{Path: path, Content: content, SHA: child, BlobOID: child}
			}
		}
		p = nul + 21
	}
}

func gitSyncContentsFromCommit(repo *Repo, ref, oid string) {
	commit := repo.Commits[oid]
	if commit == nil {
		return
	}
	branch := ""
	if strings.HasPrefix(ref, "refs/heads/") {
		branch = ref[11:]
	}
	m := repo.Contents
	if branch != "" && branch != repo.DefaultBranch {
		if repo.BranchContents == nil {
			repo.BranchContents = map[string]map[string]*ContentEntry{}
		}
		bm := repo.BranchContents[branch]
		if bm == nil {
			bm = make(map[string]*ContentEntry, len(repo.Contents))
			for k, v := range repo.Contents {
				bm[k] = v
			}
			repo.BranchContents[branch] = bm
		}
		m = bm
	}
	gitWalkTree(repo, commit.Tree, "", m, map[string]bool{})
}

func gitApplyReceive(repo *Repo, commands []gitCommand, raw []byte) ([]string, []gitNgRef) {
	if repo.Refs == nil {
		repo.Refs = map[string]string{}
	}
	if repo.Packs == nil {
		repo.Packs = map[string][]byte{}
	}
	var objects []*GitObject
	if len(raw) >= 12 {
		if obs, ok := gitParsePack(repo, raw); ok {
			objects = obs
		}
	}
	for _, obj := range objects {
		PutObject(repo, obj.Type, obj.Body)
	}
	var okRefs []string
	var ngRefs []gitNgRef
	for _, cmd := range commands {
		if cmd.new == gitZero {
			delete(repo.Refs, cmd.ref)
			okRefs = append(okRefs, cmd.ref)
			continue
		}
		if cmd.old != gitZero && !gitIsAncestor(repo, cmd.old, cmd.new) {
			ngRefs = append(ngRefs, gitNgRef{ref: cmd.ref, reason: "non-fast-forward"})
			continue
		}
		repo.Refs[cmd.ref] = cmd.new
		if cmd.ref == "refs/heads/main" {
			repo.Refs["HEAD"] = cmd.new
		}
		gitSyncContentsFromCommit(repo, cmd.ref, cmd.new)
		if _, exists := repo.Packs[cmd.new]; !exists && len(objects) > 0 {
			cp := make([]byte, len(raw))
			copy(cp, raw)
			repo.Packs[cmd.new] = cp
		}
		okRefs = append(okRefs, cmd.ref)
	}
	return okRefs, ngRefs
}

/* ---------------------------- upload-pack side --------------------------- */

type gitReachItem struct {
	oid   string
	level int
}

/** Walk reachable objects from a commit up to a shallow depth. */
func gitReachable(repo *Repo, tip string, depth int, held map[string]bool) ([]*GitObject, []string) {
	objects := []*GitObject{}
	shallow := []string{}
	seen := map[string]bool{}
	for k, v := range held {
		if v {
			seen[k] = true
		}
	}
	queue := []gitReachItem{{oid: tip, level: 0}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if seen[item.oid] {
			continue
		}
		obj := repo.Objects[item.oid]
		if obj == nil {
			continue
		}
		seen[item.oid] = true
		objects = append(objects, obj)
		switch obj.Type {
		case "tree":
			body := obj.Body
			p := 0
			for p < len(body) {
				space := bytes.IndexByte(body[p:], ' ')
				if space < 0 {
					break
				}
				space += p
				nul := bytes.IndexByte(body[space+1:], 0)
				if nul < 0 {
					break
				}
				nul += space + 1
				if nul+21 > len(body) {
					break
				}
				child := hex.EncodeToString(body[nul+1 : nul+21])
				queue = append(queue, gitReachItem{oid: child, level: item.level})
				p = nul + 21
			}
			continue
		case "tag":
			if m := gitReObject.FindStringSubmatch(string(obj.Body)); m != nil {
				queue = append(queue, gitReachItem{oid: m[1], level: item.level})
			}
			continue
		}
		for _, ref := range gitSortedKeys(repo.Refs) {
			if strings.HasPrefix(ref, "refs/tags/") && repo.Refs[ref] == item.oid && obj.Type != "tag" {
				queue = append(queue, gitReachItem{oid: item.oid, level: item.level})
			}
		}
		if obj.Type != "commit" {
			continue
		}
		text := string(obj.Body)
		treeLine, parentLine := "", ""
		for _, line := range strings.Split(text, "\n") {
			if treeLine == "" && strings.HasPrefix(line, "tree ") {
				treeLine = line
			}
			if parentLine == "" && strings.HasPrefix(line, "parent ") {
				parentLine = line
			}
		}
		if treeLine != "" {
			queue = append(queue, gitReachItem{oid: treeLine[5:], level: item.level})
		}
		if item.level+1 >= depth {
			shallow = append(shallow, item.oid)
			continue
		}
		if parentLine != "" {
			queue = append(queue, gitReachItem{oid: parentLine[7:], level: item.level + 1})
		}
	}
	return objects, shallow
}

func gitPutBE32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

func gitWritePack(objects []*GitObject) []byte {
	parts := [][]byte{[]byte("PACK"), make([]byte, 4), make([]byte, 4)}
	gitPutBE32(parts[1], 2)
	gitPutBE32(parts[2], uint32(len(objects)))
	for _, obj := range objects {
		raw := obj.Body
		typeID := 4
		switch obj.Type {
		case "commit":
			typeID = 1
		case "tree":
			typeID = 2
		case "blob":
			typeID = 3
		}
		size := len(raw)
		b := byte((typeID << 4) | (size & 0x0f))
		size >>= 4
		for size > 0 {
			parts = append(parts, []byte{b | 0x80})
			b = byte(size & 0x7f)
			size >>= 7
		}
		parts = append(parts, []byte{b})
		var zbuf bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&zbuf, 1)
		zw.Write(raw)
		zw.Close()
		parts = append(parts, zbuf.Bytes())
	}
	body := gitConcat(parts...)
	sum := sha1.Sum(body)
	return gitConcat(body, sum[:])
}

func gitSideBandPack(pack []byte) []byte {
	parts := [][]byte{gitPkt(gitConcat([]byte{2}, []byte("PACK\n")))}
	for i := 0; i < len(pack); i += 65515 {
		end := i + 65515
		if end > len(pack) {
			end = len(pack)
		}
		parts = append(parts, gitPkt(gitConcat([]byte{1}, pack[i:end])))
	}
	parts = append(parts, gitFlush)
	return gitConcat(parts...)
}

func gitSideBandReport(report []byte) []byte {
	var parts [][]byte
	for _, line := range gitParsePktLines(report) {
		parts = append(parts, gitPkt(gitConcat([]byte{1}, line)))
	}
	parts = append(parts, gitPkt([]byte{1})) // flush inside band 1
	parts = append(parts, gitFlush)
	return gitConcat(parts...)
}

func gitReportStatus(okRefs []string, ngRefs []gitNgRef) []byte {
	var parts [][]byte
	parts = append(parts, gitPktString("unpack ok\n"))
	for _, ref := range okRefs {
		parts = append(parts, gitPktString("ok "+ref+"\n"))
	}
	for _, ng := range ngRefs {
		parts = append(parts, gitPktString("ng "+ng.ref+" "+ng.reason+"\n"))
	}
	parts = append(parts, gitFlush)
	return gitConcat(parts...)
}
