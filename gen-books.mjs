// Builds movie-shelf/docs-books/<id>.md (qmd collection "books"), data/books.json, cache/covers/
// and unmatched-books.md from /Volumes/store/book (read-only), Douban and Open Library.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { segment, hasCJK } from './lib/seg.mjs';

const ROOT = path.dirname(fileURLToPath(import.meta.url));
const BOOK_DIR = process.env.BOOK_DIR || '/Volumes/store/book';
const SMB_BASE = 'smb://192.168.50.94/store/book/';
const DOCS = path.join(ROOT, 'docs-books');
const CACHE = path.join(ROOT, 'cache');
const COVERS = path.join(CACHE, 'covers');
const META_CACHE = path.join(CACHE, 'bookmeta');
const SCAN_CACHE = path.join(CACHE, 'book-scan.json');

const BOOK_EXT = new Set(['.epub', '.pdf', '.mobi', '.azw3', '.azw', '.djvu', '.cbz', '.fb2', '.txt']);
const UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36';
const DOUBAN_GAP_MS = 2500;
const args = new Set(process.argv.slice(2));
const run = promisify(execFile);

const sleep = ms => new Promise(r => setTimeout(r, ms));
const sha1 = s => crypto.createHash('sha1').update(s).digest('hex');
const norm = s => (s || '').normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '');
const decodeEntities = s => (s || '').replace(/&(#x?[0-9a-f]+|amp|lt|gt|quot|apos|nbsp);/gi, (_, e) => {
  if (e[0] === '#') return String.fromCodePoint(parseInt(e[1].toLowerCase() === 'x' ? e.slice(2) : e.slice(1), e[1].toLowerCase() === 'x' ? 16 : 10));
  return { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' }[e.toLowerCase()];
});
const htmlText = s => decodeEntities((s || '').replace(/<br\s*\/?>|<\/p>/gi, '\n').replace(/<[^>]+>/g, ''))
  .split('\n').map(l => l.replace(/\s+/g, ' ').trim()).filter(Boolean).join('\n');

/** KOReader's partial MD5 (util.partialMD5): 1 KiB samples at 0 and 1024 << 2i for i = 0..10. */
async function partialMD5(fh, size) {
  const h = crypto.createHash('md5');
  const buf = Buffer.alloc(1024);
  for (let i = -1; i <= 10; i++) {
    const off = i < 0 ? 0 : 1024 * 4 ** i;
    if (off >= size) break;
    const { bytesRead } = await fh.read(buf, 0, 1024, off);
    h.update(buf.subarray(0, bytesRead));
  }
  return h.digest('hex');
}

/** Minimal random-access zip reader (central directory + stored/deflate entries). */
export async function openZip(fh, size) {
  const tailLen = Math.min(size, 65557);
  const tail = Buffer.alloc(tailLen);
  await fh.read(tail, 0, tailLen, size - tailLen);
  const eocd = tail.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  if (eocd < 0) throw new Error('not a zip');
  const cdSize = tail.readUInt32LE(eocd + 12), cdOff = tail.readUInt32LE(eocd + 16);
  if (cdOff + cdSize > size) throw new Error('bad zip central directory');
  const cd = Buffer.alloc(cdSize);
  await fh.read(cd, 0, cdSize, cdOff);
  const entries = new Map();
  for (let p = 0; p + 46 <= cd.length && cd.readUInt32LE(p) === 0x02014b50;) {
    const nlen = cd.readUInt16LE(p + 28), elen = cd.readUInt16LE(p + 30), clen = cd.readUInt16LE(p + 32);
    entries.set(cd.toString('utf8', p + 46, p + 46 + nlen), {
      method: cd.readUInt16LE(p + 10), csize: cd.readUInt32LE(p + 20), lho: cd.readUInt32LE(p + 42),
    });
    p += 46 + nlen + elen + clen;
  }
  const lower = new Map([...entries.keys()].map(k => [k.toLowerCase(), k]));
  return {
    names: [...entries.keys()],
    has: name => entries.has(name) || lower.has(name.toLowerCase()),
    async read(name) {
      const e = entries.get(name) || entries.get(lower.get(name.toLowerCase()));
      if (!e) return null;
      const lh = Buffer.alloc(30);
      await fh.read(lh, 0, 30, e.lho);
      if (e.lho + 30 + e.csize > size) throw new Error('bad zip entry');
      const data = Buffer.alloc(e.csize);
      await fh.read(data, 0, e.csize, e.lho + 30 + lh.readUInt16LE(26) + lh.readUInt16LE(28));
      return e.method === 8 ? zlib.inflateRawSync(data) : data;
    },
  };
}

function attr(tag, name) {
  return decodeEntities(tag.match(new RegExp(`\\s${name}\\s*=\\s*["']([^"']*)["']`, 'i'))?.[1] || '');
}

async function readEpub(fh, size) {
  const zip = await openZip(fh, size);
  const container = (await zip.read('META-INF/container.xml'))?.toString('utf8') || '';
  const opfPath = container.match(/full-path\s*=\s*["']([^"']+)["']/)?.[1];
  if (!opfPath) throw new Error('no OPF');
  const opf = (await zip.read(opfPath)).toString('utf8');
  const { dc, vals, metaContent, role } = parseOpf(opf);
  const items = [...opf.matchAll(/<item\b[^>]*>/gi)].map(m => ({ id: attr(m[0], 'id'), href: attr(m[0], 'href'), type: attr(m[0], 'media-type'), props: attr(m[0], 'properties') }));
  const coverId = metaContent('cover');
  const coverItem = items.find(i => i.props.split(/\s+/).includes('cover-image'))
    || items.find(i => i.id === coverId && i.type.startsWith('image/'))
    || items.find(i => i.type.startsWith('image/') && /cover/i.test(i.id + i.href));
  let cover = null;
  if (coverItem) {
    const p = path.posix.normalize(path.posix.join(path.posix.dirname(opfPath), decodeURIComponent(coverItem.href)));
    cover = await zip.read(p);
  }
  return {
    title: vals('title')[0], authors: dc.filter(d => d.k === 'creator' && d.v && role(d) !== 'trl').map(d => d.v),
    translators: dc.filter(d => d.k === 'creator' && d.v && role(d) === 'trl').map(d => d.v),
    publisher: vals('publisher')[0], date: vals('date')[0], language: vals('language')[0],
    description: vals('description')[0], subjects: vals('subject'), series: metaContent('calibre:series'),
    isbn: findIsbn(vals('identifier')), cover,
  };
}

/** Parses OPF metadata: dc:* elements (with creator roles, EPUB 2 or 3) and <meta name content>. */
function parseOpf(opf) {
  const dc = [...opf.matchAll(/<dc:(\w+)([^>]*)>([\s\S]*?)<\/dc:\1>/gi)].map(m => ({ k: m[1].toLowerCase(), attrs: m[2], v: htmlText(m[3]) }));
  const refines = new Map();
  for (const m of opf.matchAll(/<meta\b([^>]*)>([^<]*)<\/meta>/gi)) {
    const r = attr(m[1], 'refines').replace(/^#/, '');
    if (r && attr(m[1], 'property') === 'role') refines.set(r, m[2].trim());
  }
  const role = d => (attr(d.attrs, 'opf:role') || refines.get(attr(d.attrs, 'id')) || 'aut').toLowerCase();
  const vals = k => dc.filter(d => d.k === k && d.v).map(d => d.v);
  const metaTags = [...opf.matchAll(/<meta\b[^>]*>/gi)].map(m => m[0]);
  const metaContent = name => { const t = metaTags.find(t => attr(t, 'name') === name); return t ? attr(t, 'content') : undefined; };
  const identifiers = dc.filter(d => d.k === 'identifier').map(d => ({ scheme: (attr(d.attrs, 'opf:scheme') || '').toLowerCase(), v: d.v }));
  return { dc, vals, metaContent, role, identifiers };
}

function findIsbn(list) {
  for (const raw of list) {
    const d = raw.replace(/^urn:isbn:/i, '').replace(/[\s-]/g, '');
    if (/^97[89]\d{10}$/.test(d)) return d;
    if (/^\d{9}[\dX]$/i.test(d)) return isbn10to13(d);
  }
  return null;
}

function isbn10to13(i10) {
  const core = '978' + i10.slice(0, 9);
  const sum = [...core].reduce((s, c, i) => s + +c * (i % 2 ? 3 : 1), 0);
  return core + ((10 - sum % 10) % 10);
}

/** MOBI / AZW3 metadata from the PalmDB record 0 MOBI + EXTH headers. */
async function readMobi(fh, size) {
  if (size < 78 + 16) throw new Error('too small for a MOBI file');
  const head = Buffer.alloc(78);
  await fh.read(head, 0, 78, 0);
  const nrec = head.readUInt16BE(76);
  if (nrec < 2 || 78 + nrec * 8 > size) throw new Error('bad PalmDB record table');
  const recTable = Buffer.alloc(nrec * 8 + 8);
  await fh.read(recTable, 0, recTable.length, 78);
  const recOff = i => recTable.readUInt32BE(i * 8);
  const span = i => { const a = recOff(i), b = recOff(i + 1); if (!(b > a && b <= size && b - a < 64 << 20)) throw new Error('bad record offsets'); return b - a; };
  const r0 = Buffer.alloc(span(0));
  await fh.read(r0, 0, r0.length, recOff(0));
  if (r0.toString('latin1', 16, 20) !== 'MOBI') throw new Error('no MOBI header');
  const hlen = r0.readUInt32BE(20);
  const utf8 = r0.readUInt32BE(28) === 65001;
  const str = (b, s, e) => b.toString(utf8 ? 'utf8' : 'latin1', s, e).replace(/\0+$/, '').trim();
  const out = { title: str(r0, r0.readUInt32BE(84), r0.readUInt32BE(84) + r0.readUInt32BE(88)), authors: [], subjects: [] };
  const firstImage = r0.readUInt32BE(16 + 0x5c);
  let coverIdx = null;
  if (r0.readUInt32BE(128) & 0x40) {
    let p = 16 + hlen + 12;
    const count = r0.readUInt32BE(16 + hlen + 8);
    for (let k = 0; k < count && p + 8 <= r0.length; k++) {
      const type = r0.readUInt32BE(p), len = r0.readUInt32BE(p + 4);
      const v = () => str(r0, p + 8, p + len);
      if (type === 100) out.authors.push(v());
      else if (type === 101) out.publisher = v();
      else if (type === 103) out.description = htmlText(v());
      else if (type === 104) out.isbn = findIsbn([v()]);
      else if (type === 105) out.subjects.push(v());
      else if (type === 106) out.date = v();
      else if (type === 503) out.title = v();
      else if (type === 524) out.language = v();
      else if (type === 201) coverIdx = r0.readUInt32BE(p + 8);
      p += len;
    }
  }
  if (coverIdx != null && firstImage > 0 && firstImage + coverIdx + 1 < nrec) {
    const i = firstImage + coverIdx;
    const buf = Buffer.alloc(span(i));
    await fh.read(buf, 0, buf.length, recOff(i));
    if (buf[0] === 0xff && buf[1] === 0xd8 || buf.toString('latin1', 1, 4) === 'PNG') out.cover = buf;
  }
  return out;
}

async function pdfTextLayer(fh, size) {
  const buf = Buffer.alloc(Math.min(size, 4 << 20));
  await fh.read(buf, 0, buf.length, 0);
  const s = buf.toString('latin1');
  const fonts = (s.match(/\/Font\b/g) || []).length, uni = (s.match(/\/ToUnicode\b/g) || []).length;
  return fonts === 0 ? 'scan' : uni > 0 ? 'text' : 'unknown';
}

const JUNK_AUTHOR = /^(unknown|佚名|未知|epubw\.com|www\..*|.*\.(com|net|org))$/i;
const cleanPeople = list => (list || []).flatMap(a => a.split(/\s*[,，;；]\s*(?=\S)/)).map(a => a.trim()).filter(a => a && !JUNK_AUTHOR.test(a));

const NOT_AUTHOR = /版|丛书|译丛|系列|文库|名著|全集|卷|册|套装|典藏|修订|精选|edition|vol/i;

/** Guess title / author / edition hints from a file name. */
function parseFileName(file) {
  let s = path.basename(file).replace(/\.[^.]+$/, '').replace(/\s*[(（]\s*(z-lib\.org|z-library|zlibrary)\s*[)）]/gi, '')
    .replace(/^\s*epubw\.com\s*-\s*/i, '').replace(/[.\s]*(中文)?(扫描|高清)版?$/, '').trim();
  let author = '';
  let m = s.match(/^\[([^\]]+)\][.\s]*(?:[(（]([^)）]+)[)）])?[.\s]*(.*)$/);
  if (m) { s = m[1]; author = m[3] || ''; }
  else if ((m = s.match(/^《([^》]+)》\s*(?:作者[:：]\s*)?[-—]?\s*(.*)$/))) [, s, author] = m;
  else if ((m = s.match(/^(.*?)\s+by\s+(.+)$/i))) [, s, author] = m;
  else if ((m = s.match(/^(.*?\S)\s*[(（]((?:[^()（）]|[(（][^()（）]*[)）])+)[)）]$/)) && !NOT_AUTHOR.test(m[2])) [, s, author] = m;
  if ((m = s.match(/^(.+?)\s*[-—]\s*([^-—]{2,30})$/)) && hasCJK(m[1]) && /[·•\p{Script=Han}]/u.test(m[2]) && !NOT_AUTHOR.test(m[2])) {
    s = m[1];
    author ||= m[2];
  }
  author = author.split(/[、,，]/)[0].replace(/[\[［]([^\]］]*)$/, '$1').replace(/\s*(著|编著|主编|等)$/, '').trim();
  const title = s.replace(/[《》]/g, '').replace(/_.*译$/, '').trim();
  return { title, author };
}

