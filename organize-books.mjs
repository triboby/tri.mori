// Plans (default) or applies (--apply) a reorganisation of /Volumes/store/book into category folders with
// "书名 - 作者.ext" file names, based on data/books.json from gen-books.mjs.
//   node organize-books.mjs           → writes book-reorg-plan.md + logs/book-reorg-plan.tsv, touches nothing
//   node organize-books.mjs --apply   → performs the moves in logs/book-reorg-plan.tsv, logs old/new paths for rollback
//   node organize-books.mjs --rollback logs/book-reorg-<stamp>.tsv
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import crypto from 'node:crypto';
import { hasCJK } from './lib/seg.mjs';

const ROOT = path.dirname(fileURLToPath(import.meta.url));
const BOOK_DIR = process.env.BOOK_DIR || '/Volumes/store/book';
const PLAN_TSV = path.join(ROOT, 'logs/book-reorg-plan.tsv');
const PLAN_MD = path.join(ROOT, 'book-reorg-plan.md');
const MISC_DIR = '_misc';
const SIDECARS = path.join(ROOT, 'logs/book-reorg-sidecars');
const COVERS = path.join(ROOT, 'cache/covers');

const CATEGORIES = ['计算机', '数学', '文学 & 小说', '政治 & 哲学', '心理学 & 教育', '历史 & 社会', '经营 & 管理 & 产品', '金融 & 投资 & 理财', '未分类'];

/** Tag → category signals; earlier tags weigh more. */
const RULES = [
  ['计算机', /计算机|编程|程序|软件|前端|javascript|操作系统|算法|数据结构|网络|http|git|rust|c\+\+|c语言|^c$|css|html|node|运维|编译|分布式|web|架构|prometheus/i],
  ['文学 & 小说', /小说|文学|诗歌|诗词|散文|杂文|武侠|科幻|推理|童话|诗经|名著/],
  ['金融 & 投资 & 理财', /投资|理财|金融|经济|财商|基金/],
  ['经营 & 管理 & 产品', /管理学|企业管理|商业|营销|产品|创业|企业|领导力|品牌|互联网|战略|^管理$/],
  ['历史 & 社会', /历史|社会学|^社会$|传记|中国史|近代史|古代史|纪实|企业史/],
  ['政治 & 哲学', /哲学|政治|国学|心学|伦理|儒学|国际关系/],
  ['心理学 & 教育', /心理|学习|方法论|思维|成长|自我管理|个人管理|时间管理|沟通|写作|逻辑|习惯|效率|阅读|教育|情商|励志|人际|自控|社交|职场/],
  ['数学', /数学|统计|微积分|概率/],
];

