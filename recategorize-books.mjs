// Re-classifies books into a two-level taxonomy (大类 / 细类, modelled on 中图法) and writes it into each
// book folder's metadata.opf on the NAS (home:category, home:subcategory, home:clc, first two dc:subject).
//   node recategorize-books.mjs                      → finds CIP codes, writes book-recategorize-plan.md, touches nothing
//   node recategorize-books.mjs --apply              → backs up every metadata.opf to logs/opf-backup-<stamp>/, then rewrites them
//   node recategorize-books.mjs --rollback <backup>  → restores the backed-up OPFs
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { openZip } from './gen-books.mjs';

const ROOT = path.dirname(fileURLToPath(import.meta.url));
const BOOK_DIR = process.env.BOOK_DIR || '/Volumes/store/book';
const CLC_CACHE = path.join(ROOT, 'cache/clc.json');
const PLAN_MD = path.join(ROOT, 'book-recategorize-plan.md');

/** 大类 in display order, with the 中图法 classes they correspond to. */
const TAXONOMY = [
  ['哲学', 'B（不含 B84）', ['中国哲学', '逻辑与思维', '哲学通识']],
  ['心理学', 'B84', ['个人成长', '认知与决策', '情绪与心理治疗', '社会心理', '心理学通识']],
  ['社会', 'C', ['社会学', '人际沟通', '婚恋与两性']],
  ['政治', 'D', ['政治学', '中国政治']],
  ['经济', 'F0–F8', ['经济学', '投资理财']],
  ['管理', 'F27 / C93', ['企业管理', '职场与效率', '市场营销', '产品', '互联网与创新', '商业传记']],
  ['教育', 'G4–G7', ['学习方法']],
  ['语言', 'H', ['写作', '口才与表达']],
  ['文学', 'I', ['中国小说', '外国小说', '推理悬疑', '科幻', '散文随笔', '诗歌', '中国古典文学']],
  ['历史', 'K', ['中国史', '世界史', '通俗历史', '传记']],
  ['文化艺术', 'G0–G2 / J', ['文化常识', '设计']],
  ['数理科学', 'O', ['数学', '博弈论', '物理']],
  ['医学健康', 'R', ['医学与健康']],
  ['生活', 'TS97', ['整理与极简']],
  ['计算机', 'TP', ['编程语言', '前端开发', '数据结构与算法', '计算机原理', '操作系统', '计算机网络', '软件工程', '架构与运维', '办公软件']],
];

