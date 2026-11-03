package main

// shelf recategorize-books: re-classifies books into a two-level taxonomy (大类 / 细类, modelled on 中图法) and writes it
// into each book folder's metadata.opf. Port of recategorize-books.mjs:
//   shelf recategorize-books                      → finds CIP codes, writes book-recategorize-plan.md, touches nothing
//   shelf recategorize-books --apply              → backs up every metadata.opf to logs/opf-backup-<stamp>/, then rewrites them
//   shelf recategorize-books --rollback <backup>  → restores the backed-up OPFs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"movieshelf/icu"
)

type taxon struct {
	cat, clc string
	subs     []string
}

var taxonomy = []taxon{
	{"哲学", "B（不含 B84）", []string{"中国哲学", "逻辑与思维", "哲学通识"}},
	{"心理学", "B84", []string{"个人成长", "认知与决策", "情绪与心理治疗", "社会心理", "心理学通识"}},
	{"社会", "C", []string{"社会学", "人际沟通", "婚恋与两性"}},
	{"政治", "D", []string{"政治学", "中国政治"}},
	{"经济", "F0–F8", []string{"经济学", "投资理财"}},
	{"管理", "F27 / C93", []string{"企业管理", "职场与效率", "市场营销", "产品", "互联网与创新", "商业传记"}},
	{"教育", "G4–G7", []string{"学习方法"}},
	{"语言", "H", []string{"写作", "口才与表达"}},
	{"文学", "I", []string{"中国小说", "外国小说", "推理悬疑", "科幻", "散文随笔", "诗歌", "中国古典文学"}},
	{"历史", "K", []string{"中国史", "世界史", "通俗历史", "传记"}},
	{"文化艺术", "G0–G2 / J", []string{"文化常识", "设计"}},
	{"数理科学", "O", []string{"数学", "博弈论", "物理"}},
	{"医学健康", "R", []string{"医学与健康"}},
	{"生活", "TS97", []string{"整理与极简"}},
	{"计算机", "TP", []string{"编程语言", "前端开发", "数据结构与算法", "计算机原理", "操作系统", "计算机网络", "软件工程", "架构与运维", "办公软件"}},
}

