package main

// Hybrid search through the qmd CLI (one process per search, serialized), with queries built like server.mjs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"movieshelf/jieba"
)

func hasCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x3040 && r <= 0x30FF) || (r >= 0x3400 && r <= 0x9FFF) || (r >= 0xAC00 && r <= 0xD7AF) {
			return true
		}
	}
	return false
}

func isLetterOrNumber(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

func hasLetterOrNumber(s string) bool {
	for _, r := range s {
		if isLetterOrNumber(r) {
			return true
		}
	}
	return false
}

// segment is lib/seg.mjs segment(): jieba with HMM, punctuation and whitespace dropped.
func segment(text string, search bool) []string {
	j := jieba.Default()
	var words []string
	if search {
		words = j.CutForSearch(text, true)
	} else {
		words = j.Cut(text, true)
	}
	out := words[:0:0]
	for _, w := range words {
		w = jsTrim(w)
		if hasLetterOrNumber(w) {
			out = append(out, w)
		}
	}
	return out
}

func buildQueries(q string) []*Obj {
	var words []string
	if hasCJK(q) {
		words = segment(q, false)
	} else {
		words = jsSplitSpace(q)
	}
	lex := strings.Join(words, " ")
	var qs []*Obj
	if lex != "" {
		qs = append(qs, newObj("type", "lex", "query", lex))
	}
	return append(qs, newObj("type", "vec", "query", q))
}

// qmdSpaces collapses whitespace runs like t.replace(/\s+/g, ' '), keeping leading and trailing single spaces.
func qmdSpaces(t string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range t {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

type qmdResult struct {
	File  string  `json:"file"`
	Score float64 `json:"score"`
}

func (s *server) qmdQuery(queries []*Obj, collections []string, limit int) ([]qmdResult, error) {
	lines := make([]string, len(queries))
	for i, q := range queries {
		t := qmdSpaces(jsString(q.Get("query")))
		if strings.Count(t, `"`)%2 == 1 {
			t = strings.ReplaceAll(t, `"`, "")
		}
		lines[i] = jsString(q.Get("type")) + ": " + t
	}
	args := []string{s.cfg.qmdCLI, "--index", s.cfg.qmdIndex, "query", strings.Join(lines, "\n"), "--no-rerank", "--format", "json", "-n", fmt.Sprint(limit)}
	for _, c := range collections {
		args = append(args, "-c", c)
	}
	s.qmdMu.Lock()
	defer s.qmdMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cfg.node, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		ls := strings.Split(strings.TrimSpace(msg), "\n")
		return nil, errors.New("qmd query failed: " + ls[len(ls)-1])
	}
	out := stdout.Bytes()
	if len(out) == 0 {
		out = []byte("[]")
	}
	var res []qmdResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, errors.New("qmd query returned invalid JSON: " + err.Error())
	}
	return res, nil
}

var (
	reQueryStrip = regexp.MustCompile(`\?.*$`)
	reQmdKind    = regexp.MustCompile(`qmd://(\w+)/`)
)

func (s *server) search(q string, collections []string, limit int) (*Obj, error) {
	t0 := time.Now()
	queries := buildQueries(q)
	results, err := s.qmdQuery(queries, collections, limit)
	if err != nil {
		return nil, err
	}
	ms, err := s.movies.load()
	if err != nil {
		return nil, err
	}
	bs, err := s.books.load()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	hits := []any{}
	for _, r := range results {
		ref := reQueryStrip.ReplaceAllString(r.File, "")
		var kind string
		if len(collections) == 1 {
			kind = collections[0]
		} else if m := reQmdKind.FindStringSubmatch(ref); m != nil {
			kind = m[1]
		}
		base := strings.TrimSuffix(path.Base(ref), ".md")
		if ref == "" {
			base = ""
		}
		var id any = base
		if kind == "movies" {
			id = strToNumber(base)
		}
		if !truthy(id) || seen[kind+jsString(id)] {
			continue
		}
		byID := bs.byID
		if kind == "movies" {
			byID = ms.byID
		}
		if _, ok := byID[mapKey(id)]; !ok {
			continue
		}
		seen[kind+jsString(id)] = true
		var k any = kind
		if kind == "" {
			k = undefined
		}
		hits = append(hits, newObj("kind", k, "id", id, "score", jsRound(r.Score*1000)/1000))
	}
	qs := make([]any, len(queries))
	for i, x := range queries {
		qs[i] = x
	}
	elapsed := float64(time.Since(t0).Nanoseconds()) / 1e6
	return newObj("q", q, "queries", qs, "ms", jsRound(elapsed), "hits", hits), nil
}
