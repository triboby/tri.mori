package main

// shelf lists [--refresh]: fetches movie charts (Douban Top 250; an IMDb Top 250 approximated from the public
// IMDb datasets), resolves each entry's IMDb / TMDB id,
// matches them against data/movies.json (main and kids libraries) and writes data/lists.json for /api/lists.
// Chart pages are cached for a month under cache/lists/; per-title lookups are cached for good.

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const listMaxAge = 30 * 24 * time.Hour

type listItem struct {
	Rank      int     `json:"rank"`
	Title     string  `json:"title"`
	Original  string  `json:"original,omitempty"`
	Year      int     `json:"year,omitempty"`
	Rating    float64 `json:"rating,omitempty"`
	DoubanID  string  `json:"doubanId,omitempty"`
	ImdbID    string  `json:"imdbId,omitempty"`
	TmdbID    int     `json:"tmdbId,omitempty"`
	Poster    string  `json:"poster,omitempty"`
	MovieID   int     `json:"movieId,omitempty"`
	MatchedBy string  `json:"matchedBy,omitempty"`
	Suspect   int     `json:"suspect,omitempty"`
	Votes     int     `json:"votes,omitempty"`
	OnNas     string  `json:"onNas,omitempty"`
}

type chart struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Source  string     `json:"source"`
	Note    string     `json:"note,omitempty"`
	Fetched int64      `json:"fetched"`
	Total   int        `json:"total"`
	Have    int        `json:"have"`
	Items   []listItem `json:"items"`
}

type libMovie struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	EnTitle       string `json:"enTitle"`
	OriginalTitle string `json:"originalTitle"`
	Year          int    `json:"year"`
	ImdbID        string `json:"imdbId"`
}

type listRunner struct {
	g          *genMovies
	dir        string
	refresh    bool
	zhTitles   bool
	nas        map[string]parsedName
	lastDouban time.Time
}

var (
	reDoubanItem    = regexp.MustCompile(`(?s)<div class="item">(.*?)</li>`)
	reDoubanRank    = regexp.MustCompile(`<em[^>]*>(\d+)</em>`)
	reDoubanSubject = regexp.MustCompile(`movie\.douban\.com/subject/(\d+)/`)
	reDoubanTitles  = regexp.MustCompile(`<span class="title">([^<]*)</span>`)
	reDoubanYear    = regexp.MustCompile(`<br>\s*(\d{4})`)
	reDoubanRating  = regexp.MustCompile(`property="v:average">([\d.]+)<`)
	reDoubanImdb    = regexp.MustCompile(`IMDb:</span>\s*(tt\d+)`)
)

// doubanHTML is one polite Douban GET (about 3 s apart, three tries); blocked or failing pages return an error.
func (l *listRunner) doubanHTML(u string) (string, error) {
	var last error
	for t := 0; t < 3; t++ {
		if wait := time.Until(l.lastDouban.Add(time.Duration(3000+rand.Intn(1000)) * time.Millisecond)); wait > 0 {
			time.Sleep(wait)
		}
		l.lastDouban = time.Now()
		r, err := fetch(u, map[string]string{"User-Agent": doubanUA, "Referer": "https://movie.douban.com/", "Accept-Language": "zh-CN,zh;q=0.9"}, 20*time.Second)
		switch {
		case err != nil:
			last = errors.New(fetchErrMsg(err))
		case r.status == 403 || r.status == 418 || reSecDouban.MatchString(r.url):
			return "", fmt.Errorf("Douban blocked (%d)", r.status)
		case !r.ok():
			last = fmt.Errorf("HTTP %d", r.status)
		default:
			return decodeUTF8(r.body), nil
		}
		time.Sleep(time.Duration(3000*(1<<t)) * time.Millisecond)
	}
	return "", fmt.Errorf("%s: %w", u, last)
}

