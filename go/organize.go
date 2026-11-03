package main

// shelf organize-books: plans (default) or applies (--apply) a reorganisation of /Volumes/store/book into
// "书名 - 作者" folders with metadata.opf + cover.jpg sidecars, based on data/books.json. Port of organize-books.mjs:
//   shelf organize-books                         → writes book-reorg-plan.md + logs/book-reorg-plan.tsv, touches nothing
//   shelf organize-books --apply                 → performs the moves in logs/book-reorg-plan.tsv, logs old/new paths
//   shelf organize-books --rollback <log.tsv>    → undoes an apply log

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const miscDir = "_misc"

var organizeCategories = []string{"计算机", "数学", "文学 & 小说", "政治 & 哲学", "心理学 & 教育", "历史 & 社会", "经营 & 管理 & 产品", "金融 & 投资 & 理财", "未分类"}

type catRule struct {
	cat string
	re  *jsRe
}

var organizeRules = []catRule{
	{"计算机", re(`计算机|编程|程序|软件|前端|javascript|操作系统|算法|数据结构|网络|http|git|rust|c\+\+|c语言|^c$|css|html|node|运维|编译|分布式|web|架构|prometheus`, "i")},
	{"文学 & 小说", re(`小说|文学|诗歌|诗词|散文|杂文|武侠|科幻|推理|童话|诗经|名著`, "")},
	{"金融 & 投资 & 理财", re(`投资|理财|金融|经济|财商|基金`, "")},
	{"经营 & 管理 & 产品", re(`管理学|企业管理|商业|营销|产品|创业|企业|领导力|品牌|互联网|战略|^管理$`, "")},
	{"历史 & 社会", re(`历史|社会学|^社会$|传记|中国史|近代史|古代史|纪实|企业史`, "")},
	{"政治 & 哲学", re(`哲学|政治|国学|心学|伦理|儒学|国际关系`, "")},
	{"心理学 & 教育", re(`心理|学习|方法论|思维|成长|自我管理|个人管理|时间管理|沟通|写作|逻辑|习惯|效率|阅读|教育|情商|励志|人际|自控|社交|职场`, "")},
	{"数学", re(`数学|统计|微积分|概率`, "")},
}

// organizeOverrides are manual category decisions where Douban tags are ambiguous (by book id).
var organizeOverrides = map[string]string{
	"db35135792": "经营 & 管理 & 产品", "db30597601": "经营 & 管理 & 产品", "db1941558": "经营 & 管理 & 产品", "db3646976": "政治 & 哲学",
	"db1883245": "文学 & 小说", "db35546622": "金融 & 投资 & 理财", "db35383731": "数学", "db35292688": "数学", "db4140492": "金融 & 投资 & 理财",
	"db25763679": "心理学 & 教育", "db37235927": "政治 & 哲学", "db1467022": "数学", "db37294999": "历史 & 社会", "db26698660": "文学 & 小说",
	"db35593780": "历史 & 社会", "db26576861": "历史 & 社会", "db27074369": "经营 & 管理 & 产品", "db26849305": "经营 & 管理 & 产品",
	"db26929955": "经营 & 管理 & 产品", "db27179563": "经营 & 管理 & 产品", "db26390229": "文学 & 小说", "db30539780": "历史 & 社会",
	"db1084435": "数学", "db26677472": "数学", "db21966353": "金融 & 投资 & 理财", "db26607419": "政治 & 哲学", "db27172839": "政治 & 哲学",
	"db4723970": "经营 & 管理 & 产品", "db36336321": "经营 & 管理 & 产品", "db24700113": "经营 & 管理 & 产品", "db1039311": "心理学 & 教育",
	"db35006472": "心理学 & 教育", "db1935312": "历史 & 社会", "db3772799": "文学 & 小说", "db36062395": "文学 & 小说", "db34442149": "心理学 & 教育",
	"db35272152": "经营 & 管理 & 产品", "db4927701": "政治 & 哲学", "db6534729": "政治 & 哲学", "db25813440": "心理学 & 教育", "db26606009": "心理学 & 教育",
	"db33391219": "心理学 & 教育", "db1786387": "心理学 & 教育", "db1005576": "心理学 & 教育", "hd2b8f8b229": "心理学 & 教育", "h724676e187": "心理学 & 教育",
	"h39a1701c08": "心理学 & 教育", "hde847fbc5c": "心理学 & 教育", "h3fdb545f64": "心理学 & 教育", "h5e5b3d3bdb": "历史 & 社会", "h627711ab17": "政治 & 哲学",
	"h67fe73b1e0": "政治 & 哲学",
}

type nameOverride struct {
	title, author string
	hasEd         bool // ed: null drops a misleading edition
}