/** Manual decisions where Douban tags are ambiguous (by book id from books.json). */
const OVERRIDES = {
  db35135792: '经营 & 管理 & 产品', // 善战者说
  db30597601: '经营 & 管理 & 产品', // 曾国藩领导力十二讲
  db1941558: '经营 & 管理 & 产品', // 设计中的设计
  db3646976: '政治 & 哲学', // 菜根谭
  db1883245: '文学 & 小说', // 诗经
  db35546622: '金融 & 投资 & 理财', // 置身事内
  db35383731: '数学', // 贝叶斯的博弈
  db35292688: '数学', // 微积分的力量
  db4140492: '金融 & 投资 & 理财', // 风险、不确定性与利润
  db25763679: '心理学 & 教育', // 不确定世界的理性选择
  db37235927: '政治 & 哲学', // 控制论与科学方法论
  db1467022: '数学', // 上帝掷骰子吗 (科普)
  db37294999: '历史 & 社会', // 未来简史
  db26698660: '文学 & 小说', // 巨人的陨落
  db35593780: '历史 & 社会', // 工作、消费主义和新穷人
  db26576861: '历史 & 社会', // 最好的告别
  db27074369: '经营 & 管理 & 产品', // 可口可乐传
  db26849305: '经营 & 管理 & 产品', // 成为乔布斯
  db26929955: '经营 & 管理 & 产品', // 腾讯传
  db27179563: '经营 & 管理 & 产品', // 激荡十年，水大鱼大
  db26390229: '文学 & 小说', // 把生命浪费在美好的事物上
  db30539780: '历史 & 社会', // 看见
  db1084435: '数学', // 策略思维 (博弈论)
  db26677472: '数学', // 妙趣横生博弈论
  db21966353: '金融 & 投资 & 理财', // 贫穷的本质
  db26607419: '政治 & 哲学', // 论中国
  db27172839: '政治 & 哲学', // 苏菲的世界
  db4723970: '经营 & 管理 & 产品', // 人人都是产品经理
  db36336321: '经营 & 管理 & 产品', // 卓有成效的管理者
  db24700113: '经营 & 管理 & 产品', // 赢
  db1039311: '心理学 & 教育', // 职场动物进化手册
  db35006472: '心理学 & 教育', // 和秋叶一起学Excel
  db1935312: '历史 & 社会', // 不可不知的2000个文化常识
  db3772799: '文学 & 小说', // 西游记潜规则
  db36062395: '文学 & 小说', // 福尔摩斯探案全集 (was in 心理学 & 教育)
  db34442149: '心理学 & 教育', // 清醒思考的策略 (was in 金融 & 投资 & 理财)
  db35272152: '经营 & 管理 & 产品', // 未来呼啸而来 (was in 金融 & 投资 & 理财)
  db4927701: '政治 & 哲学', // 四书直解 (was in 文学 & 小说)
  db6534729: '政治 & 哲学', // 冰鉴 (was in 心理学 & 教育)
  db25813440: '心理学 & 教育', // 80/20法则
  db26606009: '心理学 & 教育', // 精力管理
  db33391219: '心理学 & 教育', // 金字塔原理
  db1786387: '心理学 & 教育', // 影响力 (经管目录里的另一版)
  db1005576: '心理学 & 教育', // 影响力
  hd2b8f8b229: '心理学 & 教育', // 心理学全集（套装）
  h724676e187: '心理学 & 教育', // 埃利斯作品集
  h39a1701c08: '心理学 & 教育', // 批判性思维套装
  hde847fbc5c: '心理学 & 教育', // 有效睡眠
  h3fdb545f64: '心理学 & 教育', // 迎刃自信方法论
  h5e5b3d3bdb: '历史 & 社会', // 理想国译丛套装
  h627711ab17: '政治 & 哲学', // 中国36问
  h67fe73b1e0: '政治 & 哲学', // the dictator's handbook (英文版)
};

/** Proper names for books Douban could not match (title, author; ed: null drops a misleading edition). */
const NAME_OVERRIDES = {
  h5e5b3d3bdb: { title: '理想国译丛（套装32册）', author: '' },
  haf83871480: { title: '沟通的艺术', author: '罗纳德·阿德勒' },
  h627711ab17: { title: '中国36问', author: '' },
  h67fe73b1e0: { title: "The Dictator's Handbook", author: 'Bruce Bueno de Mesquita' },
  hdba99ca5a9: { title: '深入理解计算机系统', author: 'Randal E. Bryant' },
  h6da9c9c8c0: { title: '深入理解计算机系统', author: 'Randal E. Bryant' },
  hb53cbd8f5d: { title: 'JavaScript高级程序设计', author: '马特·弗里斯比' },
  h9f8b76c1d6: { title: 'Hands-On Data Structures and Algorithms with Rust', author: 'Claus Matzinger' },
  h26ec87ff3c: { title: 'Hands-On Functional Programming in Rust', author: 'Andrew Johnson' },
  h7ef975134f: { title: 'Hands-On Data Structures and Algorithms with JavaScript', author: 'Kashyap Mukkamala' },
  hf3eb27460c: { title: '松本行弘的程序世界', author: '松本行弘' },
  hf24bd75994: { title: '计算机网络：自顶向下方法', author: 'James F·Kurose' },
  hbde998bcc8: { title: '图解HTTP', author: '上野宣' },
  he2f9898953: { title: '你不知道的JavaScript', author: '辛普森' },
  h20319508db: { title: '罪与罚', author: '陀思妥耶夫斯基' },
  h5278cba3a5: { title: 'WebKit技术内幕', author: '朱永盛' },
  hd2b8f8b229: { title: '心理学全集（套装全5册）', author: '' },
  h724676e187: { title: '埃利斯经典作品集（套装共3册）', author: '阿尔伯特·埃利斯' },
  h39a1701c08: { title: '批判性思维（套装共6册）', author: '', ed: null },
  h3fdb545f64: { title: '迎刃自信方法论', author: '' },
};
/** Unmatched files that are another format of a matched book: share that book's folder and OPF. */
const MERGE = { h9f8b76c1d6: 'db34988997', h20319508db: 'db30278620' };

