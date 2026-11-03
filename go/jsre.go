package main

// JavaScript RegExp semantics on top of regexp2's ECMAScript mode: \s and \S get JS's Unicode whitespace set,
// \p{Script=Han} becomes explicit ranges, and the helpers mirror test / match / matchAll / replace / split.

import (
	"strings"

	"github.com/dlclark/regexp2"
)

const jsSpaceSet = "\\t\\n\\v\\f\\r \\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000\\ufeff"

const hanSet = "\\u2e80-\\u2e99\\u2e9b-\\u2ef3\\u2f00-\\u2fd5\\u3005\\u3007\\u3021-\\u3029\\u3038-\\u303b\\u3400-\\u4dbf\\u4e00-\\u9fff\\uf900-\\ufa6d\\ufa70-\\ufad9"

// jsRe is a compiled JS regular expression literal /pattern/flags.
type jsRe struct {
	re     *regexp2.Regexp
	global bool
}

func translatePattern(p string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '\\' && i+1 < len(p) {
			n := p[i+1]
			switch {
			case n == 's':
				if inClass {
					b.WriteString(jsSpaceSet)
				} else {
					b.WriteString("[" + jsSpaceSet + "]")
				}
				i++
				continue
			case n == 'S' && !inClass:
				b.WriteString("[^" + jsSpaceSet + "]")
				i++
				continue
			case strings.HasPrefix(p[i:], `\p{Script=Han}`):
				if inClass {
					b.WriteString(hanSet)
				} else {
					b.WriteString("[" + hanSet + "]")
				}
				i += len(`\p{Script=Han}`) - 1
				continue
			}
			b.WriteByte(c)
			b.WriteByte(n)
			i++
			continue
		}
		if c == '[' && !inClass {
			inClass = true
		} else if c == ']' && inClass {
			inClass = false
		}
		b.WriteByte(c)
	}
	return b.String()
}

// re compiles a JS regex; flags may contain g, i and u.
func re(pattern, flags string) *jsRe {
	opts := regexp2.RegexOptions(regexp2.ECMAScript)
	if strings.Contains(flags, "i") {
		opts |= regexp2.IgnoreCase
	}
	return &jsRe{regexp2.MustCompile(translatePattern(pattern), opts), strings.Contains(flags, "g")}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func (r *jsRe) Test(s string) bool { return must(r.re.MatchString(s)) }

// Match is s.match(re) without the g flag: groups[0] is the whole match; ok[i] tells whether group i took part.
func (r *jsRe) Match(s string) (groups []string, ok []bool) {
	m := must(r.re.FindStringMatch(s))
	if m == nil {
		return nil, nil
	}
	return groupsOf(m)
}

func groupsOf(m *regexp2.Match) ([]string, []bool) {
	gs := m.Groups()
	groups, ok := make([]string, len(gs)), make([]bool, len(gs))
	for i, g := range gs {
		if len(g.Captures) > 0 {
			groups[i], ok[i] = g.String(), true
		}
	}
	return groups, ok
}

// Group is m?.[i] || ” for a non-global match.
func (r *jsRe) Group(s string, i int) string {
	g, _ := r.Match(s)
	if g == nil || i >= len(g) {
		return ""
	}
	return g[i]
}

type reMatch struct {
	groups []string
	ok     []bool
	index  int // rune offset of the match
	start  int // byte offsets of the match
	end    int
}

// MatchAll is [...s.matchAll(re)] / s.match(re) with the g flag.
func (r *jsRe) MatchAll(s string) []reMatch {
	var out []reMatch
	runes := []rune(s)
	offsets := runeByteOffsets(s)
	m := must(r.re.FindRunesMatch(runes))
	for m != nil {
		g, ok := groupsOf(m)
		out = append(out, reMatch{g, ok, m.Index, offsets[m.Index], offsets[m.Index+m.Length]})
		m = must(r.re.FindNextMatch(m))
	}
	return out
}

func runeByteOffsets(s string) []int {
	offs := make([]int, 0, len(s)+1)
	for i := range s {
		offs = append(offs, i)
	}
	return append(offs, len(s))
}

// Replace is s.replace(re, repl) where repl may use $1…$9 and $&; the g flag decides first-only or all.
func (r *jsRe) Replace(s, repl string) string {
	return r.ReplaceFunc(s, func(m reMatch) string { return expandRepl(repl, m) })
}

func expandRepl(repl string, m reMatch) string {
	if !strings.Contains(repl, "$") {
		return repl
	}
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c == '$' && i+1 < len(repl) {
			n := repl[i+1]
			switch {
			case n == '$':
				b.WriteByte('$')
				i++
				continue
			case n == '&':
				b.WriteString(m.groups[0])
				i++
				continue
			case n >= '1' && n <= '9':
				k := int(n - '0')
				if k < len(m.groups) {
					b.WriteString(m.groups[k])
					i++
					continue
				}
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ReplaceFunc is s.replace(re, fn).
func (r *jsRe) ReplaceFunc(s string, fn func(reMatch) string) string {
	var ms []reMatch
	if r.global {
		ms = r.MatchAll(s)
	} else if all := r.MatchAll(s); len(all) > 0 {
		ms = all[:1]
	}
	if len(ms) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range ms {
		b.WriteString(s[last:m.start])
		b.WriteString(fn(m))
		last = m.end
	}
	b.WriteString(s[last:])
	return b.String()
}

// Split is s.split(re) for patterns without capture groups.
func (r *jsRe) Split(s string) []string {
	if s == "" {
		if r.Test("") {
			return []string{}
		}
		return []string{""}
	}
	var out []string
	last := 0
	for _, m := range r.MatchAll(s) {
		if m.end == m.start && (m.start == 0 || m.start == len(s)) {
			continue
		}
		if m.end == m.start && m.start == last {
			continue
		}
		out = append(out, s[last:m.start])
		last = m.end
	}
	return append(out, s[last:])
}
