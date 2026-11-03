// Builds movie-shelf/docs/<tmdbId>.md (qmd collection), data/movies.json and unmatched.md
// from the NAS movie sources below (read-only), Radarr and TMDB.
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { segment, hasCJK } from './lib/seg.mjs';

const ROOT = path.dirname(fileURLToPath(import.meta.url));
/** NAS movie folders. `library` tags every movie found there; NAS poster.jpg is only written where `nasPosters` is set. */
const SOURCES = [
  { library: 'main', dir: process.env.NAS_DIR || '/Volumes/store/movie', smb: 'smb://192.168.50.94/store/movie/', nasPosters: true },
  { library: 'kids', dir: process.env.KIDS_NAS_DIR || '/Volumes/video/movie', smb: 'smb://192.168.50.94/video/movie/', nasPosters: false },
];
const LIBRARY_LABEL = { main: '主片库', kids: '少儿' };
const RADARR = 'http://localhost:7878';
const DOCS = path.join(ROOT, 'docs');
const CACHE = path.join(ROOT, 'cache');
const POSTERS = path.join(CACHE, 'posters');
const TMDB_CACHE = path.join(CACHE, 'tmdb');

const VIDEO_EXT = new Set(['.mkv', '.mp4', '.avi', '.m2ts', '.ts', '.mov', '.wmv', '.iso', '.rmvb']);
const SUB_EXT = new Set(['.srt', '.ass', '.ssa', '.sub', '.idx', '.sup', '.vtt', '.smi']);

const env = Object.fromEntries((await fs.readFile(path.join(ROOT, '.env'), 'utf8'))
  .split('\n').filter(l => l.includes('=')).map(l => [l.slice(0, l.indexOf('=')).trim(), l.slice(l.indexOf('=') + 1).trim()]));
const TMDB_KEY = env.TMDB_API_KEY;
if (!TMDB_KEY) throw new Error('TMDB_API_KEY missing in .env');

const regionName = new Intl.DisplayNames(['zh-CN'], { type: 'region' });

async function pool(items, n, fn) {
  const out = new Array(items.length);
  let i = 0;
  await Promise.all(Array.from({ length: n }, async () => {
    while (i < items.length) { const k = i++; out[k] = await fn(items[k], k); }
  }));
  return out;
}

async function fetchJSON(url, opts = {}, tries = 4) {
  for (let t = 0; ; t++) {
    try {
      const r = await fetch(url, { ...opts, signal: AbortSignal.timeout(30000) });
      if (r.status === 404) return null;
      if (r.status === 429 || r.status >= 500) throw new Error(`HTTP ${r.status}`);
      if (!r.ok) throw new Error(`HTTP ${r.status} ${url.replace(TMDB_KEY, '***')}`);
      return await r.json();
    } catch (e) {
      if (t >= tries) throw e;
      await new Promise(res => setTimeout(res, 1000 * 2 ** t));
    }
  }
}

async function cached(name, fn) {
  const f = path.join(TMDB_CACHE, name + '.json');
  try { return JSON.parse(await fs.readFile(f, 'utf8')); } catch {}
  const v = await fn();
  await fs.writeFile(f, JSON.stringify(v));
  return v;
}

const tmdb = (p, params = {}) => {
  const u = new URL('https://api.themoviedb.org/3' + p);
  u.searchParams.set('api_key', TMDB_KEY);
  for (const [k, v] of Object.entries(params)) u.searchParams.set(k, v);
  return fetchJSON(u.toString());
};

async function radarrMovies() {
  try {
    const xml = await fs.readFile(path.join(process.env.HOME, 'Library/Application Support/Radarr/config.xml'), 'utf8');
    const key = xml.match(/<ApiKey>([^<]+)<\/ApiKey>/)?.[1];
    return (await fetchJSON(`${RADARR}/api/v3/movie`, { headers: { 'X-Api-Key': key } }, 1)) || [];
  } catch (e) {
    console.warn('Radarr unavailable:', e.message);
    return [];
  }
}