// recatMap is book id → '大类/细类', curated from Douban tags, titles and CIP codes.
var recatMap = map[string]string{
	"db3646976": "哲学/中国哲学", "db3007268": "哲学/中国哲学", "db6534729": "哲学/中国哲学", "db4927701": "哲学/中国哲学",
	"db26828205": "哲学/逻辑与思维", "db25772550": "哲学/逻辑与思维", "db20428922": "哲学/逻辑与思维", "db24697776": "哲学/逻辑与思维",
	"f82ee0aec30": "哲学/逻辑与思维", "db2986872": "哲学/逻辑与思维",
	"db1331665": "哲学/哲学通识", "db37235927": "哲学/哲学通识", "db27172839": "哲学/哲学通识",
	"db35876121": "心理学/个人成长", "db20507212": "心理学/个人成长", "db26425651": "心理学/个人成长", "db36894917": "心理学/个人成长",
	"db1775691": "心理学/个人成长", "db6533613": "心理学/个人成长", "db26761696": "心理学/个人成长", "db26911425": "心理学/个人成长",
	"db10786473": "心理学/个人成长", "db27078435": "心理学/个人成长", "f85099c5157": "心理学/个人成长", "db26935293": "心理学/个人成长",
	"db5325618": "心理学/个人成长", "db26606009": "心理学/个人成长",
	"db25763679": "心理学/认知与决策", "db34442149": "心理学/认知与决策", "db20492550": "心理学/认知与决策", "db10785583": "心理学/认知与决策",
	"db35481512": "心理学/情绪与心理治疗", "fc2503cef26": "心理学/情绪与心理治疗", "db4062809": "心理学/情绪与心理治疗",
	"db25963532": "心理学/情绪与心理治疗", "db1135734": "心理学/情绪与心理治疗", "db30181152": "心理学/情绪与心理治疗",
	"db1005576": "心理学/社会心理", "db1786387": "心理学/社会心理",
	"db26743051": "心理学/心理学通识", "db26296233": "心理学/心理学通识", "db36166780": "心理学/心理学通识", "db30419261": "心理学/心理学通识",
	"f24b590ab57": "心理学/心理学通识",
	"db35038832":  "社会/社会学", "db35593780": "社会/社会学", "db1795079": "社会/社会学", "db25863621": "社会/社会学",
	"db5504201": "社会/人际沟通", "db26342463": "社会/人际沟通", "db5344908": "社会/人际沟通", "db25894861": "社会/人际沟通",
	"db3533221": "社会/人际沟通", "fae7fcad6f1": "社会/人际沟通",
	"db1223845": "社会/婚恋与两性", "db25839107": "社会/婚恋与两性",
	"db25881102": "政治/政治学", "ff801ab42bf": "政治/政治学", "db20366368": "政治/政治学",
	"f1580421003": "政治/中国政治", "db26607419": "政治/中国政治", "db35546622": "政治/中国政治",
	"db2326852": "经济/经济学", "db4140492": "经济/经济学", "db35069279": "经济/经济学", "db26897464": "经济/经济学", "db10434287": "经济/经济学",
	"db27667177": "经济/经济学", "db21966353": "经济/经济学", "db27111109": "经济/经济学",
	"db26634824": "经济/投资理财", "db26370442": "经济/投资理财", "db26752026": "经济/投资理财", "db4737015": "经济/投资理财",
	"db1033778": "经济/投资理财", "db27204860": "经济/投资理财", "db27599920": "经济/投资理财", "db37003888": "经济/投资理财", "db35295592": "经济/投资理财",
	"db1022936": "管理/企业管理", "db36336321": "管理/企业管理", "db24700113": "管理/企业管理", "db35135792": "管理/企业管理",
	"db25813440": "管理/职场与效率", "db2792535": "管理/职场与效率", "db26876510": "管理/职场与效率", "db26656532": "管理/职场与效率",
	"db1039311": "管理/职场与效率", "db33391219": "管理/职场与效率",
	"db30426372": "管理/市场营销", "db1017180": "管理/市场营销",
	"db34899655": "管理/产品", "db4723970": "管理/产品",
	"db35272152": "管理/互联网与创新", "db35128450": "管理/互联网与创新", "db26297606": "管理/互联网与创新",
	"db27074369": "管理/商业传记", "db26849305": "管理/商业传记", "db27179563": "管理/商业传记", "db26929955": "管理/商业传记",
	"db35368398": "教育/学习方法", "db26895993": "教育/学习方法", "db1013208": "教育/学习方法", "db25783654": "教育/学习方法", "db27047949": "教育/学习方法",
	"db35503571": "语言/写作", "db26990918": "语言/写作",
	"db26948148": "语言/口才与表达", "db26865749": "语言/口才与表达", "db26799348": "语言/口才与表达",
	"db35005045": "文学/中国小说", "db35587028": "文学/中国小说", "db3532347": "文学/中国小说",
	"db11539568": "文学/外国小说", "db26803714": "文学/外国小说", "db26340138": "文学/外国小说", "db26698660": "文学/外国小说",
	"db4010969": "文学/外国小说", "db25862578": "文学/外国小说", "db20443559": "文学/外国小说", "db30134903": "文学/外国小说",
	"db24257229": "文学/外国小说", "db10594787": "文学/外国小说", "db30278620": "文学/外国小说", "db25887924": "文学/外国小说",
	"db10554308": "文学/推理悬疑", "db36062395": "文学/推理悬疑", "db1040771": "文学/推理悬疑",
	"db6518605": "文学/科幻", "db26863404": "文学/科幻",
	"db5910656": "文学/散文随笔", "db3369600": "文学/散文随笔", "db26390229": "文学/散文随笔", "db27013716": "文学/散文随笔", "db30539780": "文学/散文随笔",
	"db6440343": "文学/诗歌", "db30462444": "文学/诗歌",
	"db1883245": "文学/中国古典文学", "db3772799": "文学/中国古典文学",
	"db36216782": "历史/中国史", "db36794613": "历史/中国史", "db26603137": "历史/中国史", "db2121038": "历史/中国史", "db25935941": "历史/中国史",
	"db36912612": "历史/中国史", "db34446495": "历史/中国史", "db25837482": "历史/中国史",
	"fb487afff48": "历史/世界史", "db37294999": "历史/世界史",
	"db3901238": "历史/通俗历史", "db24859453": "历史/通俗历史", "db25945487": "历史/通俗历史",
	"db33440205": "历史/传记", "db30597601": "历史/传记",
	"db1935312": "文化艺术/文化常识", "db1941558": "文化艺术/设计",
	"db35383731": "数理科学/数学", "db35292688": "数理科学/数学", "db2201479": "数理科学/数学",
	"db26677472": "数理科学/博弈论", "db1084435": "数理科学/博弈论", "db1467022": "数理科学/物理",
	"db26576861": "医学健康/医学与健康", "faa0d1bf9b5": "医学健康/医学与健康",
	"db24749465": "生活/整理与极简", "db35284864": "生活/整理与极简",
	"db35006472": "计算机/办公软件", "db25786074": "计算机/前端开发", "db10746113": "计算机/计算机网络",
	"db26462816": "计算机/软件工程", "db6526452": "计算机/软件工程", "db35325133": "计算机/编程语言",
	"db4822685": "计算机/计算机原理", "db3296317": "计算机/计算机原理",
}

