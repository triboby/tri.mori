package main

// shelf gen-books: builds docs-books/<id>.md (qmd collection "books"), data/books.json, cache/covers/ and
// unmatched-books.md from /Volumes/store/book (read-only), Douban and Open Library. Port of gen-books.mjs with the
// same flags (--rescan, --offline) and cache files.

import (
	"bytes"
	"compress/flate"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"
)

var bookExt = map[string]bool{".epub": true, ".pdf": true, ".mobi": true, ".azw3": true, ".azw": true, ".djvu": true, ".cbz": true, ".fb2": true, ".txt": true}

const (
	doubanUA    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"
	doubanGapMs = 2500
	bookSMBBase = "smb://192.168.50.94/store/book/"
)

func sha1hex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// normBook is gen-books.mjs norm(): NFKC, lowercase, letters and digits only.
func normBook(s string) string {
	return reNonLN.ReplaceAllString(strings.ToLower(norm.NFKC.String(s)), "")
}

var reEntity = re(`&(#x?[0-9a-f]+|amp|lt|gt|quot|apos|nbsp);`, "gi")

// jsParseInt is parseInt(s, radix) for radix 10 or 16.
func jsParseInt(s string, radix int) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	sign := 1.0
	if s != "" && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	if radix == 16 && len(s) > 1 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
	n, digits := 0.0, 0
	for _, c := range s {
		d := -1
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'z':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			d = int(c-'A') + 10
		}
		if d < 0 || d >= radix {
			break
		}
		n = n*float64(radix) + float64(d)
		digits++
	}
	if digits == 0 {
		return math.NaN()
	}
	return sign * n
}

var reFloatPrefix = regexp.MustCompile(`^[+-]?(Infinity|(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?)`)

