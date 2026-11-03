package main

// shelf gen: builds docs/<tmdbId>.md (qmd collection), data/movies.json and unmatched.md from the NAS movie sources
// (read-only), Radarr and TMDB. Port of gen.mjs; the same flags (--rescan, --no-nas-posters) and cache files.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"

	"movieshelf/icu"
)

type movieSource struct {
	library, dir, smb string
	nasPosters        bool
}

var libraryLabel = map[string]string{"main": "主片库", "kids": "少儿"}

var (
	videoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m2ts": true, ".ts": true, ".mov": true, ".wmv": true, ".iso": true, ".rmvb": true}
	subExt   = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".sup": true, ".vtt": true, ".smi": true}
)

type genMovies struct {
	root, docs, cache, posters, tmdbCache string
	sources                               []movieSource
	tmdbKey                               string
	args                                  map[string]bool
	mu                                    sync.Mutex
	unmatched                             []unmatchedEntry
}

type unmatchedEntry struct{ name, reason string }

var reNonLN = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// normTitle is gen.mjs norm(): NFKD, strip combining marks, lowercase, & → and, letters and digits only.
func normTitle(s string) string {
	s = norm.NFKD.String(s)
	s = strings.Map(func(r rune) rune {
		if r >= 0x300 && r <= 0x36F {
			return -1
		}
		return r
	}, s)
	s = strings.ReplaceAll(strings.ToLower(s), "&", "and")
	return reNonLN.ReplaceAllString(s, "")
}

func (g *genMovies) fetchJSON(u string, headers map[string]string, tries int) (any, error) {
	for t := 0; ; t++ {
		v, err := func() (any, error) {
			r, err := fetch(u, headers, 30*time.Second)
			if err != nil {
				return nil, err
			}
			if r.status == 404 {
				return nil, nil
			}
			if r.status == 429 || r.status >= 500 {
				return nil, fmt.Errorf("HTTP %d", r.status)
			}
			if !r.ok() {
				return nil, fmt.Errorf("HTTP %d %s", r.status, strings.ReplaceAll(u, g.tmdbKey, "***"))
			}
			return parseJSON(r.body)
		}()
		if err == nil {
			return v, nil
		}
		if t >= tries {
			return nil, err
		}
		time.Sleep(time.Second * time.Duration(1<<t))
	}
}

func (g *genMovies) cached(name string, fn func() (any, error)) (any, error) {
	f := filepath.Join(g.tmdbCache, name+".json")
	if v, err := readJSONFile(f); err == nil {
		return v, nil
	}
	v, err := fn()
	if err != nil {
		return nil, err
	}
	return v, writeJSONFile(f, v)
}

func (g *genMovies) tmdb(p string, params [][2]string) (any, error) {
	q := url.Values{}
	q.Set("api_key", g.tmdbKey)
	for _, kv := range params {
		q.Set(kv[0], kv[1])
	}
	return g.fetchJSON("https://api.themoviedb.org/3"+p+"?"+q.Encode(), nil, 4)
}

var reRadarrKey = regexp.MustCompile(`<ApiKey>([^<]+)</ApiKey>`)

func (g *genMovies) radarrMovies() []any {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, "Library/Application Support/Radarr/config.xml"))
	if err != nil {
		warn("Radarr unavailable: %s", err)
		return nil
	}
	key := ""
	if m := reRadarrKey.FindSubmatch(data); m != nil {
		key = string(m[1])
	}
	v, err := g.fetchJSON("http://localhost:7878/api/v3/movie", map[string]string{"X-Api-Key": key}, 1)
	if err != nil {
		warn("Radarr unavailable: %s", fetchErrMsg(err))
		return nil
	}
	return arr(v)
}

// fetchErrMsg is the message Node's fetch gives for a network error.
func fetchErrMsg(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return "fetch failed"
	}
	return err.Error()
}

type parsedName struct {
	titles []string
	year   float64
	hasYr  bool
	rng    bool
}

var (
	reVideoSuffix = re(`\.(mkv|mp4|avi|m2ts|ts|mov|wmv|iso|rmvb)$`, "i")
	reYearRange   = re(`(19|20)\d{2}\s*-\s*(19|20)\d{2}`, "")
	reTitleYear   = re(`^(.*?)[\s.([]+((?:19|20)\d{2})(?:[\s.)\]]|$)`, "")
	reQualityTail = re(`[.\s](720p|1080p|2160p|4k|bluray|web).*$`, "i")
	reDotUnder    = re(`[._]`, "g")
	reSpaces      = re(`\s+`, "g")
	reAKA         = re(`\s+AKA\s+`, "i")
	reTagBrackets = re(`\[[^\]]*\]|【[^】]*】|\((?:\d{3,4}p|4k|uhd)\)`, "gi")
	reCJKLatin    = regexp.MustCompile(`^([\p{Han}\d：:·，！？]*\p{Han}[\p{Han}\d：:·，！？]*)\s+([A-Za-z].*)$`)
)