/** Main title for lookups: drop parentheticals, subtitle after a colon, edition suffixes. */
const shortTitle = t => (t || '').replace(/[(（【\[][^)）】\]]*[)）】\]]/g, ' ').split(/[：:]|——/)[0]
  .replace(/\s*(原书)?第?\s*[\d一二三四五六七八九十]+\s*版.*$/, '').replace(/\s+/g, ' ').trim();

async function walk(dir, rel = '') {
  const out = [];
  for (const d of await fs.readdir(dir, { withFileTypes: true })) {
    if (d.name.startsWith('.') || d.name.startsWith('@') || d.name === '#recycle' || d.name === '_duplicates' || d.name === '_misc' || d.name.endsWith('.sdr')) continue;
    const r = rel ? `${rel}/${d.name}` : d.name;
    if (d.isDirectory()) out.push(...await walk(path.join(dir, d.name), r));
    else if (BOOK_EXT.has(path.extname(d.name).toLowerCase())) out.push(r);
  }
  return out;
}

async function scanFile(rel) {
  const full = path.join(BOOK_DIR, rel);
  const st = await fs.stat(full);
  const fh = await fs.open(full, 'r');
  try {
    const format = path.extname(rel).slice(1).toLowerCase();
    const file = { path: rel, format, size: st.size, mtime: Math.round(st.mtimeMs), md5: await partialMD5(fh, st.size) };
    let meta = {};
    try {
      if (format === 'epub') meta = await readEpub(fh, st.size);
      else if (format === 'mobi' || format === 'azw3' || format === 'azw') meta = await readMobi(fh, st.size);
      else if (format === 'pdf') file.textLayer = await pdfTextLayer(fh, st.size);
    } catch (e) { console.warn(`meta ${rel}: ${e.message}`); }
    if (meta.cover) {
      const f = path.join(COVERS, `file-${file.md5}.jpg`);
      await fs.writeFile(f + '.src', meta.cover);
      try { await run('sips', ['-s', 'format', 'jpeg', '-Z', '600', f + '.src', '--out', f]); file.cover = path.basename(f); }
      catch (e) { console.warn(`cover ${rel}: ${e.message}`); }
      await fs.rm(f + '.src', { force: true });
    }
    delete meta.cover;
    file.meta = meta;
    return file;
  } finally { await fh.close(); }
}