/** Books whose current category is kept even if the rules disagree, unless overridden above. */
const keepCurrent = b => b.category !== '未分类' && !(b.id in OVERRIDES);

/** Computer sub-folders: keep the existing one, otherwise derive from tags. */
const CS_SUB = [
  ['前端开发', /前端|javascript|css|html|web|node/i],
  ['操作系统', /操作系统|计算机系统|^os$/i],
  ['网络', /网络|http|tcp/i],
  ['数据结构与算法', /算法|数据结构/],
  ['编程语言', /rust|c\+\+|c语言|^c$|python|go|git|编程语言|程序设计/i],
  ['架构应用', /分布式|架构|运维|云计算|prometheus|数据库/i],
  ['编程思想', /软件工程|编程思想|敏捷|编译|计算机原理|编码|项目管理/],
];
const CS_SUB_OVERRIDES = { db30329536: '架构应用', db1102259: '编程思想', db4822685: '编程思想', db34788443: '编程思想', h6da9c9c8c0: '操作系统', hf24bd75994: '网络', db36081529: '网络' };

function categorize(b) {
  if (OVERRIDES[b.id]) return { cat: OVERRIDES[b.id], how: 'manual' };
  if (keepCurrent(b)) return { cat: b.category, how: 'keep' };
  const score = new Map();
  b.tags.slice(0, 8).forEach((t, i) => {
    for (const [cat, re] of RULES) if (re.test(t)) { score.set(cat, (score.get(cat) || 0) + (8 - i)); break; }
  });
  const ranked = [...score].sort((x, y) => y[1] - x[1]);
  if (!ranked.length) return { cat: '未分类', how: 'none' };
  const sure = ranked.length === 1 || ranked[0][1] >= ranked[1][1] * 1.5;
  return { cat: ranked[0][0], how: sure ? 'tags' : 'tags?' };
}

function csSub(b, file) {
  const cur = file.path.split('/');
  if (cur[0] === '计算机' && cur.length > 2 && CS_SUB.some(([s]) => s === cur[1]) || cur[1] === '编程语言' || cur[1] === '编程思想') return cur[1];
  if (CS_SUB_OVERRIDES[b.id]) return CS_SUB_OVERRIDES[b.id];
  for (const t of b.tags.slice(0, 6)) for (const [sub, re] of CS_SUB) if (re.test(t)) return sub;
  return '';
}

const NATION = /[\[［【(（]\s*(?:美|美国|英|英国|法|德|日|俄|奥|奥地利|加|加拿大|澳|以色列|荷|希|挪威|爱尔兰|意大利|韩|黎巴嫩|哥伦比亚|印|清|明|汉|宋|唐)\s*[\]］】)）]\s*/g;
const decode = s => (s || '').replace(/&#39;/g, "'").replace(/&amp;/g, '&').replace(/&quot;/g, '"');

function cleanAuthor(a) {
  let s = decode(a).replace(NATION, '').replace(/\|.*$/, '').trim();
  const cjkInParens = s.match(/^[^（(]*[A-Za-z][^（(]*[（(]([^)）]*[\u4e00-\u9fff][^)）]*)[)）]$/);
  if (cjkInParens) s = cjkInParens[1];
  else if (hasCJK(s)) s = s.replace(/\s*[(（][^)）]*[A-Za-z][^)）]*[)）]\s*/g, '');
  s = s.replace(/\s*(著|编著|编订|主编|编|撰|等|译者)$/, '').replace(/撰$/, '').replace(/\s+/g, ' ').trim();
  return /^(详细书签|unknown)$/i.test(s) ? '' : s;
}