func parseName(name string) parsedName {
	s := reTagBrackets.Replace(reVideoSuffix.Replace(name, ""), " ")
	p := parsedName{rng: reYearRange.Test(s)}
	m, _ := reTitleYear.Match(s)
	title := ""
	if m != nil {
		title = m[1]
		p.year, _ = strconv.ParseFloat(m[2], 64)
		p.hasYr = true
	} else {
		title = reQualityTail.Replace(s, "")
	}
	title = jsTrim(reSpaces.Replace(reDotUnder.Replace(title, " "), " "))
	for _, t := range reAKA.Split(title) {
		if t = jsTrim(t); t == "" {
			continue
		}
		// "哪吒之魔童降世 Ne Zha": search the Chinese and the Latin title separately as well.
		if m := reCJKLatin.FindStringSubmatch(t); m != nil {
			p.titles = append(p.titles, m[1], jsTrim(m[2]))
			continue
		}
		p.titles = append(p.titles, t)
	}
	return p
}

type tmdbCand struct {
	id    any
	names map[string]bool
	year  float64
}

func yearOf(date any) float64 {
	s, _ := date.(string)
	y := strToNumber(utf16Slice(s, 4))
	if math.IsNaN(y) {
		return 0
	}
	return y
}

func (g *genMovies) searchTmdb(p parsedName) (any, string, error) {
	id, reason, err := g.searchTmdbWith(p, false)
	if err == nil && id == nil && p.hasYr && p.year != 0 && strings.HasPrefix(reason, "TMDB 无") {
		// Short or punctuated titles ("WALL E") only surface among TMDB results when the year is a filter.
		return g.searchTmdbWith(p, true)
	}
	return id, reason, err
}

func (g *genMovies) searchTmdbWith(p parsedName, byYear bool) (any, string, error) {
	var order []string
	cands := map[string]*tmdbCand{}
	for _, t := range p.titles {
		for _, lang := range []string{"zh-CN", "en-US"} {
			yr := "x"
			if p.hasYr && p.year != 0 {
				yr = jsNum(p.year)
			}
			params := [][2]string{{"query", t}, {"language", lang}, {"include_adult", "false"}}
			key := fmt.Sprintf("search_%s_%s_%s", normTitle(t), yr, lang)
			if byYear {
				params = append(params, [2]string{"year", yr})
				key += "_y"
			}
			r, err := g.cached(key, func() (any, error) { return g.tmdb("/search/movie", params) })
			if err != nil {
				return nil, "", err
			}
			for _, x := range arr(getv(r, "results")) {
				k := mapKey(getv(x, "id"))
				c := cands[k]
				if c == nil {
					c = &tmdbCand{id: getv(x, "id"), names: map[string]bool{}, year: yearOf(getv(x, "release_date"))}
					cands[k] = c
					order = append(order, k)
				}
				c.names[normTitle(strOr(getv(x, "title")))] = true
				c.names[normTitle(strOr(getv(x, "original_title")))] = true
			}
		}
	}
	wanted := map[string]bool{}
	for _, t := range p.titles {
		wanted[normTitle(t)] = true
	}
	var hits []*tmdbCand
	for _, k := range order {
		c := cands[k]
		for n := range c.names {
			if wanted[n] {
				hits = append(hits, c)
				break
			}
		}
	}
	if p.hasYr && p.year != 0 {
		var f []*tmdbCand
		for _, c := range hits {
			if c.year != 0 && math.Abs(c.year-p.year) <= 1 {
				f = append(f, c)
			}
		}
		hits = f
	}
	if len(hits) == 1 {
		return hits[0].id, "", nil
	}
	if p.hasYr && p.year != 0 && len(hits) > 1 {
		var exact []*tmdbCand
		for _, c := range hits {
			if c.year == p.year {
				exact = append(exact, c)
			}
		}
		if len(exact) == 1 {
			return exact[0].id, "", nil
		}
	}
	if len(hits) > 0 {
		return nil, fmt.Sprintf("%d 个同名候选，无法确定", len(hits)), nil
	}
	return nil, "TMDB 无标题+年份一致的结果", nil
}

// walkMovie is gen.mjs walk(): files up to two folder levels below dir, dot-entries skipped.
func walkMovie(dir string, depth int) ([]string, error) {
	es, err := readDirents(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if strings.HasPrefix(e.name, ".") {
			continue
		}
		p := filepath.Join(dir, e.name)
		if e.isDir {
			if depth > 0 {
				sub, err := walkMovie(p, depth-1)
				if err != nil {
					return nil, err
				}
				out = append(out, sub...)
			}
		} else {
			out = append(out, p)
		}
	}
	return out, nil
}

var langTokens = []struct {
	re   *jsRe
	name string
}{
	{re(`^(chs|sc|gb|zh-?hans|zho?-?cn|chn|简|简体|简中|简体中文|cn)$`, "i"), "简体中文"},
	{re(`^(cht|tc|big5|zh-?hant|zh-?tw|zh-?hk|繁|繁体|繁體|繁中|繁体中文)$`, "i"), "繁体中文"},
	{re(`^(chi|zh|zho|chinese|中文|中字|国语)$`, "i"), "中文"},
	{re(`^(eng?|english|英|英文)$`, "i"), "英语"},
	{re(`^(jpn?|ja|japanese|日|日文)$`, "i"), "日语"},
	{re(`^(kor?|korean|韩)$`, "i"), "韩语"},
	{re(`^(fre|fra|fr|french)$`, "i"), "法语"},
	{re(`^(ger|deu|de|german)$`, "i"), "德语"},
	{re(`^(spa|es|spanish)$`, "i"), "西班牙语"},
	{re(`^(ita|it|italian)$`, "i"), "意大利语"},
	{re(`^(rus|ru|russian)$`, "i"), "俄语"},
	{re(`^(por|pt|portuguese)$`, "i"), "葡萄牙语"},
}