// organizeNames are proper names for books Douban could not match.
var organizeNames = map[string]nameOverride{
	"h5e5b3d3bdb": {"理想国译丛（套装32册）", "", false}, "haf83871480": {"沟通的艺术", "罗纳德·阿德勒", false}, "h627711ab17": {"中国36问", "", false},
	"h67fe73b1e0": {"The Dictator's Handbook", "Bruce Bueno de Mesquita", false}, "hdba99ca5a9": {"深入理解计算机系统", "Randal E. Bryant", false},
	"h6da9c9c8c0": {"深入理解计算机系统", "Randal E. Bryant", false}, "hb53cbd8f5d": {"JavaScript高级程序设计", "马特·弗里斯比", false},
	"h9f8b76c1d6": {"Hands-On Data Structures and Algorithms with Rust", "Claus Matzinger", false},
	"h26ec87ff3c": {"Hands-On Functional Programming in Rust", "Andrew Johnson", false},
	"h7ef975134f": {"Hands-On Data Structures and Algorithms with JavaScript", "Kashyap Mukkamala", false},
	"hf3eb27460c": {"松本行弘的程序世界", "松本行弘", false}, "hf24bd75994": {"计算机网络：自顶向下方法", "James F·Kurose", false},
	"hbde998bcc8": {"图解HTTP", "上野宣", false}, "he2f9898953": {"你不知道的JavaScript", "辛普森", false}, "h20319508db": {"罪与罚", "陀思妥耶夫斯基", false},
	"h5278cba3a5": {"WebKit技术内幕", "朱永盛", false}, "hd2b8f8b229": {"心理学全集（套装全5册）", "", false},
	"h724676e187": {"埃利斯经典作品集（套装共3册）", "阿尔伯特·埃利斯", false}, "h39a1701c08": {"批判性思维（套装共6册）", "", true},
	"h3fdb545f64": {"迎刃自信方法论", "", false},
}

// organizeMerge maps unmatched files that are another format of a matched book to that book.
var organizeMerge = map[string]string{"h9f8b76c1d6": "db34988997", "h20319508db": "db30278620"}

var csSubRules = []catRule{
	{"前端开发", re(`前端|javascript|css|html|web|node`, "i")},
	{"操作系统", re(`操作系统|计算机系统|^os$`, "i")},
	{"网络", re(`网络|http|tcp`, "i")},
	{"数据结构与算法", re(`算法|数据结构`, "")},
	{"编程语言", re(`rust|c\+\+|c语言|^c$|python|go|git|编程语言|程序设计`, "i")},
	{"架构应用", re(`分布式|架构|运维|云计算|prometheus|数据库`, "i")},
	{"编程思想", re(`软件工程|编程思想|敏捷|编译|计算机原理|编码|项目管理`, "")},
}

var csSubOverrides = map[string]string{"db30329536": "架构应用", "db1102259": "编程思想", "db4822685": "编程思想", "db34788443": "编程思想", "h6da9c9c8c0": "操作系统", "hf24bd75994": "网络", "db36081529": "网络"}

func bookID(b *Obj) string { return jsString(b.Get("id")) }