/** Book-folder sidecar on the NAS (metadata.opf + cover.jpg, written by organize-books.mjs or by hand). */
const sidecars = new Map();
function readSidecar(dir) {
  if (!sidecars.has(dir)) sidecars.set(dir, (async () => {
    let opf;
    try { opf = await fs.readFile(path.join(BOOK_DIR, dir, 'metadata.opf'), 'utf8'); } catch { return null; }
    const { dc, vals, metaContent, role, identifiers } = parseOpf(opf);
    const category = metaContent('home:category'), sub = metaContent('home:subcategory');
    const coverFile = path.join(BOOK_DIR, dir, 'cover.jpg');
    let cover = null;
    try {
      const st = await fs.stat(coverFile);
      cover = `nas-${sha1(dir).slice(0, 12)}.jpg`;
      const dst = path.join(COVERS, cover);
      const cur = await fs.stat(dst).catch(() => null);
      if (!cur || cur.mtimeMs < st.mtimeMs) await fs.copyFile(coverFile, dst);
    } catch {}
    const num = v => v === undefined ? undefined : +v || undefined;
    return {
      source: 'nas-opf', sidecar: true,
      doubanId: identifiers.find(i => i.scheme === 'douban')?.v,
      title: vals('title')[0], subtitle: metaContent('douban:subtitle'), origTitle: metaContent('douban:original_title'),
      authors: dc.filter(d => d.k === 'creator' && d.v && role(d) !== 'trl').map(d => d.v),
      translators: dc.filter(d => d.k === 'creator' && d.v && role(d) === 'trl').map(d => d.v),
      publisher: vals('publisher')[0], year: vals('date')[0]?.match(/\d{4}/)?.[0], pages: num(metaContent('douban:pages')),
      series: metaContent('calibre:series'), isbn: identifiers.find(i => i.scheme === 'isbn')?.v,
      rating: num(metaContent('douban:rating')), votes: num(metaContent('douban:votes')),
      intro: vals('description')[0], tags: vals('subject').filter(t => t !== category && t !== sub),
      category, subcategory: sub, clc: metaContent('home:clc'), cover,
    };
  })());
  return sidecars.get(dir);
}