var csFromOld = map[string]string{"前端开发": "前端开发", "编程语言": "编程语言", "数据结构与算法": "数据结构与算法", "操作系统": "操作系统", "网络": "计算机网络", "编程思想": "软件工程", "架构应用": "架构与运维"}

// badCIP are CIP codes that were mis-read from the copyright page.
var badCIP = map[string]bool{"db35038832": true}

var (
	reCSAPP  = re(`深入理解计算机系统|computer system`, "i")
	reDSTitl = re(`data structures`, "i")
)

func classify(b *Obj) (string, string) {
	if p := recatMap[bookID(b)]; p != "" {
		return p, "curated"
	}
	if b.Get("category") == "计算机" {
		t := jsString(b.Get("title"))
		if reCSAPP.Test(t) {
			return "计算机/计算机原理", "rule"
		}
		if reDSTitl.Test(t) {
			return "计算机/数据结构与算法", "rule"
		}
		sub := csFromOld[strOr(b.Get("subcategory"))]
		if sub == "" {
			sub = "编程语言"
		}
		return "计算机/" + sub, "rule"
	}
	return "", "missing"
}

var (
	reHTMLName = re(`\.x?html?$`, "i")
	reCIPMark  = re(`CIP|中国版本图书馆`, "")
	reCLC      = re(`(?:Ⅳ|IV)\s*[.．、。]?\s*[①Ｉ]?\s*([A-Z][A-Z]?\s?\d[\d.\-=/]*\d)`, "")
	reAnySpace = re(`\s`, "g")
)

type recategorizer struct{ root, bookDir, clcCache, planMD string }

// stringifyIndent1 is JSON.stringify(flatObject, null, 1) for an object of strings and nulls.
func stringifyIndent1(o *Obj) []byte {
	if len(o.Keys) == 0 {
		return []byte("{}")
	}
	var b bytes.Buffer
	b.WriteString("{")
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n ")
		writeJSONString(&b, k)
		b.WriteString(": ")
		writeJSON(&b, o.Vals[i])
	}
	b.WriteString("\n}")
	return b.Bytes()
}

// cipCodes finds 中图分类号 in the CIP block on an epub's copyright page (Ⅳ.①K249…), cached per file.
func (rc *recategorizer) cipCodes(bs []*Obj) (map[string]any, error) {
	cache := &Obj{}
	if v, err := readJSONFile(rc.clcCache); err == nil {
		if o, ok := v.(*Obj); ok {
			cache = o
		}
	}
	for _, b := range bs {
		for _, fv := range arr(b.Get("files")) {
			f := fv.(*Obj)
			if f.Get("format") != "epub" || cache.Has(jsString(f.Get("id"))) {
				continue
			}
			var code any = nil
			fp := jsString(f.Get("path"))
			err := func() error {
				full := filepath.Join(rc.bookDir, fp)
				st, err := statFile(full)
				if err != nil {
					return err
				}
				fh, err := os.Open(full)
				if err != nil {
					return err
				}
				defer fh.Close()
				z, err := openZip(fh, st.size)
				if err != nil {
					return err
				}
				var htmls []string
				for _, n := range z.names {
					if reHTMLName.Test(n) {
						htmls = append(htmls, n)
					}
				}
				for _, name := range htmls[:min(15, len(htmls))] {
					data, err := z.read(name)
					if err != nil {
						return err
					}
					t := reTag.Replace(decodeUTF8(data), " ")
					if !reCIPMark.Test(t) {
						continue
					}
					if m, _ := reCLC.Match(t); m != nil {
						code = reAnySpace.Replace(m[1], "")
						break
					}
				}
				return nil
			}()
			if err != nil {
				warn("cip %s: %s", fp, err)
			}
			cache.Set(jsString(f.Get("id")), code)
		}
	}
	if err := os.WriteFile(rc.clcCache, stringifyIndent1(cache), 0o644); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, b := range bs {
		var code any = nil
		if !badCIP[bookID(b)] {
			for _, fv := range arr(b.Get("files")) {
				if c := cache.Get(jsString(getv(fv, "id"))); truthy(c) {
					code = c
					break
				}
			}
		}
		out[bookID(b)] = code
	}
	return out, nil
}