func (l *listRunner) readCache(name string, v any, maxAge time.Duration) bool {
	f := filepath.Join(l.dir, name)
	st, err := os.Stat(f)
	if err != nil || (maxAge > 0 && time.Since(st.ModTime()) > maxAge) {
		return false
	}
	b, err := os.ReadFile(f)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (l *listRunner) writeCache(name string, v any) {
	b, _ := json.MarshalIndent(v, "", " ")
	if err := os.WriteFile(filepath.Join(l.dir, name), b, 0o644); err != nil {
		warn("cache %s: %s", name, err)
	}
}

// doubanTop250 reads the ten chart pages, then each film's page once for its IMDb id.
func (l *listRunner) doubanTop250() (*chart, error) {
	c := &chart{ID: "douban", Name: "豆瓣电影 Top 250", Source: "https://movie.douban.com/top250"}
	if l.refresh || !l.readCache("douban-top250.json", c, listMaxAge) {
		c.Items = nil
		for start := 0; start < 250; start += 25 {
			html, err := l.doubanHTML(fmt.Sprintf("https://movie.douban.com/top250?start=%d", start))
			if err != nil {
				return nil, err
			}
			for _, m := range reDoubanItem.FindAllStringSubmatch(html, -1) {
				it := listItem{}
				if r := reDoubanRank.FindStringSubmatch(m[1]); r != nil {
					it.Rank, _ = strconv.Atoi(r[1])
				}
				if s := reDoubanSubject.FindStringSubmatch(m[1]); s != nil {
					it.DoubanID = s[1]
				}
				if ts := reDoubanTitles.FindAllStringSubmatch(m[1], -1); len(ts) > 0 {
					it.Title = strings.TrimSpace(decodeEntities(ts[0][1]))
					if len(ts) > 1 {
						it.Original = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(decodeEntities(ts[1][1])), "/"))
					}
				}
				if y := reDoubanYear.FindStringSubmatch(m[1]); y != nil {
					it.Year, _ = strconv.Atoi(y[1])
				}
				if r := reDoubanRating.FindStringSubmatch(m[1]); r != nil {
					it.Rating, _ = strconv.ParseFloat(r[1], 64)
				}
				c.Items = append(c.Items, it)
			}
			fmt.Fprintf(os.Stderr, "douban top250: %d entries\n", len(c.Items))
		}
		if len(c.Items) < 250 {
			return nil, fmt.Errorf("douban top250: only %d entries parsed", len(c.Items))
		}
		c.Fetched = time.Now().UnixMilli()
		l.writeCache("douban-top250.json", c)
	}
	imdb := map[string]string{}
	l.readCache("douban-imdb.json", &imdb, 0)
	for i := range c.Items {
		it := &c.Items[i]
		id, ok := imdb[it.DoubanID]
		if !ok && it.DoubanID != "" {
			html, err := l.doubanHTML("https://movie.douban.com/subject/" + it.DoubanID + "/")
			if err != nil {
				warn("douban subject %s (%s): %s", it.DoubanID, it.Title, err)
				if strings.Contains(err.Error(), "blocked") {
					break
				}
				continue
			}
			if m := reDoubanImdb.FindStringSubmatch(html); m != nil {
				id = m[1]
			}
			imdb[it.DoubanID] = id
			l.writeCache("douban-imdb.json", imdb)
			fmt.Fprintf(os.Stderr, "douban #%d %s → %s\n", it.Rank, it.Title, id)
		}
		it.ImdbID = id
	}
	return c, nil
}

// resolve finds the TMDB id (and a poster for films the library lacks): via TMDB /find when the IMDb id is known,
// otherwise by searching TMDB for the original (or Chinese) title within a year of the chart's year.
func (l *listRunner) resolve(it *listItem, have bool) {
	if l.g.tmdbKey == "" {
		return
	}
	var res []any
	if it.ImdbID != "" {
		r, err := l.g.cached("find_"+it.ImdbID, func() (any, error) {
			return l.g.tmdb("/find/"+it.ImdbID, [][2]string{{"external_source", "imdb_id"}, {"language", "zh-CN"}})
		})
		if err != nil {
			warn("tmdb find %s: %s", it.ImdbID, err)
			return
		}
		res, _ = getv(r, "movie_results").([]any)
	} else {
		for _, q := range []string{it.Original, it.Title} {
			if q == "" || len(res) > 0 {
				continue
			}
			r, err := l.g.cached(fmt.Sprintf("listsearch_%s_%d", normTitle(q), it.Year), func() (any, error) {
				return l.g.tmdb("/search/movie", [][2]string{{"query", q}, {"year", strconv.Itoa(it.Year)}, {"language", "zh-CN"}})
			})
			if err != nil {
				warn("tmdb search %s: %s", q, err)
				continue
			}
			all, _ := getv(r, "results").([]any)
			for _, x := range all {
				if normTitle(jsString(getv(x, "original_title"))) == normTitle(q) || normTitle(jsString(getv(x, "title"))) == normTitle(q) {
					res = []any{x}
					break
				}
			}
		}
	}
	if len(res) == 0 {
		return
	}
	id, _ := strconv.Atoi(jsString(getv(res[0], "id")))
	it.TmdbID = id
	if t := jsString(getv(res[0], "title")); l.zhTitles && t != "" {
		it.Title = t
	}
	if !have {
		if p, ok := l.g.poster(id, getv(res[0], "poster_path")).(string); ok {
			it.Poster = p
		}
	}
}