/** Book id → '大类/细类'. Curated from Douban tags, titles and CIP codes where present. */
const MAP = {
  // 哲学
  db3646976: '哲学/中国哲学', db3007268: '哲学/中国哲学', db6534729: '哲学/中国哲学', db4927701: '哲学/中国哲学',
  db26828205: '哲学/逻辑与思维', db25772550: '哲学/逻辑与思维', db20428922: '哲学/逻辑与思维', db24697776: '哲学/逻辑与思维',
  f82ee0aec30: '哲学/逻辑与思维', db2986872: '哲学/逻辑与思维',
  db1331665: '哲学/哲学通识', db37235927: '哲学/哲学通识', db27172839: '哲学/哲学通识',
  // 心理学
  db35876121: '心理学/个人成长', db20507212: '心理学/个人成长', db26425651: '心理学/个人成长', db36894917: '心理学/个人成长',
  db1775691: '心理学/个人成长', db6533613: '心理学/个人成长', db26761696: '心理学/个人成长', db26911425: '心理学/个人成长',
  db10786473: '心理学/个人成长', db27078435: '心理学/个人成长', f85099c5157: '心理学/个人成长', db26935293: '心理学/个人成长',
  db5325618: '心理学/个人成长', db26606009: '心理学/个人成长',
  db25763679: '心理学/认知与决策', db34442149: '心理学/认知与决策', db20492550: '心理学/认知与决策', db10785583: '心理学/认知与决策',
  db35481512: '心理学/情绪与心理治疗', fc2503cef26: '心理学/情绪与心理治疗', db4062809: '心理学/情绪与心理治疗',
  db25963532: '心理学/情绪与心理治疗', db1135734: '心理学/情绪与心理治疗', db30181152: '心理学/情绪与心理治疗',
  db1005576: '心理学/社会心理', db1786387: '心理学/社会心理',
  db26743051: '心理学/心理学通识', db26296233: '心理学/心理学通识', db36166780: '心理学/心理学通识', db30419261: '心理学/心理学通识',
  f24b590ab57: '心理学/心理学通识',
  // 社会
  db35038832: '社会/社会学', db35593780: '社会/社会学', db1795079: '社会/社会学', db25863621: '社会/社会学',
  db5504201: '社会/人际沟通', db26342463: '社会/人际沟通', db5344908: '社会/人际沟通', db25894861: '社会/人际沟通',
  db3533221: '社会/人际沟通', fae7fcad6f1: '社会/人际沟通',
  db1223845: '社会/婚恋与两性', db25839107: '社会/婚恋与两性',
  // 政治
  db25881102: '政治/政治学', ff801ab42bf: '政治/政治学', db20366368: '政治/政治学',
  f1580421003: '政治/中国政治', db26607419: '政治/中国政治', db35546622: '政治/中国政治',
  // 经济
  db2326852: '经济/经济学', db4140492: '经济/经济学', db35069279: '经济/经济学', db26897464: '经济/经济学', db10434287: '经济/经济学',
  db27667177: '经济/经济学', db21966353: '经济/经济学', db27111109: '经济/经济学',
  db26634824: '经济/投资理财', db26370442: '经济/投资理财', db26752026: '经济/投资理财', db4737015: '经济/投资理财',
  db1033778: '经济/投资理财', db27204860: '经济/投资理财', db27599920: '经济/投资理财', db37003888: '经济/投资理财', db35295592: '经济/投资理财',
  // 管理
  db1022936: '管理/企业管理', db36336321: '管理/企业管理', db24700113: '管理/企业管理', db35135792: '管理/企业管理',
  db25813440: '管理/职场与效率', db2792535: '管理/职场与效率', db26876510: '管理/职场与效率', db26656532: '管理/职场与效率',
  db1039311: '管理/职场与效率', db33391219: '管理/职场与效率',
  db30426372: '管理/市场营销', db1017180: '管理/市场营销',
  db34899655: '管理/产品', db4723970: '管理/产品',
  db35272152: '管理/互联网与创新', db35128450: '管理/互联网与创新', db26297606: '管理/互联网与创新',
  db27074369: '管理/商业传记', db26849305: '管理/商业传记', db27179563: '管理/商业传记', db26929955: '管理/商业传记',
  // 教育
  db35368398: '教育/学习方法', db26895993: '教育/学习方法', db1013208: '教育/学习方法', db25783654: '教育/学习方法', db27047949: '教育/学习方法',
  // 语言
  db35503571: '语言/写作', db26990918: '语言/写作',
  db26948148: '语言/口才与表达', db26865749: '语言/口才与表达', db26799348: '语言/口才与表达',
  // 文学
  db35005045: '文学/中国小说', db35587028: '文学/中国小说', db3532347: '文学/中国小说',
  db11539568: '文学/外国小说', db26803714: '文学/外国小说', db26340138: '文学/外国小说', db26698660: '文学/外国小说',
  db4010969: '文学/外国小说', db25862578: '文学/外国小说', db20443559: '文学/外国小说', db30134903: '文学/外国小说',
  db24257229: '文学/外国小说', db10594787: '文学/外国小说', db30278620: '文学/外国小说', db25887924: '文学/外国小说',
  db10554308: '文学/推理悬疑', db36062395: '文学/推理悬疑', db1040771: '文学/推理悬疑',
  db6518605: '文学/科幻', db26863404: '文学/科幻',
  db5910656: '文学/散文随笔', db3369600: '文学/散文随笔', db26390229: '文学/散文随笔', db27013716: '文学/散文随笔', db30539780: '文学/散文随笔',
  db6440343: '文学/诗歌', db30462444: '文学/诗歌',
  db1883245: '文学/中国古典文学', db3772799: '文学/中国古典文学',
  // 历史
  db36216782: '历史/中国史', db36794613: '历史/中国史', db26603137: '历史/中国史', db2121038: '历史/中国史', db25935941: '历史/中国史',
  db36912612: '历史/中国史', db34446495: '历史/中国史', db25837482: '历史/中国史',
  fb487afff48: '历史/世界史', db37294999: '历史/世界史',
  db3901238: '历史/通俗历史', db24859453: '历史/通俗历史', db25945487: '历史/通俗历史',
  db33440205: '历史/传记', db30597601: '历史/传记',
  // 文化艺术 / 数理科学 / 医学健康 / 生活
  db1935312: '文化艺术/文化常识', db1941558: '文化艺术/设计',
  db35383731: '数理科学/数学', db35292688: '数理科学/数学', db2201479: '数理科学/数学',
  db26677472: '数理科学/博弈论', db1084435: '数理科学/博弈论', db1467022: '数理科学/物理',
  db26576861: '医学健康/医学与健康', faa0d1bf9b5: '医学健康/医学与健康',
  db24749465: '生活/整理与极简', db35284864: '生活/整理与极简',
  // 计算机（其余按原子分类映射）
  db35006472: '计算机/办公软件', db25786074: '计算机/前端开发', db10746113: '计算机/计算机网络',
  db26462816: '计算机/软件工程', db6526452: '计算机/软件工程', db35325133: '计算机/编程语言',
  db4822685: '计算机/计算机原理', db3296317: '计算机/计算机原理',
};