var (
	xmlEscRecat   = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	reCurrentCats = re(`<meta name="home:(?:category|subcategory)" content="([^"]*)"\/>`, "g")
	reSubjectLine = re(`[ \t]*<dc:subject>([^<]*)<\/dc:subject>\n`, "g")
	reHomeMeta    = re(`[ \t]*<meta name="home:(category|subcategory|clc|clc_source)"[^>]*\/>\n`, "g")
	reLanguageEl  = re(`(<dc:language>[^<]*<\/dc:language>\n)`, "")
	reMetadataEnd = re(`(\s*)<\/metadata>`, "")
	reLt          = re(`&lt;`, "g")
	reGt          = re(`&gt;`, "g")
	reQuotEnt     = re(`&quot;`, "g")
)

var oldCats = []string{"计算机", "数学", "未分类", "文学 & 小说", "政治 & 哲学", "心理学 & 教育", "历史 & 社会", "经营 & 管理 & 产品", "金融 & 投资 & 理财"}

func decodeRecat(s string) string {
	return reAmpAmp.Replace(reQuotEnt.Replace(reGt.Replace(reLt.Replace(s, "<"), ">"), `"`), "&")
}

// rewriteOpf drops old category subjects/metas from an OPF and inserts the new ones after dc:language.
func rewriteOpf(opf, cat, sub string, clc any) string {
	drop := map[string]bool{cat: true, sub: true}
	for _, c := range oldCats {
		drop[c] = true
	}
	for _, m := range reCurrentCats.MatchAll(opf) {
		drop[decodeRecat(m.groups[1])] = true
	}
	out := reSubjectLine.ReplaceFunc(opf, func(m reMatch) string {
		if drop[decodeRecat(m.groups[1])] {
			return ""
		}
		return m.groups[0]
	})
	out = reHomeMeta.Replace(out, "")
	subjects := "    <dc:subject>" + xmlEscRecat.Replace(cat) + "</dc:subject>\n    <dc:subject>" + xmlEscRecat.Replace(sub) + "</dc:subject>\n"
	out = reLanguageEl.ReplaceFunc(out, func(m reMatch) string { return m.groups[1] + subjects })
	metas := "    <meta name=\"home:category\" content=\"" + xmlEscRecat.Replace(cat) + "\"/>\n    <meta name=\"home:subcategory\" content=\"" + xmlEscRecat.Replace(sub) + "\"/>\n"
	if truthy(clc) {
		metas += "    <meta name=\"home:clc\" content=\"" + xmlEscRecat.Replace(jsString(clc)) + "\"/>\n    <meta name=\"home:clc_source\" content=\"cip\"/>\n"
	}
	return reMetadataEnd.ReplaceFunc(out, func(m reMatch) string {
		return "\n" + strings.TrimRightFunc(metas, isJSSpace) + m.groups[1] + "</metadata>"
	})
}

type recatRow struct {
	b             *Obj
	cat, sub, how string
	clc           any
}

func (rc *recategorizer) load() ([]*recatRow, error) {
	bs, err := loadBooks(rc.root)
	if err != nil {
		return nil, err
	}
	clc, err := rc.cipCodes(bs)
	if err != nil {
		return nil, err
	}
	rows := make([]*recatRow, len(bs))
	for i, b := range bs {
		pair, how := classify(b)
		if pair == "" {
			pair = "未分类/未分类"
		}
		parts := strings.Split(pair, "/")
		rows[i] = &recatRow{b, parts[0], parts[1], how, clc[bookID(b)]}
	}
	return rows, nil
}

