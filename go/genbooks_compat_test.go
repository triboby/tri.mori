package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestGenBooksCompatDump writes the gen-books pure-function outputs for inputs in $SHELF_COMPAT_DIR/inputs.json,
// for comparison with the same functions run under Node.
func TestGenBooksCompatDump(t *testing.T) {
	dir := os.Getenv("SHELF_COMPAT_DIR")
	if dir == "" {
		t.Skip("SHELF_COMPAT_DIR not set")
	}
	v, err := readJSONFile(filepath.Join(dir, "inputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := v.(*Obj)
	out := &Obj{}
	var pf, st, cp, ht, fi, db, ds, ol []any
	for _, n := range arr(in.Get("names")) {
		g := parseFileName(n.(string))
		pf = append(pf, newObj("title", g.title, "author", g.author))
	}
	for _, x := range arr(in.Get("titles")) {
		st = append(st, shortTitle(x.(string)))
	}
	for _, x := range arr(in.Get("people")) {
		cp = append(cp, cleanPeople(x))
	}
	for _, x := range arr(in.Get("html")) {
		ht = append(ht, htmlText(x.(string)))
	}
	for _, x := range arr(in.Get("isbns")) {
		fi = append(fi, findIsbn(arr(x)))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "html", "*.html"))
	sort.Strings(files)
	for _, f := range files {
		data, _ := os.ReadFile(f)
		id := filepath.Base(f[:len(f)-len(".html")])
		db = append(db, parseDoubanSubject("https://book.douban.com/subject/"+id+"/", decodeUTF8(data)))
	}
	g := &genBooks{metaCache: filepath.Join(dir, "G/cache/bookmeta")}
	titles := arr(in.Get("titles"))
	for _, x := range titles[:min(40, len(titles))] {
		ds = append(ds, orNull(g.doubanSearch(x.(string), "")))
	}
	for _, q := range [][2]string{{"Clean Agile", "Robert C. Martin"}, {"The Dictator's Handbook", ""}, {"Hands-On Functional Programming in Rust", "Andrew Johnson"}} {
		ol = append(ol, orNull(g.openLibrary(q[0], q[1])))
	}
	out.Set("parseFileName", pf)
	out.Set("shortTitle", st)
	out.Set("cleanPeople", cp)
	out.Set("htmlText", ht)
	out.Set("findIsbn", fi)
	out.Set("douban", db)
	out.Set("doubanSearch", ds)
	out.Set("openLibrary", ol)
	if err := writeJSONFile(filepath.Join(dir, "go-out.json"), out); err != nil {
		t.Fatal(err)
	}
}