// ---------- remote metadata (cached) ----------

async function cached(name, fn) {
  const f = path.join(META_CACHE, name.replace(/[^\w.-]/g, '_') + '.json');
  try { return JSON.parse(await fs.readFile(f, 'utf8')); } catch {}
  const v = await fn();
  if (v !== undefined) await fs.writeFile(f, JSON.stringify(v));
  return v;
}

let lastDouban = 0, doubanBlocked = false;
/** Polite Douban GET: one request at a time, DOUBAN_GAP_MS apart. Returns undefined (uncached) when blocked. */
async function doubanGet(url, { json = false } = {}) {
  if (doubanBlocked) return undefined;
  const wait = lastDouban + DOUBAN_GAP_MS + Math.random() * 1000 - Date.now();
  if (wait > 0) await sleep(wait);
  lastDouban = Date.now();
  for (let t = 0; t < 3; t++) {
    try {
      const r = await fetch(url, { headers: { 'User-Agent': UA, Referer: 'https://book.douban.com/', 'Accept-Language': 'zh-CN,zh;q=0.9' }, signal: AbortSignal.timeout(20000) });
      if (r.status === 404) return null;
      if (r.status === 403 || r.status === 418 || /sec\.douban\.com/.test(r.url)) {
        doubanBlocked = true;
        console.warn(`Douban blocked (${r.status}); skipping Douban for the rest of this run`);
        return undefined;
      }
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return json ? await r.json() : { url: r.url, html: await r.text() };
    } catch (e) {
      if (t === 2) { console.warn(`douban ${url}: ${e.message}`); return undefined; }
      await sleep(3000 * 2 ** t);
    }
  }
}

