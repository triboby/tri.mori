// 片库 web server: shelf UIs for movies and books, /api/*, qmd hybrid search (no rerank, one CLI process per search),
// an OPDS catalog for KOReader (/opds) and a KOReader progress sync server (/kosync).
import http from 'node:http';
import fs from 'node:fs/promises';
import { existsSync, createReadStream } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import Database from 'better-sqlite3';
import { segment, hasCJK } from './lib/seg.mjs';

const ROOT = path.dirname(fileURLToPath(import.meta.url));
const PORT = +(process.env.PORT || 8686);
const HOST = process.env.HOST || '0.0.0.0';
const QMD_INDEX = process.env.QMD_INDEX || 'movies';
const DB_PATH = path.join(os.homedir(), `.cache/qmd/${QMD_INDEX}.sqlite`);
const QMD_CLI = path.join(ROOT, 'node_modules/@tobilu/qmd/dist/cli/qmd.js');
const BOOK_DIR = process.env.BOOK_DIR || '/Volumes/store/book';
const KOSYNC_REGISTRATION = process.env.KOSYNC_REGISTRATION !== 'off';

const MIME = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css', '.jpg': 'image/jpeg', '.svg': 'image/svg+xml', '.json': 'application/json' };
const BOOK_MIME = {
  epub: 'application/epub+zip', pdf: 'application/pdf', mobi: 'application/x-mobipocket-ebook', azw3: 'application/vnd.amazon.ebook',
  azw: 'application/vnd.amazon.ebook', djvu: 'image/vnd.djvu', cbz: 'application/vnd.comicbook+zip', fb2: 'application/x-fictionbook+xml', txt: 'text/plain; charset=utf-8',
};

/** JSON file loader that re-reads when the file's mtime changes. */
function jsonList(file, key) {
  let state = { mtime: -1, list: [], byId: new Map() };
  return async () => {
    let st;
    try { st = await fs.stat(file); } catch { return state; }
    if (st.mtimeMs !== state.mtime) {
      const list = JSON.parse(await fs.readFile(file, 'utf8'));
      state = { mtime: st.mtimeMs, list, byId: new Map(list.map(m => [m[key], m])) };
      if (key === 'id' && list[0]?.files) state.byFile = new Map(list.flatMap(b => b.files.map(f => [f.id, { book: b, file: f }])));
    }
    return state;
  };
}
const loadMovies = jsonList(path.join(ROOT, 'data/movies.json'), 'id');
const loadBooks = jsonList(path.join(ROOT, 'data/books.json'), 'id');

let qmdQueue = Promise.resolve();