const norm = s => (s || '').normalize('NFKD').replace(/[̀-ͯ]/g, '').toLowerCase()
  .replace(/&/g, 'and').replace(/[^\p{L}\p{N}]+/gu, '');

/** Parse a raw release / folder name into candidate titles and year. */
function parseName(name) {
  let s = name.replace(/\.(mkv|mp4|avi|m2ts|ts|mov|wmv|iso|rmvb)$/i, '');
  const range = /(19|20)\d{2}\s*-\s*(19|20)\d{2}/.test(s);
  const m = s.match(/^(.*?)[\s.([]+((?:19|20)\d{2})(?:[\s.)\]]|$)/);
  let title = m ? m[1] : s.replace(/[.\s](720p|1080p|2160p|4k|bluray|web).*$/i, '');
  const year = m ? +m[2] : null;
  title = title.replace(/[._]/g, ' ').replace(/\s+/g, ' ').trim();
  const titles = title.split(/\s+AKA\s+/i).map(t => t.trim()).filter(Boolean);
  return { titles, year, range };
}

async function searchTmdb({ titles, year }) {
  const cands = new Map();
  for (const t of titles) {
    for (const language of ['zh-CN', 'en-US']) {
      const params = { query: t, language, include_adult: 'false' };
      const r = await cached(`search_${norm(t)}_${year || 'x'}_${language}`, () => tmdb('/search/movie', params));
      for (const x of r?.results || []) {
        const c = cands.get(x.id) || { id: x.id, names: new Set(), year: +(x.release_date || '').slice(0, 4) || null, popularity: x.popularity };
        c.names.add(norm(x.title)); c.names.add(norm(x.original_title));
        cands.set(x.id, c);
      }
    }
  }
  const wanted = new Set(titles.map(norm));
  let hits = [...cands.values()].filter(c => [...c.names].some(n => wanted.has(n)));
  if (year) hits = hits.filter(c => c.year && Math.abs(c.year - year) <= 1);
  if (hits.length === 1) return { id: hits[0].id };
  if (year && hits.length > 1) {
    const exact = hits.filter(c => c.year === year);
    if (exact.length === 1) return { id: exact[0].id };
  }
  return { reason: hits.length ? `${hits.length} 个同名候选，无法确定` : 'TMDB 无标题+年份一致的结果' };
}

async function walk(dir, depth = 2) {
  const out = [];
  for (const e of await fs.readdir(dir, { withFileTypes: true })) {
    if (e.name.startsWith('.')) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) { if (depth > 0) out.push(...await walk(p, depth - 1)); }
    else out.push(p);
  }
  return out;
}

const LANG_TOKENS = [
  [/^(chs|sc|gb|zh-?hans|zho?-?cn|chn|简|简体|简中|简体中文|cn)$/i, '简体中文'],
  [/^(cht|tc|big5|zh-?hant|zh-?tw|zh-?hk|繁|繁体|繁體|繁中|繁体中文)$/i, '繁体中文'],
  [/^(chi|zh|zho|chinese|中文|中字|国语)$/i, '中文'],
  [/^(eng?|english|英|英文)$/i, '英语'],
  [/^(jpn?|ja|japanese|日|日文)$/i, '日语'],
  [/^(kor?|korean|韩)$/i, '韩语'],
  [/^(fre|fra|fr|french)$/i, '法语'],
  [/^(ger|deu|de|german)$/i, '德语'],
  [/^(spa|es|spanish)$/i, '西班牙语'],
  [/^(ita|it|italian)$/i, '意大利语'],
  [/^(rus|ru|russian)$/i, '俄语'],
  [/^(por|pt|portuguese)$/i, '葡萄牙语'],
];