func categorize(b *Obj) (string, string) {
	id := bookID(b)
	if c := organizeOverrides[id]; c != "" {
		return c, "manual"
	}
	if _, overridden := organizeOverrides[id]; b.Get("category") != "未分类" && !overridden {
		return jsString(b.Get("category")), "keep"
	}
	var order []string
	score := map[string]int{}
	tags := arr(b.Get("tags"))
	for i, t := range tags[:min(8, len(tags))] {
		for _, r := range organizeRules {
			if r.re.Test(jsString(t)) {
				if _, ok := score[r.cat]; !ok {
					order = append(order, r.cat)
				}
				score[r.cat] += 8 - i
				break
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return score[order[j]] < score[order[i]] })
	if len(order) == 0 {
		return "未分类", "none"
	}
	if len(order) == 1 || float64(score[order[0]]) >= float64(score[order[1]])*1.5 {
		return order[0], "tags"
	}
	return order[0], "tags?"
}

func csSub(b *Obj, filePath string) string {
	cur := strings.Split(filePath, "/")
	at := func(i int) string {
		if i < len(cur) {
			return cur[i]
		}
		return "\x00undefined"
	}
	known := false
	for _, r := range csSubRules {
		if r.cat == at(1) {
			known = true
		}
	}
	if cur[0] == "计算机" && len(cur) > 2 && known || at(1) == "编程语言" || at(1) == "编程思想" {
		return at(1)
	}
	if s := csSubOverrides[bookID(b)]; s != "" {
		return s
	}
	tags := arr(b.Get("tags"))
	for _, t := range tags[:min(6, len(tags))] {
		for _, r := range csSubRules {
			if r.re.Test(jsString(t)) {
				return r.cat
			}
		}
	}
	return ""
}

var (
	reNation      = re(`[\[［【(（]\s*(?:美|美国|英|英国|法|德|日|俄|奥|奥地利|加|加拿大|澳|以色列|荷|希|挪威|爱尔兰|意大利|韩|黎巴嫩|哥伦比亚|印|清|明|汉|宋|唐)\s*[\]］】)）]\s*`, "g")
	reAmp39       = re(`&#39;`, "g")
	reAmpAmp      = re(`&amp;`, "g")
	reAmpQuot     = re(`&quot;`, "g")
	rePipeTail    = re(`\|.*$`, "")
	reCJKInParens = re(`^[^（(]*[A-Za-z][^（(]*[（(]([^)）]*[一-鿿][^)）]*)[)）]$`, "")
	reLatinParens = re(`\s*[(（][^)）]*[A-Za-z][^)）]*[)）]\s*`, "g")
	reAuthorTail  = re(`\s*(著|编著|编订|主编|编|撰|等|译者)$`, "")
	reZhuanTail   = re(`撰$`, "")
	reJunkName    = re(`^(详细书签|unknown)$`, "i")
)

func decodeBasic(s string) string {
	return reAmpQuot.Replace(reAmpAmp.Replace(reAmp39.Replace(s, "'"), "&"), `"`)
}

func cleanAuthor(a string) string {
	s := jsTrim(rePipeTail.Replace(reNation.Replace(decodeBasic(a), ""), ""))
	if m, _ := reCJKInParens.Match(s); m != nil {
		s = m[1]
	} else if hasCJK(s) {
		s = reLatinParens.Replace(s, "")
	}
	s = jsTrim(reSpacesG.Replace(reZhuanTail.Replace(reAuthorTail.Replace(s, ""), ""), " "))
	if reJunkName.Test(s) {
		return ""
	}
	return s
}

var (
	cnNum         = map[string]float64{"一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10}
	reEditionCN   = re(`第\s*([\d一二三四五六七八九十]+)\s*版`, "")
	reEditionEN   = re(`(\d+)\s*(?:st|nd|rd|th)\s+edition`, "i")
	reVolume      = re(`[（(]\s*([上中下])\s*卷\s*[)）]|([上中下])卷`, "")
	reTitleEd     = re(`[(（]\s*第?\s*[\d一二三四五六七八九十]+\s*版[^)）]*[)）]|(原书)?第\s*[\d一二三四五六七八九十]+\s*版`, "g")
	reTitleEdEN   = re(`\d+(?:st|nd|rd|th)\s+edition`, "ig")
	reLenticular  = re(`【[^】]*】`, "g")
	reTitleVol    = re(`[(（]\s*[上中下]卷\s*[)）]`, "g")
	reTitleJunk   = re(`_完整版|\s*彩色版$|\.?\[[^\]]*\].*$`, "g")
	reTrailComma  = re(`[,，]\s*$`, "")
	reOpenParen   = re(`\s*[(（]\s*`, "g")
	reCloseParen  = re(`\s*[)）]`, "g")
	reEmptyParen  = re(`（\s*）`, "g")
	reSlashes     = re(`[\\/]`, "g")
	reColonG      = re(`:`, "g")
	reUnsafeChars = re(`[*?"<>|]`, "g")
)

// editionOf returns nil when there is no edition, else the edition number (NaN for numerals like 十二).
func editionOf(s string) any {
	m, _ := reEditionCN.Match(s)
	if m == nil {
		m, _ = reEditionEN.Match(s)
	}
	if m == nil {
		return nil
	}
	if n, ok := cnNum[m[1]]; ok {
		return n
	}
	return strToNumber(m[1])
}

func volumeOf(s string) any {
	m, ok := reVolume.Match(s)
	for i := 1; m != nil && i < len(m); i++ {
		if ok[i] && m[i] != "" {
			return m[i]
		}
	}
	return nil
}

func cleanTitle(t string) string {
	s := reTitleEd.Replace(decodeBasic(t), "")
	s = reTitleEdEN.Replace(s, "")
	s = reLenticular.Replace(s, "")
	s = reTitleVol.Replace(s, "")
	s = reTitleJunk.Replace(s, "")
	s = reTrailComma.Replace(s, "")
	s = reOpenParen.Replace(s, "（")
	s = reCloseParen.Replace(s, "）")
	s = reEmptyParen.Replace(s, "")
	return jsTrim(reSpacesG.Replace(s, " "))
}

func safeName(s string) string {
	return jsTrim(reSpacesG.Replace(reUnsafeChars.Replace(reColonG.Replace(reSlashes.Replace(s, "／"), "："), ""), " "))
}

// posixJoin is path.posix.join.
func posixJoin(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	if len(keep) == 0 {
		return "."
	}
	return path.Clean(strings.Join(keep, "/"))
}

type planRow struct {
	id, md5, format, from, dir, sub, folder, base string
	size                                          any
	vol, year, title                              any
	cat, how                                      string
	flags                                         []string
	dup                                           string
	keep                                          *planRow
	to                                            string
}

type planFolder struct {
	dir   string
	book  *Obj
	cat   string
	sub   string
	title string
}

var (
	reEpubwPrefix = re(`^epubw\.com\s*-\s*`, "i")
	reZlibTag     = re(`\s*[(（]z-lib\.org[)）]`, "i")
	reByTail      = re(`\s+by\s+.*$`, "i")
	reExtTail     = re(`(\.[^.]+)$`, "")
	reAuthorPart  = re(` - [^-]+$`, "")
)

func edString(ed any) string {
	if truthy(ed) {
		return "（第" + jsString(ed) + "版）"
	}
	return ""
}

func organizePlan(books []*Obj) ([]*planRow, []*planFolder) {
	var rows []*planRow
	byID := map[string]*Obj{}
	for _, b := range books {
		byID[bookID(b)] = b
	}
	for _, b := range books {
		id := bookID(b)
		cat, how := categorize(b)
		var flags []string
		if b.Get("source") == "file" {
			if _, ok := organizeNames[id]; ok {
				if organizeMerge[id] != "" {
					flags = append(flags, "未匹配豆瓣，并入同一本书")
				} else {
					flags = append(flags, "未匹配豆瓣，名字已手工修正")
				}
			} else {
				flags = append(flags, "未匹配豆瓣，名字来自文件名")
			}
		}
		if how == "tags?" {
			flags = append(flags, "分类存疑")
		}
		if how == "none" {
			flags = append(flags, "无标签，放未分类")
		}
		if b.Get("category") != "未分类" && cat != b.Get("category") {
			flags = append(flags, "改分类："+jsString(b.Get("category"))+" → "+cat)
		}
		for _, fv := range arr(b.Get("files")) {
			f := fv.(*Obj)
			fp := jsString(f.Get("path"))
			fileBase := reLastExt.Replace(path.Base(fp), "")
			own := jsString(b.Get("title"))
			if b.Get("source") == "file" {
				own = reByTail.Replace(reZlibTag.Replace(reEpubwPrefix.Replace(fileBase, ""), ""), "")
			}
			o, hasO := organizeNames[id]
			title := cleanTitle(own)
			if hasO {
				title = o.title
			}
			var ed any
			if hasO && o.hasEd {
				ed = nil
			} else if ed = editionOf(fileBase); ed == nil {
				ed = editionOf(jsString(b.Get("title")))
			}
			vol := volumeOf(fileBase)
			if vol == nil {
				vol = volumeOf(jsString(b.Get("title")))
			}
			fflags := append([]string(nil), flags...)
			if ef, et := editionOf(fileBase), editionOf(jsString(b.Get("title"))); truthy(ef) && truthy(et) && ef != et {
				fflags = append(fflags, "版本存疑：文件是第"+jsString(ef)+"版，豆瓣匹配到第"+jsString(et)+"版")
			}
			author := ""
			if hasO {
				author = o.author
			} else {
				a := ""
				if as := arr(b.Get("authors")); len(as) > 0 && truthy(as[0]) {
					a = jsString(as[0])
				}
				author = cleanAuthor(a)
			}
			join := func(t string) string {
				if author != "" && !strings.Contains(t, author) {
					t = t + " - " + author
				}
				return utf16Slice(safeName(t), 100)
			}
			volS := ""
			if truthy(vol) {
				volS = "（" + jsString(vol) + "卷）"
			}
			sub := ""
			if cat == "计算机" {
				sub = csSub(b, fp)
			}
			rid := id
			if m := organizeMerge[id]; m != "" {
				rid = m
			}
			rows = append(rows, &planRow{id: rid, md5: jsString(f.Get("id")), size: f.Get("size"), format: jsString(f.Get("format")), from: fp, dir: "", sub: sub,
				folder: join(title + edString(ed)), base: join(title + volS + edString(ed)), vol: vol, year: getv(b, "year"), title: b.Get("title"), cat: cat, how: how, flags: fflags})
		}
	}

	var hashOrder []string
	byHash := map[string][]*planRow{}
	for _, r := range rows {
		k := r.md5 + jsString(r.size)
		if _, ok := byHash[k]; !ok {
			hashOrder = append(hashOrder, k)
		}
		byHash[k] = append(byHash[k], r)
	}
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	for _, k := range hashOrder {
		group := byHash[k]
		if len(group) < 2 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			a, b := group[i], group[j]
			if d := b2i(strings.Contains(b.from, "/")) - b2i(strings.Contains(a.from, "/")); d != 0 {
				return d < 0
			}
			return utf16Len(a.from) < utf16Len(b.from)
		})
		for _, r := range group[1:] {
			r.dup, r.keep = group[0].from, group[0]
		}
	}

	owner := map[string]*planRow{}
	for _, r := range rows {
		if r.dup != "" {
			continue
		}
		key := posixJoin(r.dir, r.folder)
		clash := func(k string) bool {
			o := owner[strings.ToLower(k)]
			return o != nil && o.id != r.id && !(truthy(r.vol) && truthy(o.vol))
		}
		for k := 2; clash(key); k++ {
			suffix := fmt.Sprintf(" (%d)", k)
			if k == 2 && truthy(r.year) {
				suffix = "（" + jsString(r.year) + "）"
			}
			key = posixJoin(r.dir, r.folder+suffix)
			if !contains(r.flags, "同名不同书，已加后缀") {
				r.flags = append(r.flags, "同名不同书，已加后缀")
			}
			if k == 2 && truthy(r.year) || k > 2 {
				r.base += suffix
				r.folder += suffix
			}
		}
		if owner[strings.ToLower(key)] == nil {
			owner[strings.ToLower(key)] = r
		}
		r.to = posixJoin(r.dir, r.folder, r.base+"."+r.format)
	}

	seen := map[string]int{}
	for _, r := range rows {
		if r.dup != "" {
			continue
		}
		k := strings.ToLower(r.to)
		if _, ok := seen[k]; !ok {
			seen[k] = 1
			continue
		}
		seen[k]++
		r.to = reExtTail.Replace(r.to, fmt.Sprintf(" (%d)$1", seen[k]))
		r.flags = append(r.flags, "同一本书同格式有多个不同文件")
	}

	dupCount := map[string]int{}
	for _, r := range rows {
		if r.dup == "" {
			continue
		}
		dupCount[r.keep.to]++
		n := dupCount[r.keep.to]
		ns := ""
		if n > 1 {
			ns = fmt.Sprint(n)
		}
		r.to = reExtTail.Replace(r.keep.to, " (重复"+ns+")$1")
	}

	var folders []*planFolder
	fidx := map[string]int{}
	for _, r := range rows {
		if r.dup != "" {
			continue
		}
		dir := path.Dir(r.to)
		pf := &planFolder{dir, byID[r.id], r.cat, r.sub, reAuthorPart.Replace(r.folder, "")}
		if i, ok := fidx[dir]; !ok {
			fidx[dir] = len(folders)
			folders = append(folders, pf)
		} else if folders[i].book.Get("source") == "file" && byID[r.id].Get("source") != "file" {
			folders[i] = pf
		}
	}
	return rows, folders
}

