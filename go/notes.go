package main

// POST /api/notes/inbox: saves a Markdown note into the Obsidian vault's inbox folder through Obsidian's own CLI
// (`obsidian create`), so the running app indexes it and resolves name clashes. NOTES_TOKEN, when set, is required.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type notesConfig struct {
	token, vault, inbox, bin string
}

type inboxNote struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Source  string   `json:"source"`
	Tags    []string `json:"tags"`
}

const obsidianAppCLI = "/Applications/Obsidian.app/Contents/MacOS/obsidian"

var (
	notesMu       sync.Mutex
	reBadNameRune = regexp.MustCompile(`[\\/:*?"<>|#^\[\]\x00-\x1f]+`)
	reNoteSpaces  = regexp.MustCompile(`\s+`)
	reCreated     = regexp.MustCompile(`(?m)^Created: (.+)$`)
)

// loadNotesConfig reads NOTES_TOKEN, OBSIDIAN_VAULT, OBSIDIAN_INBOX and OBSIDIAN_BIN from the environment, then .env.
func loadNotesConfig(root string) notesConfig {
	file, _ := parseEnvFile(filepath.Join(root, ".env"))
	get := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		if v := file[k]; v != "" {
			return v
		}
		return def
	}
	bin := "obsidian"
	if _, err := os.Stat(obsidianAppCLI); err == nil {
		bin = obsidianAppCLI
	}
	return notesConfig{
		token: get("NOTES_TOKEN", ""),
		vault: get("OBSIDIAN_VAULT", ""),
		inbox: strings.Trim(get("OBSIDIAN_INBOX", "00-Inbox"), "/"),
		bin:   get("OBSIDIAN_BIN", bin),
	}
}

func bearerToken(r *http.Request) string {
	if t := r.Header.Get("X-Mori-Token"); t != "" {
		return t
	}
	a := r.Header.Get("Authorization")
	if len(a) > 7 && strings.EqualFold(a[:7], "Bearer ") {
		return strings.TrimSpace(a[7:])
	}
	return ""
}

// noteFileName turns a title into "YYYYMMDD_<title>.md", matching the inbox's existing naming.
func noteFileName(title string, now time.Time) string {
	name := strings.Trim(reNoteSpaces.ReplaceAllString(reBadNameRune.ReplaceAllString(title, " "), " "), " .")
	for len(name) > 120 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" {
		return now.Format("20060102_150405") + ".md"
	}
	return now.Format("20060102") + "_" + name + ".md"
}

func yamlString(s string) string { return strconv.Quote(s) }

func noteMarkdown(n inboxNote, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	if n.Title != "" {
		b.WriteString("title: " + yamlString(n.Title) + "\n")
	}
	b.WriteString("date: " + now.Format("2006-01-02") + "\n")
	if n.Source != "" {
		b.WriteString("source: " + yamlString(n.Source) + "\n")
	}
	tags := make([]string, 0, len(n.Tags))
	for _, t := range n.Tags {
		if t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")); t != "" {
			tags = append(tags, yamlString(t))
		}
	}
	b.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(n.Content, "\n") + "\n")
	return b.String()
}

func readInboxNote(r *http.Request) (inboxNote, error) {
	var n inboxNote
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil {
		return n, err
	}
	if len(data) > 1<<20 {
		return n, errBodyTooLarge
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(data, &n); err != nil {
			return n, errors.New("invalid JSON")
		}
	} else {
		q := r.URL.Query()
		n = inboxNote{Title: q.Get("title"), Content: string(data), Source: q.Get("source")}
		if t := q.Get("tags"); t != "" {
			n.Tags = strings.Split(t, ",")
		}
	}
	n.Title = strings.TrimSpace(n.Title)
	n.Source = strings.TrimSpace(strings.ReplaceAll(n.Source, "\n", " "))
	if strings.TrimSpace(n.Content) == "" {
		return n, errors.New("content is required")
	}
	return n, nil
}

func (s *server) apiNotesInbox(w http.ResponseWriter, r *http.Request) error {
	nc := s.notes
	if r.Method != "POST" {
		return send(w, 405, stringify(newObj("error", "method not allowed")), "application/json; charset=utf-8", map[string]string{"Allow": "POST"})
	}
	if nc.vault == "" {
		return sendJSON(w, 503, newObj("error", "notes inbox is not configured (OBSIDIAN_VAULT)"))
	}
	if nc.token != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(nc.token)) != 1 {
		return sendJSON(w, 401, newObj("error", "unauthorized"))
	}
	n, err := readInboxNote(r)
	if err != nil {
		code := 400
		if errors.Is(err, errBodyTooLarge) {
			code = 413
		}
		return sendJSON(w, code, newObj("error", err.Error()))
	}
	now := time.Now()
	rel := nc.inbox + "/" + noteFileName(n.Title, now)
	if nc.inbox == "" {
		rel = noteFileName(n.Title, now)
	}

	notesMu.Lock()
	defer notesMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	chunks := splitNoteContent(noteMarkdown(n, now), noteChunkBytes)
	text, err := obsidianCLI(ctx, nc, "create", "path="+rel, "content="+chunks[0])
	m := reCreated.FindStringSubmatch(text)
	if err != nil || m == nil {
		log.Printf("%s notes inbox failed: %s", nowISO(), text)
		return sendJSON(w, 502, newObj("error", "obsidian create failed", "detail", text))
	}
	path := strings.TrimSpace(m[1])
	for i, c := range chunks[1:] {
		text, err := obsidianCLI(ctx, nc, "append", "path="+path, "inline", "content="+c)
		if err != nil || !strings.HasPrefix(text, "Appended to:") {
			log.Printf("%s notes inbox %s incomplete at part %d/%d: %s", nowISO(), path, i+2, len(chunks), text)
			return sendJSON(w, 502, newObj("error", "obsidian append failed; note is incomplete", "path", path, "detail", text))
		}
	}
	log.Printf("%s notes inbox %s", nowISO(), path)
	return sendJSON(w, 201, newObj("ok", true, "path", path))
}

// noteChunkBytes keeps each CLI call well below the ~8KB size above which `obsidian` still writes but prints nothing.
const noteChunkBytes = 4000

func obsidianCLI(ctx context.Context, nc notesConfig, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, nc.bin, append([]string{"vault=" + nc.vault}, args...)...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text == "" && err != nil {
		text = err.Error()
	}
	return text, err
}

// splitNoteContent cuts s into pieces of at most max bytes, preferring line ends and never splitting a rune
// or separating a backslash from the character after it (the CLI expands \n and \t).
func splitNoteContent(s string, max int) []string {
	var out []string
	for len(s) > max {
		cut := strings.LastIndexByte(s[:max], '\n') + 1
		if cut < max/2 {
			cut = max
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
		}
		for cut > 1 && s[cut-1] == '\\' {
			cut--
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return append(out, s)
}