var cipLetter = map[string]*jsRe{
	"哲学": re(`^B(?!84)`, ""), "心理学": re(`^B84|^R395`, ""), "社会": re(`^C9[1-3]`, ""), "政治": re(`^D`, ""), "经济": re(`^F(?!27|4|7)`, ""),
	"管理": re(`^F27|^F4|^F7|^C93|^C96`, ""), "教育": re(`^G[4-7]`, ""), "语言": re(`^H`, ""), "文学": re(`^I`, ""), "历史": re(`^K`, ""),
	"文化艺术": re(`^G[0-2]|^J`, ""), "数理科学": re(`^O|^C91[56]`, ""), "医学健康": re(`^R`, ""), "生活": re(`^TS`, ""), "计算机": re(`^TP`, ""),
}

func letterOK(cat string, clc any) bool {
	r := cipLetter[cat]
	return r != nil && r.Test(jsString(clc))
}

func (rc *recategorizer) writePlan() ([]*recatRow, error) {
	rows, err := rc.load()
	if err != nil {
		return nil, err
	}
	var missing []*recatRow
	count := map[string]int{}
	var pairOrder []string
	for _, r := range rows {
		if r.how == "missing" {
			missing = append(missing, r)
		}
		k := r.cat + "/" + r.sub
		if _, ok := count[k]; !ok {
			pairOrder = append(pairOrder, k)
		}
		count[k]++
	}
	catCount := func(c string) int {
		n := 0
		for _, r := range rows {
			if r.cat == c {
				n++
			}
		}
		return n
	}
	var cipRows, agree []*recatRow
	for _, r := range rows {
		if truthy(r.clc) {
			cipRows = append(cipRows, r)
			if letterOK(r.cat, r.clc) {
				agree = append(agree, r)
			}
		}
	}
	var tax []string
	for _, t := range taxonomy {
		subs := make([]string, len(t.subs))
		for i, s := range t.subs {
			subs[i] = fmt.Sprintf("%s %d", s, count[t.cat+"/"+s])
		}
		tax = append(tax, fmt.Sprintf("| **%s** | %s | %d | %s |", t.cat, t.clc, catCount(t.cat), strings.Join(subs, "，")))
	}
	var list []string
	for _, t := range taxonomy {
		var rs []*recatRow
		for _, r := range rows {
			if r.cat == t.cat {
				rs = append(rs, r)
			}
		}
		sort.SliceStable(rs, func(i, j int) bool {
			if c := icu.ZH.Compare(rs[i].sub, rs[j].sub); c != 0 {
				return c < 0
			}
			return icu.ZH.Compare(jsString(rs[i].b.Get("title")), jsString(rs[j].b.Get("title"))) < 0
		})
		ls := make([]string, len(rs))
		for i, r := range rs {
			orig := jsString(r.b.Get("category"))
			if s := r.b.Get("subcategory"); truthy(s) {
				orig += " / " + jsString(s)
			}
			warnMark := ""
			if truthy(r.clc) && !letterOK(r.cat, r.clc) {
				warnMark = " ⚠︎"
			}
			ls[i] = "| " + r.sub + " | " + strings.ReplaceAll(jsString(r.b.Get("title")), "|", `\|`) + " | " + orig + " | " + strOr(or(r.clc, "")) + warnMark + " |"
		}
		list = append(list, fmt.Sprintf("\n### %s（%d）\n\n| 细类 | 书 | 原分类 | 中图分类号（CIP） |\n| --- | --- | --- | --- |\n", t.cat, len(rs))+strings.Join(ls, "\n"))
	}
	missingNote := ""
	if len(missing) > 0 {
		ts := make([]string, len(missing))
		for i, r := range missing {
			ts[i] = jsString(r.b.Get("title"))
		}
		missingNote = fmt.Sprintf("；**%d 本没有归类**：%s", len(missing), strings.Join(ts, "、"))
	}
	bt := "`"
	md := "# 书库重新分类方案\n\n" +
		"生成时间：" + localeStringZH(time.Now()) + fmt.Sprintf("。共 %d 本。\n\n", len(rows)) +
		"- 两级分类，参考《中国图书馆分类法》：**大类**对应中图法基本大类（心理学 B84、管理 F27/C93 因为书多单独成类），**细类**是一个具体主题。\n" +
		"- 写进每本书的 " + bt + "metadata.opf" + bt + "：" + bt + "home:category" + bt + "=大类、" + bt + "home:subcategory" + bt + "=细类、前两个 " + bt + "dc:subject" + bt + "；能从书里版权页 CIP 读到中图分类号的写 " + bt + "home:clc" + bt + "。\n" +
		"- 只改 OPF，不移动文件。执行前把全部 OPF 备份到 " + bt + "logs/opf-backup-<时间>/" + bt + "，可一键还原。\n\n" +
		"## 分类来源\n\n" +
		"- 国家图书馆 OPAC（opac.nlc.cn）按 ISBN 查官方分类号：**本机连不上**（直连超时、代理 502），这次没用上。\n" +
		fmt.Sprintf("- 版权页 CIP 中图分类号：**%d 本**（只看 epub 版权页；扫描版 PDF 读不到）。其中 %d 本和下面的大类一致，不一致的在清单里标 ⚠︎。\n", len(cipRows), len(agree)) +
		"- 其余按豆瓣标签 + 书名人工归类" + missingNote + "。\n\n" +
		"## 分类表\n\n" +
		"| 大类 | 中图法 | 书数 | 细类（书数） |\n| --- | --- | --- | --- |\n" + strings.Join(tax, "\n") + "\n\n" +
		"## 每本书\n" + strings.Join(list, "\n") + "\n"
	if err := os.WriteFile(rc.planMD, []byte(md), 0o644); err != nil {
		return nil, err
	}
	info("plan: %d books, %d 大类, %d 细类, CIP %d (%d agree), missing %d", len(rows), len(taxonomy), len(pairOrder), len(cipRows), len(agree), len(missing))
	return rows, nil
}

