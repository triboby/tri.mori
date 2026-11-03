package main

import (
	"strings"
	"testing"
	"time"
)

func TestNoteFileName(t *testing.T) {
	now := time.Date(2026, 11, 3, 9, 5, 7, 0, time.Local)
	cases := map[string]string{
		"读书笔记: Go / 并发?": "20261103_读书笔记 Go 并发.md",
		"  #tag [[x]]  ":    "20261103_tag x.md",
		"":                  "20261103_090507.md",
		"../..":             "20261103_090507.md",
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