const CN_NUM = { 一: 1, 二: 2, 三: 3, 四: 4, 五: 5, 六: 6, 七: 7, 八: 8, 九: 9, 十: 10 };
function editionOf(s) {
  const m = (s || '').match(/第\s*([\d一二三四五六七八九十]+)\s*版/) || (s || '').match(/(\d+)\s*(?:st|nd|rd|th)\s+edition/i);
  return m ? +(CN_NUM[m[1]] || m[1]) : null;
}
const volumeOf = s => (s || '').match(/[（(]\s*([上中下])\s*卷\s*[)）]|([上中下])卷/)?.slice(1).find(Boolean) || null;

function cleanTitle(t) {
  return decode(t).replace(/[(（]\s*第?\s*[\d一二三四五六七八九十]+\s*版[^)）]*[)）]|(原书)?第\s*[\d一二三四五六七八九十]+\s*版/g, '')
    .replace(/\d+(?:st|nd|rd|th)\s+edition/ig, '').replace(/【[^】]*】/g, '').replace(/[(（]\s*[上中下]卷\s*[)）]/g, '')
    .replace(/_完整版|\s*彩色版$|\.?\[[^\]]*\].*$/g, '').replace(/[,，]\s*$/, '')
    .replace(/\s*[(（]\s*/g, '（').replace(/\s*[)）]/g, '）').replace(/（\s*）/g, '').replace(/\s+/g, ' ').trim();
}