function subLangs(file) {
  const base = path.basename(file).replace(/\.[^.]+$/, '');
  const langs = new Set();
  for (const tok of base.split(/[.\-_&\s[\]()]+/)) {
    for (const [re, name] of LANG_TOKENS) if (re.test(tok)) langs.add(name);
  }
  if (!langs.size && hasCJK(base)) langs.add('中文');
  if (langs.has('中文') && (langs.has('简体中文') || langs.has('繁体中文'))) langs.delete('中文');
  return [...langs];
}

async function scanEntry(src, name, isDir, rootFiles) {
  const full = path.join(src.dir, name);
  const st = await fs.stat(full);
  let files;
  if (isDir) files = await walk(full);
  else {
    const stem = name.replace(/\.[^.]+$/, '');
    files = [full, ...rootFiles.filter(f => f !== name && f.startsWith(stem)).map(f => path.join(src.dir, f))];
  }
  const videos = files.filter(f => VIDEO_EXT.has(path.extname(f).toLowerCase()) && !/sample/i.test(path.basename(f)));
  const subs = files.filter(f => SUB_EXT.has(path.extname(f).toLowerCase()));
  let added = st.birthtimeMs || st.mtimeMs;
  let videoFiles = [];
  if (videos.length) {
    const vs = await Promise.all(videos.map(v => fs.stat(v)));
    added = Math.min(...vs.map(v => v.birthtimeMs || v.mtimeMs));
    videoFiles = videos.map((v, i) => ({ path: path.relative(src.dir, v), size: vs[i].size }));
  }
  const langs = new Set();
  for (const s of subs) for (const l of subLangs(s)) langs.add(l);
  if (subs.length && !langs.size) langs.add('未知');
  return {
    name, isDir, videos: videos.length, videoFiles, added, dirMtime: st.mtimeMs,
    subs: subs.map(s => path.relative(src.dir, s)), subLangs: [...langs],
  };
}

async function personCJKName(id, fallback) {
  const p = await cached(`person_${id}`, () => tmdb(`/person/${id}`, { language: 'zh-CN' }));
  const akas = (p?.also_known_as || []).filter(hasCJK);
  const simp = akas.find(a => /[一-鿿]/.test(a) && !/[぀-ヿ가-힯]/.test(a));
  return { name: fallback, cjk: simp || null, akas: akas.slice(0, 4) };
}

function region(d) {
  const lang = d.original_language;
  const countries = (d.origin_country || d.production_countries?.map(c => c.iso_3166_1) || []);
  if (['zh', 'cn', 'yue'].includes(lang) || countries.some(c => ['CN', 'HK', 'TW', 'MO'].includes(c)) && !['ja', 'ko'].includes(lang)) return '华语';
  if (['ja', 'ko'].includes(lang) || (countries.some(c => ['JP', 'KR'].includes(c)) && lang !== 'en')) return '日韩';
  return '欧美';
}

async function movieDetails(id) {
  const zh = await cached(`movie_${id}_zh`, () => tmdb(`/movie/${id}`, { language: 'zh-CN', append_to_response: 'credits,keywords' }));
  if (!zh) return null;
  const en = await cached(`movie_${id}_en`, () => tmdb(`/movie/${id}`, { language: 'en-US' }));
  const crew = zh.credits?.crew || [];
  const directorsRaw = crew.filter(c => c.job === 'Director').slice(0, 3);
  const castRaw = (zh.credits?.cast || []).slice(0, 8);
  const directors = await Promise.all(directorsRaw.map(c => personCJKName(c.id, c.name)));
  const cast = await Promise.all(castRaw.map(async (c, i) => i < 5 ? { ...(await personCJKName(c.id, c.name)), character: c.character } : { name: c.name, cjk: null, akas: [], character: c.character }));
  const countryCodes = (zh.production_countries || []).map(c => c.iso_3166_1);
  const posterPath = zh.poster_path || en?.poster_path;
  return {
    id, title: zh.title, enTitle: en?.title || zh.original_title, originalTitle: zh.original_title,
    year: +(zh.release_date || '').slice(0, 4) || null, runtime: zh.runtime || null,
    genres: (zh.genres || []).map(g => g.name),
    keywords: (zh.keywords?.keywords || []).map(k => k.name),
    countries: countryCodes.map(c => { try { return regionName.of(c); } catch { return c; } }),
    region: region(zh), originalLanguage: zh.original_language,
    overview: zh.overview || '', overviewEn: en?.overview || '',
    tagline: zh.tagline || '', imdbId: zh.imdb_id || null, rating: zh.vote_average || null,
    directors, cast, posterPath,
  };
}