/** Runs one `qmd query` CLI process (searches are serialized so at most one model is loaded) and returns its JSON results. */
function qmdQuery(queries, collections, limit) {
  const line = t => { t = t.replace(/\s+/g, ' '); return (t.match(/"/g) || []).length % 2 ? t.replaceAll('"', '') : t; };
  const doc = queries.map(({ type, query }) => `${type}: ${line(query)}`).join('\n');
  const args = [QMD_CLI, '--index', QMD_INDEX, 'query', doc, '--no-rerank', '--format', 'json', '-n', String(limit), ...collections.flatMap(c => ['-c', c])];
  const run = () => new Promise((resolve, reject) => {
    execFile(process.execPath, args, { timeout: 60_000, maxBuffer: 16 << 20 }, (err, stdout, stderr) => {
      if (err) return reject(new Error(`qmd query failed: ${(stderr || err.message).trim().split('\n').pop()}`));
      try { resolve(JSON.parse(stdout || '[]')); } catch (e) { reject(new Error(`qmd query returned invalid JSON: ${e.message}`)); }
    });
  });
  const p = qmdQueue.then(run, run);
  qmdQueue = p.catch(() => {});
  return p;
}

/** Turn a user query into qmd typed sub-queries: jieba-segmented lex + raw vec. */
function buildQueries(q) {
  const words = hasCJK(q) ? segment(q, { search: false }) : q.split(/\s+/).filter(Boolean);
  const lex = words.join(' ');
  const queries = [];
  if (lex) queries.push({ type: 'lex', query: lex });
  queries.push({ type: 'vec', query: q });
  return queries;
}

/** Hybrid search over the given qmd collections; hits carry their kind ('movies' | 'books') and id. */
async function search(q, collections = ['movies'], limit = 24) {
  const t0 = performance.now();
  const queries = buildQueries(q);
  const results = await qmdQuery(queries, collections, limit);
  const [{ byId: movieById }, { byId: bookById }] = await Promise.all([loadMovies(), loadBooks()]);
  const seen = new Set();
  const hits = [];
  for (const r of results) {
    const ref = (r.file || '').replace(/\?.*$/, '');
    const kind = collections.length === 1 ? collections[0] : ref.match(/qmd:\/\/(\w+)\//)?.[1];
    const base = path.basename(ref, '.md');
    const id = kind === 'movies' ? +base : base;
    if (!id || seen.has(kind + id) || !(kind === 'movies' ? movieById : bookById).has(id)) continue;
    seen.add(kind + id);
    hits.push({ kind, id, score: Math.round(r.score * 1000) / 1000 });
  }
  return { q, queries, ms: Math.round(performance.now() - t0), hits };
}

// ---------- KOReader progress sync (koreader-sync-server API) ----------

const kdb = new Database(path.join(ROOT, 'data/kosync.sqlite'));
kdb.pragma('journal_mode = WAL');
kdb.exec(`
  CREATE TABLE IF NOT EXISTS users (username TEXT PRIMARY KEY, userkey TEXT NOT NULL, created INTEGER NOT NULL);
  CREATE TABLE IF NOT EXISTS progress (
    username TEXT NOT NULL, document TEXT NOT NULL, progress TEXT, percentage REAL,
    device TEXT, device_id TEXT, timestamp INTEGER NOT NULL, PRIMARY KEY (username, document));
`);
const keyHash = k => crypto.createHash('sha256').update(String(k)).digest('hex');
const sql = {
  user: kdb.prepare('SELECT userkey FROM users WHERE username = ?'),
  addUser: kdb.prepare('INSERT INTO users (username, userkey, created) VALUES (?, ?, ?)'),
  get: kdb.prepare('SELECT document, progress, percentage, device, device_id, timestamp FROM progress WHERE username = ? AND document = ?'),
  put: kdb.prepare(`INSERT INTO progress (username, document, progress, percentage, device, device_id, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT (username, document) DO UPDATE SET progress = excluded.progress, percentage = excluded.percentage,
    device = excluded.device, device_id = excluded.device_id, timestamp = excluded.timestamp`),
  latest: kdb.prepare('SELECT document, MAX(timestamp) AS timestamp, percentage, device, username FROM progress GROUP BY document'),
};

async function readJSON(req) {
  let body = '';
  for await (const chunk of req) { body += chunk; if (body.length > 65536) throw new Error('body too large'); }
  try { return JSON.parse(body || '{}'); } catch { return {}; }
}

function kosyncAuth(req) {
  const user = req.headers['x-auth-user'], key = req.headers['x-auth-key'];
  const row = user && key && sql.user.get(String(user));
  return row && row.userkey === keyHash(key) ? String(user) : null;
}

async function kosync(req, res, sub) {
  const json = (code, body) => send(res, code, body, 'application/json');
  if (sub === '/healthcheck') return json(200, { state: 'OK' });
  if (sub === '/users/create' && req.method === 'POST') {
    const { username, password } = await readJSON(req);
    if (!KOSYNC_REGISTRATION) return json(402, { code: 2005, message: 'Registration is disabled.' });
    if (!username || !password || typeof username !== 'string') return json(403, { code: 2003, message: 'Invalid request' });
    if (sql.user.get(username)) return json(402, { code: 2002, message: 'Username is already registered.' });
    sql.addUser.run(username, keyHash(password), Date.now());
    console.log(`${new Date().toISOString()} kosync user created ${JSON.stringify(username)}`);
    return json(201, { username });
  }
  const user = kosyncAuth(req);
  if (!user) return json(401, { code: 2001, message: 'Unauthorized' });
  if (sub === '/users/auth') return json(200, { authorized: 'OK' });
  if (sub === '/syncs/progress' && req.method === 'PUT') {
    const b = await readJSON(req);
    if (!b.document || b.progress == null) return json(403, { code: 2003, message: 'Invalid request' });
    const ts = Math.floor(Date.now() / 1000);
    sql.put.run(user, String(b.document), String(b.progress), +b.percentage || 0, String(b.device || ''), String(b.device_id || ''), ts);
    return json(200, { document: b.document, timestamp: ts });
  }
  const m = sub.match(/^\/syncs\/progress\/([^/]+)$/);
  if (m && req.method === 'GET') return json(200, sql.get.get(user, decodeURIComponent(m[1])) || {});
  return json(404, { code: 2000, message: 'Not found' });
}

/** Latest reading progress per book file (KOReader partial MD5), across all sync users. */
const readingProgress = () => new Map(sql.latest.all().map(r => [r.document, r]));

// ---------- OPDS 1.2 catalog ----------

const xml = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;' }[c]));
const NAV = 'application/atom+xml;profile=opds-catalog;kind=navigation';
const ACQ = 'application/atom+xml;profile=opds-catalog;kind=acquisition';
const fmtSize = b => b >= 1e6 ? (b / 1e6).toFixed(1) + ' MB' : Math.max(1, Math.round(b / 1e3)) + ' KB';
const safeName = s => s.replace(/[\\/:*?"<>|\n\r]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 120);
const fileHref = (b, f) => `/books/file/${f.id}/${encodeURIComponent(path.basename(f.path))}`;

function feed({ id, title, self, kind, entries, updated }) {
  return `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/" xmlns:opds="http://opds-spec.org/2010/catalog">
  <id>${xml(id)}</id>
  <title>${xml(title)}</title>
  <updated>${updated}</updated>
  <author><name>家庭书架</name></author>
  <link rel="self" href="${xml(self)}" type="${kind}"/>
  <link rel="start" href="/opds" type="${NAV}"/>
  <link rel="search" href="/opds/search.xml" type="application/opensearchdescription+xml"/>
  <link rel="search" href="/opds/search?q={searchTerms}" type="${ACQ}"/>
${entries.join('\n')}
</feed>`;
}

const navEntry = (title, href, content, updated, type = ACQ) => `  <entry>
    <title>${xml(title)}</title><id>urn:shelf:nav:${xml(href)}</id><updated>${updated}</updated>
    <content type="text">${xml(content)}</content>
    <link rel="subsection" href="${xml(href)}" type="${type}"/>
  </entry>`;

function bookEntry(b) {
  const updated = new Date(b.added).toISOString();
  const summary = [b.rating ? `豆瓣 ${b.rating}` : '', b.year, b.publisher, b.intro].filter(Boolean).join(' · ');
  return `  <entry>
    <title>${xml(b.title)}</title><id>urn:shelf:book:${xml(b.id)}</id><updated>${updated}</updated>
${b.authors.map(a => `    <author><name>${xml(a)}</name></author>`).join('\n')}
${b.publisher ? `    <dc:publisher>${xml(b.publisher)}</dc:publisher>\n` : ''}${b.year ? `    <dc:issued>${xml(b.year)}</dc:issued>\n` : ''}${b.isbn ? `    <dc:identifier>urn:isbn:${xml(b.isbn)}</dc:identifier>\n` : ''}${b.tags.map(t => `    <category term="${xml(t)}" label="${xml(t)}"/>`).join('\n')}
    <summary type="text">${xml(summary.slice(0, 1200))}</summary>
${b.cover ? `    <link rel="http://opds-spec.org/image" href="/covers/${xml(b.cover)}" type="image/jpeg"/>
    <link rel="http://opds-spec.org/image/thumbnail" href="/covers/${xml(b.cover)}" type="image/jpeg"/>\n` : ''}${b.files.map(f => `    <link rel="http://opds-spec.org/acquisition" href="${xml(fileHref(b, f))}" type="${BOOK_MIME[f.format] || 'application/octet-stream'}" title="${f.format.toUpperCase()} ${fmtSize(f.size)}"/>`).join('\n')}
  </entry>`;
}

const bookText = b => [b.title, b.subtitle, b.origTitle, ...b.authors, ...b.translators, b.series, b.isbn, b.publisher, ...b.tags, ...b.files.map(f => f.path)]
  .filter(Boolean).join(' ').normalize('NFKC').toLowerCase();

async function opds(req, res, url) {
  const { list } = await loadBooks();
  const now = new Date().toISOString();
  const atom = (body, kind) => send(res, 200, body, `${kind}; charset=utf-8`, { 'Cache-Control': 'no-cache' });
  const p = url.pathname.replace(/\/+$/, '') || '/opds';
  const page = (id, title, books) => {
    const n = Math.max(1, +url.searchParams.get('page') || 1), size = 50;
    const entries = books.slice((n - 1) * size, n * size).map(bookEntry);
    let body = feed({ id, title, self: url.pathname + url.search, kind: ACQ, entries, updated: now });
    const pages = Math.ceil(books.length / size);
    const link = (rel, k) => { const u = new URLSearchParams(url.searchParams); u.set('page', k); return `  <link rel="${rel}" href="${xml(url.pathname + '?' + u)}" type="${ACQ}"/>`; };
    const extra = [n > 1 && link('previous', n - 1), n < pages && link('next', n + 1)].filter(Boolean).join('\n');
    if (extra) body = body.replace('\n  <entry>', `\n${extra}\n  <entry>`);
    return atom(body, ACQ);
  };

  if (p === '/opds') {
    const cats = new Map();
    for (const b of list) cats.set(b.category, (cats.get(b.category) || 0) + 1);
    const entries = [
      navEntry('最近添加', '/opds/new', `最新入库的 50 本`, now),
      navEntry('豆瓣高分', '/opds/top', `豆瓣 8.5 分以上`, now),
      navEntry('全部书籍', '/opds/all', `共 ${list.length} 本`, now),
      ...[...cats].sort((a, b) => b[1] - a[1]).map(([c, n]) => navEntry(c, `/opds/cat/${encodeURIComponent(c)}`, `${n} 本`, now, NAV)),
    ];
    return atom(feed({ id: 'urn:shelf:opds', title: '家庭书架', self: '/opds', kind: NAV, entries, updated: now }), NAV);
  }
  if (p === '/opds/search.xml') {
    return send(res, 200, `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>家庭书架</ShortName><Description>搜索书名、作者、标签、ISBN</Description><InputEncoding>UTF-8</InputEncoding><OutputEncoding>UTF-8</OutputEncoding>
  <Url type="${ACQ}" template="/opds/search?q={searchTerms}"/>
</OpenSearchDescription>`, 'application/opensearchdescription+xml; charset=utf-8');
  }
  if (p === '/opds/new') return page('urn:shelf:new', '最近添加', list.slice(0, 50));
  if (p === '/opds/top') return page('urn:shelf:top', '豆瓣高分', list.filter(b => b.rating >= 8.5).sort((a, b) => b.rating - a.rating));
  if (p === '/opds/all') return page('urn:shelf:all', '全部书籍', [...list].sort((a, b) => a.title.localeCompare(b.title, 'zh-CN')));
  const cat = p.match(/^\/opds\/cat\/([^/]+)(?:\/([^/]+))?$/);
  if (cat) {
    const c = decodeURIComponent(cat[1]), sub = cat[2] && decodeURIComponent(cat[2]);
    const inCat = list.filter(b => b.category === c);
    if (sub) return page(`urn:shelf:cat:${c}:${sub}`, `${c} · ${sub}`, sub === '全部' ? inCat : inCat.filter(b => b.subcategory === sub));
    const subs = new Map();
    for (const b of inCat) if (b.subcategory) subs.set(b.subcategory, (subs.get(b.subcategory) || 0) + 1);
    const base = `/opds/cat/${encodeURIComponent(c)}`;
    const entries = [navEntry(`全部${c}`, `${base}/${encodeURIComponent('全部')}`, `${inCat.length} 本`, now),
      ...[...subs].sort((a, b) => b[1] - a[1]).map(([s, n]) => navEntry(s, `${base}/${encodeURIComponent(s)}`, `${n} 本`, now))];
    return atom(feed({ id: `urn:shelf:cat:${c}`, title: c, self: base, kind: NAV, entries, updated: now }), NAV);
  }
  if (p === '/opds/search') {
    const terms = (url.searchParams.get('q') || '').normalize('NFKC').toLowerCase().split(/\s+/).filter(Boolean);
    const hits = terms.length ? list.filter(b => { const t = bookText(b); return terms.every(w => t.includes(w)); }) : [];
    return page(`urn:shelf:search:${terms.join(' ')}`, `搜索：${terms.join(' ')}`, hits);
  }
  return send(res, 404, { error: 'not found' });
}

async function serveBookFile(req, res, fileId) {
  const { byFile } = await loadBooks();
  const hit = byFile?.get(fileId);
  if (!hit) return send(res, 404, { error: 'not found' });
  const full = path.join(BOOK_DIR, hit.file.path);
  if (!full.startsWith(BOOK_DIR + path.sep)) return send(res, 400, { error: 'bad path' });
  const st = await fs.stat(full);
  const name = path.basename(hit.file.path);
  res.writeHead(200, {
    'Content-Type': BOOK_MIME[hit.file.format] || 'application/octet-stream', 'Content-Length': st.size,
    'Content-Disposition': `attachment; filename="${encodeURIComponent(name)}"; filename*=UTF-8''${encodeURIComponent(name)}`,
  });
  console.log(`${new Date().toISOString()} download ${hit.file.path} (${req.headers['user-agent'] || ''})`);
  createReadStream(full).pipe(res);
}

// ---------- HTTP ----------

const send = (res, code, body, type = 'application/json; charset=utf-8', extra = {}) => {
  res.writeHead(code, { 'Content-Type': type, ...extra });
  res.end(typeof body === 'string' || Buffer.isBuffer(body) ? body : JSON.stringify(body));
};

async function serveFile(res, file, cache = 'no-cache') {
  try {
    const data = await fs.readFile(file);
    send(res, 200, data, MIME[path.extname(file)] || 'application/octet-stream', { 'Cache-Control': cache });
  } catch { send(res, 404, { error: 'not found' }); }
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://x');
  try {
    if (url.pathname.startsWith('/kosync')) return await kosync(req, res, url.pathname.slice('/kosync'.length) || '/');
    if (url.pathname === '/opds' || url.pathname.startsWith('/opds/')) return await opds(req, res, url);
    if (url.pathname === '/api/movies') return send(res, 200, (await loadMovies()).list);
    if (url.pathname === '/api/books') {
      const prog = readingProgress();
      return send(res, 200, (await loadBooks()).list.map(b => {
        const p = b.files.map(f => prog.get(f.id)).filter(Boolean).sort((x, y) => y.timestamp - x.timestamp)[0];
        return p ? { ...b, progress: { percentage: p.percentage, device: p.device, timestamp: p.timestamp } } : b;
      }));
    }
    if (url.pathname === '/api/search') {
      const q = (url.searchParams.get('q') || '').trim().slice(0, 200);
      const kind = url.searchParams.get('kind') || 'movies';
      const collections = kind === 'all' ? ['movies', 'books'] : kind === 'books' ? ['books'] : ['movies'];
      if (!q) return send(res, 200, { q, hits: [], ms: 0 });
      const out = await search(q, collections, kind === 'all' ? 36 : 24);
      console.log(`${new Date().toISOString()} search[${kind}] ${JSON.stringify(q)} ${out.ms}ms ${out.hits.length} hits`);
      return send(res, 200, out);
    }
    if (url.pathname === '/api/health') {
      return send(res, 200, { ok: true, movies: (await loadMovies()).list.length, books: (await loadBooks()).list.length, rssMB: Math.round(process.memoryUsage().rss / 1048576) });
    }
    const m = url.pathname.match(/^\/posters\/(\d+\.jpg)$/);
    if (m) return serveFile(res, path.join(ROOT, 'cache/posters', m[1]), 'public, max-age=604800');
    const c = url.pathname.match(/^\/covers\/([\w-]+\.jpg)$/);
    if (c) return serveFile(res, path.join(ROOT, 'cache/covers', c[1]), 'public, max-age=604800');
    const bf = url.pathname.match(/^\/books\/file\/([0-9a-f]{32})(\/.*)?$/);
    if (bf) return await serveBookFile(req, res, bf[1]);
    if (url.pathname === '/' || url.pathname === '/index.html') return serveFile(res, path.join(ROOT, 'public/index.html'));
    if (url.pathname === '/books' || url.pathname === '/all') return serveFile(res, path.join(ROOT, 'public/books.html'));
    if (url.pathname === '/shelf.css') return serveFile(res, path.join(ROOT, 'public/shelf.css'));
    send(res, 404, { error: 'not found' });
  } catch (e) {
    console.error(e);
    if (!res.headersSent) send(res, 500, { error: String(e.message || e) });
    else res.destroy();
  }
});

if (!existsSync(DB_PATH)) console.warn(`qmd index not found at ${DB_PATH}; search will fail until it is built`);
server.listen(PORT, HOST, () => console.log(`片库 listening on http://${HOST}:${PORT}`));