var (
	reLastExt   = re(`\.[^.]+$`, "")
	reSubTokSep = re(`[.\-_&\s[\]()]+`, "")
	reSample    = re(`sample`, "i")
)

// orderedSet keeps insertion order like a JS Set of strings.
type orderedSet struct {
	items []string
	has   map[string]bool
}

func (s *orderedSet) add(v string) {
	if s.has == nil {
		s.has = map[string]bool{}
	}
	if !s.has[v] {
		s.has[v] = true
		s.items = append(s.items, v)
	}
}

func (s *orderedSet) del(v string) {
	if !s.has[v] {
		return
	}
	delete(s.has, v)
	for i, x := range s.items {
		if x == v {
			s.items = append(s.items[:i:i], s.items[i+1:]...)
			break
		}
	}
}

func (s *orderedSet) list() []any {
	out := make([]any, len(s.items))
	for i, x := range s.items {
		out[i] = x
	}
	return out
}

func subLangs(file string) []string {
	base := reLastExt.Replace(filepath.Base(file), "")
	var langs orderedSet
	for _, tok := range reSubTokSep.Split(base) {
		for _, lt := range langTokens {
			if lt.re.Test(tok) {
				langs.add(lt.name)
			}
		}
	}
	if len(langs.items) == 0 && hasCJK(base) {
		langs.add("中文")
	}
	if langs.has["中文"] && (langs.has["简体中文"] || langs.has["繁体中文"]) {
		langs.del("中文")
	}
	return langs.items
}

func (g *genMovies) scanEntry(src movieSource, name string, isDir bool, rootFiles []string) (*Obj, error) {
	full := filepath.Join(src.dir, name)
	st, err := statFile(full)
	if err != nil {
		return nil, err
	}
	var files []string
	if isDir {
		if files, err = walkMovie(full, 2); err != nil {
			return nil, err
		}
	} else {
		stem := reLastExt.Replace(name, "")
		files = []string{full}
		for _, f := range rootFiles {
			if f != name && strings.HasPrefix(f, stem) {
				files = append(files, filepath.Join(src.dir, f))
			}
		}
	}
	var videos, subs []string
	for _, f := range files {
		ext := strings.ToLower(jsExtname(f))
		if videoExt[ext] && !reSample.Test(filepath.Base(f)) {
			videos = append(videos, f)
		}
		if subExt[ext] {
			subs = append(subs, f)
		}
	}
	added := st.birthMs
	if added == 0 {
		added = st.mtimeMs
	}
	videoFiles := []any{}
	if len(videos) > 0 {
		added = math.Inf(1)
		for _, v := range videos {
			vs, err := statFile(v)
			if err != nil {
				return nil, err
			}
			a := vs.birthMs
			if a == 0 {
				a = vs.mtimeMs
			}
			added = math.Min(added, a)
			videoFiles = append(videoFiles, newObj("path", jsRelative(src.dir, v), "size", float64(vs.size)))
		}
	}
	var langs orderedSet
	subRel := []any{}
	for _, s := range subs {
		for _, l := range subLangs(s) {
			langs.add(l)
		}
		subRel = append(subRel, jsRelative(src.dir, s))
	}
	if len(subs) > 0 && len(langs.items) == 0 {
		langs.add("未知")
	}
	return newObj("name", name, "isDir", isDir, "videos", float64(len(videos)), "videoFiles", videoFiles, "added", added,
		"dirMtime", st.mtimeMs, "subs", subRel, "subLangs", langs.list()), nil
}

type person struct {
	name      any
	cjk       any
	akas      []any
	character any
	hasChar   bool
}

var (
	reHanMain = re(`[一-鿿]`, "")
	reKanaHan = re(`[぀-ヿ가-힯]`, "")
)

func (g *genMovies) personCJKName(id, fallback any) (person, error) {
	p, err := g.cached("person_"+jsString(id), func() (any, error) { return g.tmdb("/person/"+jsString(id), [][2]string{{"language", "zh-CN"}}) })
	if err != nil {
		return person{}, err
	}
	var akas []any
	for _, a := range arr(getv(p, "also_known_as")) {
		if s, ok := a.(string); ok && hasCJK(s) {
			akas = append(akas, s)
		}
	}
	var simp any = nil
	for _, a := range akas {
		s := a.(string)
		if reHanMain.Test(s) && !reKanaHan.Test(s) {
			simp = s
			break
		}
	}
	return person{name: fallback, cjk: simp, akas: akas[:min(4, len(akas))]}, nil
}

