// Package icu reproduces the bits of Node's ICU (77.1, CLDR 47) the shelf relies on, from tables dumped by
// tools/gen-icu-tables.mjs: string collation for zh-CN (pinyin) and the default en-US locale, and zh-CN region names.
package icu

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"sort"
	"sync"
)

var (
	//go:embed zh.bin
	zhTable []byte
	//go:embed en.bin
	enTable []byte
	//go:embed regions.json
	regionsJSON []byte
)

const recSize = 16

// Collator compares strings like String.prototype.localeCompare for one locale.
type Collator struct {
	table        []byte
	contractions map[[2]rune]rune
	once         sync.Once
	cps          []rune
}

var (
	// ZH is localeCompare(…, 'zh-CN'), with the CLDR pinyin contractions "&anchor<XY/Y" (重庆 as chóng, …).
	ZH = &Collator{table: zhTable, contractions: map[[2]rune]rune{{'重', '庆'}: '虫', {'沈', '阳'}: '弞', {'藏', '文'}: '銺'}}
	// Default is localeCompare without a locale under Node's default en-US.
	Default = &Collator{table: enTable}
)

func (c *Collator) load() {
	n := len(c.table) / recSize
	c.cps = make([]rune, n)
	for i := 0; i < n; i++ {
		c.cps[i] = rune(binary.LittleEndian.Uint32(c.table[i*recSize:]))
	}
}

// weights returns the primary, secondary and tertiary ranks of r; characters outside the table sort after it by code point.
func (c *Collator) weights(r rune) [3]uint32 {
	c.once.Do(c.load)
	i := sort.Search(len(c.cps), func(i int) bool { return c.cps[i] >= r })
	if i < len(c.cps) && c.cps[i] == r {
		rec := c.table[i*recSize:]
		return [3]uint32{binary.LittleEndian.Uint32(rec[4:]), binary.LittleEndian.Uint32(rec[8:]), binary.LittleEndian.Uint32(rec[12:])}
	}
	k := 1<<24 + uint32(r)
	return [3]uint32{k, k, k}
}

func (c *Collator) keys(rs []rune, level int) []uint32 {
	out := make([]uint32, 0, len(rs))
	for i, r := range rs {
		bump := uint32(0)
		if i+1 < len(rs) {
			if anchor, ok := c.contractions[[2]rune{r, rs[i+1]}]; ok {
				r, bump = anchor, 1
			}
		}
		if w := c.weights(r)[level]; w != 0 {
			out = append(out, w*2+bump)
		}
	}
	return out
}

// Compare returns -1, 0 or 1, comparing all primary weights first, then secondary, then tertiary.
func (c *Collator) Compare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for level := 0; level < 3; level++ {
		ka, kb := c.keys(ra, level), c.keys(rb, level)
		for i := 0; i < len(ka) && i < len(kb); i++ {
			if ka[i] != kb[i] {
				if ka[i] < kb[i] {
					return -1
				}
				return 1
			}
		}
		if len(ka) != len(kb) {
			if len(ka) < len(kb) {
				return -1
			}
			return 1
		}
	}
	return 0
}

var (
	regionsOnce sync.Once
	regions     map[string]string
)

// RegionName is new Intl.DisplayNames(['zh-CN'], { type: 'region' }).of(code), falling back to the code itself.
func RegionName(code string) string {
	regionsOnce.Do(func() { json.Unmarshal(regionsJSON, &regions) })
	if n, ok := regions[code]; ok {
		return n
	}
	return code
}
