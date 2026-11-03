package main

// 片库 web server: shelf pages, /api/*, qmd search, OPDS for KOReader (/opds) and KOReader progress sync (/kosync).
// Drop-in replacement for server.mjs with the same routes and responses.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	root, host, port       string
	qmdIndex, qmdCLI       string
	node, bookDir, kdbPath string
	registration           bool
}

type server struct {
	cfg    config
	movies *jsonList
	books  *jsonList
	kdb    *kosyncDB
	qmdMu  sync.Mutex
}

type reqURL struct {
	pathname, search string
	params           []param
}

var mimeTypes = map[string]string{".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".jpg": "image/jpeg", ".svg": "image/svg+xml", ".json": "application/json"}

func send(w http.ResponseWriter, code int, body []byte, typ string, extra map[string]string) error {
	h := w.Header()
	h.Set("Content-Type", typ)
	for k, v := range extra {
		h.Set(k, v)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	_, err := w.Write(body)
	return err
}

func sendJSON(w http.ResponseWriter, code int, v any) error {
	return send(w, code, stringify(v), "application/json; charset=utf-8", nil)
}

func serveFile(w http.ResponseWriter, file, cache string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return sendJSON(w, 404, newObj("error", "not found"))
	}
	typ, ok := mimeTypes[filepath.Ext(file)]
	if !ok {
		typ = "application/octet-stream"
	}
	return send(w, 200, data, typ, map[string]string{"Cache-Control": cache})
}

func (s *server) serveBookFile(w http.ResponseWriter, r *http.Request, fileID string) error {
	st, err := s.books.load()
	if err != nil {
		return err
	}
	hit, ok := st.byFile[mapKey(fileID)]
	if st.byFile == nil || !ok {
		return sendJSON(w, 404, newObj("error", "not found"))
	}
	rel := jsString(hit.file.Get("path"))
	full := filepath.Join(s.cfg.bookDir, rel)
	if !strings.HasPrefix(full, s.cfg.bookDir+string(filepath.Separator)) {
		return sendJSON(w, 400, newObj("error", "bad path"))
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("ENOENT: no such file or directory, stat '%s'", full)
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	name := filepath.Base(rel)
	typ, ok := bookMIME[jsString(hit.file.Get("format"))]
	if !ok {
		typ = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	h.Set("Content-Disposition", `attachment; filename="`+encodeURIComponent(name)+`"; filename*=UTF-8''`+encodeURIComponent(name))
	w.WriteHeader(200)
	log.Printf("%s download %s (%s)", nowISO(), rel, r.Header.Get("User-Agent"))
	_, err = io.Copy(w, f)
	return err
}

func (s *server) apiBooks(w http.ResponseWriter) error {
	prog, err := s.kdb.latest()
	if err != nil {
		return err
	}
	st, err := s.books.load()
	if err != nil {
		return err
	}
	out := make([]any, len(st.list))
	for i, x := range st.list {
		b, ok := x.(*Obj)
		if !ok {
			out[i] = x
			continue
		}
		var ps []progressRow
		for _, f := range strList(b.Get("files")) {
			if fo, ok := f.(*Obj); ok {
				if p, ok := prog[jsString(fo.Get("id"))]; ok {
					ps = append(ps, p)
				}
			}
		}
		if len(ps) == 0 {
			out[i] = b
			continue
		}
		sort.SliceStable(ps, func(i, j int) bool { return ps[j].timestamp < ps[i].timestamp })
		nb := b.Clone()
		nb.Set("progress", newObj("percentage", ps[0].percentage, "device", ps[0].device, "timestamp", ps[0].timestamp))
		out[i] = nb
	}
	return sendJSON(w, 200, out)
}

func rssMB() float64 {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return jsRound(float64(ru.Maxrss) / 1048576)
}

var (
	rePoster   = regexp.MustCompile(`^/posters/(\d+\.jpg)$`)
	reCover    = regexp.MustCompile(`^/covers/([\w-]+\.jpg)$`)
	reBookFile = regexp.MustCompile(`^/books/file/([0-9a-f]{32})(/.*)?$`)
)

func (s *server) route(w http.ResponseWriter, r *http.Request, u *reqURL) error {
	p := u.pathname
	switch {
	case strings.HasPrefix(p, "/kosync"):
		sub := p[len("/kosync"):]
		if sub == "" {
			sub = "/"
		}
		return s.kosync(w, r, sub)
	case p == "/opds" || strings.HasPrefix(p, "/opds/"):
		return s.opds(w, r, u)
	case p == "/api/movies":
		st, err := s.movies.load()
		if err != nil {
			return err
		}
		list := st.list
		if list == nil {
			list = []any{}
		}
		return sendJSON(w, 200, list)
	case p == "/api/books":
		return s.apiBooks(w)
	case p == "/api/search":
		q, _ := getParam(u.params, "q")
		q = utf16Slice(jsTrim(q), 200)
		kind, ok := getParam(u.params, "kind")
		if !ok || kind == "" {
			kind = "movies"
		}
		collections := []string{"movies"}
		if kind == "all" {
			collections = []string{"movies", "books"}
		} else if kind == "books" {
			collections = []string{"books"}
		}
		if q == "" {
			return sendJSON(w, 200, newObj("q", q, "hits", []any{}, "ms", 0.0))
		}
		limit := 24
		if kind == "all" {
			limit = 36
		}
		out, err := s.search(q, collections, limit)
		if err != nil {
			return err
		}
		log.Printf("%s search[%s] %s %sms %d hits", nowISO(), kind, stringify(q), jsString(out.Get("ms")), len(out.Get("hits").([]any)))
		return sendJSON(w, 200, out)
	case p == "/api/health":
		ms, err := s.movies.load()
		if err != nil {
			return err
		}
		bs, err := s.books.load()
		if err != nil {
			return err
		}
		return sendJSON(w, 200, newObj("ok", true, "movies", float64(len(ms.list)), "books", float64(len(bs.list)), "rssMB", rssMB()))
	}
	if m := rePoster.FindStringSubmatch(p); m != nil {
		return serveFile(w, filepath.Join(s.cfg.root, "cache/posters", m[1]), "public, max-age=604800")
	}
	if m := reCover.FindStringSubmatch(p); m != nil {
		return serveFile(w, filepath.Join(s.cfg.root, "cache/covers", m[1]), "public, max-age=604800")
	}
	if m := reBookFile.FindStringSubmatch(p); m != nil {
		return s.serveBookFile(w, r, m[1])
	}
	if p == "/shelf.css" {
		return serveFile(w, filepath.Join(s.cfg.root, "public/shelf.css"), "no-cache")
	}
	if done, err := s.routeExtra(w, r, p); done || err != nil {
		return err
	}
	return sendJSON(w, 404, newObj("error", "not found"))
}

// headerWatch records whether a handler already started its response.
type headerWatch struct {
	http.ResponseWriter
	wrote bool
}

func (h *headerWatch) WriteHeader(code int) { h.wrote = true; h.ResponseWriter.WriteHeader(code) }
func (h *headerWatch) Write(b []byte) (int, error) {
	h.wrote = true
	return h.ResponseWriter.Write(b)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pathname, search, query := whatwgURL(r.RequestURI)
	u := &reqURL{pathname, search, parseQuery(query)}
	hw := &headerWatch{ResponseWriter: w}
	defer func() {
		if v := recover(); v != nil {
			log.Printf("panic: %v", v)
			if !hw.wrote {
				sendJSON(hw, 500, newObj("error", fmt.Sprint(v)))
			}
		}
	}()
	if err := s.route(hw, r, u); err != nil {
		log.Printf("error: %v", err)
		if !hw.wrote {
			sendJSON(hw, 500, newObj("error", err.Error()))
		} else if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				c.Close()
			}
		}
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func findRoot() string {
	if v := os.Getenv("SHELF_ROOT"); v != "" {
		return v
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		cands = append(cands, d, filepath.Dir(d))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd, filepath.Dir(wd))
	}
	for _, c := range cands {
		if _, err := os.Stat(filepath.Join(c, "data/movies.json")); err == nil {
			return c
		}
	}
	return "."
}

func serve() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
	root := findRoot()
	home, _ := os.UserHomeDir()
	cfg := config{
		root:         root,
		host:         env("HOST", "0.0.0.0"),
		port:         env("PORT", "8686"),
		qmdIndex:     env("QMD_INDEX", "movies"),
		qmdCLI:       filepath.Join(root, "node_modules/@tobilu/qmd/dist/cli/qmd.js"),
		node:         env("NODE_BIN", filepath.Join(home, ".nvm/versions/node/v24.13.0/bin/node")),
		bookDir:      env("BOOK_DIR", "/Volumes/store/book"),
		kdbPath:      env("KOSYNC_DB", filepath.Join(root, "data/kosync.sqlite")),
		registration: os.Getenv("KOSYNC_REGISTRATION") != "off",
	}
	kdb, err := openKosync(cfg.kdbPath)
	if err != nil {
		log.Fatalf("kosync db: %v", err)
	}
	s := &server{cfg: cfg, movies: newJSONList(filepath.Join(root, "data/movies.json")), books: newJSONList(filepath.Join(root, "data/books.json")), kdb: kdb}
	if _, err := os.Stat(filepath.Join(home, ".cache/qmd", cfg.qmdIndex+".sqlite")); err != nil {
		fmt.Fprintf(os.Stderr, "qmd index not found at %s; search will fail until it is built\n", filepath.Join(home, ".cache/qmd", cfg.qmdIndex+".sqlite"))
	}
	srv := &http.Server{Addr: cfg.host + ":" + cfg.port, Handler: s, ReadHeaderTimeout: 30 * time.Second}
	log.Printf("片库 listening on http://%s:%s", cfg.host, cfg.port)
	log.Fatal(srv.ListenAndServe())
}