async function poster(id, p) {
  if (!p) return null;
  const f = path.join(POSTERS, `${id}.jpg`);
  try { await fs.access(f); return `${id}.jpg`; } catch {}
  for (let t = 0; t < 3; t++) {
    try {
      const r = await fetch(`https://image.tmdb.org/t/p/w342${p}`, { signal: AbortSignal.timeout(30000) });
      if (!r.ok) throw new Error(`poster HTTP ${r.status}`);
      await fs.writeFile(f, Buffer.from(await r.arrayBuffer()));
      return `${id}.jpg`;
    } catch (e) { if (t === 2) console.warn(`poster ${id}:`, e.message); }
  }
  return null;
}

function radarrImdb(r, tmdbImdbId) {
  const imdbId = r?.imdbId || tmdbImdbId || null;
  const v = r?.ratings?.imdb;
  if (v?.value && v?.votes) return { imdbId, imdbRating: v.value, imdbVotes: v.votes, imdbSource: 'radarr' };
  return { imdbId, imdbRating: null, imdbVotes: null, imdbSource: null };
}

/** Fill missing IMDb ratings from IMDb's daily title.ratings.tsv.gz dataset (cached per day). */
async function fillImdbFromDataset(movies) {
  const missing = new Map(movies.filter(m => !m.imdbRating && m.imdbId).map(m => [m.imdbId, m]));
  if (!missing.size) return;
  const f = path.join(CACHE, `imdb-title.ratings-${new Date().toISOString().slice(0, 10)}.tsv.gz`);
  try {
    let buf;
    try { buf = await fs.readFile(f); } catch {
      const r = await fetch('https://datasets.imdbws.com/title.ratings.tsv.gz', { signal: AbortSignal.timeout(120000) });
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      buf = Buffer.from(await r.arrayBuffer());
      for (const old of await fs.readdir(CACHE)) if (old.startsWith('imdb-title.ratings-')) await fs.unlink(path.join(CACHE, old));
      await fs.writeFile(f, buf);
    }
    const { gunzipSync } = await import('node:zlib');
    for (const line of gunzipSync(buf).toString('utf8').split('\n')) {
      const m = missing.get(line.slice(0, line.indexOf('\t')));
      if (!m) continue;
      const [, rating, votes] = line.split('\t');
      Object.assign(m, { imdbRating: +rating, imdbVotes: +votes, imdbSource: 'imdb-dataset' });
    }
  } catch (e) {
    console.warn('IMDb dataset unavailable:', e.message);
  }
}

const NAS_POSTER_NAMES = ['poster.jpg', 'folder.jpg', 'cover.jpg'];

/**
 * Write a w780 TMDB poster as poster.jpg into each NAS movie folder that has no
 * poster.jpg/folder.jpg/cover.jpg yet. Never overwrites; every created file is
 * appended to logs/nas-posters-<date>.txt so it can be undone.
 */