var xmlEsc4 = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func xmlEsc4s(v any) string { return xmlEsc4.Replace(strOr(v)) }

// renderOpf is a Calibre-compatible metadata.opf for one book folder.
func renderOpf(b *Obj, folderTitle, cat, sub string) string {
	h := md5.Sum([]byte("book:" + bookID(b)))
	x := hex.EncodeToString(h[:])
	uuid := x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
	title := decodeBasic(jsString(b.Get("title")))
	if b.Get("source") == "file" {
		title = folderTitle
	}
	lang := "en"
	if hasCJK(jsString(b.Get("title"))) {
		lang = "zh"
	}
	var lines []string
	add := func(cond any, s func() string) {
		if truthy(cond) {
			lines = append(lines, s())
		}
	}
	lines = append(lines, `<dc:identifier opf:scheme="uuid" id="uuid_id">`+uuid+`</dc:identifier>`)
	add(b.Get("isbn"), func() string {
		return `<dc:identifier opf:scheme="ISBN">` + xmlEsc4s(b.Get("isbn")) + `</dc:identifier>`
	})
	add(b.Get("doubanId"), func() string {
		return `<dc:identifier opf:scheme="DOUBAN">` + xmlEsc4s(b.Get("doubanId")) + `</dc:identifier>`
	})
	lines = append(lines, `<dc:title>`+xmlEsc4s(title)+`</dc:title>`)
	for _, a := range arr(b.Get("authors")) {
		lines = append(lines, `<dc:creator opf:role="aut">`+xmlEsc4s(decodeBasic(jsString(a)))+`</dc:creator>`)
	}
	for _, a := range arr(b.Get("translators")) {
		lines = append(lines, `<dc:creator opf:role="trl">`+xmlEsc4s(decodeBasic(jsString(a)))+`</dc:creator>`)
	}
	add(b.Get("publisher"), func() string { return `<dc:publisher>` + xmlEsc4s(b.Get("publisher")) + `</dc:publisher>` })
	add(b.Get("year"), func() string { return `<dc:date>` + xmlEsc4s(b.Get("year")) + `-01-01T00:00:00+00:00</dc:date>` })
	add(b.Get("intro"), func() string { return `<dc:description>` + xmlEsc4s(b.Get("intro")) + `</dc:description>` })
	lines = append(lines, `<dc:language>`+lang+`</dc:language>`, `<dc:subject>`+xmlEsc4s(cat)+`</dc:subject>`)
	add(sub, func() string { return `<dc:subject>` + xmlEsc4s(sub) + `</dc:subject>` })
	for _, t := range arr(b.Get("tags")) {
		if t != cat && t != sub {
			lines = append(lines, `<dc:subject>`+xmlEsc4s(decodeBasic(jsString(t)))+`</dc:subject>`)
		}
	}
	add(b.Get("series"), func() string { return `<meta name="calibre:series" content="` + xmlEsc4s(b.Get("series")) + `"/>` })
	add(b.Get("rating"), func() string {
		return `<meta name="calibre:rating" content="` + jsNum(jsRound(toNumber(b.Get("rating")))) + `"/>`
	})
	add(b.Get("rating"), func() string { return `<meta name="douban:rating" content="` + jsString(b.Get("rating")) + `"/>` })
	add(b.Get("votes"), func() string { return `<meta name="douban:votes" content="` + jsString(b.Get("votes")) + `"/>` })
	add(b.Get("subtitle"), func() string { return `<meta name="douban:subtitle" content="` + xmlEsc4s(b.Get("subtitle")) + `"/>` })
	add(b.Get("origTitle"), func() string {
		return `<meta name="douban:original_title" content="` + xmlEsc4s(b.Get("origTitle")) + `"/>`
	})
	add(b.Get("pages"), func() string { return `<meta name="douban:pages" content="` + jsString(b.Get("pages")) + `"/>` })
	lines = append(lines, `<meta name="home:category" content="`+xmlEsc4s(cat)+`"/>`)
	add(sub, func() string { return `<meta name="home:subcategory" content="` + xmlEsc4s(sub) + `"/>` })
	lines = append(lines, `<meta name="home:source" content="`+jsString(b.Get("source"))+`"/>`)
	return `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    ` + strings.Join(lines, "\n    ") + `
  </metadata>
  <guide>
    <reference type="cover" title="Cover" href="cover.jpg"/>
  </guide>
</package>
`
}