function parseDoubanSubject(url, html) {
  const id = url.match(/subject\/(\d+)/)?.[1];
  if (!id || !html.includes('id="info"')) return null;
  const info = html.slice(html.indexOf('id="info"'), html.indexOf('</div>', html.indexOf('id="info"')));
  const text = decodeEntities(info.replace(/<[^>]+>/g, ' ')).replace(/\s+/g, ' ');
  const labels = ['作者', '出版社', '出品方', '副标题', '原作名', '译者', '出版年', '页数', '定价', '装帧', '丛书', 'ISBN', '统一书号'];
  const field = {};
  const re = new RegExp(`(${labels.join('|')})\\s*:`, 'g');
  const marks = [...text.matchAll(re)];
  marks.forEach((m, i) => { field[m[1]] = text.slice(m.index + m[0].length, marks[i + 1]?.index ?? text.length).trim(); });
  const people = s => (s || '').split(/\s*\/\s*/).map(x => x.trim()).filter(Boolean);
  const reportStart = html.indexOf('id="link-report"');
  const report = reportStart >= 0 ? html.slice(reportStart, html.indexOf('<h2', reportStart + 1)) : '';
  const intros = [...report.matchAll(/<div class="intro">([\s\S]*?)<\/div>/g)].map(m => htmlText(m[1])).filter(t => !/\(展开全部\)$/.test(t));
  const tags = (html.match(/criteria = '([^']*)'/)?.[1] || '').split('|').filter(t => t.startsWith('7:')).map(t => t.slice(2));
  const rating = parseFloat(html.match(/property="v:average">\s*([\d.]+)/)?.[1]);
  return {
    source: 'douban', doubanId: id,
    title: decodeEntities(html.match(/property="v:itemreviewed">([^<]*)</)?.[1] || '').trim(),
    subtitle: field['副标题'], origTitle: field['原作名'], authors: people(field['作者']), translators: people(field['译者']),
    publisher: field['出版社'], year: field['出版年']?.match(/\d{4}/)?.[0], pages: parseInt(field['页数']) || undefined,
    series: field['丛书'], isbn: field['ISBN'] || field['统一书号'],
    rating: Number.isFinite(rating) ? rating : undefined, votes: parseInt(html.match(/property="v:votes">(\d+)/)?.[1]) || undefined,
    intro: intros.sort((a, b) => b.length - a.length)[0], tags,
    coverUrl: html.match(/property="og:image" content="([^"]+)"/)?.[1],
  };
}