// nasUnmatched reads the NAS folders gen could not match (unmatched.md) as parsed names, so chart entries
// missing from the library can still be flagged as already on the NAS.
func nasUnmatched(root string) map[string]parsedName {
	out := map[string]parsedName{}
	b, err := os.ReadFile(filepath.Join(root, "unmatched.md"))
	if err != nil {
		return out
	}
	for _, m := range regexp.MustCompile("(?m)^- `([^`]+)`").FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = parseName(filepath.Base(m[1]))
	}
	return out
}

func matchChart(c *chart, lib []libMovie, l *listRunner) {
	byImdb, byTmdb := map[string]int{}, map[int]int{}
	for _, m := range lib {
		if m.ImdbID != "" {
			byImdb[m.ImdbID] = m.ID
		}
		byTmdb[m.ID] = m.ID
	}
	titleHit := func(it listItem) int {
		for _, m := range lib {
			if it.Year != 0 && m.Year != 0 && (m.Year-it.Year > 1 || it.Year-m.Year > 1) {
				continue
			}
			for _, a := range []string{it.Title, it.Original} {
				if a = normTitle(a); a == "" {
					continue
				}
				for _, b := range []string{m.Title, m.EnTitle, m.OriginalTitle} {
					if a == normTitle(b) {
						return m.ID
					}
				}
			}
		}
		return 0
	}
	c.Have = 0
	for i := range c.Items {
		it := &c.Items[i]
		it.MovieID, it.MatchedBy, it.Suspect = 0, "", 0
		if id, ok := byImdb[it.ImdbID]; ok && it.ImdbID != "" {
			it.MovieID, it.MatchedBy = id, "imdb"
		}
		l.resolve(it, it.MovieID != 0)
		if id, ok := byTmdb[it.TmdbID]; it.MovieID == 0 && ok && it.TmdbID != 0 {
			it.MovieID, it.MatchedBy = id, "tmdb"
		}
		if it.MovieID == 0 {
			if id := titleHit(*it); id != 0 {
				if it.ImdbID == "" && it.TmdbID == 0 {
					it.MovieID, it.MatchedBy = id, "title"
				} else {
					it.Suspect = id
				}
			}
		}
		if it.MovieID != 0 {
			c.Have++
		}
		it.OnNas = ""
		for path, p := range l.nas {
			if it.MovieID != 0 || (p.hasYr && it.Year != 0 && (int(p.year)-it.Year > 1 || it.Year-int(p.year) > 1)) {
				continue
			}
			for _, t := range p.titles {
				if n := normTitle(t); n != "" && (n == normTitle(it.Title) || n == normTitle(it.Original)) {
					it.OnNas = path
				}
			}
		}
	}
	c.Total = len(c.Items)
}

