package main

// Endpoints added for the v3 pages: library status for 片库体检, and placeholders for features without a data source yet
// (Plex watch history, casting, Radarr add, lists). Placeholders answer {"pending": true, ...} so pages can hide or label them.

import (
	"bufio"
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

//go:embed all:webdist
var webdistFS embed.FS

var webPages = map[string]string{
	"/": "index.html", "/index.html": "index.html", "/books": "books.html", "/all": "books.html",
	"/pick": "pick.html", "/movie": "movie.html", "/fill": "fill.html", "/health": "health.html",
}

// serveWeb serves the embedded Astro build; hashed assets under /_astro/ are cached for a year.
func serveWeb(w http.ResponseWriter, p string) (bool, error) {
	name, ok := webPages[p]
	cache := "no-cache"
	if !ok {
		clean := path.Clean(p)
		if clean != p || strings.Contains(p, "..") || !(strings.HasPrefix(p, "/_astro/") || p == "/favicon.svg") {
			return false, nil
		}
		name = strings.TrimPrefix(p, "/")
		if strings.HasPrefix(p, "/_astro/") {
			cache = "public, max-age=31536000, immutable"
		}
	}
	data, err := fs.ReadFile(webdistFS, "webdist/"+name)
	if err != nil {
		return false, nil
	}
	typ := mimeTypes[path.Ext(name)]
	if typ == "" {
		typ = "application/octet-stream"
	}
	return true, send(w, 200, data, typ, map[string]string{"Cache-Control": cache})
}

var (
	reUnmatchedMovie = regexp.MustCompile("^- `(.+)` — (.*)$")
	reUnmatchedBook  = regexp.MustCompile("^- `(.+)` → (.*)$")
)

func readUnmatched(file string, re *regexp.Regexp) []any {
	out := []any{}
	f, err := os.Open(file)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := re.FindStringSubmatch(sc.Text()); m != nil {
			out = append(out, newObj("name", m[1], "reason", m[2]))
		}
	}
	return out
}

func mtimeMs(file string) any {
	st, err := statFile(file)
	if err != nil {
		return nil
	}
	return jsRound(st.mtimeMs)
}

func volume(name, dir string) *Obj {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil || !strings.HasPrefix(dir, "/Volumes/") {
		return newObj("name", name, "path", dir, "mounted", false)
	}
	bs := float64(st.Bsize)
	return newObj("name", name, "path", dir, "mounted", true, "total", float64(st.Blocks)*bs, "free", float64(st.Bavail)*bs)
}

func (s *server) apiLibrary(w http.ResponseWriter) error {
	return sendJSON(w, 200, newObj(
		"moviesUpdated", mtimeMs(filepath.Join(s.cfg.root, "data/movies.json")),
		"booksUpdated", mtimeMs(filepath.Join(s.cfg.root, "data/books.json")),
		"unmatchedMovies", readUnmatched(filepath.Join(s.cfg.root, "unmatched.md"), reUnmatchedMovie),
		"unmatchedBooks", readUnmatched(filepath.Join(s.cfg.root, "unmatched-books.md"), reUnmatchedBook),
		"volumes", []any{volume("store", "/Volumes/store"), volume("video", "/Volumes/video")},
	))
}

var placeholders = map[string]*Obj{
	"/api/watch":      newObj("pending", true, "source", "Plex 观看记录、IINA 进度", "items", []any{}),
	"/api/lists":      newObj("pending", true, "source", "豆瓣 / IMDb 榜单、TMDB 人物作品表、Radarr 合集", "lists", []any{}),
	"/api/play":       newObj("pending", true, "source", "Plex 客厅投屏"),
	"/api/radarr/add": newObj("pending", true, "source", "Radarr 加片"),
}

func (s *server) routeExtra(w http.ResponseWriter, r *http.Request, p string) (bool, error) {
	if p == "/api/library" {
		return true, s.apiLibrary(w)
	}
	if p == "/api/lists" {
		if b, err := os.ReadFile(filepath.Join(s.cfg.root, "data/lists.json")); err == nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, err = w.Write(b)
			return true, err
		}
	}
	if ph, ok := placeholders[p]; ok {
		code := 200
		if r.Method == "POST" {
			code = 501
		}
		return true, sendJSON(w, code, ph)
	}
	return serveWeb(w, p)
}
