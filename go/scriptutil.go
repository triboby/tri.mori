package main

// Node-compatible helpers for the rebuild commands: fs (directory order, stat times in ms), fetch, JSON files,
// path.extname / path.relative, Date formatting and a concurrency pool.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type dirent struct {
	name  string
	isDir bool
}

// readDirents is fs.readdir(dir, { withFileTypes: true }): names in byte order (libuv sorts with strcmp), symlinks not followed.
func readDirents(dir string) ([]dirent, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	es, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Name() < es[j].Name() })
	out := make([]dirent, len(es))
	for i, e := range es {
		out[i] = dirent{e.Name(), e.IsDir()}
	}
	return out, nil
}

func readdirNames(dir string) ([]string, error) {
	es, err := readDirents(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.name
	}
	return out, nil
}

type fileStat struct {
	size             int64
	mtimeMs, birthMs float64
	isDir, isRegular bool
}

func msFromTimespec(ts syscall.Timespec) float64 {
	return float64(ts.Sec)*1e3 + float64(ts.Nsec)/1e6
}

// statFile is fs.stat (follows symlinks), with Node's millisecond time fields.
func statFile(p string) (fileStat, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(p, &st); err != nil {
		return fileStat{}, &os.PathError{Op: "stat", Path: p, Err: err}
	}
	mode := st.Mode & syscall.S_IFMT
	return fileStat{st.Size, msFromTimespec(st.Mtimespec), msFromTimespec(st.Birthtimespec), mode == syscall.S_IFDIR, mode == syscall.S_IFREG}, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// errCode is e.code for a Node fs error (ENOENT, EACCES, …), or the message.
func errCode(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ENOENT:
			return "ENOENT"
		case syscall.EACCES:
			return "EACCES"
		case syscall.EPERM:
			return "EPERM"
		case syscall.EEXIST:
			return "EEXIST"
		case syscall.ENOTDIR:
			return "ENOTDIR"
		case syscall.EIO:
			return "EIO"
		}
	}
	return err.Error()
}

// jsExtname is path.extname.
func jsExtname(p string) string {
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	i := strings.LastIndexByte(base, '.')
	if i <= 0 || strings.Trim(base, ".") == "" {
		return ""
	}
	return base[i:]
}

// jsRelative is path.relative for a path inside base.
func jsRelative(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return r
}

// readJSONFile parses a JSON file like JSON.parse(await fs.readFile(f, 'utf8')).
func readJSONFile(f string) (any, error) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	return parseJSON([]byte(decodeUTF8(data)))
}

func writeJSONFile(f string, v any) error { return os.WriteFile(f, stringify(v), 0o644) }

// decodeUTF8 is Buffer.toString('utf8'): each maximal invalid subsequence becomes one U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r != utf8.RuneError || n > 1 {
			sb.WriteRune(r)
			i += n
			continue
		}
		sb.WriteRune(utf8.RuneError)
		i += maximalSubpart(b[i:])
	}
	return sb.String()
}

func maximalSubpart(b []byte) int {
	c := b[0]
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case c >= 0xC2 && c <= 0xDF:
		need = 1
	case c == 0xE0:
		need, lo = 2, 0xA0
	case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
		need = 2
	case c == 0xED:
		need, hi = 2, 0x9F
	case c == 0xF0:
		need, lo = 3, 0x90
	case c >= 0xF1 && c <= 0xF3:
		need = 3
	case c == 0xF4:
		need, hi = 3, 0x8F
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(b); k++ {
		if b[n] < lo || b[n] > hi {
			break
		}
		lo, hi = 0x80, 0xBF
		n++
	}
	return n
}

// latin1 is Buffer.toString('latin1').
func latin1(b []byte) string {
	rs := make([]rune, len(b))
	for i, c := range b {
		rs[i] = rune(c)
	}
	return string(rs)
}

var httpClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}

type fetchResp struct {
	status int
	url    string
	body   []byte
}

func (r *fetchResp) ok() bool { return r.status >= 200 && r.status < 300 }

// fetch is a minimal global fetch(): follows redirects, honours HTTP(S)_PROXY / NO_PROXY.
func fetch(url string, headers map[string]string, timeout time.Duration) (*fetchResp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &fetchResp{resp.StatusCode, resp.Request.URL.String(), body}, nil
}

// localeStringZH is new Date().toLocaleString('zh-CN') in local time.
func localeStringZH(t time.Time) string {
	return fmt.Sprintf("%d/%d/%d %02d:%02d:%02d", t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
}

// isoStamp is new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19).
func isoStamp(t time.Time) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(t.UTC().Format("2006-01-02T15:04:05.000Z"))[:19]
}

// pool runs fn over items with n workers; results keep the item order.
func pool[T, R any](items []T, n int, fn func(T, int) R) []R {
	out := make([]R, len(items))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range next {
				out[k] = fn(items[k], k)
			}
		}()
	}
	for k := range items {
		next <- k
	}
	close(next)
	wg.Wait()
	return out
}

func warn(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

func info(format string, a ...any) { fmt.Printf(format+"\n", a...) }

// parseEnvFile reads KEY=value lines like the scripts' .env loader.
func parseEnvFile(f string) (map[string]string, error) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(data), "\n") {
		if k := strings.IndexByte(l, '='); k >= 0 {
			out[jsTrim(l[:k])] = jsTrim(l[k+1:])
		}
	}
	return out, nil
}

// getv reads a nested property of parsed JSON (o?.a?.b); missing values are undefined.
func getv(v any, keys ...string) any {
	for _, k := range keys {
		o, ok := v.(*Obj)
		if !ok || !o.Has(k) {
			return undefined
		}
		v = o.Get(k)
	}
	return v
}

func getStr(v any, keys ...string) string {
	s, _ := getv(v, keys...).(string)
	return s
}

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

// or is a || b.
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

func nullish(v any) bool { return v == nil || v == undefined }

// orNull is v ?? null for a value that should be dropped to null when missing.
func orNull(v any) any {
	if v == undefined {
		return nil
	}
	return v
}