async function writeNasPosters(movies) {
  const dir = path.join(CACHE, 'posters-w780');
  await fs.mkdir(dir, { recursive: true });
  const logFile = path.join(ROOT, 'logs', `nas-posters-${new Date().toISOString().slice(0, 10)}.txt`);
  const stats = { written: 0, skipped: 0, noPoster: 0, failed: 0 };
  const skipped = [];
  const jobs = movies.flatMap(m => m.nasEntries.filter(e => e.src.nasPosters).map(e => ({ m, folder: path.join(e.src.dir, e.name) })));
  await pool(jobs, 4, async ({ m, folder }) => {
    if (!m.posterPath) { stats.noPoster++; return; }
    try {
      if (!(await fs.stat(folder)).isDirectory()) return;
      const names = new Set((await fs.readdir(folder)).map(n => n.toLowerCase()));
      const existing = NAS_POSTER_NAMES.find(n => names.has(n));
      if (existing) { stats.skipped++; skipped.push(`${path.basename(folder)} (${existing})`); return; }
      const src = path.join(dir, `${m.id}.jpg`);
      try { await fs.access(src); } catch {
        const r = await fetch(`https://image.tmdb.org/t/p/w780${m.posterPath}`, { signal: AbortSignal.timeout(60000) });
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        const buf = Buffer.from(await r.arrayBuffer());
        if (buf.length > 1024 * 1024) throw new Error(`w780 poster too large (${buf.length} bytes)`);
        await fs.writeFile(src, buf);
      }
      const dest = path.join(folder, 'poster.jpg');
      await fs.copyFile(src, dest, fs.constants.COPYFILE_EXCL);
      await fs.appendFile(logFile, dest + '\n');
      stats.written++;
    } catch (e) {
      if (e.code === 'EEXIST') { stats.skipped++; skipped.push(`${path.basename(folder)} (poster.jpg)`); return; }
      stats.failed++;
      console.warn(`nas poster ${folder}: ${e.code || e.message}`);
    }
  });
  if (skipped.length) await fs.writeFile(path.join(ROOT, 'logs', 'nas-posters-skipped.txt'), skipped.sort().join('\n') + '\n');
  console.log(`nas posters: ${JSON.stringify(stats)}${stats.written ? ` (log: ${path.relative(ROOT, logFile)})` : ''}`);
}

const yamlStr = s => JSON.stringify(String(s ?? ''));
const yamlList = a => `[${a.map(yamlStr).join(', ')}]`;
const personLabel = p => p.cjk && p.cjk !== p.name ? `${p.cjk}（${p.name}）` : p.name;

function renderDoc(m) {
  const seg = t => segment(t).join(' ');
  const zhText = [m.title, m.overview, m.tagline, ...m.genres, ...m.countries,
    ...m.directors.flatMap(d => [d.cjk, ...d.akas]), ...m.cast.flatMap(c => [c.cjk, ...c.akas])]
    .filter(t => t && hasCJK(t)).join('。');
  return `---
title: ${yamlStr(m.title)}
tmdb_id: ${m.id}
year: ${m.year ?? ''}
genres: ${yamlList(m.genres)}
countries: ${yamlList(m.countries)}
region: ${m.region}
director: ${yamlStr(m.directors.map(d => d.cjk || d.name).join(' / '))}
library: ${yamlList(m.libraries)}
nas_path: ${yamlList(m.nasPaths)}
has_subs: ${m.subLangs.length > 0}
video_file: ${yamlStr(m.videos[0]?.path || '')}
imdb_id: ${yamlStr(m.imdbId || '')}
imdb_rating: ${m.imdbRating ?? ''}
imdb_votes: ${m.imdbVotes ?? ''}
sub_langs: ${yamlList(m.subLangs)}
---

# ${m.title}${m.year ? ` (${m.year})` : ''}

## 标题

- 中文名：${m.title}
- 英文名：${m.enTitle}
- 原名：${m.originalTitle}
- 年份：${m.year ?? '未知'}${m.runtime ? `，片长 ${m.runtime} 分钟` : ''}
- 类型：${m.genres.join('、')}
- 国家/地区：${m.countries.join('、')}（${m.region}）
- 片库：${m.libraries.map(l => LIBRARY_LABEL[l] || l).join('、')}

## 简介

${m.overview || '（暂无中文简介）'}

${m.overviewEn}

## 关键词

${[...m.genres, ...m.keywords].join(', ')}

## 演职员

- 导演：${m.directors.map(personLabel).join('、')}
- 主演：${m.cast.map(personLabel).join('、')}

## 分词

${seg(zhText)}
`;
}