async function doubanSubject(id) {
  return cached(`douban-subject-${id}`, async () => {
    const r = await doubanGet(`https://book.douban.com/subject/${id}/`);
    return r === undefined ? undefined : r && parseDoubanSubject(r.url, r.html);
  });
}

async function doubanByIsbn(isbn) {
  const idHit = await cached(`douban-isbn-${isbn}`, async () => {
    const r = await doubanGet(`https://book.douban.com/isbn/${isbn}/`);
    if (r === undefined) return undefined;
    const s = r && parseDoubanSubject(r.url, r.html);
    if (s) await fs.writeFile(path.join(META_CACHE, `douban-subject-${s.doubanId}.json`), JSON.stringify(s));
    return { id: s?.doubanId || null };
  });
  return idHit?.id ? doubanSubject(idHit.id) : null;
}

function authorMatch(a, b) {
  const clean = s => norm((s || '').replace(/[\[［(（【][^\]］)）】]*[\]］)）】]/g, ''));
  const x = clean(a), y = clean(b);
  return !!x && !!y && (x.includes(y) || y.includes(x));
}

async function doubanSearch(title, author) {
  const q = shortTitle(title);
  if (!q) return null;
  const list = await cached(`douban-suggest-${sha1(q)}`, () => doubanGet(`https://book.douban.com/j/subject_suggest?q=${encodeURIComponent(q)}`, { json: true }));
  if (!Array.isArray(list)) return null;
  const books = list.filter(x => x.type === 'b');
  const nq = norm(q), nt = norm(title);
  const scored = books.map((b, i) => {
    const bt = norm(b.title);
    let s = bt === nq || bt === nt ? 3 : bt && (nt.includes(bt) || bt.includes(nq)) ? 1 : 0;
    if (author && authorMatch(b.author_name, author)) s += 2;
    return { b, s: s - i * 0.01 };
  }).sort((a, b) => b.s - a.s);
  const best = scored[0];
  if (!best || best.s < (author ? 2.5 : 2.9)) return null;
  return doubanSubject(best.b.id);
}