func walkAll(dir, rel string) ([]string, error) {
	es, err := readDirents(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range es {
		if strings.HasPrefix(d.name, ".") || d.name == "#recycle" || strings.HasPrefix(d.name, "@") {
			continue
		}
		r := d.name
		if rel != "" {
			r = rel + "/" + d.name
		}
		if d.isDir {
			sub, err := walkAll(filepath.Join(dir, d.name), r)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
		} else {
			out = append(out, r)
		}
	}
	return out, nil
}

type organizer struct{ root, bookDir, planTSV, planMD, sidecars, covers string }

func newOrganizer() *organizer {
	root := findRoot()
	return &organizer{root, env("BOOK_DIR", "/Volumes/store/book"), filepath.Join(root, "logs/book-reorg-plan.tsv"), filepath.Join(root, "book-reorg-plan.md"),
		filepath.Join(root, "logs/book-reorg-sidecars"), filepath.Join(root, "cache/covers")}
}

func loadBooks(root string) ([]*Obj, error) {
	v, err := readJSONFile(filepath.Join(root, "data/books.json"))
	if err != nil {
		return nil, err
	}
	return books(arr(v)), nil
}

var reImageFile = re(`\.(jpe?g|png)$`, "i")

func (o *organizer) writePlan() error {
	bs, err := loadBooks(o.root)
	if err != nil {
		return err
	}
	rows, folders := organizePlan(bs)
	known := map[string]bool{}
	for _, r := range rows {
		known[r.from] = true
	}
	all, err := walkAll(o.bookDir, "")
	if err != nil {
		return err
	}
	var misc, extra []string
	for _, p := range all {
		if known[p] || p == "process_books.py" {
			continue
		}
		if reImageFile.Test(p) && strings.Contains(p, "/") {
			misc = append(misc, p)
		} else {
			extra = append(extra, p)
		}
	}
	var moves []*planRow
	for _, r := range rows {
		if r.from != r.to {
			moves = append(moves, r)
		}
	}

	if err := os.RemoveAll(o.sidecars); err != nil {
		return err
	}
	var writes [][2]string
	for _, f := range folders {
		stage := filepath.Join(o.sidecars, f.dir)
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "metadata.opf"), []byte(renderOpf(f.book, f.title, f.cat, f.sub)), 0o644); err != nil {
			return err
		}
		writes = append(writes, [2]string{filepath.Join(stage, "metadata.opf"), f.dir + "/metadata.opf"})
		if c := f.book.Get("cover"); truthy(c) {
			if err := copyFile(filepath.Join(o.covers, jsString(c)), filepath.Join(stage, "cover.jpg")); err != nil {
				return err
			}
			writes = append(writes, [2]string{filepath.Join(stage, "cover.jpg"), f.dir + "/cover.jpg"})
		}
	}

	if err := os.MkdirAll(filepath.Dir(o.planTSV), 0o755); err != nil {
		return err
	}
	tsv := []string{"op\tfrom\tto"}
	for _, r := range moves {
		tsv = append(tsv, "mv\t"+r.from+"\t"+r.to)
	}
	for _, p := range misc {
		tsv = append(tsv, "mv\t"+p+"\t"+miscDir+"/"+strings.ReplaceAll(p, "/", "__"))
	}
	for _, w := range writes {
		tsv = append(tsv, "write\t"+w[0]+"\t"+w[1])
	}
	if err := os.WriteFile(o.planTSV, []byte(strings.Join(tsv, "\n")+"\n"), 0o644); err != nil {
		return err
	}

	count := func(rs []*planRow, f func(*planRow) bool) int {
		n := 0
		for _, r := range rs {
			if f(r) {
				n++
			}
		}
		return n
	}
	byCat := map[string]int{}
	for _, f := range folders {
		byCat[f.cat]++
	}
	var flagged []*planRow
	for _, r := range rows {
		if len(r.flags) > 0 && r.dup == "" {
			flagged = append(flagged, r)
		}
	}
	cell := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	line := func(r *planRow) string {
		c := r.cat
		if r.sub != "" {
			c += " / " + r.sub
		}
		return "| " + cell(r.from) + " | " + cell(r.to) + " | " + cell(c) + " | " + cell(strings.Join(r.flags, "；")) + " |"
	}
	section := func(title string, rs []*planRow) string {
		if len(rs) == 0 {
			return ""
		}
		ls := make([]string, len(rs))
		for i, r := range rs {
			ls[i] = line(r)
		}
		return fmt.Sprintf("\n### %s（%d）\n\n| 现在 | 整理后 | 分类（写入 OPF） | 备注 |\n| --- | --- | --- | --- |\n%s\n", title, len(rs), strings.Join(ls, "\n"))
	}
	var sample *planFolder
	for _, f := range folders {
		if truthy(f.book.Get("doubanId")) && len(arr(f.book.Get("translators"))) > 0 {
			sample = f
			break
		}
	}
	if sample == nil {
		sample = folders[0]
	}
	var dupRows []*planRow
	for _, r := range rows {
		if r.dup != "" {
			c := *r
			c.flags = []string{"和 " + r.dup + " 完全相同"}
			dupRows = append(dupRows, &c)
		}
	}
	var catStats []string
	for _, c := range organizeCategories {
		if n, ok := byCat[c]; ok {
			catStats = append(catStats, fmt.Sprintf("%s %d", c, n))
		}
	}
	var catSections strings.Builder
	for _, c := range organizeCategories {
		var rs []*planRow
		for _, r := range moves {
			if r.dup == "" && r.cat == c && len(r.flags) == 0 {
				rs = append(rs, r)
			}
		}
		catSections.WriteString(section(c, rs))
	}
	miscSection := ""
	if len(misc) > 0 {
		ls := make([]string, len(misc))
		for i, p := range misc {
			ls[i] = "- " + p + " → " + miscDir + "/"
		}
		miscSection = fmt.Sprintf("\n### 图片（%d）\n\n%s\n", len(misc), strings.Join(ls, "\n"))
	}
	extraS := ""
	if len(extra) > 0 {
		extraS = "、" + strings.Join(extra, "、")
	}
	bt := "`"
	md := "# 书库整理方案（预演，尚未执行）\n\n" +
		"生成时间：" + localeStringZH(time.Now()) + "。来源：" + bt + "data/books.json" + bt + fmt.Sprintf("（%d 本 / %d 个文件）。\n\n", len(bs), len(rows)) +
		"## 规则\n\n" +
		"- 结构和 " + bt + "store/movie" + bt + " 一样摊平：" + bt + "/Volumes/store/book/<书名 - 作者>/" + bt + "，不再按分类建目录。\n" +
		"- 每个书目录里：书文件 " + bt + "书名 - 作者.扩展名" + bt + "（同一本书的不同格式共用一个名字）+ " + bt + "cover.jpg" + bt + " + " + bt + "metadata.opf" + bt + "（Calibre 兼容）。\n" +
		"- 书名用豆瓣书名（不带副标题），作者用豆瓣第一作者并去掉 [美] 之类的国籍括号；版次「（第N版）」，分卷「（上卷）」。\n" +
		"  不同版本是不同目录；分卷放在同一个目录里。去掉 z-lib / ePUBw / 琳宝书屋 等来源标记，非法字符替换成全角。\n" +
		"- 分类不再体现在路径上，而是写进 " + bt + "metadata.opf" + bt + "：第一个 " + bt + "dc:subject" + bt + " 和 " + bt + `<meta name="home:category">` + bt + "\n" +
		"  （计算机类另有 " + bt + "home:subcategory" + bt + "）。分类沿用现有 9 个名字。书架页和 OPDS 按这个字段分组。\n" +
		"- 内容完全相同的重复文件（大小 + KOReader 哈希一致）放进同一个书目录，命名「书名 - 作者 (重复).扩展名」，不删除；书架和 OPDS 只显示一份。\n" +
		"  内容不同的同格式文件命名「… (2).扩展名」。不同版本是不同目录。\n" +
		"- 旧的多格式文件夹里的广告/封面图片移到 " + bt + miscDir + "/" + bt + "。整理后变空的目录（包括 9 个分类目录）会删掉，回滚时会重建。\n" +
		"- 只改路径不改文件内容，KOReader 的进度同步（按文件内容哈希）不受影响。process_books.py、.claude 不动。\n\n" +
		"## 统计\n\n" +
		fmt.Sprintf("- 书目录：%d 个（metadata.opf %d 个，cover.jpg %d 个）\n", len(folders), len(folders), len(writes)-len(folders)) +
		fmt.Sprintf("- 书文件 %d 个：移动/改名 %d，其中完全重复的文件 %d 个（放进各自书目录，标「(重复)」），保持不变 %d\n", len(rows), len(moves), count(rows, func(r *planRow) bool { return r.dup != "" }), len(rows)-len(moves)) +
		"- 图片 → " + bt + miscDir + "/" + bt + fmt.Sprintf("：%d；其他不动的文件：process_books.py%s\n", len(misc), extraS) +
		fmt.Sprintf("- 需要人工看一眼（flag）：%d 个文件\n", len(flagged)) +
		"- 各分类的书目录数：" + strings.Join(catStats, "，") + "\n\n" +
		"## 需要确认的条目\n" + section("分类、版本或名字存疑", flagged) +
		"\n## 重复文件\n" + section("放进同一书目录（原来那份写在备注里）", dupRows) +
		"\n## 全部移动清单（按分类）\n" + catSections.String() + "\n" + miscSection +
		"\n## metadata.opf 示例：" + sample.dir + "\n\n" +
		"```xml\n" + jsTrim(renderOpf(sample.book, sample.title, sample.cat, sample.sub)) + "\n```\n\n" +
		"全部 OPF 和封面已预生成在 " + bt + "logs/book-reorg-sidecars/" + bt + "，可以先看。\n"
	if err := os.WriteFile(o.planMD, []byte(md), 0o644); err != nil {
		return err
	}
	info("plan: %d book folders, %d files, %d moves, %d duplicates, %d flagged, %d images, %d sidecars",
		len(folders), len(rows), len(moves), count(rows, func(r *planRow) bool { return r.dup != "" }), len(flagged), len(misc), len(writes))
	info("wrote %s and %s", jsRelative(o.root, o.planMD), jsRelative(o.root, o.planTSV))
	return nil
}

