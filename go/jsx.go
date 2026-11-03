package main

// JavaScript-compatible primitives (JSON.parse/stringify, String(), URL handling) so responses match server.mjs byte for byte.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Obj is a JSON object that keeps key order, as JS objects do.
type Obj struct {
	Keys []string
	Vals []any
}

func (o *Obj) Get(k string) any {
	for i, key := range o.Keys {
		if key == k {
			return o.Vals[i]
		}
	}
	return nil
}

func (o *Obj) Has(k string) bool {
	for _, key := range o.Keys {
		if key == k {
			return true
		}
	}
	return false
}

func (o *Obj) Set(k string, v any) {
	for i, key := range o.Keys {
		if key == k {
			o.Vals[i] = v
			return
		}
	}
	o.Keys = append(o.Keys, k)
	o.Vals = append(o.Vals, v)
}

func (o *Obj) Clone() *Obj {
	return &Obj{append([]string(nil), o.Keys...), append([]any(nil), o.Vals...)}
}

func newObj(kv ...any) *Obj {
	o := &Obj{}
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// undefined marks a missing value, which JSON.stringify drops from objects.
type undefinedT struct{}

var undefined = undefinedT{}

// parseJSON decodes like JSON.parse: numbers are float64, objects keep key order, duplicate keys keep the last value.
func parseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("Unexpected non-whitespace character after JSON")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := t.(type) {
	case json.Delim:
		if t == '{' {
			o := &Obj{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(kt.(string), v)
			}
			_, err := dec.Token()
			return o, err
		}
		if t == '[' {
			a := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err := dec.Token()
			return a, err
		}
		return nil, errors.New("unexpected delimiter")
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, err
		}
		return f, nil
	default:
		return t, nil
	}
}

// stringify encodes like JSON.stringify (no indentation).
func stringify(v any) []byte {
	var b bytes.Buffer
	writeJSON(&b, v)
	return b.Bytes()
}

func writeJSON(b *bytes.Buffer, v any) {
	switch v := v.(type) {
	case nil, undefinedT:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(jsNum(v))
		}
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case string:
		writeJSONString(b, v)
	case []any:
		b.WriteByte('[')
		for i, x := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if _, u := x.(undefinedT); u {
				x = nil
			}
			writeJSON(b, x)
		}
		b.WriteByte(']')
	case *Obj:
		b.WriteByte('{')
		n := 0
		for i, k := range v.Keys {
			if _, u := v.Vals[i].(undefinedT); u {
				continue
			}
			if n > 0 {
				b.WriteByte(',')
			}
			n++
			writeJSONString(b, k)
			b.WriteByte(':')
			writeJSON(b, v.Vals[i])
		}
		b.WriteByte('}')
	case json.RawMessage:
		b.Write(v)
	default:
		panic(fmt.Sprintf("stringify: unsupported %T", v))
	}
}

func writeJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// jsNum formats a float64 like Number.prototype.toString().
func jsNum(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k, n := len(digits), x+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	es := "+"
	if n-1 < 0 {
		es = "-"
	}
	ev := n - 1
	if ev < 0 {
		ev = -ev
	}
	if k == 1 {
		return sign + digits + "e" + es + strconv.Itoa(ev)
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + es + strconv.Itoa(ev)
}

// jsString is String(v).
func jsString(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case undefinedT:
		return "undefined"
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return jsNum(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			switch x.(type) {
			case nil, undefinedT:
			default:
				parts[i] = jsString(x)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// strOr is String(v ?? ”).
func strOr(v any) string {
	switch v.(type) {
	case nil, undefinedT:
		return ""
	}
	return jsString(v)
}

func truthy(v any) bool {
	switch v := v.(type) {
	case nil, undefinedT:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case int:
		return v != 0
	case int64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}

// toNumber is the unary + operator.
func toNumber(v any) float64 {
	switch v := v.(type) {
	case nil:
		return 0
	case undefinedT:
		return math.NaN()
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		return strToNumber(v)
	case []any:
		return strToNumber(jsString(v))
	}
	return math.NaN()
}

func strToNumber(s string) float64 {
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' && strings.ContainsRune("xXoObB", rune(s[1])) {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		n, ok := new(big.Int).SetString(s[2:], base)
		if !ok {
			return math.NaN()
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789+-.eE", c) {
			return math.NaN()
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return f
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsSplitSpace is s.split(/\s+/).filter(Boolean).
func jsSplitSpace(s string) []string { return strings.FieldsFunc(s, isJSSpace) }

// utf16Slice is s.slice(0, n) on UTF-16 code units; a split surrogate pair becomes U+FFFD as Node would encode it.
func utf16Slice(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	u = u[:n]
	if last := u[n-1]; last >= 0xD800 && last < 0xDC00 {
		return string(utf16.Decode(u[:n-1])) + "�"
	}
	return string(utf16.Decode(u))
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// isoString is new Date(ms).toISOString().
func isoString(ms float64) (string, error) {
	if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
		return "", errors.New("Invalid time value")
	}
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z"), nil
}

func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// toFixed1 is x.toFixed(1): the exact binary value rounded half up to one decimal.
func toFixed1(x float64) string {
	v := new(big.Float).SetPrec(2000).SetFloat64(x)
	v.Mul(v, big.NewFloat(10))
	fl, _ := v.Int(nil)
	frac := new(big.Float).SetPrec(2000).Sub(v, new(big.Float).SetInt(fl))
	if frac.Cmp(big.NewFloat(0.5)) >= 0 {
		fl.Add(fl, big.NewInt(1))
	}
	s := fl.String()
	if len(s) < 2 {
		s = "0" + s
	}
	return s[:len(s)-1] + "." + s[len(s)-1:]
}

// jsRound is Math.round.
func jsRound(x float64) float64 {
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x80 && (isAlnum(c) || strings.IndexByte("-_.!~*'()", c) >= 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

var errURIMalformed = errors.New("URI malformed")

func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", errURIMalformed
		}
		h, ok1 := unhex(s[i+1])
		l, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", errURIMalformed
		}
		b = append(b, h<<4|l)
		i += 2
	}
	if !utf8.Valid(b) {
		return "", errURIMalformed
	}
	return string(b), nil
}

// percentDecodeLoose is the WHATWG percent-decode used by URLSearchParams: bad escapes stay literal.
func percentDecodeLoose(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			h, ok1 := unhex(s[i+1])
			l, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b = append(b, h<<4|l)
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return strings.ToValidUTF8(string(b), "�")
}

type param struct{ k, v string }

// parseQuery parses an (already URL-encoded) query string like URLSearchParams.
func parseQuery(q string) []param {
	var out []param
	for _, part := range strings.Split(q, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		out = append(out, param{percentDecodeLoose(strings.ReplaceAll(k, "+", " ")), percentDecodeLoose(strings.ReplaceAll(v, "+", " "))})
	}
	return out
}

func getParam(ps []param, k string) (string, bool) {
	for _, p := range ps {
		if p.k == k {
			return p.v, true
		}
	}
	return "", false
}

// setParam is URLSearchParams.set: replaces the first k, drops the rest, or appends.
func setParam(ps []param, k, v string) []param {
	out := make([]param, 0, len(ps)+1)
	done := false
	for _, p := range ps {
		if p.k == k {
			if !done {
				out = append(out, param{k, v})
				done = true
			}
			continue
		}
		out = append(out, p)
	}
	if !done {
		out = append(out, param{k, v})
	}
	return out
}

func formEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ':
			b.WriteByte('+')
		case c < 0x80 && (isAlnum(c) || strings.IndexByte("*-._", c) >= 0):
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func encodeParams(ps []param) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = formEncode(p.k) + "=" + formEncode(p.v)
	}
	return strings.Join(parts, "&")
}

func pctEncodeSet(s string, set string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7E || strings.IndexByte(set, c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// whatwgURL splits a request target into the pathname and search that new URL(req.url, 'http://x') would report.
func whatwgURL(target string) (pathname, search, query string) {
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	p, q, hasQ := strings.Cut(target, "?")
	p = strings.ReplaceAll(p, `\`, "/")
	segs := strings.Split(p, "/")
	var out []string
	for i, s := range segs {
		if i == 0 {
			continue
		}
		ls := strings.ToLower(s)
		last := i == len(segs)-1
		switch ls {
		case ".", "%2e":
			if last {
				out = append(out, "")
			}
		case "..", ".%2e", "%2e.", "%2e%2e":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, pctEncodeSet(s, " \"#<>?`{}"))
		}
	}
	pathname = "/" + strings.Join(out, "/")
	if hasQ {
		query = pctEncodeSet(q, " \"#<>'")
		if query != "" {
			search = "?" + query
		}
	}
	return
}