const CS_FROM_OLD = { 前端开发: '前端开发', 编程语言: '编程语言', 数据结构与算法: '数据结构与算法', 操作系统: '操作系统', 网络: '计算机网络', 编程思想: '软件工程', 架构应用: '架构与运维' };

/** CIP codes that were mis-read from the copyright page. */
const BAD_CIP = new Set(['db35038832']);

function classify(b) {
  if (MAP[b.id]) return { pair: MAP[b.id], how: 'curated' };
  if (b.category === '计算机') {
    if (/深入理解计算机系统|computer system/i.test(b.title)) return { pair: '计算机/计算机原理', how: 'rule' };
    if (/data structures/i.test(b.title)) return { pair: '计算机/数据结构与算法', how: 'rule' };
    return { pair: `计算机/${CS_FROM_OLD[b.subcategory] || '编程语言'}`, how: 'rule' };
  }
  return { pair: null, how: 'missing' };
}

/** 中图分类号 from the CIP block on an epub's copyright page (Ⅳ.①K249…), cached per file. */
async function cipCodes(books) {
  let cache = {};
  try { cache = JSON.parse(await fs.readFile(CLC_CACHE, 'utf8')); } catch {}
  for (const b of books) {
    for (const f of b.files.filter(f => f.format === 'epub')) {
      if (f.id in cache) continue;
      let code = null;
      const full = path.join(BOOK_DIR, f.path);
      try {
        const st = await fs.stat(full);
        const fh = await fs.open(full, 'r');
        try {
          const zip = await openZip(fh, st.size);
          for (const name of zip.names.filter(x => /\.x?html?$/i.test(x)).slice(0, 15)) {
            const t = (await zip.read(name)).toString('utf8').replace(/<[^>]+>/g, ' ');
            const m = /CIP|中国版本图书馆/.test(t) && t.match(/(?:Ⅳ|IV)\s*[.．、。]?\s*[①Ｉ]?\s*([A-Z][A-Z]?\s?\d[\d.\-=/]*\d)/);
            if (m) { code = m[1].replace(/\s/g, ''); break; }
          }
        } finally { await fh.close(); }
      } catch (e) { console.warn(`cip ${f.path}: ${e.message}`); }
      cache[f.id] = code;
    }
  }
  await fs.writeFile(CLC_CACHE, JSON.stringify(cache, null, 1));
  return new Map(books.map(b => [b.id, BAD_CIP.has(b.id) ? null : b.files.map(f => cache[f.id]).find(Boolean) || null]));
}