func (o *organizer) nas(p string) string { return filepath.Join(o.bookDir, p) }

// pruneEmptyDirs removes empty folders below dir (deepest first), logging each for rollback.
func (o *organizer) pruneEmptyDirs(log func(op, from, to string) error, dir string) error {
	es, err := readDirents(dir)
	if err != nil {
		return err
	}
	for _, d := range es {
		if !d.isDir || strings.HasPrefix(d.name, ".") || strings.HasPrefix(d.name, "@") || d.name == "#recycle" {
			continue
		}
		sub := filepath.Join(dir, d.name)
		if err := o.pruneEmptyDirs(log, sub); err != nil {
			return err
		}
		names, err := readdirNames(sub)
		if err != nil {
			return err
		}
		left := 0
		for _, n := range names {
			if n != ".DS_Store" {
				left++
			}
		}
		if left == 0 {
			os.Remove(filepath.Join(sub, ".DS_Store"))
			if err := os.Remove(sub); err != nil {
				return err
			}
			if err := log("rmdir", "", jsRelative(o.bookDir, sub)); err != nil {
				return err
			}
		}
	}
	return nil
}

func readTSV(f string) ([][]string, error) {
	data, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(jsTrim(string(data)), "\n")
	var out [][]string
	for _, l := range lines[1:] {
		out = append(out, strings.Split(l, "\t"))
	}
	return out, nil
}