async function openLibrary(title, author) {
  const q = shortTitle(title);
  const u = new URL('https://openlibrary.org/search.json');
  u.searchParams.set('title', q);
  if (author) u.searchParams.set('author', author);
  u.searchParams.set('limit', '5');
  u.searchParams.set('fields', 'key,title,subtitle,author_name,first_publish_year,isbn,publisher,subject,cover_i,number_of_pages_median');
  const res = await cached(`ol-search-${sha1(u.search)}`, async () => {
    try {
      const r = await fetch(u, { headers: { 'User-Agent': 'movie-shelf/1.0 (home library)' }, signal: AbortSignal.timeout(20000) });
      return r.ok ? await r.json() : undefined;
    } catch (e) { console.warn(`openlibrary: ${e.message}`); return undefined; }
  });
  const doc = res?.docs?.find(d => norm(d.title) === norm(q) || norm(q).includes(norm(d.title)));
  if (!doc) return null;
  const work = await cached(`ol-work-${doc.key.split('/').pop()}`, async () => {
    try { const r = await fetch(`https://openlibrary.org${doc.key}.json`, { signal: AbortSignal.timeout(20000) }); return r.ok ? await r.json() : undefined; }
    catch { return undefined; }
  });
  const desc = typeof work?.description === 'string' ? work.description : work?.description?.value;
  return {
    source: 'openlibrary', olKey: doc.key, title: doc.title, subtitle: doc.subtitle, authors: doc.author_name || [],
    year: doc.first_publish_year && String(doc.first_publish_year), publisher: doc.publisher?.[0], pages: doc.number_of_pages_median,
    isbn: doc.isbn?.find(i => i.length === 13), intro: desc?.split(/\n-{3,}|\n\(\[source\]/)[0].trim(),
    tags: (doc.subject || []).slice(0, 12), coverUrl: doc.cover_i && `https://covers.openlibrary.org/b/id/${doc.cover_i}-L.jpg`,
  };
}

async function downloadCover(url, id) {
  const f = path.join(COVERS, `${id}.jpg`);
  try { await fs.access(f); return path.basename(f); } catch {}
  try {
    const r = await fetch(url, { headers: { 'User-Agent': UA, Referer: 'https://book.douban.com/' }, signal: AbortSignal.timeout(30000) });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    const buf = Buffer.from(await r.arrayBuffer());
    if (buf.length < 2000) throw new Error('placeholder image');
    await fs.writeFile(f, buf);
    return path.basename(f);
  } catch (e) { console.warn(`cover ${url}: ${e.message}`); return null; }
}

// ---------- docs ----------

const yamlStr = s => JSON.stringify(String(s ?? ''));
const yamlList = a => `[${a.map(yamlStr).join(', ')}]`;

function renderDoc(b) {
  const zhText = [b.title, b.subtitle, b.intro, b.series, ...b.authors, ...b.translators, ...b.tags, b.category, b.folder]
    .filter(t => t && hasCJK(t)).join('。');
  const line = (k, v) => v ? `- ${k}：${v}\n` : '';
  return `---
title: ${yamlStr(b.title)}
book_id: ${yamlStr(b.id)}
authors: ${yamlList(b.authors)}
translators: ${yamlList(b.translators)}
publisher: ${yamlStr(b.publisher || '')}
year: ${b.year ?? ''}
isbn: ${yamlStr(b.isbn || '')}
douban_id: ${yamlStr(b.doubanId || '')}
douban_rating: ${b.rating ?? ''}
category: ${yamlStr(b.category)}
formats: ${yamlList(b.files.map(f => f.format))}
nas_path: ${yamlList(b.files.map(f => SMB_BASE + f.path))}
---

# ${b.title}${b.authors.length ? ` — ${b.authors.join('、')}` : ''}

## 书目

${line('书名', b.title)}${line('副标题', b.subtitle)}${line('原作名', b.origTitle)}${line('作者', b.authors.join('、'))}${line('译者', b.translators.join('、'))}${line('出版社', b.publisher)}${line('出版年', b.year)}${line('丛书', b.series)}${line('ISBN', b.isbn)}- 分类：${b.category}${b.folder && b.folder !== b.category ? `（${b.folder}）` : ''}
- 格式：${[...new Set(b.files.map(f => f.format))].join('、')}

## 简介

${b.intro || '（暂无简介）'}

## 标签

${b.tags.join(', ')}

## 分词

${segment(zhText).join(' ')}
`;
}

// ---------- main ----------

async function main() {
  await Promise.all([DOCS, COVERS, META_CACHE, path.join(ROOT, 'data')].map(d => fs.mkdir(d, { recursive: true })));
  const rels = await walk(BOOK_DIR);
  console.log(`NAS book files: ${rels.length}`);

  let scanCache = {};
  if (!args.has('--rescan')) try { scanCache = JSON.parse(await fs.readFile(SCAN_CACHE, 'utf8')); } catch {}
  const files = [];
  for (const rel of rels) {
    const st = await fs.stat(path.join(BOOK_DIR, rel));
    if (st.size === 0) { console.log(`skip (empty / copying): ${rel}`); continue; }
    const c = scanCache[rel];
    if (c && c.size === st.size && c.mtime === Math.round(st.mtimeMs)) { files.push(c); continue; }
    try { files.push(await scanFile(rel)); } catch (e) { console.warn(`skip ${rel}: ${e.message}`); }
    if (files.length % 20 === 0) console.log(`scanned ${files.length}/${rels.length}`);
  }
  await fs.writeFile(SCAN_CACHE, JSON.stringify(Object.fromEntries(files.map(f => [f.path, f]))));

  // Byte-identical copies (same size + KOReader hash) are listed once; prefer the copy not marked "(重复)".
  const seenHash = new Map();
  for (const f of [...files].sort((a, b) => /\(重复\d*\)/.test(a.path) - /\(重复\d*\)/.test(b.path))) {
    const k = f.md5 + ':' + f.size;
    if (seenHash.has(k)) f.duplicateOf = seenHash.get(k); else seenHash.set(k, f.path);
  }
  const dupes = files.filter(f => f.duplicateOf).length;
  if (dupes) console.log(`identical copies hidden: ${dupes}`);
  files.splice(0, files.length, ...files.filter(f => !f.duplicateOf));

  const useRemote = !args.has('--offline');
  const stats = { 'nas-opf': 0, douban: 0, openlibrary: 0, file: 0 };
  const unmatched = [];
  let done = 0;
  for (const f of files) {
    const fromName = parseFileName(f.path);
    const m = f.meta || {};
    const title = (m.title && !/^(unknown|untitled)$/i.test(m.title) && !/\.(pdf|docx?|epub)$/i.test(m.title)) ? m.title : fromName.title;
    const author = cleanPeople(m.authors)[0] || fromName.author;
    const dir = path.posix.dirname(f.path);
    let remote = dir !== '.' ? await readSidecar(dir) : null;
    if (!remote && useRemote) {
      if (m.isbn) remote = await doubanByIsbn(m.isbn);
      if (!remote) remote = await doubanSearch(title, author);
      if (!remote && fromName.title !== title) remote = await doubanSearch(fromName.title, author);
      if (!remote && !hasCJK(title)) remote = await openLibrary(title, author);
    }
    f.local = { title, author, fromName };
    f.remote = remote || null;
    stats[remote?.source || 'file']++;
    if (!remote) unmatched.push(f);
    if (++done % 10 === 0) console.log(`metadata ${done}/${files.length} ${JSON.stringify(stats)}`);
  }

  const works = new Map();
  for (const f of files) {
    const r = f.remote;
    let key = r?.sidecar ? (r.doubanId ? `db${r.doubanId}` : `f${sha1(path.posix.dirname(f.path)).slice(0, 10)}`) : r?.doubanId ? `db${r.doubanId}` : r?.olKey ? `ol${r.olKey.split('/').pop()}` : `h${sha1(norm(shortTitle(f.local.title)) + '|' + norm(f.local.author)).slice(0, 10)}`;
    if (r?.sidecar) {
      const dir = path.posix.dirname(f.path);
      const owner = works.get(key)?.[0];
      if (owner && path.posix.dirname(owner.path) !== dir) key += '-' + sha1(dir).slice(0, 4);
    }
    (works.get(key) || works.set(key, []).get(key)).push(f);
  }

  const books = [];
  for (const [id, group] of works) {
    group.sort((a, b) => ['epub', 'azw3', 'mobi', 'pdf'].indexOf(a.format) - ['epub', 'azw3', 'mobi', 'pdf'].indexOf(b.format));
    const r = group[0].remote || {}, m = group.find(f => f.meta?.title)?.meta || {}, loc = group[0].local;
    const top = r.category || (group[0].path.includes('/') ? group[0].path.split('/')[0] : '未分类');
    const folder = path.posix.dirname(group[0].path);
    let cover = r.cover || group.find(f => f.cover)?.cover || null;
    if (!cover && r.coverUrl && useRemote) cover = await downloadCover(r.coverUrl, id);
    books.push({
      id, source: r.source || 'file', doubanId: r.doubanId, olKey: r.olKey,
      title: r.title || loc.title, subtitle: r.subtitle, origTitle: r.origTitle,
      authors: r.authors?.length ? r.authors : cleanPeople(m.authors).length ? cleanPeople(m.authors) : loc.author ? [loc.author] : [],
      translators: r.translators?.length ? r.translators : m.translators || [],
      publisher: r.publisher || m.publisher, year: r.year || m.date?.match(/\d{4}/)?.[0], pages: r.pages,
      series: r.series || m.series, isbn: r.isbn || m.isbn, rating: r.rating, votes: r.votes,
      intro: r.intro || m.description, tags: [...new Set([...(r.tags || []), ...(m.subjects || [])])].slice(0, 16),
      category: top, subcategory: r.subcategory, clc: r.clc, folder: folder === '.' ? '' : folder, cover,
      added: Math.max(...group.map(f => f.mtime)),
      files: group.map(f => ({ id: f.md5, path: f.path, format: f.format, size: f.size, textLayer: f.textLayer })),
    });
  }

  for (const b of books) await fs.writeFile(path.join(DOCS, `${b.id}.md`), renderDoc(b));
  const keep = new Set(books.map(b => `${b.id}.md`));
  for (const f of await fs.readdir(DOCS)) if (f.endsWith('.md') && !keep.has(f)) await fs.unlink(path.join(DOCS, f));

  books.sort((a, b) => b.added - a.added);
  await fs.writeFile(path.join(ROOT, 'data/books.json'), JSON.stringify(books));

  const lines = unmatched.map(f => `- \`${f.path}\` → 解析为「${f.local.title}」${f.local.author ? ` / ${f.local.author}` : ''}`);
  await fs.writeFile(path.join(ROOT, 'unmatched-books.md'), `# 未匹配豆瓣 / Open Library 的书\n\n生成时间：${new Date().toLocaleString('zh-CN')}。` +
    `这些文件只用了文件内元数据和文件名。${doubanBlocked ? '\n\n**本次运行中豆瓣开始拒绝请求，后面的条目没有查询豆瓣，稍后重跑即可（已查询的结果有缓存）。**' : ''}\n\n${lines.join('\n') || '（无）'}\n`);
  console.log(`books: ${books.length} works from ${files.length} files; metadata ${JSON.stringify(stats)}; covers ${books.filter(b => b.cover).length}`);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) await main();