func region(d any) string {
	lang := getv(d, "original_language")
	var countries []any
	if oc := getv(d, "origin_country"); truthy(oc) {
		countries = arr(oc)
	} else {
		for _, c := range arr(getv(d, "production_countries")) {
			countries = append(countries, getv(c, "iso_3166_1"))
		}
	}
	has := func(set ...string) bool {
		for _, c := range countries {
			for _, s := range set {
				if c == s {
					return true
				}
			}
		}
		return false
	}
	in := func(v any, set ...string) bool {
		for _, s := range set {
			if v == s {
				return true
			}
		}
		return false
	}
	if in(lang, "zh", "cn", "yue") || has("CN", "HK", "TW", "MO") && !in(lang, "ja", "ko") {
		return "华语"
	}
	if in(lang, "ja", "ko") || (has("JP", "KR") && lang != "en") {
		return "日韩"
	}
	return "欧美"
}

type movieDetail struct {
	id                            any
	title, enTitle, originalTitle any
	year, runtime                 any
	genres, keywords, countries   []any
	region                        string
	overview, overviewEn, tagline string
	imdbID, rating                any
	directors, cast               []person
	posterPath                    any
}

func names(v any) []any {
	out := []any{}
	for _, x := range arr(v) {
		out = append(out, getv(x, "name"))
	}
	return out
}

func nullIfFalsy(v any) any {
	if truthy(v) {
		return v
	}
	return nil
}

func (g *genMovies) movieDetails(id any) (*movieDetail, error) {
	ids := jsString(id)
	zh, err := g.cached("movie_"+ids+"_zh", func() (any, error) {
		return g.tmdb("/movie/"+ids, [][2]string{{"language", "zh-CN"}, {"append_to_response", "credits,keywords"}})
	})
	if err != nil || !truthy(zh) {
		return nil, err
	}
	en, err := g.cached("movie_"+ids+"_en", func() (any, error) { return g.tmdb("/movie/"+ids, [][2]string{{"language", "en-US"}}) })
	if err != nil {
		return nil, err
	}
	crew := arr(getv(zh, "credits", "crew"))
	var directorsRaw []any
	for _, c := range crew {
		if getv(c, "job") == "Director" && len(directorsRaw) < 3 {
			directorsRaw = append(directorsRaw, c)
		}
	}
	castRaw := arr(getv(zh, "credits", "cast"))
	castRaw = castRaw[:min(8, len(castRaw))]
	d := &movieDetail{id: id}
	for _, c := range directorsRaw {
		p, err := g.personCJKName(getv(c, "id"), getv(c, "name"))
		if err != nil {
			return nil, err
		}
		d.directors = append(d.directors, p)
	}
	for i, c := range castRaw {
		if i < 5 {
			p, err := g.personCJKName(getv(c, "id"), getv(c, "name"))
			if err != nil {
				return nil, err
			}
			p.character, p.hasChar = getv(c, "character"), true
			d.cast = append(d.cast, p)
		} else {
			d.cast = append(d.cast, person{name: getv(c, "name"), cjk: nil, akas: []any{}, character: getv(c, "character"), hasChar: true})
		}
	}
	d.title = getv(zh, "title")
	d.enTitle = or(getv(en, "title"), getv(zh, "original_title"))
	d.originalTitle = getv(zh, "original_title")
	if y := yearOf(getv(zh, "release_date")); y != 0 {
		d.year = y
	}
	d.runtime = nullIfFalsy(getv(zh, "runtime"))
	d.genres = names(getv(zh, "genres"))
	d.keywords = names(getv(zh, "keywords", "keywords"))
	d.countries = []any{}
	for _, c := range arr(getv(zh, "production_countries")) {
		d.countries = append(d.countries, icu.RegionName(jsString(getv(c, "iso_3166_1"))))
	}
	d.region = region(zh)
	d.overview = strOr(or(getv(zh, "overview"), ""))
	d.overviewEn = strOr(or(getv(en, "overview"), ""))
	d.tagline = strOr(or(getv(zh, "tagline"), ""))
	d.imdbID = nullIfFalsy(getv(zh, "imdb_id"))
	d.rating = nullIfFalsy(getv(zh, "vote_average"))
	d.posterPath = or(getv(zh, "poster_path"), getv(en, "poster_path"))
	return d, nil
}

func (g *genMovies) poster(id any, p any) any {
	if !truthy(p) {
		return nil
	}
	name := jsString(id) + ".jpg"
	f := filepath.Join(g.posters, name)
	if exists(f) {
		return name
	}
	for t := 0; t < 3; t++ {
		err := func() error {
			r, err := fetch("https://image.tmdb.org/t/p/w342"+jsString(p), nil, 30*time.Second)
			if err != nil {
				return errors.New(fetchErrMsg(err))
			}
			if !r.ok() {
				return fmt.Errorf("poster HTTP %d", r.status)
			}
			return os.WriteFile(f, r.body, 0o644)
		}()
		if err == nil {
			return name
		}
		if t == 2 {
			warn("poster %s: %s", jsString(id), err)
		}
	}
	return nil
}

type movie struct {
	*movieDetail
	entries                []*scanned
	nasPaths, libraries    []any
	added                  float64
	subs, subLangs, videos []any
	poster                 any
	imdbRating, imdbVotes  any
	imdbSource             any
}

type scanned struct {
	scan   *Obj
	id     any
	source string
	src    movieSource
	label  string
}

