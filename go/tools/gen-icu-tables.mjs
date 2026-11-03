// Dumps ICU data the Go code needs to match Node exactly, into icu/:
//   zh.bin / en.bin — per-character collation ranks for localeCompare(…, 'zh-CN') and the default en-US locale.
//     Record: u32 code point, u32 primary, secondary and tertiary ranks (little endian); rank 0 = ignorable at that level.
//   regions.json — Intl.DisplayNames(['zh-CN'], { type: 'region' }) for every two-letter code it names.
import fs from 'node:fs';
const ranges = [[0x20, 0x7E], [0xA0, 0x24F], [0x370, 0x3FF], [0x400, 0x52F], [0x590, 0x6FF], [0x900, 0xDFF], [0xE00, 0xEFF], [0x1100, 0x11FF],
  [0x2000, 0x206F], [0x2150, 0x218F], [0x2460, 0x24FF], [0x3000, 0x30FF], [0x3130, 0x318F], [0x3400, 0x4DBF], [0x4E00, 0x9FFF],
  [0xAC00, 0xD7AF], [0xF900, 0xFAFF], [0xFE30, 0xFE4F], [0xFF00, 0xFFEF], [0x20000, 0x2A6DF], [0x2A700, 0x2EBEF], [0x2F800, 0x2FA1F]];
const chars = [];
for (const [a, b] of ranges) for (let c = a; c <= b; c++) { const s = String.fromCodePoint(c); if (s.normalize('NFC') === s) chars.push(s); }
const out = new URL('../icu/', import.meta.url);
for (const [file, locale] of [['zh.bin', 'zh-CN'], ['en.bin', undefined]]) {
  const rank = sensitivity => {
    const col = new Intl.Collator(locale, { sensitivity });
    const ign = new Set(chars.filter(c => col.compare(c, '') === 0));
    const sorted = chars.filter(c => !ign.has(c)).sort(col.compare);
    const r = new Map(); let k = 0;
    sorted.forEach((c, i) => { if (i === 0 || col.compare(sorted[i - 1], c) !== 0) k++; r.set(c, k); });
    return c => r.get(c) || 0;
  };
  const [p, s, t] = ['base', 'accent', 'variant'].map(rank);
  const buf = Buffer.alloc(chars.length * 16);
  chars.forEach((c, i) => [c.codePointAt(0), p(c), s(c), t(c)].forEach((v, k) => buf.writeUInt32LE(v, i * 16 + k * 4)));
  fs.writeFileSync(new URL(file, out), buf);
}
const dn = new Intl.DisplayNames(['zh-CN'], { type: 'region' });
const regions = {};
for (let a = 65; a <= 90; a++) for (let b = 65; b <= 90; b++) {
  const c = String.fromCharCode(a, b);
  try { const n = dn.of(c); if (n !== c) regions[c] = n; } catch {}
}
fs.writeFileSync(new URL('regions.json', out), JSON.stringify(regions));
console.log(chars.length, 'chars,', Object.keys(regions).length, 'regions; icu', process.versions.icu, 'cldr', process.versions.cldr, 'default locale', new Intl.Collator().resolvedOptions().locale);