func field(op []string, i int) string {
	if i < len(op) {
		return op[i]
	}
	return "undefined"
}

func (o *organizer) apply() error {
	ops, err := readTSV(o.planTSV)
	if err != nil {
		return err
	}
	logFile := filepath.Join(o.root, "logs/book-reorg-"+isoStamp(time.Now())+".tsv")
	if err := os.WriteFile(logFile, []byte("op\tfrom\tto\n"), 0o644); err != nil {
		return err
	}
	log := func(op, from, to string) error {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(op + "\t" + from + "\t" + to + "\n")
		return err
	}
	stats := struct{ mv, write, skipped, rmdir int }{}
	for _, op := range ops {
		kind, from, to := field(op, 0), field(op, 1), field(op, 2)
		switch kind {
		case "mv":
			if !exists(o.nas(from)) {
				warn("missing, skipped: %s", from)
				stats.skipped++
				continue
			}
			if strings.ToLower(from) != strings.ToLower(to) && exists(o.nas(to)) {
				warn("target exists, skipped: %s", to)
				stats.skipped++
				continue
			}
			if err := os.MkdirAll(filepath.Dir(o.nas(to)), 0o755); err != nil {
				return err
			}
			if err := os.Rename(o.nas(from), o.nas(to)); err != nil {
				return err
			}
			if err := log("mv", from, to); err != nil {
				return err
			}
			stats.mv++
		case "write":
			if exists(o.nas(to)) {
				warn("exists, not overwritten: %s", to)
				stats.skipped++
				continue
			}
			if err := os.MkdirAll(filepath.Dir(o.nas(to)), 0o755); err != nil {
				return err
			}
			if err := copyFile(from, o.nas(to)); err != nil {
				return err
			}
			if err := log("write", "", to); err != nil {
				return err
			}
			stats.write++
		}
	}
	if err := o.pruneEmptyDirs(func(op, from, to string) error { stats.rmdir++; return log(op, from, to) }, o.bookDir); err != nil {
		return err
	}
	info(`done {"mv":%d,"write":%d,"skipped":%d,"rmdir":%d}; log: %s`, stats.mv, stats.write, stats.skipped, stats.rmdir, jsRelative(o.root, logFile))
	info("rollback: shelf organize-books --rollback %s", jsRelative(o.root, logFile))
	return nil
}