func radarrImdb(r any, tmdbImdbID any) (any, any, any, any) {
	imdbID := or(or(getv(r, "imdbId"), tmdbImdbID), nil)
	v := getv(r, "ratings", "imdb")
	if truthy(getv(v, "value")) && truthy(getv(v, "votes")) {
		return imdbID, getv(v, "value"), getv(v, "votes"), "radarr"
	}
	return imdbID, nil, nil, nil
}

func (g *genMovies) fillImdbFromDataset(movies []*movie) {
	missing := map[string]*movie{}
	for _, m := range movies {
		if !truthy(m.imdbRating) && truthy(m.imdbID) {
			missing[jsString(m.imdbID)] = m
		}
	}
	if len(missing) == 0 {
		return
	}
	f := filepath.Join(g.cache, "imdb-title.ratings-"+time.Now().UTC().Format("2006-01-02")+".tsv.gz")
	err := func() error {
		buf, err := os.ReadFile(f)
		if err != nil {
			r, err := fetch("https://datasets.imdbws.com/title.ratings.tsv.gz", nil, 120*time.Second)
			if err != nil {
				return errors.New(fetchErrMsg(err))
			}
			if !r.ok() {
				return fmt.Errorf("HTTP %d", r.status)
			}
			buf = r.body
			olds, _ := readdirNames(g.cache)
			for _, old := range olds {
				if strings.HasPrefix(old, "imdb-title.ratings-") {
					os.Remove(filepath.Join(g.cache, old))
				}
			}
			if err := os.WriteFile(f, buf, 0o644); err != nil {
				return err
			}
		}
		zr, err := gzip.NewReader(bytes.NewReader(buf))
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			line := sc.Text()
			k := strings.IndexByte(line, '\t')
			id := ""
			if k >= 0 {
				id = line[:k]
			} else if len(line) > 0 {
				id = line[:len(line)-1]
			}
			m := missing[id]
			if m == nil {
				continue
			}
			parts := strings.Split(line, "\t")
			get := func(i int) any {
				if i < len(parts) {
					return parts[i]
				}
				return undefined
			}
			m.imdbRating, m.imdbVotes, m.imdbSource = toNumber(get(1)), toNumber(get(2)), "imdb-dataset"
		}
		return sc.Err()
	}()
	if err != nil {
		warn("IMDb dataset unavailable: %s", err)
	}
}

var nasPosterNames = []string{"poster.jpg", "folder.jpg", "cover.jpg"}

// writeNasPosters copies a w780 TMDB poster as poster.jpg into each nasPosters folder that has none yet; never overwrites,
// and logs every created file to logs/nas-posters-<date>.txt.
func (g *genMovies) writeNasPosters(movies []*movie) {
	dir := filepath.Join(g.cache, "posters-w780")
	os.MkdirAll(dir, 0o755)
	logFile := filepath.Join(g.root, "logs", "nas-posters-"+time.Now().UTC().Format("2006-01-02")+".txt")
	var mu sync.Mutex
	stats := struct{ written, skipped, noPoster, failed int }{}
	var skipped []string
	type job struct {
		m      *movie
		folder string
	}
	var jobs []job
	for _, m := range movies {
		for _, e := range m.entries {
			if e.src.nasPosters {
				jobs = append(jobs, job{m, filepath.Join(e.src.dir, jsString(e.scan.Get("name")))})
			}
		}
	}
	pool(jobs, 4, func(j job, _ int) struct{} {
		if !truthy(j.m.posterPath) {
			mu.Lock()
			stats.noPoster++
			mu.Unlock()
			return struct{}{}
		}
		err := func() error {
			st, err := statFile(j.folder)
			if err != nil {
				return err
			}
			if !st.isDir {
				return nil
			}
			ns, err := readdirNames(j.folder)
			if err != nil {
				return err
			}
			have := map[string]bool{}
			for _, n := range ns {
				have[strings.ToLower(n)] = true
			}
			for _, n := range nasPosterNames {
				if have[n] {
					mu.Lock()
					stats.skipped++
					skipped = append(skipped, fmt.Sprintf("%s (%s)", filepath.Base(j.folder), n))
					mu.Unlock()
					return nil
				}
			}
			src := filepath.Join(dir, jsString(j.m.id)+".jpg")
			if !exists(src) {
				r, err := fetch("https://image.tmdb.org/t/p/w780"+jsString(j.m.posterPath), nil, 60*time.Second)
				if err != nil {
					return errors.New(fetchErrMsg(err))
				}
				if !r.ok() {
					return fmt.Errorf("HTTP %d", r.status)
				}
				if len(r.body) > 1024*1024 {
					return fmt.Errorf("w780 poster too large (%d bytes)", len(r.body))
				}
				if err := os.WriteFile(src, r.body, 0o644); err != nil {
					return err
				}
			}
			dest := filepath.Join(j.folder, "poster.jpg")
			data, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			if _, err := out.Write(data); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			lf, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer lf.Close()
			lf.WriteString(dest + "\n")
			stats.written++
			return nil
		}()
		if err != nil {
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, os.ErrExist) {
				stats.skipped++
				skipped = append(skipped, fmt.Sprintf("%s (poster.jpg)", filepath.Base(j.folder)))
				return struct{}{}
			}
			stats.failed++
			warn("nas poster %s: %s", j.folder, errCode(err))
		}
		return struct{}{}
	})
	if len(skipped) > 0 {
		sort.Strings(skipped)
		os.WriteFile(filepath.Join(g.root, "logs", "nas-posters-skipped.txt"), []byte(strings.Join(skipped, "\n")+"\n"), 0o644)
	}
	logNote := ""
	if stats.written > 0 {
		logNote = " (log: " + jsRelative(g.root, logFile) + ")"
	}
	info(`nas posters: {"written":%d,"skipped":%d,"noPoster":%d,"failed":%d}%s`, stats.written, stats.skipped, stats.noPoster, stats.failed, logNote)
}

