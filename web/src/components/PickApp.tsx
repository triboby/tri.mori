// 选片: the whole library as a poster wall with multi-select filters and sorting; the state lives in the URL query.
import { useEffect, useMemo, useRef, useState } from 'react';
import { getMovies, type Movie } from '../lib/api';
import { isKids, score, tech } from '../lib/util';
import { Empty, MovieCard } from './cards';
import { PickSkel } from './Skeleton';

type Opt = { value: string; label: string; test: (m: Movie) => boolean };
type Group = { key: string; label: string; opts: Opt[] };

const PAGE = 60;
const ZH = /中文/;
const decade = (m: Movie) => (m.year ? Math.floor(m.year / 10) * 10 : null);
const height = (m: Movie) => tech(m).height;

function buildGroups(movies: Movie[]): Group[] {
  const count = (vals: string[]) => [...vals.reduce((c, v) => c.set(v, (c.get(v) || 0) + 1), new Map<string, number>())].sort((a, b) => b[1] - a[1]).map(([v]) => v);
  const decades = [...new Set(movies.map(decade).filter((d): d is number => d != null))].sort((a, b) => b - a);
  return [
    { key: 'lib', label: '库', opts: [
      { value: 'main', label: '电影', test: m => !isKids(m) },
      { value: 'kids', label: '少儿', test: m => !!isKids(m) },
    ] },
    { key: 'region', label: '地区', opts: count(movies.map(m => m.region)).map(r => ({ value: r, label: r, test: m => m.region === r })) },
    { key: 'genre', label: '类型', opts: count(movies.flatMap(m => m.genres)).map(g => ({ value: g, label: g, test: m => m.genres.includes(g) })) },
    { key: 'decade', label: '年代', opts: decades.map(d => ({ value: String(d), label: d >= 2000 ? `${d} 年代` : `${String(d).slice(2)} 年代`, test: m => decade(m) === d })) },
    { key: 'rating', label: '评分', opts: [
      { value: '8', label: '8 分以上', test: m => score(m) >= 8 },
      { value: '7', label: '7–8', test: m => score(m) >= 7 && score(m) < 8 },
      { value: '6', label: '6–7', test: m => score(m) >= 6 && score(m) < 7 },
      { value: '0', label: '6 分以下', test: m => score(m) < 6 },
    ] },
    { key: 'len', label: '片长', opts: [
      { value: '90', label: '≤ 90 分钟', test: m => !!m.runtime && m.runtime <= 90 },
      { value: '120', label: '90–120', test: m => !!m.runtime && m.runtime > 90 && m.runtime <= 120 },
      { value: '150', label: '120–150', test: m => !!m.runtime && m.runtime > 120 && m.runtime <= 150 },
      { value: 'long', label: '> 150', test: m => !!m.runtime && m.runtime > 150 },
    ] },
    { key: 'res', label: '画质', opts: [
      { value: '4k', label: '4K', test: m => height(m) >= 2160 },
      { value: '1080', label: '1080p', test: m => height(m) === 1080 },
      { value: 'sd', label: '更低', test: m => height(m) > 0 && height(m) < 1080 },
    ] },
    { key: 'sub', label: '字幕', opts: [
      { value: 'zh', label: '有中文字幕', test: m => m.subLangs.some(l => ZH.test(l)) },
      { value: 'none', label: '无中文字幕', test: m => !m.subLangs.some(l => ZH.test(l)) },
    ] },
  ];
}

const collator = new Intl.Collator('zh-CN-u-co-pinyin');
const SORTS: Record<string, { label: string; cmp: (a: Movie, b: Movie) => number }> = {
  added: { label: '入库时间', cmp: (a, b) => b.added - a.added },
  rating: { label: '评分', cmp: (a, b) => score(b) - score(a) || (b.imdbVotes || 0) - (a.imdbVotes || 0) },
  year: { label: '年份', cmp: (a, b) => (b.year || 0) - (a.year || 0) || b.added - a.added },
  title: { label: '片名', cmp: (a, b) => collator.compare(a.title, b.title) },
};

type Sel = Record<string, string[]>;

function readURL(): { sel: Sel; sort: string } {
  const p = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const sel: Sel = {};
  for (const [k, v] of p) if (k !== 'sort' && v) sel[k] = v.split(',');
  const sort = p.get('sort') || 'added';
  return { sel, sort: sort in SORTS ? sort : 'added' };
}