async function main() {
  await Promise.all([DOCS, POSTERS, TMDB_CACHE, path.join(ROOT, 'data')].map(d => fs.mkdir(d, { recursive: true })));
  const entries = [];
  for (const src of SOURCES) {
    const dirents = (await fs.readdir(src.dir, { withFileTypes: true })).filter(d => !d.name.startsWith('.') && !d.name.startsWith('@'));
    const rootNames = dirents.map(d => d.name);
    const list = dirents.filter(d => d.isDirectory() || VIDEO_EXT.has(path.extname(d.name).toLowerCase()));
    entries.push(...list.map(dirent => ({ src, dirent, rootNames })));
    console.log(`NAS entries (${src.library}, ${src.dir}): ${list.length}`);
  }

  const scanCacheFile = path.join(CACHE, 'scan.json');
  let scanCache = {};
  if (!process.argv.includes('--rescan')) try { scanCache = JSON.parse(await fs.readFile(scanCacheFile, 'utf8')); } catch {}
  const newScanCache = {};
  let done = 0;

  const radarrList = await radarrMovies();
  const radarr = new Map(radarrList.map(m => [m.path, m]));
  const radarrById = new Map(radarrList.map(m => [m.tmdbId, m]));
  console.log(`Radarr movies: ${radarr.size}`);

  const unmatched = [];
  const scanned = await pool(entries, 8, async ({ src, dirent, rootNames }) => {
    const name = dirent.name;
    const key = src === SOURCES[0] ? name : `${src.library}:${name}`;
    const label = src === SOURCES[0] ? name : path.join(src.dir, name);
    let scan = scanCache[key]?.videoFiles ? scanCache[key] : null;
    if (scan) {
      try { if ((await fs.stat(path.join(src.dir, name))).mtimeMs !== scan.dirMtime) scan = null; } catch { scan = null; }
    }
    if (!scan) {
      try { scan = await scanEntry(src, name, dirent.isDirectory(), rootNames); }
      catch (e) { console.warn(`skip ${label}: ${e.code || e.message}`); return null; }
    }
    newScanCache[key] = scan;
    if (++done % 10 === 0) {
      console.log(`scanned ${done}/${entries.length}`);
      await fs.writeFile(scanCacheFile, JSON.stringify({ ...scanCache, ...newScanCache }));
    }
    let id = null, source = null;
    const r = radarr.get(path.join(src.dir, name));
    if (r?.tmdbId) { id = r.tmdbId; source = 'radarr'; }
    else if (name.match(/\{tmdb-(\d+)\}/)) { id = +name.match(/\{tmdb-(\d+)\}/)[1]; source = 'folder'; }
    else {
      const parsed = parseName(name);
      if (parsed.range) { unmatched.push({ name: label, reason: '多部合集（年份区间），未自动拆分' }); return null; }
      if (!parsed.titles.length) { unmatched.push({ name: label, reason: '无法解析片名' }); return null; }
      let res;
      try { res = await searchTmdb(parsed); }
      catch (e) { unmatched.push({ name: label, reason: `TMDB 请求失败：${e.cause?.code || e.message}` }); return null; }
      if (!res.id) { unmatched.push({ name: label, reason: `${res.reason}（解析为「${parsed.titles.join(' / ')}」${parsed.year || '无年份'}）` }); return null; }
      id = res.id; source = 'tmdb-search';
    }
    return { ...scan, id, source, src, label };
  });

  await fs.writeFile(scanCacheFile, JSON.stringify(newScanCache));

  const byId = new Map();
  for (const s of scanned.filter(Boolean)) {
    const m = byId.get(s.id) || { id: s.id, entries: [] };
    m.entries.push(s); byId.set(s.id, m);
  }

  const movies = (await pool([...byId.values()], 6, async g => {
    let d;
    try { d = await movieDetails(g.id); }
    catch (e) { for (const x of g.entries) unmatched.push({ name: x.label, reason: `TMDB 请求失败：${e.cause?.code || e.message}` }); return null; }
    if (!d) { for (const e of g.entries) unmatched.push({ name: e.label, reason: `TMDB id ${g.id} 不存在` }); return null; }
    const subLangs = [...new Set(g.entries.flatMap(e => e.subLangs))];
    const m = {
      ...d,
      nasEntries: g.entries.map(e => ({ src: e.src, name: e.name })),
      nasPaths: g.entries.map(e => e.src.smb + e.name),
      libraries: [...new Set(g.entries.map(e => e.src.library))],
      sources: [...new Set(g.entries.map(e => e.source))],
      added: Math.min(...g.entries.map(e => e.added)),
      subs: g.entries.flatMap(e => e.subs), subLangs,
      videos: g.entries.flatMap(e => (e.videoFiles || []).map(v => ({ ...v, dir: e.src.dir }))).sort((a, b) => b.size - a.size),
      poster: await poster(g.id, d.posterPath),
      ...radarrImdb(radarrById.get(g.id), d.imdbId),
    };
    return m;
  })).filter(Boolean);

  await fillImdbFromDataset(movies);
  for (const m of movies) await fs.writeFile(path.join(DOCS, `${m.id}.md`), renderDoc(m));
  console.log(`imdb ratings: ${movies.filter(m => m.imdbRating).length}/${movies.length} ` +
    `(radarr ${movies.filter(m => m.imdbSource === 'radarr').length}, dataset ${movies.filter(m => m.imdbSource === 'imdb-dataset').length})`);

  const keep = new Set(movies.map(m => `${m.id}.md`));
  for (const f of await fs.readdir(DOCS)) if (f.endsWith('.md') && !keep.has(f)) await fs.unlink(path.join(DOCS, f));

  movies.sort((a, b) => b.added - a.added);
  const slim = movies.map(m => ({
    id: m.id, title: m.title, enTitle: m.enTitle, originalTitle: m.originalTitle, year: m.year,
    runtime: m.runtime, genres: m.genres, countries: m.countries, region: m.region, rating: m.rating,
    overview: m.overview || m.overviewEn, directors: m.directors.map(personLabel),
    cast: m.cast.slice(0, 6).map(personLabel), subLangs: m.subLangs, subs: m.subs,
    nasPaths: m.nasPaths, libraries: m.libraries, added: m.added, poster: m.poster,
    videos: m.videos, imdbId: m.imdbId, imdbRating: m.imdbRating, imdbVotes: m.imdbVotes,
  }));
  await fs.writeFile(path.join(ROOT, 'data/movies.json'), JSON.stringify(slim));

  unmatched.sort((a, b) => a.name.localeCompare(b.name));
  await fs.writeFile(path.join(ROOT, 'unmatched.md'), `# 未匹配的条目

生成时间：${new Date().toLocaleString('zh-CN')}。以下 NAS 条目无法高置信度匹配到 TMDB，未写入片库。
可以在 Radarr 中手动导入，或把文件夹改名为 \`片名 (年份) {tmdb-ID}\` 后重新生成。

${unmatched.map(u => `- \`${u.name}\` — ${u.reason}`).join('\n') || '（无）'}
`);
  console.log(`movies: ${movies.length} (from ${scanned.filter(Boolean).length} NAS entries), unmatched: ${unmatched.length}`);

  if (!process.argv.includes('--no-nas-posters')) await writeNasPosters(movies);
}

await main();