func (o *organizer) rollback(logFile string) error {
	ops, err := readTSV(logFile)
	if err != nil {
		return err
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	stats := struct{ mv, rm, mkdir, skipped int }{}
	for _, op := range ops {
		kind, from, to := field(op, 0), field(op, 1), field(op, 2)
		switch kind {
		case "rmdir":
			if err := os.MkdirAll(o.nas(to), 0o755); err != nil {
				return err
			}
			stats.mkdir++
		case "write":
			if err := os.Remove(o.nas(to)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			stats.rm++
		case "mv":
			if !exists(o.nas(to)) || exists(o.nas(from)) {
				warn("cannot move back: %s", to)
				stats.skipped++
				continue
			}
			if err := os.MkdirAll(filepath.Dir(o.nas(from)), 0o755); err != nil {
				return err
			}
			if err := os.Rename(o.nas(to), o.nas(from)); err != nil {
				return err
			}
			stats.mv++
		}
	}
	for _, op := range ops {
		if kind := field(op, 0); kind == "mv" || kind == "write" {
			dir := filepath.Dir(o.nas(field(op, 2)))
			if names, err := readdirNames(dir); err == nil {
				left := 0
				for _, n := range names {
					if n != ".DS_Store" {
						left++
					}
				}
				if left == 0 {
					os.Remove(filepath.Join(dir, ".DS_Store"))
					os.Remove(dir)
				}
			}
		}
	}
	info(`rolled back {"mv":%d,"rm":%d,"mkdir":%d,"skipped":%d}`, stats.mv, stats.rm, stats.mkdir, stats.skipped)
	return nil
}

func runOrganize(args []string) error {
	o := newOrganizer()
	if len(args) > 0 && args[0] == "--apply" {
		return o.apply()
	}
	if len(args) > 0 && args[0] == "--rollback" {
		if len(args) < 2 {
			return errors.New("usage: shelf organize-books --rollback <log.tsv>")
		}
		abs, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		return o.rollback(abs)
	}
	return o.writePlan()
}