function writeURL(sel: Sel, sort: string) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(sel)) if (v.length) p.set(k, v.join(','));
  if (sort !== 'added') p.set('sort', sort);
  const qs = p.toString().replace(/%2C/g, ',');
  history.replaceState(null, '', location.pathname + (qs ? '?' + qs : ''));
}

export default function PickApp() {
  const [movies, setMovies] = useState<Movie[] | null>(null);
  const [error, setError] = useState<string>();
  const [{ sel, sort }, setState] = useState(readURL);
  const [shown, setShown] = useState(PAGE);
  const [open, setOpen] = useState(false);
  const sentinel = useRef<HTMLDivElement>(null);
  useEffect(() => { getMovies().then(setMovies).catch(e => setError(String(e.message || e))); }, []);
  useEffect(() => { writeURL(sel, sort); setShown(PAGE); }, [sel, sort]);

  const groups = useMemo(() => (movies ? buildGroups(movies) : []), [movies]);
  const list = useMemo(() => {
    if (!movies) return [];
    const active = groups.map(g => ({ g, on: g.opts.filter(o => sel[g.key]?.includes(o.value)) })).filter(x => x.on.length);
    return movies.filter(m => active.every(({ on }) => on.some(o => o.test(m)))).sort(SORTS[sort].cmp);
  }, [movies, groups, sel, sort]);

  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver(es => { if (es[0].isIntersecting) setShown(n => n + PAGE); }, { rootMargin: '800px' });
    io.observe(el);
    return () => io.disconnect();
  }, [movies, list.length > shown]);

  if (!movies) return <PickSkel error={error} />;
  const nSel = Object.values(sel).reduce((n, v) => n + v.length, 0);
  const toggle = (key: string, value: string) => setState(s => {
    const cur = s.sel[key] || [];
    return { ...s, sel: { ...s.sel, [key]: cur.includes(value) ? cur.filter(v => v !== value) : [...cur, value] } };
  });
  const clear = () => setState(s => ({ ...s, sel: {} }));

  return (
    <main className="mx-auto flex max-w-[1440px] flex-col gap-4 px-4 pb-10 pt-5 md:px-8 md:pt-6">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 className="m-0 text-[24px] font-semibold tracking-tight md:text-[28px]">选片</h1>
        <span className="font-mono text-[13px] text-muted">{nSel ? `命中 ${list.length} / ${movies.length} 部` : `全部 ${movies.length} 部`}</span>
        <button type="button" className="btn md:hidden" aria-expanded={open} aria-controls="pick-filters" onClick={() => setOpen(o => !o)}>
          筛选{nSel > 0 && <span className="rounded bg-accent px-1.5 font-mono text-[11px] text-accent-ink">{nSel}</span>}
        </button>
        {nSel > 0 && <button type="button" className="btn" onClick={clear}>清空条件</button>}
        <label className="ml-auto flex items-center gap-2 text-[13px] text-muted">排序
          <select value={sort} onChange={e => setState(s => ({ ...s, sort: e.target.value }))} className="min-h-9 rounded-lg border border-edge-2 bg-chip px-2 text-sm text-text">
            {Object.entries(SORTS).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
          </select>
        </label>
      </div>

      <div id="pick-filters" className={`${open ? 'flex' : 'hidden'} card-panel flex-col gap-2.5 p-3 md:flex md:border-0 md:bg-transparent md:p-0`}>
        {groups.map(g => (
          <div key={g.key} className="flex items-start gap-2">
            <span className="w-9 shrink-0 pt-2 font-mono text-xs text-muted">{g.label}</span>
            <div className="flex flex-wrap gap-1.5">
              {g.opts.map(o => (
                <button key={o.value} type="button" className="chip min-h-9 rounded-lg px-3 text-[13px]" aria-pressed={!!sel[g.key]?.includes(o.value)} onClick={() => toggle(g.key, o.value)}>{o.label}</button>
              ))}
            </div>
          </div>
        ))}
      </div>

      {list.length ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(104px,1fr))] gap-x-3 gap-y-4 md:grid-cols-[repeat(auto-fill,minmax(140px,1fr))] md:gap-x-4">
          {list.slice(0, shown).map(m => <MovieCard key={m.id} m={m} />)}
        </div>
      ) : <Empty>没有符合这些条件的片子。<button type="button" className="btn ml-auto" onClick={clear}>清空条件</button></Empty>}
      {list.length > shown && <div ref={sentinel} className="h-10" />}
    </main>
  );
}
