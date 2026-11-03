// Package jieba is a pure Go port of the segmentation used by @node-rs/jieba 2.0.4
// (jieba-rs 0.11.0): default-mode Cut and CutForSearch with the same dictionary and HMM model,
// so documents and queries segment exactly as they did under Node.
package jieba

import (
	_ "embed"
	"math"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed dict.txt
var dictText string

//go:embed hmm.model
var hmmText string

const minFloat = -3.14e100

type edge struct {
	end int
	lf  float64
}

type route struct {
	prob   float64
	end    int
	inDict bool
}

// Jieba holds the dictionary as a sorted word list (substrings of the embedded file) and log frequencies.
type Jieba struct {
	words    []string
	logFreq  []float64
	logTotal float64
	initP    [4]float64
	transP   [4][4]float64
	emitP    map[rune][4]float64
}

var (
	once     sync.Once
	defJieba *Jieba
)

// Default returns the shared instance, loading the dictionary on first use.
func Default() *Jieba {
	once.Do(func() {
		defJieba = load()
		debug.FreeOSMemory()
	})
	return defJieba
}

func load() *Jieba {
	var words []string
	var freqs []int
	for text := dictText; len(text) > 0; {
		line := text
		if k := strings.IndexByte(text, '\n'); k >= 0 {
			line, text = text[:k], text[k+1:]
		} else {
			text = ""
		}
		word, rest, _ := strings.Cut(strings.TrimLeft(line, " \t\r"), " ")
		word = strings.TrimRight(word, "\t\r")
		if word == "" {
			continue
		}
		freqStr, _, _ := strings.Cut(strings.TrimLeft(rest, " "), " ")
		freq, _ := strconv.Atoi(strings.TrimRight(freqStr, "\r"))
		words = append(words, word)
		freqs = append(freqs, freq)
	}
	order := make([]int32, len(words))
	for i := range order {
		order[i] = int32(i)
	}
	sort.SliceStable(order, func(a, b int) bool { return words[order[a]] < words[order[b]] })
	j := &Jieba{words: make([]string, 0, len(words)), logFreq: make([]float64, 0, len(words))}
	total := 0
	for i, k := range order {
		if i+1 < len(order) && words[order[i+1]] == words[k] {
			continue
		}
		j.words = append(j.words, words[k])
		j.logFreq = append(j.logFreq, math.Log(float64(freqs[k])))
		total += freqs[k]
	}
	j.logTotal = math.Log(float64(total))
	j.loadHMM()
	return j
}

func (j *Jieba) loadHMM() {
	var lines []string
	for _, l := range strings.Split(hmmText, "\n") {
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "#") {
		i++
	}
	parseRow := func(s string) (out [4]float64) {
		for k, v := range strings.Fields(s) {
			out[k], _ = strconv.ParseFloat(v, 64)
		}
		return
	}
	j.initP = parseRow(lines[i])
	i++
	for i < len(lines) && strings.HasPrefix(lines[i], "#") {
		i++
	}
	for r := 0; i < len(lines) && !strings.HasPrefix(lines[i], "#"); i++ {
		if r < 4 {
			j.transP[r] = parseRow(lines[i])
		}
		r++
	}
	i++
	j.emitP = map[rune][4]float64{}
	state := 0
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") || lines[i] == "" {
			continue
		}
		for _, wp := range strings.Split(lines[i], ",") {
			k := strings.IndexByte(wp, ':')
			if k < 0 {
				continue
			}
			ch, _ := utf8.DecodeRuneInString(wp[:k])
			p, _ := strconv.ParseFloat(wp[k+1:], 64)
			if ch < 0x4E00 || ch > 0x9FD5 {
				continue
			}
			row, ok := j.emitP[ch]
			if !ok {
				row = [4]float64{minFloat, minFloat, minFloat, minFloat}
			}
			row[state] = p
			j.emitP[ch] = row
		}
		state++
	}
}

func (j *Jieba) lookup(w string) (int, bool) {
	k := sort.SearchStrings(j.words, w)
	return k, k < len(j.words) && j.words[k] == w
}

func (j *Jieba) hasPrefix(p string) bool {
	k := sort.SearchStrings(j.words, p)
	return k < len(j.words) && strings.HasPrefix(j.words[k], p)
}