func yamlStr(v any) string { return string(stringify(strOr(v))) }

func yamlList(a []any) string {
	parts := make([]string, len(a))
	for i, x := range a {
		parts[i] = yamlStr(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func personLabel(p person) string {
	if truthy(p.cjk) && p.cjk != p.name {
		return jsString(p.cjk) + "（" + jsString(p.name) + "）"
	}
	return jsString(p.name)
}

func joinAny(a []any, sep string) string {
	parts := make([]string, len(a))
	for i, x := range a {
		parts[i] = strOr(x)
	}
	return strings.Join(parts, sep)
}

func orEmpty(v any) string {
	if nullish(v) {
		return ""
	}
	return jsString(v)
}

func renderMovieDoc(m *movie) string {
	var zh []string
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" && hasCJK(s) {
			zh = append(zh, s)
		}
	}
	add(m.title)
	add(m.overview)
	add(m.tagline)
	for _, x := range m.genres {
		add(x)
	}
	for _, x := range m.countries {
		add(x)
	}
	for _, ps := range [][]person{m.directors, m.cast} {
		for _, p := range ps {
			add(p.cjk)
			for _, a := range p.akas {
				add(a)
			}
		}
	}
	dirNames := make([]string, len(m.directors))
	for i, d := range m.directors {
		dirNames[i] = jsString(or(d.cjk, d.name))
	}
	videoFile := ""
	if len(m.videos) > 0 {
		videoFile = strOr(getv(m.videos[0], "path"))
	}
	yearHead := ""
	if truthy(m.year) {
		yearHead = " (" + jsString(m.year) + ")"
	}
	yearLine := "未知"
	if !nullish(m.year) {
		yearLine = jsString(m.year)
	}
	if truthy(m.runtime) {
		yearLine += "，片长 " + jsString(m.runtime) + " 分钟"
	}
	libs := make([]string, len(m.libraries))
	for i, l := range m.libraries {
		if lbl, ok := libraryLabel[jsString(l)]; ok {
			libs[i] = lbl
		} else {
			libs[i] = jsString(l)
		}
	}
	labels := func(ps []person) string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = personLabel(p)
		}
		return strings.Join(out, "、")
	}
	overview := m.overview
	if overview == "" {
		overview = "（暂无中文简介）"
	}
	kw := append(append([]any{}, m.genres...), m.keywords...)
	return `---
title: ` + yamlStr(m.title) + `
tmdb_id: ` + jsString(m.id) + `
year: ` + orEmpty(m.year) + `
genres: ` + yamlList(m.genres) + `
countries: ` + yamlList(m.countries) + `
region: ` + m.region + `
director: ` + yamlStr(strings.Join(dirNames, " / ")) + `
library: ` + yamlList(m.libraries) + `
nas_path: ` + yamlList(m.nasPaths) + `
has_subs: ` + strconv.FormatBool(len(m.subLangs) > 0) + `
video_file: ` + yamlStr(videoFile) + `
imdb_id: ` + yamlStr(or(m.imdbID, "")) + `
imdb_rating: ` + orEmpty(m.imdbRating) + `
imdb_votes: ` + orEmpty(m.imdbVotes) + `
sub_langs: ` + yamlList(m.subLangs) + `
---

# ` + jsString(m.title) + yearHead + `

## 标题

- 中文名：` + jsString(m.title) + `
- 英文名：` + jsString(m.enTitle) + `
- 原名：` + jsString(m.originalTitle) + `
- 年份：` + yearLine + `
- 类型：` + joinAny(m.genres, "、") + `
- 国家/地区：` + joinAny(m.countries, "、") + `（` + m.region + `）
- 片库：` + strings.Join(libs, "、") + `

## 简介

` + overview + `

` + m.overviewEn + `

## 关键词

` + joinAny(kw, ", ") + `

## 演职员

- 导演：` + labels(m.directors) + `
- 主演：` + labels(m.cast) + `

## 分词

` + strings.Join(segment(strings.Join(zh, "。"), true), " ") + `
`
}