// jsParseFloat is parseFloat(s).
func jsParseFloat(v any) float64 {
	if nullish(v) {
		return math.NaN()
	}
	s := strings.TrimLeftFunc(jsString(v), isJSSpace)
	m := reFloatPrefix.FindString(s)
	if m == "" {
		return math.NaN()
	}
	if strings.HasSuffix(m, "Infinity") {
		if m[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	f, _ := strconv.ParseFloat(m, 64)
	return f
}

func decodeEntities(s string) string {
	return reEntity.ReplaceFunc(s, func(m reMatch) string {
		e := m.groups[1]
		if e[0] == '#' {
			hexa := len(e) > 1 && (e[1] == 'x' || e[1] == 'X')
			var cp float64
			if hexa {
				cp = jsParseInt(e[2:], 16)
			} else {
				cp = jsParseInt(e[1:], 10)
			}
			if math.IsNaN(cp) || cp > 0x10FFFF {
				panic(fmt.Errorf("Invalid code point %s", jsNum(cp)))
			}
			return string(rune(int(cp)))
		}
		return map[string]string{"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " "}[strings.ToLower(e)]
	})
}

var (
	reBrP     = re(`<br\s*\/?>|<\/p>`, "gi")
	reTag     = re(`<[^>]+>`, "g")
	reSpacesG = re(`\s+`, "g")
)

func htmlText(s string) string {
	t := decodeEntities(reTag.Replace(reBrP.Replace(s, "\n"), ""))
	var out []string
	for _, l := range strings.Split(t, "\n") {
		if l = jsTrim(reSpacesG.Replace(l, " ")); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// readAt is fh.read(buf, 0, n, off): short reads past EOF leave zeros.
func readAt(f *os.File, n int, off int64) ([]byte, int) {
	buf := make([]byte, n)
	if n == 0 {
		return buf, 0
	}
	k, _ := f.ReadAt(buf, off)
	return buf, k
}

// partialMD5 is KOReader's util.partialMD5: 1 KiB samples at 0 and 1024 << 2i for i = 0..10.
func partialMD5(f *os.File, size int64) string {
	h := md5.New()
	for i := -1; i <= 10; i++ {
		off := int64(0)
		if i >= 0 {
			off = 1024 << (2 * i)
		}
		if off >= size {
			break
		}
		buf, n := readAt(f, 1024, off)
		h.Write(buf[:n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

type zipEntry struct {
	method     uint16
	csize, lho uint32
}

// zipReader is gen-books.mjs openZip(): central directory + stored/deflate entries.
type zipReader struct {
	f       *os.File
	size    int64
	names   []string
	entries map[string]zipEntry
	lower   map[string]string
}

var errNotZip = errors.New("not a zip")

func openZip(f *os.File, size int64) (*zipReader, error) {
	tailLen := min(size, 65557)
	tail, _ := readAt(f, int(tailLen), size-tailLen)
	eocd := bytes.LastIndex(tail, []byte{0x50, 0x4b, 0x05, 0x06})
	if eocd < 0 {
		return nil, errNotZip
	}
	if eocd+20 > len(tail) {
		return nil, errors.New(`The value of "offset" is out of range.`)
	}
	cdSize, cdOff := binary.LittleEndian.Uint32(tail[eocd+12:]), binary.LittleEndian.Uint32(tail[eocd+16:])
	if int64(cdOff)+int64(cdSize) > size {
		return nil, errors.New("bad zip central directory")
	}
	cd, _ := readAt(f, int(cdSize), int64(cdOff))
	z := &zipReader{f: f, size: size, entries: map[string]zipEntry{}, lower: map[string]string{}}
	for p := 0; p+46 <= len(cd) && binary.LittleEndian.Uint32(cd[p:]) == 0x02014b50; {
		nlen, elen, clen := int(binary.LittleEndian.Uint16(cd[p+28:])), int(binary.LittleEndian.Uint16(cd[p+30:])), int(binary.LittleEndian.Uint16(cd[p+32:]))
		end := min(p+46+nlen, len(cd))
		name := decodeUTF8(cd[p+46 : end])
		if _, ok := z.entries[name]; !ok {
			z.names = append(z.names, name)
		}
		z.entries[name] = zipEntry{binary.LittleEndian.Uint16(cd[p+10:]), binary.LittleEndian.Uint32(cd[p+20:]), binary.LittleEndian.Uint32(cd[p+42:])}
		p += 46 + nlen + elen + clen
	}
	for _, k := range z.names {
		z.lower[strings.ToLower(k)] = k
	}
	return z, nil
}

func (z *zipReader) read(name string) ([]byte, error) {
	e, ok := z.entries[name]
	if !ok {
		e, ok = z.entries[z.lower[strings.ToLower(name)]]
	}
	if !ok {
		return nil, nil
	}
	lh, _ := readAt(z.f, 30, int64(e.lho))
	if int64(e.lho)+30+int64(e.csize) > z.size {
		return nil, errors.New("bad zip entry")
	}
	data, _ := readAt(z.f, int(e.csize), int64(e.lho)+30+int64(binary.LittleEndian.Uint16(lh[26:]))+int64(binary.LittleEndian.Uint16(lh[28:])))
	if e.method == 8 {
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
		if err != nil {
			return nil, errors.New("invalid stored block lengths")
		}
		return out, nil
	}
	return data, nil
}

var (
	attrMu  sync.Mutex
	attrRes = map[string]*jsRe{}
)

func attr(tag, name string) string {
	attrMu.Lock()
	r := attrRes[name]
	if r == nil {
		r = re(`\s`+regexp.QuoteMeta(name)+`\s*=\s*["']([^"']*)["']`, "i")
		attrRes[name] = r
	}
	attrMu.Unlock()
	return decodeEntities(r.Group(tag, 1))
}

type dcEntry struct{ k, attrs, v string }

type opfData struct {
	dc          []dcEntry
	refines     map[string]string
	metaTags    []string
	identifiers []struct{ scheme, v string }
}

var (
	reDC       = re(`<dc:(\w+)([^>]*)>([\s\S]*?)<\/dc:\1>`, "gi")
	reMetaBody = re(`<meta\b([^>]*)>([^<]*)<\/meta>`, "gi")
	reMetaTag  = re(`<meta\b[^>]*>`, "gi")
	reHashLead = re(`^#`, "")
)

func parseOpf(opf string) *opfData {
	o := &opfData{refines: map[string]string{}}
	for _, m := range reDC.MatchAll(opf) {
		o.dc = append(o.dc, dcEntry{strings.ToLower(m.groups[1]), m.groups[2], htmlText(m.groups[3])})
	}
	for _, m := range reMetaBody.MatchAll(opf) {
		r := reHashLead.Replace(attr(m.groups[1], "refines"), "")
		if r != "" && attr(m.groups[1], "property") == "role" {
			o.refines[r] = jsTrim(m.groups[2])
		}
	}
	for _, m := range reMetaTag.MatchAll(opf) {
		o.metaTags = append(o.metaTags, m.groups[0])
	}
	for _, d := range o.dc {
		if d.k == "identifier" {
			o.identifiers = append(o.identifiers, struct{ scheme, v string }{strings.ToLower(attr(d.attrs, "opf:scheme")), d.v})
		}
	}
	return o
}

func (o *opfData) role(d dcEntry) string {
	r := attr(d.attrs, "opf:role")
	if r == "" {
		r = o.refines[attr(d.attrs, "id")]
	}
	if r == "" {
		r = "aut"
	}
	return strings.ToLower(r)
}

func (o *opfData) vals(k string) []any {
	out := []any{}
	for _, d := range o.dc {
		if d.k == k && d.v != "" {
			out = append(out, d.v)
		}
	}
	return out
}

func first(a []any) any {
	if len(a) == 0 {
		return undefined
	}
	return a[0]
}

func (o *opfData) metaContent(name string) any {
	for _, t := range o.metaTags {
		if attr(t, "name") == name {
			return attr(t, "content")
		}
	}
	return undefined
}

func (o *opfData) creators(translators bool) []any {
	out := []any{}
	for _, d := range o.dc {
		if d.k == "creator" && d.v != "" && (o.role(d) == "trl") == translators {
			out = append(out, d.v)
		}
	}
	return out
}

func (o *opfData) identifier(scheme string) any {
	for _, i := range o.identifiers {
		if i.scheme == scheme {
			return i.v
		}
	}
	return undefined
}

var (
	reFullPath  = re(`full-path\s*=\s*["']([^"']+)["']`, "")
	reItem      = re(`<item\b[^>]*>`, "gi")
	reCoverWord = re(`cover`, "i")
	reWS        = re(`\s+`, "")
)

func readEpub(f *os.File, size int64) (*Obj, []byte, error) {
	z, err := openZip(f, size)
	if err != nil {
		return nil, nil, err
	}
	cb, err := z.read("META-INF/container.xml")
	if err != nil {
		return nil, nil, err
	}
	container := decodeUTF8(cb)
	g, ok := reFullPath.Match(container)
	if g == nil || !ok[1] {
		return nil, nil, errors.New("no OPF")
	}
	opfPath := g[1]
	ob, err := z.read(opfPath)
	if err != nil {
		return nil, nil, err
	}
	if ob == nil {
		return nil, nil, errors.New("Cannot read properties of null (reading 'toString')")
	}
	opf := decodeUTF8(ob)
	o := parseOpf(opf)
	type item struct{ id, href, typ, props string }
	var items []item
	for _, m := range reItem.MatchAll(opf) {
		t := m.groups[0]
		items = append(items, item{attr(t, "id"), attr(t, "href"), attr(t, "media-type"), attr(t, "properties")})
	}
	coverID := o.metaContent("cover")
	var coverItem *item
	for i, it := range items {
		for _, p := range reWS.Split(it.props) {
			if p == "cover-image" && coverItem == nil {
				coverItem = &items[i]
			}
		}
	}
	if coverItem == nil {
		for i, it := range items {
			if it.id == coverID && strings.HasPrefix(it.typ, "image/") {
				coverItem = &items[i]
				break
			}
		}
	}
	if coverItem == nil {
		for i, it := range items {
			if strings.HasPrefix(it.typ, "image/") && reCoverWord.Test(it.id+it.href) {
				coverItem = &items[i]
				break
			}
		}
	}
	var cover []byte
	if coverItem != nil {
		href, err := decodeURIComponent(coverItem.href)
		if err != nil {
			return nil, nil, err
		}
		p := path.Clean(path.Join(path.Dir(opfPath), href))
		if cover, err = z.read(p); err != nil {
			return nil, nil, err
		}
	}
	meta := newObj("title", first(o.vals("title")), "authors", o.creators(false), "translators", o.creators(true),
		"publisher", first(o.vals("publisher")), "date", first(o.vals("date")), "language", first(o.vals("language")),
		"description", first(o.vals("description")), "subjects", o.vals("subject"), "series", o.metaContent("calibre:series"),
		"isbn", findIsbn(o.vals("identifier")))
	return meta, cover, nil
}

var (
	reUrnIsbn = re(`^urn:isbn:`, "i")
	reIsbnSep = re(`[\s-]`, "g")
	reIsbn13  = re(`^97[89]\d{10}$`, "")
	reIsbn10  = re(`^\d{9}[\dX]$`, "i")
)

func findIsbn(list []any) any {
	for _, raw := range list {
		d := reIsbnSep.Replace(reUrnIsbn.Replace(jsString(raw), ""), "")
		if reIsbn13.Test(d) {
			return d
		}
		if reIsbn10.Test(d) {
			return isbn10to13(d)
		}
	}
	return nil
}

func isbn10to13(i10 string) string {
	core := "978" + i10[:9]
	sum := 0
	for i, c := range core {
		w := 1
		if i%2 == 1 {
			w = 3
		}
		sum += int(c-'0') * w
	}
	return core + strconv.Itoa((10-sum%10)%10)
}

// errRange mirrors Node's ERR_OUT_OF_RANGE from a Buffer read past its end.
type errRange struct{}

func (errRange) Error() string { return `The value of "offset" is out of range.` }

func be32(b []byte, off int) uint32 {
	if off < 0 || off+4 > len(b) {
		panic(errRange{})
	}
	return binary.BigEndian.Uint32(b[off:])
}

func be16(b []byte, off int) uint16 {
	if off < 0 || off+2 > len(b) {
		panic(errRange{})
	}
	return binary.BigEndian.Uint16(b[off:])
}

// bufString is buf.toString(enc, s, e) with Node's clamping.
func bufString(b []byte, utf bool, s, e int) string {
	s, e = max(0, min(s, len(b))), max(0, min(e, len(b)))
	if s >= e {
		return ""
	}
	if utf {
		return decodeUTF8(b[s:e])
	}
	return latin1(b[s:e])
}

var reTrailingNul = re(`\x00+$`, "")

// readMobi reads MOBI / AZW3 metadata from the PalmDB record 0 MOBI + EXTH headers.
func readMobi(f *os.File, size int64) (meta *Obj, cover []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(errRange); ok {
				meta, cover, err = nil, nil, e
				return
			}
			panic(r)
		}
	}()
	if size < 78+16 {
		return nil, nil, errors.New("too small for a MOBI file")
	}
	head, _ := readAt(f, 78, 0)
	nrec := int(be16(head, 76))
	if nrec < 2 || int64(78+nrec*8) > size {
		return nil, nil, errors.New("bad PalmDB record table")
	}
	recTable, _ := readAt(f, nrec*8+8, 78)
	recOff := func(i int) int { return int(be32(recTable, i*8)) }
	span := func(i int) (int, error) {
		a, b := recOff(i), recOff(i+1)
		if !(b > a && int64(b) <= size && b-a < 64<<20) {
			return 0, errors.New("bad record offsets")
		}
		return b - a, nil
	}
	n0, err := span(0)
	if err != nil {
		return nil, nil, err
	}
	r0, _ := readAt(f, n0, int64(recOff(0)))
	if bufString(r0, false, 16, 20) != "MOBI" {
		return nil, nil, errors.New("no MOBI header")
	}
	hlen := int(be32(r0, 20))
	utf := be32(r0, 28) == 65001
	str := func(s, e int) string { return jsTrim(reTrailingNul.Replace(bufString(r0, utf, s, e), "")) }
	t0 := int(be32(r0, 84))
	out := newObj("title", str(t0, t0+int(be32(r0, 88))), "authors", []any{}, "subjects", []any{})
	firstImage := int(be32(r0, 16+0x5c))
	coverIdx := -1
	if be32(r0, 128)&0x40 != 0 {
		p := 16 + hlen + 12
		count := int(be32(r0, 16+hlen+8))
		for k := 0; k < count && p+8 <= len(r0); k++ {
			typ, l := be32(r0, p), int(be32(r0, p+4))
			v := func() string { return str(p+8, p+l) }
			switch typ {
			case 100:
				out.Set("authors", append(arr(out.Get("authors")), v()))
			case 101:
				out.Set("publisher", v())
			case 103:
				out.Set("description", htmlText(v()))
			case 104:
				out.Set("isbn", findIsbn([]any{v()}))
			case 105:
				out.Set("subjects", append(arr(out.Get("subjects")), v()))
			case 106:
				out.Set("date", v())
			case 503:
				out.Set("title", v())
			case 524:
				out.Set("language", v())
			case 201:
				coverIdx = int(be32(r0, p+8))
			}
			p += l
		}
	}
	if coverIdx >= 0 && firstImage > 0 && firstImage+coverIdx+1 < nrec {
		i := firstImage + coverIdx
		n, err := span(i)
		if err != nil {
			return nil, nil, err
		}
		buf, _ := readAt(f, n, int64(recOff(i)))
		if len(buf) > 1 && buf[0] == 0xff && buf[1] == 0xd8 || bufString(buf, false, 1, 4) == "PNG" {
			cover = buf
		}
	}
	return out, cover, nil
}

var (
	reFont      = regexp.MustCompile(`/Font\b`)
	reToUnicode = regexp.MustCompile(`/ToUnicode\b`)
)

func pdfTextLayer(f *os.File, size int64) string {
	buf, _ := readAt(f, int(min(size, 4<<20)), 0)
	s := []byte(latin1(buf))
	fonts, uni := len(reFont.FindAllIndex(s, -1)), len(reToUnicode.FindAllIndex(s, -1))
	switch {
	case fonts == 0:
		return "scan"
	case uni > 0:
		return "text"
	}
	return "unknown"
}

var (
	reJunkAuthor  = re(`^(unknown|佚名|未知|epubw\.com|www\..*|.*\.(com|net|org))$`, "i")
	rePeopleSplit = re(`\s*[,，;；]\s*(?=\S)`, "")
	reNotAuthor   = re(`版|丛书|译丛|系列|文库|名著|全集|卷|册|套装|典藏|修订|精选|edition|vol`, "i")
)

func cleanPeople(list any) []any {
	out := []any{}
	for _, a := range arr(list) {
		for _, p := range rePeopleSplit.Split(jsString(a)) {
			if p = jsTrim(p); p != "" && !reJunkAuthor.Test(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

var (
	reZlib        = re(`\s*[(（]\s*(z-lib\.org|z-library|zlibrary)\s*[)）]`, "gi")
	reEpubw       = re(`^\s*epubw\.com\s*-\s*`, "i")
	reScanSuffix  = re(`[.\s]*(中文)?(扫描|高清)版?$`, "")
	reBracketName = re(`^\[([^\]]+)\][.\s]*(?:[(（]([^)）]+)[)）])?[.\s]*(.*)$`, "")
	reBookQuote   = re(`^《([^》]+)》\s*(?:作者[:：]\s*)?[-—]?\s*(.*)$`, "")
	reBy          = re(`^(.*?)\s+by\s+(.+)$`, "i")
	reParenAuthor = re(`^(.*?\S)\s*[(（]((?:[^()（）]|[(（][^()（）]*[)）])+)[)）]$`, "")
	reDashAuthor  = re(`^(.+?)\s*[-—]\s*([^-—]{2,30})$`, "")
	reHanOrDot    = re(`[·•\p{Script=Han}]`, "u")
	reAuthorSep   = re(`[、,，]`, "")
	reOpenBracket = re(`[\[［]([^\]］]*)$`, "")
	reAuthorRole  = re(`\s*(著|编著|主编|等)$`, "")
	reBookQuotes  = re(`[《》]`, "g")
	reTransTail   = re(`_.*译$`, "")
)

type nameGuess struct{ title, author string }

// parseFileName guesses title / author from a file name.
func parseFileName(file string) nameGuess {
	s := jsTrim(reScanSuffix.Replace(reEpubw.Replace(reZlib.Replace(reLastExt.Replace(path.Base(file), ""), ""), ""), ""))
	author := ""
	if m, _ := reBracketName.Match(s); m != nil {
		s, author = m[1], m[3]
	} else if m, _ := reBookQuote.Match(s); m != nil {
		s, author = m[1], m[2]
	} else if m, _ := reBy.Match(s); m != nil {
		s, author = m[1], m[2]
	} else if m, _ := reParenAuthor.Match(s); m != nil && !reNotAuthor.Test(m[2]) {
		s, author = m[1], m[2]
	}
	if m, _ := reDashAuthor.Match(s); m != nil && hasCJK(m[1]) && reHanOrDot.Test(m[2]) && !reNotAuthor.Test(m[2]) {
		s = m[1]
		if author == "" {
			author = m[2]
		}
	}
	author = jsTrim(reAuthorRole.Replace(reOpenBracket.Replace(reAuthorSep.Split(author)[0], "$1"), ""))
	title := jsTrim(reTransTail.Replace(reBookQuotes.Replace(s, ""), ""))
	return nameGuess{title, author}
}

var (
	reParenAny   = re(`[(（【\[][^)）】\]]*[)）】\]]`, "g")
	reColonSplit = re(`[：:]|——`, "")
	reEdition    = re(`\s*(原书)?第?\s*[\d一二三四五六七八九十]+\s*版.*$`, "")
)

// shortTitle is the main title for lookups: no parentheticals, subtitle or edition suffix.
func shortTitle(t string) string {
	return jsTrim(reSpacesG.Replace(reEdition.Replace(reColonSplit.Split(reParenAny.Replace(t, " "))[0], ""), " "))
}

func walkBooks(dir, rel string) ([]string, error) {
	es, err := readDirents(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range es {
		if strings.HasPrefix(d.name, ".") || strings.HasPrefix(d.name, "@") || d.name == "#recycle" || d.name == "_duplicates" || d.name == "_misc" || strings.HasSuffix(d.name, ".sdr") {
			continue
		}
		r := d.name
		if rel != "" {
			r = rel + "/" + d.name
		}
		if d.isDir {
			sub, err := walkBooks(filepath.Join(dir, d.name), r)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		} else if bookExt[strings.ToLower(jsExtname(d.name))] {
			out = append(out, r)
		}
	}
	return out, nil
}

type genBooks struct {
	root, bookDir, docs, cache, covers, metaCache, scanCache string
	args                                                     map[string]bool
	sidecars                                                 map[string]*Obj
	sidecarDone                                              map[string]bool
	lastDouban                                               time.Time
	doubanBlocked                                            bool
	mu                                                       sync.Mutex
}

func (g *genBooks) scanFile(rel string) (*Obj, error) {
	full := filepath.Join(g.bookDir, rel)
	st, err := statFile(full)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	format := strings.ToLower(strings.TrimPrefix(jsExtname(rel), "."))
	file := newObj("path", rel, "format", format, "size", float64(st.size), "mtime", jsRound(st.mtimeMs), "md5", partialMD5(f, st.size))
	meta := &Obj{}
	var cover []byte
	func() {
		var m *Obj
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
			if err != nil {
				warn("meta %s: %s", rel, err)
			} else if m != nil {
				meta = m
			}
		}()
		switch format {
		case "epub":
			m, cover, err = readEpub(f, st.size)
		case "mobi", "azw3", "azw":
			m, cover, err = readMobi(f, st.size)
		case "pdf":
			file.Set("textLayer", pdfTextLayer(f, st.size))
		}
	}()
	if cover != nil {
		out := filepath.Join(g.covers, "file-"+jsString(file.Get("md5"))+".jpg")
		if err := os.WriteFile(out+".src", cover, 0o644); err != nil {
			return nil, err
		}
		if err := exec.Command("sips", "-s", "format", "jpeg", "-Z", "600", out+".src", "--out", out).Run(); err != nil {
			warn("cover %s: %s", rel, err)
		} else {
			file.Set("cover", filepath.Base(out))
		}
		os.Remove(out + ".src")
	}
	file.Set("meta", meta)
	return file, nil
}

func numOrUndef(v any) any {
	if v == undefined {
		return undefined
	}
	if n := toNumber(v); truthy(n) {
		return n
	}
	return undefined
}

var reYear4 = re(`\d{4}`, "")

func matchYear(v any) any {
	s, ok := v.(string)
	if !ok {
		return undefined
	}
	if g, _ := reYear4.Match(s); g != nil {
		return g[0]
	}
	return undefined
}

// readSidecar reads a book folder's metadata.opf + cover.jpg on the NAS (written by organize-books or by hand).
func (g *genBooks) readSidecar(dir string) *Obj {
	if g.sidecarDone[dir] {
		return g.sidecars[dir]
	}
	g.sidecarDone[dir] = true
	data, err := os.ReadFile(filepath.Join(g.bookDir, dir, "metadata.opf"))
	if err != nil {
		return nil
	}
	o := parseOpf(decodeUTF8(data))
	category, sub := o.metaContent("home:category"), o.metaContent("home:subcategory")
	var cover any = nil
	if st, err := statFile(filepath.Join(g.bookDir, dir, "cover.jpg")); err == nil {
		name := "nas-" + sha1hex(dir)[:12] + ".jpg"
		cover = name
		dst := filepath.Join(g.covers, name)
		if cur, err := statFile(dst); err != nil || cur.mtimeMs < st.mtimeMs {
			copyFile(filepath.Join(g.bookDir, dir, "cover.jpg"), dst)
		}
	}
	tags := []any{}
	for _, t := range o.vals("subject") {
		if t != category && t != sub {
			tags = append(tags, t)
		}
	}
	r := newObj("source", "nas-opf", "sidecar", true, "doubanId", o.identifier("douban"),
		"title", first(o.vals("title")), "subtitle", o.metaContent("douban:subtitle"), "origTitle", o.metaContent("douban:original_title"),
		"authors", o.creators(false), "translators", o.creators(true),
		"publisher", first(o.vals("publisher")), "year", matchYear(first(o.vals("date"))), "pages", numOrUndef(o.metaContent("douban:pages")),
		"series", o.metaContent("calibre:series"), "isbn", o.identifier("isbn"),
		"rating", numOrUndef(o.metaContent("douban:rating")), "votes", numOrUndef(o.metaContent("douban:votes")),
		"intro", first(o.vals("description")), "tags", tags,
		"category", category, "subcategory", sub, "clc", o.metaContent("home:clc"), "cover", cover)
	g.sidecars[dir] = r
	return r
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

var reCacheName = regexp.MustCompile(`[^\w.-]`)

// cached returns the JSON cached under name, or calls fn; undefined results are not cached.
func (g *genBooks) cached(name string, fn func() any) any {
	f := filepath.Join(g.metaCache, reCacheName.ReplaceAllString(name, "_")+".json")
	if v, err := readJSONFile(f); err == nil {
		return v
	}
	v := fn()
	if v != undefined {
		writeJSONFile(f, v)
	}
	return v
}

var reSecDouban = regexp.MustCompile(`sec\.douban\.com`)

// doubanGet is one polite Douban GET, DOUBAN_GAP_MS apart; undefined (uncached) when blocked or failing.
func (g *genBooks) doubanGet(u string, asJSON bool) any {
	if g.doubanBlocked {
		return undefined
	}
	wait := time.Until(g.lastDouban.Add(time.Duration(doubanGapMs+rand.Float64()*1000) * time.Millisecond))
	if wait > 0 {
		time.Sleep(wait)
	}
	g.lastDouban = time.Now()
	for t := 0; t < 3; t++ {
		v, err := func() (any, error) {
			r, err := fetch(u, map[string]string{"User-Agent": doubanUA, "Referer": "https://book.douban.com/", "Accept-Language": "zh-CN,zh;q=0.9"}, 20*time.Second)
			if err != nil {
				return nil, errors.New(fetchErrMsg(err))
			}
			if r.status == 404 {
				return nil, nil
			}
			if r.status == 403 || r.status == 418 || reSecDouban.MatchString(r.url) {
				g.doubanBlocked = true
				warn("Douban blocked (%d); skipping Douban for the rest of this run", r.status)
				return undefined, nil
			}
			if !r.ok() {
				return nil, fmt.Errorf("HTTP %d", r.status)
			}
			if asJSON {
				return parseJSON(r.body)
			}
			return newObj("url", r.url, "html", decodeUTF8(r.body)), nil
		}()
		if err == nil {
			return v
		}
		if t == 2 {
			warn("douban %s: %s", u, err)
			return undefined
		}
		time.Sleep(time.Duration(3000*(1<<t)) * time.Millisecond)
	}
	return undefined
}

// jsSlice is s.slice(start, end) on byte offsets with JS's negative-end rule.
func jsSlice(s string, start, end int) string {
	if end < 0 {
		end = max(0, len(s)+end)
	}
	if start < 0 {
		start = max(0, len(s)+start)
	}
	end = min(end, len(s))
	if start >= end {
		return ""
	}
	return s[start:end]
}

func indexFrom(s, sub string, from int) int {
	if from > len(s) {
		return -1
	}
	if i := strings.Index(s[from:], sub); i >= 0 {
		return i + from
	}
	return -1
}

var (
	reSubjectID   = re(`subject\/(\d+)`, "")
	doubanLabels  = []string{"作者", "出版社", "出品方", "副标题", "原作名", "译者", "出版年", "页数", "定价", "装帧", "丛书", "ISBN", "统一书号"}
	reDoubanLabel = re(`(`+strings.Join(doubanLabels, "|")+`)\s*:`, "g")
	rePeopleSlash = re(`\s*\/\s*`, "")
	reIntro       = re(`<div class="intro">([\s\S]*?)<\/div>`, "g")
	reExpandAll   = re(`\(展开全部\)$`, "")
	reCriteria    = re(`criteria = '([^']*)'`, "")
	reAverage     = re(`property="v:average">\s*([\d.]+)`, "")
	reItemTitle   = re(`property="v:itemreviewed">([^<]*)<`, "")
	reVotes       = re(`property="v:votes">(\d+)`, "")
	reOgImage     = re(`property="og:image" content="([^"]+)"`, "")
)

func groupOrUndef(r *jsRe, s string, i int) any {
	g, ok := r.Match(s)
	if g == nil || !ok[i] {
		return undefined
	}
	return g[i]
}

func parseDoubanSubject(u, html string) any {
	id := groupOrUndef(reSubjectID, u, 1)
	if !truthy(id) || !strings.Contains(html, `id="info"`) {
		return nil
	}
	start := strings.Index(html, `id="info"`)
	info := jsSlice(html, start, indexFrom(html, "</div>", start))
	text := reSpacesG.Replace(decodeEntities(reTag.Replace(info, " ")), " ")
	field := map[string]string{}
	marks := reDoubanLabel.MatchAll(text)
	for i, m := range marks {
		end := len(text)
		if i+1 < len(marks) {
			end = marks[i+1].start
		}
		field[m.groups[1]] = jsTrim(jsSlice(text, m.end, end))
	}
	fieldv := func(k string) any {
		if v, ok := field[k]; ok {
			return v
		}
		return undefined
	}
	people := func(k string) []any {
		out := []any{}
		for _, x := range rePeopleSlash.Split(field[k]) {
			if x = jsTrim(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	report := ""
	if rs := strings.Index(html, `id="link-report"`); rs >= 0 {
		report = jsSlice(html, rs, indexFrom(html, "<h2", rs+1))
	}
	var intros []string
	for _, m := range reIntro.MatchAll(report) {
		if t := htmlText(m.groups[1]); !reExpandAll.Test(t) {
			intros = append(intros, t)
		}
	}
	sort.SliceStable(intros, func(i, j int) bool { return utf16Len(intros[j]) < utf16Len(intros[i]) })
	var intro any = undefined
	if len(intros) > 0 {
		intro = intros[0]
	}
	tags := []any{}
	for _, t := range strings.Split(reCriteria.Group(html, 1), "|") {
		if strings.HasPrefix(t, "7:") {
			tags = append(tags, t[2:])
		}
	}
	var rating any = undefined
	if r := jsParseFloat(groupOrUndef(reAverage, html, 1)); !math.IsNaN(r) && !math.IsInf(r, 0) {
		rating = r
	}
	pages, votes := any(undefined), any(undefined)
	if v, ok := field["页数"]; ok {
		if n := jsParseInt(v, 10); truthy(n) {
			pages = n
		}
	}
	if v, ok := groupOrUndef(reVotes, html, 1).(string); ok {
		if n := jsParseInt(v, 10); truthy(n) {
			votes = n
		}
	}
	isbn := or(fieldv("ISBN"), fieldv("统一书号"))
	return newObj("source", "douban", "doubanId", id,
		"title", jsTrim(decodeEntities(reItemTitle.Group(html, 1))),
		"subtitle", fieldv("副标题"), "origTitle", fieldv("原作名"), "authors", people("作者"), "translators", people("译者"),
		"publisher", fieldv("出版社"), "year", matchYear(fieldv("出版年")), "pages", pages,
		"series", fieldv("丛书"), "isbn", isbn, "rating", rating, "votes", votes,
		"intro", intro, "tags", tags, "coverUrl", groupOrUndef(reOgImage, html, 1))
}

func (g *genBooks) doubanSubject(id any) any {
	return g.cached("douban-subject-"+jsString(id), func() any {
		r := g.doubanGet("https://book.douban.com/subject/"+jsString(id)+"/", false)
		if r == undefined {
			return undefined
		}
		if !truthy(r) {
			return r
		}
		return parseDoubanSubject(getStr(r, "url"), getStr(r, "html"))
	})
}

func (g *genBooks) doubanByIsbn(isbn any) any {
	idHit := g.cached("douban-isbn-"+jsString(isbn), func() any {
		r := g.doubanGet("https://book.douban.com/isbn/"+jsString(isbn)+"/", false)
		if r == undefined {
			return undefined
		}
		var s any = r
		if truthy(r) {
			s = parseDoubanSubject(getStr(r, "url"), getStr(r, "html"))
		}
		if truthy(s) {
			writeJSONFile(filepath.Join(g.metaCache, "douban-subject-"+jsString(getv(s, "doubanId"))+".json"), s)
		}
		return newObj("id", or(getv(s, "doubanId"), nil))
	})
	if id := getv(idHit, "id"); truthy(id) {
		return g.doubanSubject(id)
	}
	return nil
}

var reBracketed = re(`[\[［(（【][^\]］)）】]*[\]］)）】]`, "g")

func authorMatch(a, b any) bool {
	clean := func(s any) string { return normBook(reBracketed.Replace(strOr(or(s, "")), "")) }
	x, y := clean(a), clean(b)
	return x != "" && y != "" && (strings.Contains(x, y) || strings.Contains(y, x))
}

func (g *genBooks) doubanSearch(title, author string) any {
	q := shortTitle(title)
	if q == "" {
		return nil
	}
	list := g.cached("douban-suggest-"+sha1hex(q), func() any {
		return g.doubanGet("https://book.douban.com/j/subject_suggest?q="+encodeURIComponent(q), true)
	})
	items, ok := list.([]any)
	if !ok {
		return nil
	}
	type scored struct {
		b any
		s float64
	}
	var ss []scored
	nq, nt := normBook(q), normBook(title)
	i := 0
	for _, b := range items {
		if getv(b, "type") != "b" {
			continue
		}
		bt := normBook(strOr(getv(b, "title")))
		s := 0.0
		if bt == nq || bt == nt {
			s = 3
		} else if bt != "" && (strings.Contains(nt, bt) || strings.Contains(bt, nq)) {
			s = 1
		}
		if author != "" && authorMatch(getv(b, "author_name"), author) {
			s += 2
		}
		ss = append(ss, scored{b, s - float64(i)*0.01})
		i++
	}
	sort.SliceStable(ss, func(a, b int) bool { return ss[b].s < ss[a].s })
	threshold := 2.9
	if author != "" {
		threshold = 2.5
	}
	if len(ss) == 0 || ss[0].s < threshold {
		return nil
	}
	return g.doubanSubject(getv(ss[0].b, "id"))
}

var reOLDescSplit = re(`\n-{3,}|\n\(\[source\]`, "")

func (g *genBooks) openLibrary(title, author string) any {
	q := shortTitle(title)
	ps := []param{{"title", q}}
	if author != "" {
		ps = append(ps, param{"author", author})
	}
	ps = append(ps, param{"limit", "5"}, param{"fields", "key,title,subtitle,author_name,first_publish_year,isbn,publisher,subject,cover_i,number_of_pages_median"})
	search := "?" + encodeParams(ps)
	res := g.cached("ol-search-"+sha1hex(search), func() any {
		r, err := fetch("https://openlibrary.org/search.json"+search, map[string]string{"User-Agent": "movie-shelf/1.0 (home library)"}, 20*time.Second)
		if err != nil {
			warn("openlibrary: %s", fetchErrMsg(err))
			return undefined
		}
		if !r.ok() {
			return undefined
		}
		v, err := parseJSON(r.body)
		if err != nil {
			warn("openlibrary: %s", err)
			return undefined
		}
		return v
	})
	var doc any
	for _, d := range arr(getv(res, "docs")) {
		dt := normBook(strOr(getv(d, "title")))
		if dt == normBook(q) || strings.Contains(normBook(q), dt) {
			doc = d
			break
		}
	}
	if doc == nil {
		return nil
	}
	key := jsString(getv(doc, "key"))
	work := g.cached("ol-work-"+key[strings.LastIndexByte(key, '/')+1:], func() any {
		r, err := fetch("https://openlibrary.org"+key+".json", nil, 20*time.Second)
		if err != nil || !r.ok() {
			return undefined
		}
		v, err := parseJSON(r.body)
		if err != nil {
			return undefined
		}
		return v
	})
	var desc any = undefined
	if d, ok := getv(work, "description").(string); ok {
		desc = d
	} else {
		desc = getv(work, "description", "value")
	}
	var intro any = undefined
	if s, ok := desc.(string); ok {
		intro = jsTrim(reOLDescSplit.Split(s)[0])
	}
	var isbn any = undefined
	for _, i := range arr(getv(doc, "isbn")) {
		if s, ok := i.(string); ok && utf16Len(s) == 13 {
			isbn = s
			break
		}
	}
	year := getv(doc, "first_publish_year")
	if truthy(year) {
		year = jsString(year)
	}
	cover := getv(doc, "cover_i")
	if truthy(cover) {
		cover = "https://covers.openlibrary.org/b/id/" + jsString(cover) + "-L.jpg"
	}
	authors := getv(doc, "author_name")
	if !truthy(authors) {
		authors = []any{}
	}
	subjects := arr(getv(doc, "subject"))
	return newObj("source", "openlibrary", "olKey", getv(doc, "key"), "title", getv(doc, "title"), "subtitle", getv(doc, "subtitle"),
		"authors", authors, "year", year, "publisher", firstOf(getv(doc, "publisher")), "pages", getv(doc, "number_of_pages_median"),
		"isbn", isbn, "intro", intro, "tags", append([]any{}, subjects[:min(12, len(subjects))]...), "coverUrl", cover)
}

func firstOf(v any) any {
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return undefined
	}
	return a[0]
}

func (g *genBooks) downloadCover(u, id string) any {
	f := filepath.Join(g.covers, id+".jpg")
	if exists(f) {
		return filepath.Base(f)
	}
	err := func() error {
		r, err := fetch(u, map[string]string{"User-Agent": doubanUA, "Referer": "https://book.douban.com/"}, 30*time.Second)
		if err != nil {
			return errors.New(fetchErrMsg(err))
		}
		if !r.ok() {
			return fmt.Errorf("HTTP %d", r.status)
		}
		if len(r.body) < 2000 {
			return errors.New("placeholder image")
		}
		return os.WriteFile(f, r.body, 0o644)
	}()
	if err != nil {
		warn("cover %s: %s", u, err)
		return nil
	}
	return filepath.Base(f)
}

func renderBookDoc(b *Obj) string {
	var zh []string
	add := func(v any) {
		if truthy(v) && hasCJK(jsString(v)) {
			zh = append(zh, jsString(v))
		}
	}
	for _, k := range []string{"title", "subtitle", "intro", "series"} {
		add(b.Get(k))
	}
	for _, k := range []string{"authors", "translators", "tags"} {
		for _, x := range arr(b.Get(k)) {
			add(x)
		}
	}
	add(b.Get("category"))
	add(b.Get("folder"))
	line := func(k string, v any) string {
		if truthy(v) {
			return "- " + k + "：" + jsString(v) + "\n"
		}
		return ""
	}
	files := arr(b.Get("files"))
	formats, paths := make([]any, len(files)), make([]any, len(files))
	var fset orderedSet
	for i, f := range files {
		formats[i] = getv(f, "format")
		paths[i] = bookSMBBase + jsString(getv(f, "path"))
		fset.add(jsString(getv(f, "format")))
	}
	authors := arr(b.Get("authors"))
	head := ""
	if len(authors) > 0 {
		head = " — " + joinAny(authors, "、")
	}
	folder := ""
	if f := b.Get("folder"); truthy(f) && f != b.Get("category") {
		folder = "（" + jsString(f) + "）"
	}
	intro := "（暂无简介）"
	if truthy(b.Get("intro")) {
		intro = jsString(b.Get("intro"))
	}
	return `---
title: ` + yamlStr(b.Get("title")) + `
book_id: ` + yamlStr(b.Get("id")) + `
authors: ` + yamlList(authors) + `
translators: ` + yamlList(arr(b.Get("translators"))) + `
publisher: ` + yamlStr(or(b.Get("publisher"), "")) + `
year: ` + orEmpty(getv(b, "year")) + `
isbn: ` + yamlStr(or(b.Get("isbn"), "")) + `
douban_id: ` + yamlStr(or(b.Get("doubanId"), "")) + `
douban_rating: ` + orEmpty(getv(b, "rating")) + `
category: ` + yamlStr(b.Get("category")) + `
formats: ` + yamlList(formats) + `
nas_path: ` + yamlList(paths) + `
---

# ` + jsString(b.Get("title")) + head + `

## 书目

` + line("书名", b.Get("title")) + line("副标题", b.Get("subtitle")) + line("原作名", b.Get("origTitle")) + line("作者", joinAny(authors, "、")) +
		line("译者", joinAny(arr(b.Get("translators")), "、")) + line("出版社", b.Get("publisher")) + line("出版年", b.Get("year")) +
		line("丛书", b.Get("series")) + line("ISBN", b.Get("isbn")) + "- 分类：" + jsString(b.Get("category")) + folder + `
- 格式：` + strings.Join(fset.items, "、") + `

## 简介

` + intro + `

## 标签

` + joinAny(arr(b.Get("tags")), ", ") + `

## 分词

` + strings.Join(segment(strings.Join(zh, "。"), true), " ") + `
`
}

var (
	reDupMark    = re(`\(重复\d*\)`, "")
	reJunkTitle  = re(`^(unknown|untitled)$`, "i")
	reDocExtName = re(`\.(pdf|docx?|epub)$`, "i")
)

type bookFile struct {
	scan     *Obj
	dupOf    bool
	title    string
	author   string
	fromName nameGuess
	remote   *Obj
}

func runGenBooks(args []string) error {
	root := findRoot()
	g := &genBooks{root: root, bookDir: env("BOOK_DIR", "/Volumes/store/book"), docs: filepath.Join(root, "docs-books"), cache: filepath.Join(root, "cache"),
		args: map[string]bool{}, sidecars: map[string]*Obj{}, sidecarDone: map[string]bool{}}
	for _, a := range args {
		g.args[a] = true
	}
	g.covers, g.metaCache, g.scanCache = filepath.Join(g.cache, "covers"), filepath.Join(g.cache, "bookmeta"), filepath.Join(g.cache, "book-scan.json")
	for _, d := range []string{g.docs, g.covers, g.metaCache, filepath.Join(root, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	rels, err := walkBooks(g.bookDir, "")
	if err != nil {
		return err
	}
	info("NAS book files: %d", len(rels))
	scanCache := &Obj{}
	if !g.args["--rescan"] {
		if v, err := readJSONFile(g.scanCache); err == nil {
			if o, ok := v.(*Obj); ok {
				scanCache = o
			}
		}
	}
	var files []*bookFile
	for _, rel := range rels {
		st, err := statFile(filepath.Join(g.bookDir, rel))
		if err != nil {
			return err
		}
		if st.size == 0 {
			info("skip (empty / copying): %s", rel)
			continue
		}
		if c, ok := scanCache.Get(rel).(*Obj); ok && toNumber(c.Get("size")) == float64(st.size) && toNumber(c.Get("mtime")) == jsRound(st.mtimeMs) {
			files = append(files, &bookFile{scan: c})
			continue
		}
		if s, err := g.scanFile(rel); err != nil {
			warn("skip %s: %s", rel, err)
		} else {
			files = append(files, &bookFile{scan: s})
		}
		if len(files)%20 == 0 {
			info("scanned %d/%d", len(files), len(rels))
		}
	}
	sc := &Obj{}
	for _, f := range files {
		sc.Set(jsString(f.scan.Get("path")), f.scan)
	}
	if err := writeJSONFile(g.scanCache, sc); err != nil {
		return err
	}

	sorted := append([]*bookFile(nil), files...)
	b2n := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return b2n(reDupMark.Test(jsString(sorted[i].scan.Get("path")))) < b2n(reDupMark.Test(jsString(sorted[j].scan.Get("path"))))
	})
	seenHash := map[string]bool{}
	dupes := 0
	for _, f := range sorted {
		k := jsString(f.scan.Get("md5")) + ":" + jsString(f.scan.Get("size"))
		if seenHash[k] {
			f.dupOf = true
			dupes++
		} else {
			seenHash[k] = true
		}
	}
	if dupes > 0 {
		info("identical copies hidden: %d", dupes)
	}
	kept := files[:0:0]
	for _, f := range files {
		if !f.dupOf {
			kept = append(kept, f)
		}
	}
	files = kept

	useRemote := !g.args["--offline"]
	statKeys := []string{"nas-opf", "douban", "openlibrary", "file"}
	stats := map[string]int{}
	statsJSON := func() string {
		o := &Obj{}
		for _, k := range statKeys {
			o.Set(k, float64(stats[k]))
		}
		return string(stringify(o))
	}
	var unmatched []*bookFile
	for done, f := range files {
		p := jsString(f.scan.Get("path"))
		f.fromName = parseFileName(p)
		m, _ := f.scan.Get("meta").(*Obj)
		if m == nil {
			m = &Obj{}
		}
		mt := m.Get("title")
		if s, ok := mt.(string); ok && s != "" && !reJunkTitle.Test(s) && !reDocExtName.Test(s) {
			f.title = s
		} else {
			f.title = f.fromName.title
		}
		if a := cleanPeople(m.Get("authors")); len(a) > 0 {
			f.author = jsString(a[0])
		} else {
			f.author = f.fromName.author
		}
		var remote any
		if dir := path.Dir(p); dir != "." {
			if r := g.readSidecar(dir); r != nil {
				remote = r
			}
		}
		if !truthy(remote) && useRemote {
			if isbn := m.Get("isbn"); truthy(isbn) {
				remote = g.doubanByIsbn(isbn)
			}
			if !truthy(remote) {
				remote = g.doubanSearch(f.title, f.author)
			}
			if !truthy(remote) && f.fromName.title != f.title {
				remote = g.doubanSearch(f.fromName.title, f.author)
			}
			if !truthy(remote) && !hasCJK(f.title) {
				remote = g.openLibrary(f.title, f.author)
			}
		}
		if ro, ok := remote.(*Obj); ok {
			f.remote = ro
		}
		src := "file"
		if f.remote != nil {
			src = strOr(f.remote.Get("source"))
			if src == "" {
				src = jsString(f.remote.Get("source"))
			}
		} else {
			unmatched = append(unmatched, f)
		}
		if _, ok := stats[src]; !ok && !contains(statKeys, src) {
			statKeys = append(statKeys, src)
		}
		stats[src]++
		if (done+1)%10 == 0 {
			info("metadata %d/%d %s", done+1, len(files), statsJSON())
		}
	}

	var workKeys []string
	works := map[string][]*bookFile{}
	for _, f := range files {
		r := f.remote
		p := jsString(f.scan.Get("path"))
		var key string
		switch {
		case r != nil && truthy(r.Get("sidecar")):
			if truthy(r.Get("doubanId")) {
				key = "db" + jsString(r.Get("doubanId"))
			} else {
				key = "f" + sha1hex(path.Dir(p))[:10]
			}
		case r != nil && truthy(r.Get("doubanId")):
			key = "db" + jsString(r.Get("doubanId"))
		case r != nil && truthy(r.Get("olKey")):
			k := jsString(r.Get("olKey"))
			key = "ol" + k[strings.LastIndexByte(k, '/')+1:]
		default:
			key = "h" + sha1hex(normBook(shortTitle(f.title)) + "|" + normBook(f.author))[:10]
		}
		if r != nil && truthy(r.Get("sidecar")) {
			dir := path.Dir(p)
			if owner := works[key]; len(owner) > 0 && path.Dir(jsString(owner[0].scan.Get("path"))) != dir {
				key += "-" + sha1hex(dir)[:4]
			}
		}
		if _, ok := works[key]; !ok {
			workKeys = append(workKeys, key)
		}
		works[key] = append(works[key], f)
	}

	formatRank := func(f *bookFile) int {
		switch jsString(f.scan.Get("format")) {
		case "epub":
			return 0
		case "azw3":
			return 1
		case "mobi":
			return 2
		case "pdf":
			return 3
		}
		return -1
	}
	var books []*Obj
	for _, id := range workKeys {
		group := works[id]
		sort.SliceStable(group, func(i, j int) bool { return formatRank(group[i]) < formatRank(group[j]) })
		r := group[0].remote
		if r == nil {
			r = &Obj{}
		}
		m := &Obj{}
		for _, f := range group {
			if fm, ok := f.scan.Get("meta").(*Obj); ok && truthy(fm.Get("title")) {
				m = fm
				break
			}
		}
		p0 := jsString(group[0].scan.Get("path"))
		top := r.Get("category")
		if !truthy(top) {
			if strings.Contains(p0, "/") {
				top = strings.Split(p0, "/")[0]
			} else {
				top = "未分类"
			}
		}
		folder := path.Dir(p0)
		var cover any = r.Get("cover")
		if !truthy(cover) {
			cover = nil
			for _, f := range group {
				if c := f.scan.Get("cover"); truthy(c) {
					cover = c
					break
				}
			}
		}
		if !truthy(cover) && truthy(r.Get("coverUrl")) && useRemote {
			cover = g.downloadCover(jsString(r.Get("coverUrl")), id)
		}
		var authors any
		if a := arr(r.Get("authors")); len(a) > 0 {
			authors = r.Get("authors")
		} else if c := cleanPeople(m.Get("authors")); len(c) > 0 {
			authors = c
		} else if group[0].author != "" {
			authors = []any{group[0].author}
		} else {
			authors = []any{}
		}
		translators := r.Get("translators")
		if len(arr(translators)) == 0 {
			translators = or(getv(m, "translators"), []any{})
		}
		var tagSet []any
		seenTag := map[string]bool{}
		for _, t := range append(append([]any{}, arr(r.Get("tags"))...), arr(m.Get("subjects"))...) {
			if k := mapKey(t); !seenTag[k] {
				seenTag[k] = true
				tagSet = append(tagSet, t)
			}
		}
		if tagSet == nil {
			tagSet = []any{}
		}
		added := math.Inf(-1)
		fs := []any{}
		for _, f := range group {
			added = math.Max(added, toNumber(f.scan.Get("mtime")))
			fs = append(fs, newObj("id", f.scan.Get("md5"), "path", f.scan.Get("path"), "format", f.scan.Get("format"), "size", f.scan.Get("size"), "textLayer", getv(f.scan, "textLayer")))
		}
		if folder == "." {
			folder = ""
		}
		title := r.Get("title")
		if !truthy(title) {
			title = group[0].title
		}
		year := getv(r, "year")
		if !truthy(year) {
			year = matchYear(getv(m, "date"))
		}
		books = append(books, newObj("id", id, "source", or(getv(r, "source"), "file"), "doubanId", getv(r, "doubanId"), "olKey", getv(r, "olKey"),
			"title", title, "subtitle", getv(r, "subtitle"), "origTitle", getv(r, "origTitle"),
			"authors", authors, "translators", translators,
			"publisher", or(getv(r, "publisher"), getv(m, "publisher")), "year", year, "pages", getv(r, "pages"),
			"series", or(getv(r, "series"), getv(m, "series")), "isbn", or(getv(r, "isbn"), getv(m, "isbn")), "rating", getv(r, "rating"), "votes", getv(r, "votes"),
			"intro", or(getv(r, "intro"), getv(m, "description")), "tags", tagSet[:min(16, len(tagSet))],
			"category", top, "subcategory", getv(r, "subcategory"), "clc", getv(r, "clc"), "folder", folder, "cover", cover,
			"added", added, "files", fs))
	}

	keep := map[string]bool{}
	for _, b := range books {
		name := jsString(b.Get("id")) + ".md"
		keep[name] = true
		if err := os.WriteFile(filepath.Join(g.docs, name), []byte(renderBookDoc(b)), 0o644); err != nil {
			return err
		}
	}
	olds, _ := readdirNames(g.docs)
	for _, f := range olds {
		if strings.HasSuffix(f, ".md") && !keep[f] {
			os.Remove(filepath.Join(g.docs, f))
		}
	}
	sort.SliceStable(books, func(i, j int) bool { return toNumber(books[j].Get("added")) < toNumber(books[i].Get("added")) })
	list := make([]any, len(books))
	for i, b := range books {
		list[i] = b
	}
	if err := writeJSONFile(filepath.Join(root, "data/books.json"), list); err != nil {
		return err
	}
	var lines []string
	for _, f := range unmatched {
		a := ""
		if f.author != "" {
			a = " / " + f.author
		}
		lines = append(lines, "- `"+jsString(f.scan.Get("path"))+"` → 解析为「"+f.title+"」"+a)
	}
	body := strings.Join(lines, "\n")
	if body == "" {
		body = "（无）"
	}
	blocked := ""
	if g.doubanBlocked {
		blocked = "\n\n**本次运行中豆瓣开始拒绝请求，后面的条目没有查询豆瓣，稍后重跑即可（已查询的结果有缓存）。**"
	}
	md := "# 未匹配豆瓣 / Open Library 的书\n\n生成时间：" + localeStringZH(time.Now()) + "。这些文件只用了文件内元数据和文件名。" + blocked + "\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, "unmatched-books.md"), []byte(md), 0o644); err != nil {
		return err
	}
	ncov := 0
	for _, b := range books {
		if truthy(b.Get("cover")) {
			ncov++
		}
	}
	info("books: %d works from %d files; metadata %s; covers %d", len(books), len(files), statsJSON(), ncov)
	return nil
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