func isCJKExt(c rune) bool {
	return (c >= 0x3400 && c <= 0x4DBF) || (c >= 0xF900 && c <= 0xFAFF) || (c >= 0x20000 && c <= 0x2A6DF) ||
		(c >= 0x2A700 && c <= 0x2B73F) || (c >= 0x2B740 && c <= 0x2B81F) || (c >= 0x2B820 && c <= 0x2CEAF) ||
		(c >= 0x2CEB0 && c <= 0x2EBEF) || (c >= 0x2F800 && c <= 0x2FA1F)
}

func isASCIIAlnum(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isHanDefault(c rune) bool {
	if c >= 0x4E00 && c <= 0x9FFF {
		return true
	}
	if c < 0x80 {
		return isASCIIAlnum(c) || strings.ContainsRune("+#&._%-", c)
	}
	return isCJKExt(c)
}

func isHMMHan(c rune) bool { return c >= 0x4E00 && c <= 0x9FD5 }

// block is one run of is_han_default characters with its dictionary DAG.
type block struct {
	chars []rune
	dag   [][]edge
}

func (b *block) wordAt(x, y int) bool {
	for _, e := range b.dag[x] {
		if e.end >= y {
			return e.end == y
		}
	}
	return false
}

func (b *block) str(x, y int) string { return string(b.chars[x:y]) }

type token struct {
	word string
	blk  *block
	x    int
}

func (j *Jieba) buildDAG(chars []rune) [][]edge {
	dag := make([][]edge, len(chars))
	for pos := range chars {
		for end := pos + 1; end <= len(chars); end++ {
			w := string(chars[pos:end])
			k, ok := j.lookup(w)
			if ok {
				dag[pos] = append(dag[pos], edge{end, j.logFreq[k]})
			}
			if !j.hasPrefix(w) {
				break
			}
		}
	}
	return dag
}

func (j *Jieba) calc(b *block) []route {
	n := len(b.chars)
	r := make([]route, n+1)
	r[n] = route{0, n, false}
	log1 := 0.0 - j.logTotal
	for idx := n - 1; idx >= 0; idx-- {
		var best route
		have := false
		for _, e := range b.dag[idx] {
			prob := e.lf - j.logTotal + r[e.end].prob
			if !have || prob > best.prob || (prob == best.prob && e.end > best.end) {
				best, have = route{prob, e.end, true}, true
			}
		}
		if !have {
			best = route{log1 + r[idx+1].prob, idx + 1, false}
		}
		r[idx] = best
	}
	return r
}

func (j *Jieba) cutRoute(b *block, hmm bool, emit func(token)) {
	rt := j.calc(b)
	n := len(b.chars)
	left := -1
	run := func(x, y int) {
		if !hmm {
			emit(token{b.str(x, y), b, x})
			return
		}
		if y-x == 1 {
			emit(token{b.str(x, y), b, x})
			return
		}
		if !b.wordAt(x, y) {
			j.hmmCut(b, x, y, emit)
			return
		}
		for k := x; k < y; k = rt[k].end {
			emit(token{b.str(k, rt[k].end), b, k})
		}
	}
	for x := 0; x < n; {
		y := rt[x].end
		if y-x == 1 && (hmm || isASCIIAlnum(b.chars[x])) {
			if left < 0 {
				left = x
			}
		} else {
			if left >= 0 {
				run(left, x)
				left = -1
			}
			emit(token{b.str(x, y), b, x})
		}
		x = y
	}
	if left >= 0 {
		run(left, n)
	}
}

func (j *Jieba) hmmCut(b *block, x, y int, emit func(token)) {
	pos := x
	out := func(w string) {
		emit(token{w, b, pos})
		pos += utf8.RuneCountInString(w)
	}
	chars := b.chars[x:y]
	for i := 0; i < len(chars); {
		m := isHMMHan(chars[i])
		k := i + 1
		for k < len(chars) && isHMMHan(chars[k]) == m {
			k++
		}
		if m {
			if k-i > 1 {
				j.viterbiCut(chars[i:k], out)
			} else {
				out(string(chars[i:k]))
			}
		} else {
			for _, w := range skipSplit(string(chars[i:k])) {
				if w != "" {
					out(w)
				}
			}
		}
		i = k
	}
}

var allowedPrev = [4][2]int{{1, 3}, {0, 2}, {0, 2}, {1, 3}}

func (j *Jieba) emit(c rune) [4]float64 {
	if p, ok := j.emitP[c]; ok {
		return p
	}
	return [4]float64{minFloat, minFloat, minFloat, minFloat}
}

func (j *Jieba) viterbiCut(chars []rune, out func(string)) {
	C := len(chars)
	prev := make([][4]int, C)
	var pv [4]float64
	e0 := j.emit(chars[0])
	for y := 0; y < 4; y++ {
		pv[y] = j.initP[y] + e0[y]
	}
	for t := 1; t < C; t++ {
		em := j.emit(chars[t])
		var cv [4]float64
		for y := 0; y < 4; y++ {
			y0, y1 := allowedPrev[y][0], allowedPrev[y][1]
			p0 := pv[y0] + j.transP[y0][y] + em[y]
			p1 := pv[y1] + j.transP[y1][y] + em[y]
			if p0 > p1 {
				cv[y], prev[t][y] = p0, y0
			} else {
				cv[y], prev[t][y] = p1, y1
			}
		}
		pv = cv
	}
	path := make([]int, C)
	cur := 3
	if pv[1] > pv[3] {
		cur = 1
	}
	path[C-1] = cur
	for t := C - 1; t >= 1; t-- {
		cur = prev[t][cur]
		path[t-1] = cur
	}
	begin, next := 0, 0
	for i, s := range path {
		switch s {
		case 0:
			begin = i
		case 1:
			out(string(chars[begin : i+1]))
			next = i + 1
		case 3:
			out(string(chars[i : i+1]))
			next = i + 1
		}
	}
	if next < C {
		out(string(chars[next:]))
	}
}

func isConnector(b byte) bool { return b == '.' || b == '_' || b == '-' }

func isAlnumByte(b byte) bool { return b < 0x80 && isASCIIAlnum(rune(b)) }

func skipSplit(text string) []string {
	var out []string
	pos := 0
	for pos < len(text) {
		cursor := pos
		for cursor < len(text) && !isAlnumByte(text[cursor]) {
			cursor++
		}
		if cursor == len(text) {
			out = append(out, text[pos:])
			break
		}
		end := cursor
		for {
			for end < len(text) && isAlnumByte(text[end]) {
				end++
			}
			if end+1 < len(text) && isConnector(text[end]) && isAlnumByte(text[end+1]) {
				end++
			} else {
				break
			}
		}
		if end < len(text) && text[end] == '%' {
			end++
		}
		if cursor > pos {
			out = append(out, text[pos:cursor])
		}
		out = append(out, text[cursor:end])
		pos = end
	}
	return out
}

func (j *Jieba) cutEach(s string, hmm bool, emit func(token)) {
	chars := []rune(s)
	for i := 0; i < len(chars); {
		m := isHanDefault(chars[i])
		k := i + 1
		for k < len(chars) && isHanDefault(chars[k]) == m {
			k++
		}
		if m {
			b := &block{chars: chars[i:k]}
			b.dag = j.buildDAG(b.chars)
			j.cutRoute(b, hmm, emit)
		} else {
			for x := i; x < k; {
				if chars[x] == '\r' && x+1 < k && chars[x+1] == '\n' {
					emit(token{"\r\n", nil, 0})
					x += 2
				} else {
					emit(token{string(chars[x]), nil, 0})
					x++
				}
			}
		}
		i = k
	}
}

// Cut segments s in default (accurate) mode.
func (j *Jieba) Cut(s string, hmm bool) []string {
	var out []string
	j.cutEach(s, hmm, func(t token) { out = append(out, t.word) })
	return out
}

// CutForSearch segments s in search mode: each word is preceded by its dictionary 2- and 3-grams.
func (j *Jieba) CutForSearch(s string, hmm bool) []string {
	var out []string
	j.cutEach(s, hmm, func(t token) {
		w := t.word
		if strings.IndexFunc(w, func(r rune) bool { return r == '.' || r == '_' || r == '-' }) >= 0 {
			for _, part := range strings.FieldsFunc(w, func(r rune) bool { return r == '.' || r == '_' || r == '-' }) {
				if len(part) >= 2 && strings.IndexFunc(part, func(r rune) bool { return r < 0x80 && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) }) >= 0 {
					out = append(out, part)
				}
			}
		}
		if t.blk == nil {
			out = append(out, w)
			return
		}
		cc := utf8.RuneCountInString(w)
		gram := func(i, l int) {
			if t.blk.wordAt(t.x+i, t.x+i+l) {
				out = append(out, t.blk.str(t.x+i, t.x+i+l))
			}
		}
		if cc > 2 {
			for i := 0; i < cc-1; i++ {
				gram(i, 2)
			}
		}
		if cc > 3 {
			for i := 0; i < cc-2; i++ {
				gram(i, 3)
			}
		}
		out = append(out, w)
	})
	return out
}
