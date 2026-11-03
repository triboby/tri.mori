package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNoteFileName(t *testing.T) {
	now := time.Date(2026, 11, 3, 9, 5, 7, 0, time.Local)
	cases := map[string]string{
		"读书笔记: Go / 并发?": "20261103_读书笔记 Go 并发.md",
		"  #tag [[x]]  ": "20261103_tag x.md",
		"":               "20261103_090507.md",
		"../..":          "20261103_090507.md",
	}
	for in, want := range cases {
		if got := noteFileName(in, now); got != want {
			t.Errorf("noteFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := noteFileName(strings.Repeat("森", 200), now); len(got) > len("20261103_.md")+120 {
		t.Errorf("long title not truncated: %d bytes", len(got))
	}
}

func TestNoteMarkdown(t *testing.T) {
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.Local)
	got := noteMarkdown(inboxNote{Title: `A "quoted" title`, Content: "body\n\n", Source: "https://x.y/a: b", Tags: []string{"#go", " ", "读书"}}, now)
	want := "---\ntitle: \"A \\\"quoted\\\" title\"\ndate: 2026-11-03\nsource: \"https://x.y/a: b\"\ntags: [\"go\", \"读书\"]\n---\n\nbody\n"
	if got != want {
		t.Errorf("noteMarkdown =\n%s\nwant\n%s", got, want)
	}
}

func TestSplitNoteContent(t *testing.T) {
	long := strings.Repeat("森林 line\n", 900) + strings.Repeat("长", 3000) + `a\nb` + strings.Repeat("x", 5000)
	parts := splitNoteContent(long, 4000)
	if strings.Join(parts, "") != long {
		t.Fatal("parts do not join back to the input")
	}
	for i, p := range parts {
		if len(p) > 4000 || !utf8.ValidString(p) || (i < len(parts)-1 && strings.HasSuffix(p, `\`)) {
			t.Errorf("bad part %d: %d bytes, ends %q", i, len(p), p[len(p)-3:])
		}
	}
	if got := splitNoteContent("short", 4000); len(got) != 1 {
		t.Errorf("short content split into %d parts", len(got))
	}
}