const safe = s => s.replace(/[\\/]/g, '／').replace(/:/g, '：').replace(/[*?"<>|]/g, '').replace(/\s+/g, ' ').trim();

/** One target per file: <书名 - 作者>/<书名 - 作者>.ext (flat, like store/movie); volumes share their set's folder. */
function plan(books) {
  const rows = [];
  const byId = new Map(books.map(b => [b.id, b]));
  for (const b of books) {
    const { cat, how } = categorize(b);
    const flags = [];
    if (b.source === 'file') flags.push(NAME_OVERRIDES[b.id] ? (MERGE[b.id] ? '未匹配豆瓣，并入同一本书' : '未匹配豆瓣，名字已手工修正') : '未匹配豆瓣，名字来自文件名');
    if (how === 'tags?') flags.push('分类存疑');
    if (how === 'none') flags.push('无标签，放未分类');
    if (b.category !== '未分类' && cat !== b.category) flags.push(`改分类：${b.category} → ${cat}`);
    for (const f of b.files) {
      const fileBase = path.basename(f.path).replace(/\.[^.]+$/, '');
      const own = b.source === 'file' ? fileBase.replace(/^epubw\.com\s*-\s*/i, '').replace(/\s*[(（]z-lib\.org[)）]/i, '').replace(/\s+by\s+.*$/i, '') : b.title;
      const o = NAME_OVERRIDES[b.id];
      const title = o?.title ?? cleanTitle(own);
      const ed = o && 'ed' in o ? o.ed : editionOf(fileBase) ?? editionOf(b.title), vol = volumeOf(fileBase) ?? volumeOf(b.title);
      const fflags = [...flags];
      if (editionOf(fileBase) && editionOf(b.title) && editionOf(fileBase) !== editionOf(b.title)) fflags.push(`版本存疑：文件是第${editionOf(fileBase)}版，豆瓣匹配到第${editionOf(b.title)}版`);
      const author = o?.author ?? cleanAuthor(b.authors[0] || '');
      const join = t => safe(author && !t.includes(author) ? `${t} - ${author}` : t).slice(0, 100);
      const edS = ed ? `（第${ed}版）` : '';
      const folder = join(title + edS);
      const base = join(title + (vol ? `（${vol}卷）` : '') + edS);
      const dir = '', sub = cat === '计算机' ? csSub(b, f) : '';
      rows.push({ id: MERGE[b.id] || b.id, md5: f.id, size: f.size, format: f.format, from: f.path, dir, sub, folder, base, vol, year: b.year, title: b.title, cat, how, flags: fflags });
    }
  }

  // Exact duplicates: same partial MD5 and size. They go next to the kept copy (inside a category folder if any) as "… (重复).ext".
  const byHash = new Map();
  for (const r of rows) (byHash.get(r.md5 + r.size) || byHash.set(r.md5 + r.size, []).get(r.md5 + r.size)).push(r);
  for (const group of byHash.values()) {
    if (group.length < 2) continue;
    group.sort((a, b) => (b.from.includes('/') - a.from.includes('/')) || a.from.length - b.from.length);
    for (const r of group.slice(1)) { r.dup = group[0].from; r.keep = group[0]; }
  }

  // Folder collisions between different books (not volumes of one set): add the year, then a counter.
  const owner = new Map();
  for (const r of rows.filter(r => !r.dup)) {
    let key = path.posix.join(r.dir, r.folder);
    const clash = k => owner.has(k.toLowerCase()) && owner.get(k.toLowerCase()).id !== r.id && !(r.vol && owner.get(k.toLowerCase()).vol);
    for (let k = 2; clash(key); k++) {
      const suffix = k === 2 && r.year ? `（${r.year}）` : ` (${k})`;
      key = path.posix.join(r.dir, r.folder + suffix);
      if (!r.flags.includes('同名不同书，已加后缀')) r.flags.push('同名不同书，已加后缀');
      if (k === 2 && r.year || k > 2) { r.base += suffix; r.folder += suffix; }
    }
    if (!owner.has(key.toLowerCase())) owner.set(key.toLowerCase(), r);
    r.to = path.posix.join(r.dir, r.folder, r.base + '.' + r.format);
  }

  // Same book, same format, different content inside one folder: number them.
  const seen = new Map();
  for (const r of rows.filter(r => !r.dup)) {
    const k = r.to.toLowerCase();
    if (!seen.has(k)) { seen.set(k, 1); continue; }
    const n = seen.get(k) + 1; seen.set(k, n);
    r.to = r.to.replace(/(\.[^.]+)$/, ` (${n})$1`);
    r.flags.push('同一本书同格式有多个不同文件');
  }

  const dupCount = new Map();
  for (const r of rows.filter(r => r.dup)) {
    const n = (dupCount.get(r.keep.to) || 0) + 1;
    dupCount.set(r.keep.to, n);
    r.to = r.keep.to.replace(/(\.[^.]+)$/, ` (重复${n > 1 ? n : ''})$1`);
  }

  // Sidecars per book folder: metadata.opf + cover.jpg from the first book that lands there.
  const folders = new Map();
  for (const r of rows.filter(r => !r.dup)) {
    const dir = path.posix.dirname(r.to);
    if (!folders.has(dir) || folders.get(dir).book.source === 'file' && byId.get(r.id).source !== 'file') folders.set(dir, { dir, book: byId.get(r.id), cat: r.cat, sub: r.sub, title: r.folder.replace(/ - [^-]+$/, '') });
  }
  return { rows, folders: [...folders.values()] };
}

const xmlEsc = s => String(s ?? '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

/** Calibre-compatible metadata.opf for one book folder. */
function renderOpf(b, folderTitle, cat, sub) {
  const uuid = crypto.createHash('md5').update('book:' + b.id).digest('hex').replace(/^(.{8})(.{4})(.{4})(.{4})(.{12})$/, '$1-$2-$3-$4-$5');
  const title = b.source === 'file' ? folderTitle : decode(b.title);
  const lang = hasCJK(b.title) ? 'zh' : 'en';
  const lines = [
    `<dc:identifier opf:scheme="uuid" id="uuid_id">${uuid}</dc:identifier>`,
    b.isbn && `<dc:identifier opf:scheme="ISBN">${xmlEsc(b.isbn)}</dc:identifier>`,
    b.doubanId && `<dc:identifier opf:scheme="DOUBAN">${xmlEsc(b.doubanId)}</dc:identifier>`,
    `<dc:title>${xmlEsc(title)}</dc:title>`,
    ...b.authors.map(a => `<dc:creator opf:role="aut">${xmlEsc(decode(a))}</dc:creator>`),
    ...b.translators.map(a => `<dc:creator opf:role="trl">${xmlEsc(decode(a))}</dc:creator>`),
    b.publisher && `<dc:publisher>${xmlEsc(b.publisher)}</dc:publisher>`,
    b.year && `<dc:date>${xmlEsc(b.year)}-01-01T00:00:00+00:00</dc:date>`,
    b.intro && `<dc:description>${xmlEsc(b.intro)}</dc:description>`,
    `<dc:language>${lang}</dc:language>`,
    `<dc:subject>${xmlEsc(cat)}</dc:subject>`,
    sub && `<dc:subject>${xmlEsc(sub)}</dc:subject>`,
    ...b.tags.filter(t => t !== cat && t !== sub).map(t => `<dc:subject>${xmlEsc(decode(t))}</dc:subject>`),
    b.series && `<meta name="calibre:series" content="${xmlEsc(b.series)}"/>`,
    b.rating && `<meta name="calibre:rating" content="${Math.round(b.rating)}"/>`,
    b.rating && `<meta name="douban:rating" content="${b.rating}"/>`,
    b.votes && `<meta name="douban:votes" content="${b.votes}"/>`,
    b.subtitle && `<meta name="douban:subtitle" content="${xmlEsc(b.subtitle)}"/>`,
    b.origTitle && `<meta name="douban:original_title" content="${xmlEsc(b.origTitle)}"/>`,
    b.pages && `<meta name="douban:pages" content="${b.pages}"/>`,
    `<meta name="home:category" content="${xmlEsc(cat)}"/>`,
    sub && `<meta name="home:subcategory" content="${xmlEsc(sub)}"/>`,
    `<meta name="home:source" content="${b.source}"/>`,
  ].filter(Boolean);
  return `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    ${lines.join('\n    ')}
  </metadata>
  <guide>
    <reference type="cover" title="Cover" href="cover.jpg"/>
  </guide>
</package>
`;
}

async function walkAll(dir, rel = '') {
  const out = [];
  for (const d of await fs.readdir(dir, { withFileTypes: true })) {
    if (d.name.startsWith('.') || d.name === '#recycle' || d.name.startsWith('@')) continue;
    const r = rel ? `${rel}/${d.name}` : d.name;
    if (d.isDirectory()) out.push(...await walkAll(path.join(dir, d.name), r));
    else out.push(r);
  }
  return out;
}

async function writePlan() {
  const books = JSON.parse(await fs.readFile(path.join(ROOT, 'data/books.json'), 'utf8'));
  const { rows, folders } = plan(books);
  const known = new Set(rows.map(r => r.from));
  const others = (await walkAll(BOOK_DIR)).filter(p => !known.has(p) && p !== 'process_books.py');
  const misc = others.filter(p => /\.(jpe?g|png)$/i.test(p) && p.includes('/'));
  const extra = others.filter(p => !misc.includes(p));
  const moves = rows.filter(r => r.from !== r.to);

  // Stage sidecars locally so the plan can be reviewed before anything touches the NAS.
  await fs.rm(SIDECARS, { recursive: true, force: true });
  const writes = [];
  for (const f of folders) {
    const stage = path.join(SIDECARS, f.dir);
    await fs.mkdir(stage, { recursive: true });
    await fs.writeFile(path.join(stage, 'metadata.opf'), renderOpf(f.book, f.title, f.cat, f.sub));
    writes.push([path.join(stage, 'metadata.opf'), `${f.dir}/metadata.opf`]);
    if (f.book.cover) {
      await fs.copyFile(path.join(COVERS, f.book.cover), path.join(stage, 'cover.jpg'));
      writes.push([path.join(stage, 'cover.jpg'), `${f.dir}/cover.jpg`]);
    }
  }

  await fs.mkdir(path.dirname(PLAN_TSV), { recursive: true });
  await fs.writeFile(PLAN_TSV, ['op\tfrom\tto',
    ...moves.map(r => `mv\t${r.from}\t${r.to}`),
    ...misc.map(p => `mv\t${p}\t${MISC_DIR}/${p.replace(/\//g, '__')}`),
    ...writes.map(([from, to]) => `write\t${from}\t${to}`)].join('\n') + '\n');

  const count = (rs, f) => rs.filter(f).length;
  const byCat = new Map();
  for (const f of folders) byCat.set(f.cat, (byCat.get(f.cat) || 0) + 1);
  const flagged = rows.filter(r => r.flags.length && !r.dup);
  const cell = s => String(s).replace(/\|/g, '\\|');
  const line = r => `| ${cell(r.from)} | ${cell(r.to)} | ${cell(r.cat + (r.sub ? ' / ' + r.sub : ''))} | ${cell(r.flags.join('；'))} |`;
  const section = (title, rs) => rs.length ? `\n### ${title}（${rs.length}）\n\n| 现在 | 整理后 | 分类（写入 OPF） | 备注 |\n| --- | --- | --- | --- |\n${rs.map(line).join('\n')}\n` : '';
  const sample = folders.find(f => f.book.doubanId && f.book.translators.length) || folders[0];

  const md = `# 书库整理方案（预演，尚未执行）

生成时间：${new Date().toLocaleString('zh-CN')}。来源：\`data/books.json\`（${books.length} 本 / ${rows.length} 个文件）。

## 规则

- 结构和 \`store/movie\` 一样摊平：\`/Volumes/store/book/<书名 - 作者>/\`，不再按分类建目录。
- 每个书目录里：书文件 \`书名 - 作者.扩展名\`（同一本书的不同格式共用一个名字）+ \`cover.jpg\` + \`metadata.opf\`（Calibre 兼容）。
- 书名用豆瓣书名（不带副标题），作者用豆瓣第一作者并去掉 [美] 之类的国籍括号；版次「（第N版）」，分卷「（上卷）」。
  不同版本是不同目录；分卷放在同一个目录里。去掉 z-lib / ePUBw / 琳宝书屋 等来源标记，非法字符替换成全角。
- 分类不再体现在路径上，而是写进 \`metadata.opf\`：第一个 \`dc:subject\` 和 \`<meta name="home:category">\`
  （计算机类另有 \`home:subcategory\`）。分类沿用现有 9 个名字。书架页和 OPDS 按这个字段分组。
- 内容完全相同的重复文件（大小 + KOReader 哈希一致）放进同一个书目录，命名「书名 - 作者 (重复).扩展名」，不删除；书架和 OPDS 只显示一份。
  内容不同的同格式文件命名「… (2).扩展名」。不同版本是不同目录。
- 旧的多格式文件夹里的广告/封面图片移到 \`${MISC_DIR}/\`。整理后变空的目录（包括 9 个分类目录）会删掉，回滚时会重建。
- 只改路径不改文件内容，KOReader 的进度同步（按文件内容哈希）不受影响。process_books.py、.claude 不动。

## 统计

- 书目录：${folders.length} 个（metadata.opf ${folders.length} 个，cover.jpg ${writes.length - folders.length} 个）
- 书文件 ${rows.length} 个：移动/改名 ${moves.length}，其中完全重复的文件 ${count(rows, r => r.dup)} 个（放进各自书目录，标「(重复)」），保持不变 ${rows.length - moves.length}
- 图片 → \`${MISC_DIR}/\`：${misc.length}；其他不动的文件：process_books.py${extra.length ? '、' + extra.join('、') : ''}
- 需要人工看一眼（flag）：${flagged.length} 个文件
- 各分类的书目录数：${CATEGORIES.filter(c => byCat.has(c)).map(c => `${c} ${byCat.get(c)}`).join('，')}

## 需要确认的条目
${section('分类、版本或名字存疑', flagged)}
## 重复文件
${section('放进同一书目录（原来那份写在备注里）', rows.filter(r => r.dup).map(r => ({ ...r, flags: [`和 ${r.dup} 完全相同`] })))}
## 全部移动清单（按分类）
${CATEGORIES.map(c => section(c, moves.filter(r => !r.dup && r.cat === c && !r.flags.length))).join('')}
${misc.length ? `\n### 图片（${misc.length}）\n\n${misc.map(p => `- ${p} → ${MISC_DIR}/`).join('\n')}\n` : ''}
## metadata.opf 示例：${sample.dir}

\`\`\`xml
${renderOpf(sample.book, sample.title, sample.cat, sample.sub).trim()}
\`\`\`

全部 OPF 和封面已预生成在 \`logs/book-reorg-sidecars/\`，可以先看。
`;
  await fs.writeFile(PLAN_MD, md);
  console.log(`plan: ${folders.length} book folders, ${rows.length} files, ${moves.length} moves, ${count(rows, r => r.dup)} duplicates, ${flagged.length} flagged, ${misc.length} images, ${writes.length} sidecars`);
  console.log(`wrote ${path.relative(ROOT, PLAN_MD)} and ${path.relative(ROOT, PLAN_TSV)}`);
}

async function exists(p) { try { await fs.access(p); return true; } catch { return false; } }
const nas = p => path.join(BOOK_DIR, p);

/** Removes empty folders below BOOK_DIR (deepest first), logging each for rollback. */
async function pruneEmptyDirs(log, dir = BOOK_DIR) {
  for (const d of await fs.readdir(dir, { withFileTypes: true })) {
    if (!d.isDirectory() || d.name.startsWith('.') || d.name.startsWith('@') || d.name === '#recycle') continue;
    const sub = path.join(dir, d.name);
    await pruneEmptyDirs(log, sub);
    const left = (await fs.readdir(sub)).filter(n => n !== '.DS_Store');
    if (!left.length) {
      await fs.rm(path.join(sub, '.DS_Store'), { force: true });
      await fs.rmdir(sub);
      await log('rmdir', '', path.relative(BOOK_DIR, sub));
    }
  }
}

async function apply() {
  const ops = (await fs.readFile(PLAN_TSV, 'utf8')).trim().split('\n').slice(1).map(l => l.split('\t'));
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const logFile = path.join(ROOT, `logs/book-reorg-${stamp}.tsv`);
  await fs.writeFile(logFile, 'op\tfrom\tto\n');
  const log = (op, from, to) => fs.appendFile(logFile, `${op}\t${from}\t${to}\n`);
  const stats = { mv: 0, write: 0, skipped: 0, rmdir: 0 };
  for (const [op, from, to] of ops) {
    if (op === 'mv') {
      if (!(await exists(nas(from)))) { console.warn(`missing, skipped: ${from}`); stats.skipped++; continue; }
      if (from.toLowerCase() !== to.toLowerCase() && await exists(nas(to))) { console.warn(`target exists, skipped: ${to}`); stats.skipped++; continue; }
      await fs.mkdir(path.dirname(nas(to)), { recursive: true });
      await fs.rename(nas(from), nas(to));
      await log('mv', from, to); stats.mv++;
    } else if (op === 'write') {
      if (await exists(nas(to))) { console.warn(`exists, not overwritten: ${to}`); stats.skipped++; continue; }
      await fs.mkdir(path.dirname(nas(to)), { recursive: true });
      await fs.copyFile(from, nas(to));
      await log('write', '', to); stats.write++;
    }
  }
  await pruneEmptyDirs(async (...a) => { stats.rmdir++; await log(...a); });
  console.log(`done ${JSON.stringify(stats)}; log: ${path.relative(ROOT, logFile)}`);
  console.log(`rollback: node organize-books.mjs --rollback ${path.relative(ROOT, logFile)}`);
}

/** Undoes an apply log in reverse order: recreate removed folders, delete written sidecars, move files back. */
async function rollback(logFile) {
  const ops = (await fs.readFile(logFile, 'utf8')).trim().split('\n').slice(1).map(l => l.split('\t')).reverse();
  const stats = { mv: 0, rm: 0, mkdir: 0, skipped: 0 };
  for (const [op, from, to] of ops) {
    if (op === 'rmdir') { await fs.mkdir(nas(to), { recursive: true }); stats.mkdir++; }
    else if (op === 'write') { await fs.rm(nas(to), { force: true }); stats.rm++; }
    else if (op === 'mv') {
      if (!(await exists(nas(to))) || await exists(nas(from))) { console.warn(`cannot move back: ${to}`); stats.skipped++; continue; }
      await fs.mkdir(path.dirname(nas(from)), { recursive: true });
      await fs.rename(nas(to), nas(from)); stats.mv++;
    }
  }
  const noop = async () => {};
  for (const [op, , to] of ops) if (op === 'mv' || op === 'write') {
    const dir = path.dirname(nas(to));
    try { if (!(await fs.readdir(dir)).filter(n => n !== '.DS_Store').length) { await fs.rm(path.join(dir, '.DS_Store'), { force: true }); await fs.rmdir(dir); } } catch { await noop(); }
  }
  console.log(`rolled back ${JSON.stringify(stats)}`);
}

const argv = process.argv.slice(2);
if (argv[0] === '--apply') await apply();
else if (argv[0] === '--rollback') await rollback(path.resolve(argv[1]));
else await writePlan();