func runGen(args []string) error {
	root := findRoot()
	g := &genMovies{root: root, docs: filepath.Join(root, "docs"), cache: filepath.Join(root, "cache"), args: map[string]bool{}}
	for _, a := range args {
		g.args[a] = true
	}
	g.posters = filepath.Join(g.cache, "posters")
	g.tmdbCache = filepath.Join(g.cache, "tmdb")
	g.sources = []movieSource{
		{"main", env("NAS_DIR", "/Volumes/store/movie"), "smb://192.168.50.94/store/movie/", true},
		{"kids", env("KIDS_NAS_DIR", "/Volumes/video/movie"), "smb://192.168.50.94/video/movie/", false},
	}
	envFile, err := parseEnvFile(filepath.Join(root, ".env"))
	if err != nil {
		return err
	}
	if g.tmdbKey = envFile["TMDB_API_KEY"]; g.tmdbKey == "" {
		return errors.New("TMDB_API_KEY missing in .env")
	}
	for _, d := range []string{g.docs, g.posters, g.tmdbCache, filepath.Join(root, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	type entry struct {
		src       movieSource
		d         dirent
		rootNames []string
	}
	var entries []entry
	for _, src := range g.sources {
		ds, err := readDirents(src.dir)
		if err != nil {
			return err
		}
		var rootNames []string
		var keep []dirent
		for _, d := range ds {
			if strings.HasPrefix(d.name, ".") || strings.HasPrefix(d.name, "@") {
				continue
			}
			rootNames = append(rootNames, d.name)
			keep = append(keep, d)
		}
		n := 0
		for _, d := range keep {
			if d.isDir || videoExt[strings.ToLower(jsExtname(d.name))] {
				entries = append(entries, entry{src, d, rootNames})
				n++
			}
		}
		info("NAS entries (%s, %s): %d", src.library, src.dir, n)
	}

	scanCacheFile := filepath.Join(g.cache, "scan.json")
	scanCache := &Obj{}
	if !g.args["--rescan"] {
		if v, err := readJSONFile(scanCacheFile); err == nil {
			if o, ok := v.(*Obj); ok {
				scanCache = o
			}
		}
	}
	newScanCache := &Obj{}
	newScan := make([]*Obj, len(entries))
	keys := make([]string, len(entries))
	var cacheMu sync.Mutex
	done := 0

	radarrList := g.radarrMovies()
	radarr, radarrByID := map[string]any{}, map[string]any{}
	for _, m := range radarrList {
		radarr[strOr(getv(m, "path"))] = m
		radarrByID[mapKey(getv(m, "tmdbId"))] = m
	}
	info("Radarr movies: %d", len(radarr))

	reTmdbTag := regexp.MustCompile(`\{tmdb-(\d+)\}`)
	scannedList := pool(entries, 8, func(e entry, k int) *scanned {
		name := e.d.name
		key, label := name, name
		if e.src.library != g.sources[0].library {
			key, label = e.src.library+":"+name, filepath.Join(e.src.dir, name)
		}
		var scan *Obj
		if c, ok := scanCache.Get(key).(*Obj); ok && truthy(c.Get("videoFiles")) {
			scan = c
			if st, err := statFile(filepath.Join(e.src.dir, name)); err != nil || st.mtimeMs != toNumber(scan.Get("dirMtime")) {
				scan = nil
			}
		}
		if scan == nil {
			var err error
			if scan, err = g.scanEntry(e.src, name, e.d.isDir, e.rootNames); err != nil {
				warn("skip %s: %s", label, errCode(err))
				return nil
			}
		}
		cacheMu.Lock()
		newScan[k], keys[k] = scan, key
		done++
		if done%10 == 0 {
			info("scanned %d/%d", done, len(entries))
		}
		cacheMu.Unlock()
		var id any
		source := ""
		if r, ok := radarr[filepath.Join(e.src.dir, name)]; ok && truthy(getv(r, "tmdbId")) {
			id, source = getv(r, "tmdbId"), "radarr"
		} else if m := reTmdbTag.FindStringSubmatch(name); m != nil {
			id, source = strToNumber(m[1]), "folder"
		} else {
			p := parseName(name)
			add := func(reason string) {
				g.mu.Lock()
				g.unmatched = append(g.unmatched, unmatchedEntry{label, reason})
				g.mu.Unlock()
			}
			if p.rng {
				add("多部合集（年份区间），未自动拆分")
				return nil
			}
			if len(p.titles) == 0 {
				add("无法解析片名")
				return nil
			}
			tid, reason, err := g.searchTmdb(p)
			if err != nil {
				add("TMDB 请求失败：" + fetchErrMsg(err))
				return nil
			}
			if tid == nil {
				yr := "无年份"
				if p.hasYr && p.year != 0 {
					yr = jsNum(p.year)
				}
				add(fmt.Sprintf("%s（解析为「%s」%s）", reason, strings.Join(p.titles, " / "), yr))
				return nil
			}
			id, source = tid, "tmdb-search"
		}
		return &scanned{scan, id, source, e.src, label}
	})
	for k, s := range newScan {
		if s != nil {
			newScanCache.Set(keys[k], s)
		}
	}
	if err := writeJSONFile(scanCacheFile, newScanCache); err != nil {
		return err
	}

	type group struct {
		id      any
		entries []*scanned
	}
	var groups []*group
	byID := map[string]*group{}
	nScanned := 0
	for _, s := range scannedList {
		if s == nil {
			continue
		}
		nScanned++
		k := mapKey(s.id)
		if byID[k] == nil {
			byID[k] = &group{id: s.id}
			groups = append(groups, byID[k])
		}
		byID[k].entries = append(byID[k].entries, s)
	}

	movieList := pool(groups, 6, func(gr *group, _ int) *movie {
		d, err := g.movieDetails(gr.id)
		addAll := func(reason string) {
			g.mu.Lock()
			for _, x := range gr.entries {
				g.unmatched = append(g.unmatched, unmatchedEntry{x.label, reason})
			}
			g.mu.Unlock()
		}
		if err != nil {
			addAll("TMDB 请求失败：" + fetchErrMsg(err))
			return nil
		}
		if d == nil {
			addAll("TMDB id " + jsString(gr.id) + " 不存在")
			return nil
		}
		m := &movie{movieDetail: d, entries: gr.entries, added: math.Inf(1)}
		var langs, libs orderedSet
		for _, e := range gr.entries {
			for _, l := range arr(e.scan.Get("subLangs")) {
				langs.add(jsString(l))
			}
			name := jsString(e.scan.Get("name"))
			m.nasPaths = append(m.nasPaths, e.src.smb+name)
			libs.add(e.src.library)
			m.added = math.Min(m.added, toNumber(e.scan.Get("added")))
			m.subs = append(m.subs, arr(e.scan.Get("subs"))...)
			for _, v := range arr(e.scan.Get("videoFiles")) {
				vo := v.(*Obj).Clone()
				vo.Set("dir", e.src.dir)
				m.videos = append(m.videos, vo)
			}
		}
		if m.subs == nil {
			m.subs = []any{}
		}
		if m.videos == nil {
			m.videos = []any{}
		}
		m.subLangs, m.libraries = langs.list(), libs.list()
		sort.SliceStable(m.videos, func(i, j int) bool {
			return toNumber(getv(m.videos[j], "size")) < toNumber(getv(m.videos[i], "size"))
		})
		m.poster = g.poster(gr.id, d.posterPath)
		m.imdbID, m.imdbRating, m.imdbVotes, m.imdbSource = radarrImdb(radarrByID[mapKey(gr.id)], d.imdbID)
		return m
	})
	var movies []*movie
	for _, m := range movieList {
		if m != nil {
			movies = append(movies, m)
		}
	}

	g.fillImdbFromDataset(movies)
	keep := map[string]bool{}
	for _, m := range movies {
		name := jsString(m.id) + ".md"
		keep[name] = true
		if err := os.WriteFile(filepath.Join(g.docs, name), []byte(renderMovieDoc(m)), 0o644); err != nil {
			return err
		}
	}
	nr, nradarr, nds := 0, 0, 0
	for _, m := range movies {
		if truthy(m.imdbRating) {
			nr++
		}
		switch m.imdbSource {
		case "radarr":
			nradarr++
		case "imdb-dataset":
			nds++
		}
	}
	info("imdb ratings: %d/%d (radarr %d, dataset %d)", nr, len(movies), nradarr, nds)
	olds, _ := readdirNames(g.docs)
	for _, f := range olds {
		if strings.HasSuffix(f, ".md") && !keep[f] {
			os.Remove(filepath.Join(g.docs, f))
		}
	}

	sort.SliceStable(movies, func(i, j int) bool { return movies[j].added < movies[i].added })
	slim := make([]any, len(movies))
	for i, m := range movies {
		dirs := make([]any, len(m.directors))
		for k, d := range m.directors {
			dirs[k] = personLabel(d)
		}
		cast := []any{}
		for k, c := range m.cast {
			if k < 6 {
				cast = append(cast, personLabel(c))
			}
		}
		slim[i] = newObj("id", m.id, "title", m.title, "enTitle", m.enTitle, "originalTitle", m.originalTitle, "year", m.year,
			"runtime", m.runtime, "genres", m.genres, "countries", m.countries, "region", m.region, "rating", m.rating,
			"overview", or(m.overview, m.overviewEn), "directors", dirs, "cast", cast, "subLangs", m.subLangs, "subs", m.subs,
			"nasPaths", m.nasPaths, "libraries", m.libraries, "added", m.added, "poster", m.poster,
			"videos", m.videos, "imdbId", m.imdbID, "imdbRating", m.imdbRating, "imdbVotes", m.imdbVotes)
	}
	if err := writeJSONFile(filepath.Join(root, "data/movies.json"), slim); err != nil {
		return err
	}

	sort.SliceStable(g.unmatched, func(i, j int) bool { return icu.Default.Compare(g.unmatched[i].name, g.unmatched[j].name) < 0 })
	var lines []string
	for _, u := range g.unmatched {
		lines = append(lines, "- `"+u.name+"` — "+u.reason)
	}
	body := strings.Join(lines, "\n")
	if body == "" {
		body = "（无）"
	}
	md := "# 未匹配的条目\n\n生成时间：" + localeStringZH(time.Now()) + "。以下 NAS 条目无法高置信度匹配到 TMDB，未写入片库。\n" +
		"可以在 Radarr 中手动导入，或把文件夹改名为 `片名 (年份) {tmdb-ID}` 后重新生成。\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, "unmatched.md"), []byte(md), 0o644); err != nil {
		return err
	}
	info("movies: %d (from %d NAS entries), unmatched: %d", len(movies), nScanned, len(g.unmatched))
	if !g.args["--no-nas-posters"] {
		g.writeNasPosters(movies)
	}
	return nil
}