func (rc *recategorizer) apply() error {
	rows, err := rc.writePlan()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.how == "missing" {
			return errors.New("some books have no category; fix MAP first")
		}
	}
	backup := filepath.Join(rc.root, "logs/opf-backup-"+isoStamp(time.Now()))
	var dirs []string
	folders := map[string]*recatRow{}
	for _, r := range rows {
		for _, fv := range arr(r.b.Get("files")) {
			dir := path.Dir(jsString(getv(fv, "path")))
			if _, ok := folders[dir]; dir != "." && !ok {
				folders[dir] = r
				dirs = append(dirs, dir)
			}
		}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(backup, dir), 0o755); err != nil {
			return err
		}
		if err := copyFile(filepath.Join(rc.bookDir, dir, "metadata.opf"), filepath.Join(backup, dir, "metadata.opf")); err != nil {
			return err
		}
	}
	n := 0
	for _, dir := range dirs {
		r := folders[dir]
		f := filepath.Join(rc.bookDir, dir, "metadata.opf")
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(f, []byte(rewriteOpf(decodeUTF8(data), r.cat, r.sub, r.clc)), 0o644); err != nil {
			return err
		}
		n++
	}
	info("rewrote %d metadata.opf; backup: %s", n, jsRelative(rc.root, backup))
	info("rollback: shelf recategorize-books --rollback %s", jsRelative(rc.root, backup))
	return nil
}

func (rc *recategorizer) rollback(backup string) error {
	n := 0
	var walk func(rel string) error
	walk = func(rel string) error {
		es, err := readDirents(filepath.Join(backup, rel))
		if err != nil {
			return err
		}
		for _, d := range es {
			r := filepath.Join(rel, d.name)
			if d.isDir {
				if err := walk(r); err != nil {
					return err
				}
			} else if d.name == "metadata.opf" {
				if err := copyFile(filepath.Join(backup, r), filepath.Join(rc.bookDir, r)); err != nil {
					return err
				}
				n++
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return err
	}
	info("restored %d metadata.opf", n)
	return nil
}

func runRecategorize(args []string) error {
	root := findRoot()
	rc := &recategorizer{root, env("BOOK_DIR", "/Volumes/store/book"), filepath.Join(root, "cache/clc.json"), filepath.Join(root, "book-recategorize-plan.md")}
	if len(args) > 0 && args[0] == "--apply" {
		return rc.apply()
	}
	if len(args) > 0 && args[0] == "--rollback" {
		if len(args) < 2 {
			return errors.New("usage: shelf recategorize-books --rollback <backup dir>")
		}
		abs, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		return rc.rollback(abs)
	}
	_, err := rc.writePlan()
	return err
}