const xmlEsc = s => String(s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
const decode = s => s.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&');
const OLD_CATS = ['计算机', '数学', '未分类', '文学 & 小说', '政治 & 哲学', '心理学 & 教育', '历史 & 社会', '经营 & 管理 & 产品', '金融 & 投资 & 理财'];

/** Rewrites category fields in an OPF: drops old category subjects/metas, inserts the new ones after dc:language. */
function rewriteOpf(opf, cat, sub, clc) {
  const current = [...opf.matchAll(/<meta name="home:(?:category|subcategory)" content="([^"]*)"\/>/g)].map(m => decode(m[1]));
  const drop = new Set([...OLD_CATS, ...current, cat, sub]);
  let out = opf.replace(/[ \t]*<dc:subject>([^<]*)<\/dc:subject>\n/g, (m, v) => drop.has(decode(v)) ? '' : m)
    .replace(/[ \t]*<meta name="home:(category|subcategory|clc|clc_source)"[^>]*\/>\n/g, '');
  const subjects = `    <dc:subject>${xmlEsc(cat)}</dc:subject>\n    <dc:subject>${xmlEsc(sub)}</dc:subject>\n`;
  out = out.replace(/(<dc:language>[^<]*<\/dc:language>\n)/, `$1${subjects}`);
  const metas = `    <meta name="home:category" content="${xmlEsc(cat)}"/>\n    <meta name="home:subcategory" content="${xmlEsc(sub)}"/>\n` +
    (clc ? `    <meta name="home:clc" content="${xmlEsc(clc)}"/>\n    <meta name="home:clc_source" content="cip"/>\n` : '');
  return out.replace(/(\s*)<\/metadata>/, `\n${metas.trimEnd()}$1</metadata>`);
}

async function load() {
  const books = JSON.parse(await fs.readFile(path.join(ROOT, 'data/books.json'), 'utf8'));
  const clc = await cipCodes(books);
  return books.map(b => {
    const { pair, how } = classify(b);
    const [cat, sub] = (pair || '未分类/未分类').split('/');
    return { b, cat, sub, how, clc: clc.get(b.id) };
  });
}

async function writePlan() {
  const rows = await load();
  const missing = rows.filter(r => r.how === 'missing');
  const count = new Map();
  for (const r of rows) count.set(`${r.cat}/${r.sub}`, (count.get(`${r.cat}/${r.sub}`) || 0) + 1);
  const catCount = c => rows.filter(r => r.cat === c).length;
  const cipRows = rows.filter(r => r.clc);
  const letter = { 哲学: /^B(?!84)/, 心理学: /^B84|^R395/, 社会: /^C9[1-3]/, 政治: /^D/, 经济: /^F(?!27|4|7)/, 管理: /^F27|^F4|^F7|^C93|^C96/, 教育: /^G[4-7]/, 语言: /^H/, 文学: /^I/, 历史: /^K/, 文化艺术: /^G[0-2]|^J/, 数理科学: /^O|^C91[56]/, 医学健康: /^R/, 生活: /^TS/, 计算机: /^TP/ };
  const agree = cipRows.filter(r => letter[r.cat]?.test(r.clc));
  const tax = TAXONOMY.map(([c, clc, subs]) => `| **${c}** | ${clc} | ${catCount(c)} | ${subs.map(s => `${s} ${count.get(`${c}/${s}`) || 0}`).join('，')} |`).join('\n');
  const list = TAXONOMY.map(([c]) => {
    const rs = rows.filter(r => r.cat === c).sort((x, y) => x.sub.localeCompare(y.sub, 'zh-CN') || x.b.title.localeCompare(y.b.title, 'zh-CN'));
    return `\n### ${c}（${rs.length}）\n\n| 细类 | 书 | 原分类 | 中图分类号（CIP） |\n| --- | --- | --- | --- |\n` +
      rs.map(r => `| ${r.sub} | ${r.b.title.replace(/\|/g, '\\|')} | ${r.b.category}${r.b.subcategory ? ' / ' + r.b.subcategory : ''} | ${r.clc || ''}${r.clc && !letter[r.cat]?.test(r.clc) ? ' ⚠︎' : ''} |`).join('\n');
  }).join('\n');
  const md = `# 书库重新分类方案

生成时间：${new Date().toLocaleString('zh-CN')}。共 ${rows.length} 本。

- 两级分类，参考《中国图书馆分类法》：**大类**对应中图法基本大类（心理学 B84、管理 F27/C93 因为书多单独成类），**细类**是一个具体主题。
- 写进每本书的 \`metadata.opf\`：\`home:category\`=大类、\`home:subcategory\`=细类、前两个 \`dc:subject\`；能从书里版权页 CIP 读到中图分类号的写 \`home:clc\`。
- 只改 OPF，不移动文件。执行前把全部 OPF 备份到 \`logs/opf-backup-<时间>/\`，可一键还原。

## 分类来源

- 国家图书馆 OPAC（opac.nlc.cn）按 ISBN 查官方分类号：**本机连不上**（直连超时、代理 502），这次没用上。
- 版权页 CIP 中图分类号：**${cipRows.length} 本**（只看 epub 版权页；扫描版 PDF 读不到）。其中 ${agree.length} 本和下面的大类一致，不一致的在清单里标 ⚠︎。
- 其余按豆瓣标签 + 书名人工归类${missing.length ? `；**${missing.length} 本没有归类**：${missing.map(r => r.b.title).join('、')}` : ''}。

## 分类表

| 大类 | 中图法 | 书数 | 细类（书数） |
| --- | --- | --- | --- |
${tax}

## 每本书
${list}
`;
  await fs.writeFile(PLAN_MD, md);
  console.log(`plan: ${rows.length} books, ${TAXONOMY.length} 大类, ${count.size} 细类, CIP ${cipRows.length} (${agree.length} agree), missing ${missing.length}`);
  return rows;
}

async function apply() {
  const rows = await writePlan();
  if (rows.some(r => r.how === 'missing')) throw new Error('some books have no category; fix MAP first');
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const backup = path.join(ROOT, `logs/opf-backup-${stamp}`);
  const folders = new Map();
  for (const r of rows) {
    for (const f of r.b.files) {
      const dir = path.posix.dirname(f.path);
      if (dir !== '.' && !folders.has(dir)) folders.set(dir, r);
    }
  }
  for (const dir of folders.keys()) {
    await fs.mkdir(path.join(backup, dir), { recursive: true });
    await fs.copyFile(path.join(BOOK_DIR, dir, 'metadata.opf'), path.join(backup, dir, 'metadata.opf'));
  }
  let n = 0;
  for (const [dir, r] of folders) {
    const f = path.join(BOOK_DIR, dir, 'metadata.opf');
    await fs.writeFile(f, rewriteOpf(await fs.readFile(f, 'utf8'), r.cat, r.sub, r.clc));
    n++;
  }
  console.log(`rewrote ${n} metadata.opf; backup: ${path.relative(ROOT, backup)}`);
  console.log(`rollback: node recategorize-books.mjs --rollback ${path.relative(ROOT, backup)}`);
}

async function rollback(backup) {
  let n = 0;
  async function walk(rel) {
    for (const d of await fs.readdir(path.join(backup, rel), { withFileTypes: true })) {
      const r = path.join(rel, d.name);
      if (d.isDirectory()) await walk(r);
      else if (d.name === 'metadata.opf') { await fs.copyFile(path.join(backup, r), path.join(BOOK_DIR, r)); n++; }
    }
  }
  await walk('');
  console.log(`restored ${n} metadata.opf`);
}

const argv = process.argv.slice(2);
if (argv[0] === '--apply') await apply();
else if (argv[0] === '--rollback') await rollback(path.resolve(argv[1]));
else await writePlan();
