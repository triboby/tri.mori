package main

// OPDS 1.2 catalog for KOReader, rendered exactly like server.mjs.

import (
	"math"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"

	"movieshelf/icu"
)

const (
	navType = "application/atom+xml;profile=opds-catalog;kind=navigation"
	acqType = "application/atom+xml;profile=opds-catalog;kind=acquisition"
)

var bookMIME = map[string]string{
	"epub": "application/epub+zip", "pdf": "application/pdf", "mobi": "application/x-mobipocket-ebook", "azw3": "application/vnd.amazon.ebook",
	"azw": "application/vnd.amazon.ebook", "djvu": "image/vnd.djvu", "cbz": "application/vnd.comicbook+zip", "fb2": "application/x-fictionbook+xml", "txt": "text/plain; charset=utf-8",
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func xmlEsc(v any) string { return xmlEscaper.Replace(strOr(v)) }

func fmtSize(b float64) string {
	if b >= 1e6 {
		return toFixed1(b/1e6) + " MB"
	}
	return jsNum(math.Max(1, jsRound(b/1e3))) + " KB"
}

func fileHref(f *Obj) string {
	return "/books/file/" + jsString(f.Get("id")) + "/" + encodeURIComponent(path.Base(jsString(f.Get("path"))))
}

func feed(id, title, self, kind string, entries []string, updated string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:opds="http://opds-spec.org/2010/catalog">
  <id>` + xmlEsc(id) + `</id>
  <title>` + xmlEsc(title) + `</title>
  <updated>` + updated + `</updated>
  <author><name>家庭书架</name></author>
  <link rel="self" href="` + xmlEsc(self) + `" type="` + kind + `"/>
  <link rel="start" href="/opds" type="` + navType + `"/>
  <link rel="search" href="/opds/search.xml" type="application/opensearchdescription+xml"/>
  <link rel="search" href="/opds/search?q={searchTerms}" type="` + acqType + `"/>
` + strings.Join(entries, "\n") + `
</feed>`
}

func navEntry(title, href, content, updated, typ string) string {
	return `  <entry>
    <title>` + xmlEsc(title) + `</title><id>urn:shelf:nav:` + xmlEsc(href) + `</id><updated>` + updated + `</updated>
    <content type="text">` + xmlEsc(content) + `</content>
    <link rel="subsection" href="` + xmlEsc(href) + `" type="` + typ + `"/>
  </entry>`
}

func mapJoin(v any, f func(any) string, sep string) string {
	a := strList(v)
	parts := make([]string, len(a))
	for i, x := range a {
		parts[i] = f(x)
	}
	return strings.Join(parts, sep)
}

func bookEntry(b *Obj) (string, error) {
	updated, err := isoString(toNumber(b.Get("added")))
	if err != nil {
		return "", err
	}
	var sum []string
	if truthy(b.Get("rating")) {
		sum = append(sum, "豆瓣 "+jsString(b.Get("rating")))
	}
	for _, k := range []string{"year", "publisher", "intro"} {
		if v := b.Get(k); truthy(v) {
			sum = append(sum, jsString(v))
		}
	}
	summary := strings.Join(sum, " · ")
	var s strings.Builder
	s.WriteString("  <entry>\n    <title>" + xmlEsc(b.Get("title")) + "</title><id>urn:shelf:book:" + xmlEsc(b.Get("id")) + "</id><updated>" + updated + "</updated>\n")
	s.WriteString(mapJoin(b.Get("authors"), func(a any) string { return "    <author><name>" + xmlEsc(a) + "</name></author>" }, "\n") + "\n")
	if v := b.Get("publisher"); truthy(v) {
		s.WriteString("    <dc:publisher>" + xmlEsc(v) + "</dc:publisher>\n")
	}
	if v := b.Get("year"); truthy(v) {
		s.WriteString("    <dc:issued>" + xmlEsc(v) + "</dc:issued>\n")
	}
	if v := b.Get("isbn"); truthy(v) {
		s.WriteString("    <dc:identifier>urn:isbn:" + xmlEsc(v) + "</dc:identifier>\n")
	}
	s.WriteString(mapJoin(b.Get("tags"), func(t any) string { return `    <category term="` + xmlEsc(t) + `" label="` + xmlEsc(t) + `"/>` }, "\n"))
	s.WriteString("\n    <summary type=\"text\">" + xmlEsc(utf16Slice(summary, 1200)) + "</summary>\n")
	if c := b.Get("cover"); truthy(c) {
		s.WriteString(`    <link rel="http://opds-spec.org/image" href="/covers/` + xmlEsc(c) + `" type="image/jpeg"/>
    <link rel="http://opds-spec.org/image/thumbnail" href="/covers/` + xmlEsc(c) + `" type="image/jpeg"/>
`)
	}
	s.WriteString(mapJoin(b.Get("files"), func(x any) string {
		f, _ := x.(*Obj)
		if f == nil {
			f = &Obj{}
		}
		format := jsString(f.Get("format"))
		mime, ok := bookMIME[format]
		if !ok {
			mime = "application/octet-stream"
		}
		return `    <link rel="http://opds-spec.org/acquisition" href="` + xmlEsc(fileHref(f)) + `" type="` + mime + `" title="` + strings.ToUpper(format) + " " + fmtSize(toNumber(f.Get("size"))) + `"/>`
	}, "\n"))
	s.WriteString("\n  </entry>")
	return s.String(), nil
}

func bookText(b *Obj) string {
	var parts []any
	parts = append(parts, b.Get("title"), b.Get("subtitle"), b.Get("origTitle"))
	parts = append(parts, strList(b.Get("authors"))...)
	parts = append(parts, strList(b.Get("translators"))...)
	parts = append(parts, b.Get("series"), b.Get("isbn"), b.Get("publisher"))
	parts = append(parts, strList(b.Get("tags"))...)
	for _, f := range strList(b.Get("files")) {
		if fo, ok := f.(*Obj); ok {
			parts = append(parts, fo.Get("path"))
		} else {
			parts = append(parts, undefined)
		}
	}
	var keep []string
	for _, p := range parts {
		if truthy(p) {
			keep = append(keep, jsString(p))
		}
	}
	return strings.ToLower(norm.NFKC.String(strings.Join(keep, " ")))
}

var (
	reOpdsCat  = regexp.MustCompile(`^/opds/cat/([^/]+)(?:/([^/]+))?$`)
	reTrailing = regexp.MustCompile(`/+$`)
)

func books(list []any) []*Obj {
	out := make([]*Obj, 0, len(list))
	for _, x := range list {
		if o, ok := x.(*Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

type count struct {
	key string
	n   int
}

// countBy counts values in first-seen order, then sorts by count descending (stable, like Array.sort).
func countBy(bs []*Obj, field string, skipFalsy bool) []count {
	idx := map[string]int{}
	var out []count
	for _, b := range bs {
		v := b.Get(field)
		if skipFalsy && !truthy(v) {
			continue
		}
		k := jsString(v)
		if i, ok := idx[k]; ok {
			out[i].n++
		} else {
			idx[k] = len(out)
			out = append(out, count{k, 1})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].n > out[j].n })
	return out
}

func (s *server) opds(w http.ResponseWriter, r *http.Request, u *reqURL) error {
	st, err := s.books.load()
	if err != nil {
		return err
	}
	list := books(st.list)
	now := nowISO()
	atom := func(body, kind string) error {
		return send(w, 200, []byte(body), kind+"; charset=utf-8", map[string]string{"Cache-Control": "no-cache"})
	}
	p := reTrailing.ReplaceAllString(u.pathname, "")
	if p == "" {
		p = "/opds"
	}
	page := func(id, title string, bs []*Obj) error {
		n := 1.0
		if v, ok := getParam(u.params, "page"); ok {
			if x := strToNumber(v); x > 1 && !math.IsNaN(x) {
				n = x
			}
		}
		const size = 50
		lo, hi := sliceIdx((n-1)*size, len(bs)), sliceIdx(n*size, len(bs))
		var entries []string
		for _, b := range bs[lo:max(lo, hi)] {
			e, err := bookEntry(b)
			if err != nil {
				return err
			}
			entries = append(entries, e)
		}
		body := feed(id, title, u.pathname+u.search, acqType, entries, now)
		pages := math.Ceil(float64(len(bs)) / size)
		link := func(rel string, k float64) string {
			q := setParam(append([]param(nil), u.params...), "page", jsNum(k))
			return `  <link rel="` + rel + `" href="` + xmlEsc(u.pathname+"?"+encodeParams(q)) + `" type="` + acqType + `"/>`
		}
		var extra []string
		if n > 1 {
			extra = append(extra, link("previous", n-1))
		}
		if n < pages {
			extra = append(extra, link("next", n+1))
		}
		if len(extra) > 0 {
			body = strings.Replace(body, "\n  <entry>", "\n"+strings.Join(extra, "\n")+"\n  <entry>", 1)
		}
		return atom(body, acqType)
	}

	switch p {
	case "/opds":
		cats := countBy(list, "category", false)
		entries := []string{
			navEntry("最近添加", "/opds/new", "最新入库的 50 本", now, acqType),
			navEntry("豆瓣高分", "/opds/top", "豆瓣 8.5 分以上", now, acqType),
			navEntry("全部书籍", "/opds/all", "共 "+strconv.Itoa(len(list))+" 本", now, acqType),
		}
		for _, c := range cats {
			entries = append(entries, navEntry(c.key, "/opds/cat/"+encodeURIComponent(c.key), strconv.Itoa(c.n)+" 本", now, navType))
		}
		return atom(feed("urn:shelf:opds", "家庭书架", "/opds", navType, entries, now), navType)
	case "/opds/search.xml":
		return send(w, 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>家庭书架</ShortName><Description>搜索书名、作者、标签、ISBN</Description><InputEncoding>UTF-8</InputEncoding><OutputEncoding>UTF-8</OutputEncoding>
  <Url type="`+acqType+`" template="/opds/search?q={searchTerms}"/>
</OpenSearchDescription>`), "application/opensearchdescription+xml; charset=utf-8", nil)
	case "/opds/new":
		return page("urn:shelf:new", "最近添加", list[:min(50, len(list))])
	case "/opds/top":
		var top []*Obj
		for _, b := range list {
			if toNumber(b.Get("rating")) >= 8.5 {
				top = append(top, b)
			}
		}
		sort.SliceStable(top, func(i, j int) bool { return toNumber(top[i].Get("rating")) > toNumber(top[j].Get("rating")) })
		return page("urn:shelf:top", "豆瓣高分", top)
	case "/opds/all":
		all := append([]*Obj(nil), list...)
		sort.SliceStable(all, func(i, j int) bool {
			return icu.ZH.Compare(jsString(all[i].Get("title")), jsString(all[j].Get("title"))) < 0
		})
		return page("urn:shelf:all", "全部书籍", all)
	case "/opds/search":
		q, _ := getParam(u.params, "q")
		terms := jsSplitSpace(strings.ToLower(norm.NFKC.String(q)))
		var hits []*Obj
		if len(terms) > 0 {
			for _, b := range list {
				t := bookText(b)
				ok := true
				for _, w := range terms {
					if !strings.Contains(t, w) {
						ok = false
						break
					}
				}
				if ok {
					hits = append(hits, b)
				}
			}
		}
		joined := strings.Join(terms, " ")
		return page("urn:shelf:search:"+joined, "搜索："+joined, hits)
	}
	if m := reOpdsCat.FindStringSubmatch(p); m != nil {
		c, err := decodeURIComponent(m[1])
		if err != nil {
			return err
		}
		var inCat []*Obj
		for _, b := range list {
			if v, ok := b.Get("category").(string); ok && v == c {
				inCat = append(inCat, b)
			}
		}
		if m[2] != "" {
			sub, err := decodeURIComponent(m[2])
			if err != nil {
				return err
			}
			bs := inCat
			if sub != "全部" {
				bs = nil
				for _, b := range inCat {
					if v, ok := b.Get("subcategory").(string); ok && v == sub {
						bs = append(bs, b)
					}
				}
			}
			return page("urn:shelf:cat:"+c+":"+sub, c+" · "+sub, bs)
		}
		base := "/opds/cat/" + encodeURIComponent(c)
		entries := []string{navEntry("全部"+c, base+"/"+encodeURIComponent("全部"), strconv.Itoa(len(inCat))+" 本", now, acqType)}
		for _, sc := range countBy(inCat, "subcategory", true) {
			entries = append(entries, navEntry(sc.key, base+"/"+encodeURIComponent(sc.key), strconv.Itoa(sc.n)+" 本", now, acqType))
		}
		return atom(feed("urn:shelf:cat:"+c, c, base, navType, entries, now), navType)
	}
	return sendJSON(w, 404, newObj("error", "not found"))
}

// sliceIdx clamps a fractional index like Array.prototype.slice.
func sliceIdx(x float64, n int) int {
	x = math.Trunc(x)
	if x < 0 {
		x = math.Max(0, float64(n)+x)
	}
	return int(math.Min(x, float64(n)))
}