func runLists(args []string) error {
	root := findRoot()
	l := &listRunner{dir: filepath.Join(root, "cache/lists"), nas: nasUnmatched(root)}
	for _, a := range args {
		if a == "--refresh" {
			l.refresh = true
		}
	}
	l.g = &genMovies{root: root, cache: filepath.Join(root, "cache"), posters: filepath.Join(root, "cache/posters"), tmdbCache: filepath.Join(root, "cache/tmdb")}
	if envFile, err := parseEnvFile(filepath.Join(root, ".env")); err == nil {
		l.g.tmdbKey = envFile["TMDB_API_KEY"]
	}
	if l.g.tmdbKey == "" {
		warn("TMDB_API_KEY missing in .env; matching by IMDb id and title only")
	}
	for _, d := range []string{l.dir, l.g.posters, l.g.tmdbCache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	var lib []libMovie
	b, err := os.ReadFile(filepath.Join(root, "data/movies.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &lib); err != nil {
		return err
	}

	out := filepath.Join(root, "data/lists.json")
	prev := struct {
		Charts []chart `json:"charts"`
	}{}
	if b, err := os.ReadFile(out); err == nil {
		json.Unmarshal(b, &prev)
	}
	var charts []chart
	var failed []string
	for _, src := range []func() (*chart, error){l.doubanTop250, l.imdbTop250} {
		c, err := src()
		l.zhTitles = c != nil && c.ID == "imdb"
		if err != nil {
			warn("%s", err)
			failed = append(failed, err.Error())
			continue
		}
		matchChart(c, lib, l)
		charts = append(charts, *c)
		fmt.Printf("%s: have %d / %d\n", c.Name, c.Have, c.Total)
	}
	for _, p := range prev.Charts {
		kept := false
		for _, c := range charts {
			kept = kept || c.ID == p.ID
		}
		if !kept {
			matchChart(&p, lib, l)
			charts = append(charts, p)
		}
	}
	if len(charts) == 0 {
		return fmt.Errorf("no charts fetched: %s", strings.Join(failed, "; "))
	}
	data, _ := json.MarshalIndent(map[string]any{"updated": time.Now().UnixMilli(), "charts": charts}, "", " ")
	return os.WriteFile(out, data, 0o644)
}

// imdbDataset returns a local copy of an IMDb dataset file, downloading it (streamed to disk) when missing or a month old.
func (l *listRunner) imdbDataset(name string) (string, error) {
	f := filepath.Join(l.dir, name)
	if st, err := os.Stat(f); err == nil && !l.refresh && time.Since(st.ModTime()) < listMaxAge {
		return f, nil
	}
	fmt.Fprintf(os.Stderr, "downloading %s\n", name)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://datasets.imdbws.com/"+name, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", errors.New(fetchErrMsg(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s: HTTP %d", name, resp.StatusCode)
	}
	tmp := f + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(out, resp.Body); err == nil {
		err = out.Close()
	} else {
		out.Close()
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return f, os.Rename(tmp, f)
}

// eachTSV streams the rows of a gzipped IMDb TSV file (header skipped).
func eachTSV(f string, fn func([]string)) error {
	in, err := os.Open(f)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for first := true; sc.Scan(); first = false {
		if !first {
			fn(strings.Split(sc.Text(), "\t"))
		}
	}
	return sc.Err()
}

const imdbMinVotes = 25000

// imdbTop250 approximates the IMDb Top 250 from the public datasets: feature films (non-adult) with at least
// imdbMinVotes votes, ranked by the weighted rating WR = v/(v+m)·R + m/(v+m)·C, C being those films' mean rating.
func (l *listRunner) imdbTop250() (*chart, error) {
	c := &chart{ID: "imdb", Name: "IMDb Top 250", Source: "https://developer.imdb.com/non-commercial-datasets/",
		Note: "根据 IMDb 公开评分数据计算的近似榜单，排名可能与官网不同"}
	ratingsF, err := l.imdbDataset("title.ratings.tsv.gz")
	if err != nil {
		return nil, err
	}
	basicsF, err := l.imdbDataset("title.basics.tsv.gz")
	if err != nil {
		return nil, err
	}
	type cand struct {
		id, title, original string
		year, votes         int
		rating              float64
		movie               bool
	}
	cands := map[string]*cand{}
	if err := eachTSV(ratingsF, func(p []string) {
		if len(p) < 3 {
			return
		}
		v, _ := strconv.Atoi(p[2])
		if v < imdbMinVotes {
			return
		}
		r, _ := strconv.ParseFloat(p[1], 64)
		cands[p[0]] = &cand{id: p[0], rating: r, votes: v}
	}); err != nil {
		return nil, err
	}
	if err := eachTSV(basicsF, func(p []string) {
		if len(p) < 6 {
			return
		}
		if x := cands[p[0]]; x != nil && p[1] == "movie" && p[4] == "0" {
			x.movie, x.title, x.original = true, p[2], p[3]
			x.year, _ = strconv.Atoi(p[5])
		}
	}); err != nil {
		return nil, err
	}
	var list []*cand
	sum := 0.0
	for _, x := range cands {
		if x.movie {
			list = append(list, x)
			sum += x.rating
		}
	}
	if len(list) < 250 {
		return nil, fmt.Errorf("imdb datasets: only %d films with %d+ votes", len(list), imdbMinVotes)
	}
	C, m := sum/float64(len(list)), float64(imdbMinVotes)
	wr := func(x *cand) float64 { v := float64(x.votes); return v/(v+m)*x.rating + m/(v+m)*C }
	sort.Slice(list, func(i, j int) bool { return wr(list[i]) > wr(list[j]) })
	for i, x := range list[:250] {
		c.Items = append(c.Items, listItem{Rank: i + 1, Title: x.title, Original: x.original, Year: x.year, Rating: x.rating, Votes: x.votes, ImdbID: x.id})
	}
	if st, err := os.Stat(ratingsF); err == nil {
		c.Fetched = st.ModTime().UnixMilli()
	}
	return c, nil
}
