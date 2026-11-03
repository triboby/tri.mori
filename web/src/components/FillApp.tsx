// 补片: Douban / IMDb Top 250 coverage against the library (data/lists.json), then director coverage; Radarr add stays disabled.
import { useEffect, useMemo, useState } from 'react';
import { getLists, getMovies, type Chart, type ChartItem, type Lists, type Movie } from '../lib/api';
import { fmtDate, posterURL, score, shortName } from '../lib/util';
import { MovieCard, Pending, Section } from './cards';
import { FillSkel } from './Skeleton';

const CHARTS: { id: string; name: string; source: string }[] = [
  { id: 'douban', name: '豆瓣电影 Top 250', source: 'https://movie.douban.com/top250' },
  { id: 'imdb', name: 'IMDb Top 250（近似）', source: 'https://developer.imdb.com/non-commercial-datasets/' },
];

function ChartRow({ it, movie, suspect }: { it: ChartItem; movie?: Movie; suspect?: Movie }) {
  const src = movie ? posterURL(movie) : it.poster ? `/posters/${it.poster}` : null;
  const ext = it.doubanId ? `https://movie.douban.com/subject/${it.doubanId}/` : it.imdbId ? `https://www.imdb.com/title/${it.imdbId}/` : undefined;
  return (
    <li className="card-panel flex items-center gap-3 rounded-[10px] p-2 pr-3">
      <span className="w-8 shrink-0 text-right font-mono text-sm text-muted">{it.rank}</span>
      <div className="aspect-[2/3] w-10 shrink-0 overflow-hidden rounded bg-raise">{src && <img src={src} alt="" loading="lazy" className="h-full w-full object-cover" />}</div>
      <div className="flex min-w-0 flex-1 flex-col">
        {movie ? <a href={`/movie?id=${movie.id}`} className="truncate text-[15px] text-text hover:text-accent">{it.title}</a>
          : <a href={ext} target="_blank" rel="noopener" className="truncate text-[15px] text-text hover:text-accent">{it.title}</a>}
        <span className="truncate font-mono text-[11px] text-muted">{[it.original, it.year, it.rating && it.rating.toFixed(1)].filter(Boolean).join(' · ')}</span>
        {it.onNas && <span className="truncate text-xs text-cyan" title={it.onNas}>NAS 上有但没入库：{it.onNas.split('/').pop()}（生成片库时没认出片名）</span>}
        {suspect && <span className="text-xs text-warn">片库里可能有：<a href={`/movie?id=${suspect.id}`} className="text-warn underline">{suspect.title} {suspect.year}</a>（编号没对上，请核对）</span>}
      </div>
      {movie ? <span className="shrink-0 rounded-[7px] bg-ok-bg px-2 py-1 text-xs text-ok">已在片库{it.matchedBy === 'title' ? '（按片名）' : ''}</span>
        : <span className="flex shrink-0 items-center gap-2"><span className={`rounded-[7px] px-2 py-1 text-xs ${it.onNas ? 'bg-cyan-bg text-cyan' : 'bg-raise text-muted'}`}>{it.onNas ? 'NAS 有 · 未入库' : '片库没有'}</span>
          <button type="button" className="btn hidden min-h-8 text-xs sm:inline-flex" disabled title="还没决定是否允许从页面加片">加到 Radarr</button></span>}
    </li>
  );
}

function ChartSection({ meta, chart, byId }: { meta: (typeof CHARTS)[number]; chart?: Chart; byId: Map<number, Movie> }) {
  const [missingOnly, setMissingOnly] = useState(false);
  const [all, setAll] = useState(false);
  if (!chart) {
    return (
      <Section title={meta.name} note={<a href={meta.source} target="_blank" rel="noopener" className="text-muted">数据源</a>}>
        <div className="flex flex-wrap items-center gap-3 rounded-xl border border-dashed border-edge-3 px-4 py-3.5 text-sm text-muted"><Pending>暂无数据</Pending>这张榜单还没抓取到。</div>
      </Section>
    );
  }
  const items = missingOnly ? chart.items.filter(i => !i.movieId) : chart.items;
  const shown = all ? items : items.slice(0, 50);
  const suspects = chart.items.filter(i => i.suspect || i.onNas).length;
  const pct = chart.total ? (chart.have / chart.total) * 100 : 0;
  return (
    <Section title={meta.name} note={<>{chart.id === 'imdb' ? '数据集日期' : '抓取于'} {fmtDate(chart.fetched)} · <a href={chart.source} target="_blank" rel="noopener" className="text-muted">数据源</a></>}
      action={<label className="flex cursor-pointer items-center gap-2 text-muted"><input type="checkbox" className="accent-[var(--color-accent)]" checked={missingOnly} onChange={e => setMissingOnly(e.target.checked)} />只看缺的</label>}>
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline gap-x-3"><span className="font-mono text-[28px] text-accent">{chart.have}</span><span className="font-mono text-muted">/ {chart.total}</span>
          <span className="text-[13px] text-muted">已在片库 · 缺 {chart.total - chart.have} 部{suspects ? ` · 其中 ${suspects} 部疑似已有但没对上` : ''}</span></div>
        {chart.note && <p className="m-0 text-[13px] text-muted">{chart.note}</p>}
        <div className="h-2.5 max-w-xl overflow-hidden rounded-full bg-raise" role="img" aria-label={`覆盖 ${pct.toFixed(0)}%`}><div className="h-full bg-accent" style={{ width: `${pct}%` }} /></div>
      </div>
      <ol className="m-0 flex list-none flex-col gap-2 p-0">
        {shown.map(it => <ChartRow key={it.rank} it={it} movie={it.movieId ? byId.get(it.movieId) : undefined} suspect={it.suspect ? byId.get(it.suspect) : undefined} />)}
      </ol>
      {items.length > shown.length && <div><button type="button" className="btn" onClick={() => setAll(true)}>显示全部 {items.length} 部</button></div>}
    </Section>
  );
}

export default function FillApp() {
  const [movies, setMovies] = useState<Movie[] | null>(null);
  const [lists, setLists] = useState<Lists>({});
  const [error, setError] = useState<string>();
  const [who, setWho] = useState<string | null>(null);
  useEffect(() => { Promise.all([getMovies(), getLists()]).then(([m, l]) => { setMovies(m); setLists(l); }).catch(e => setError(String(e.message || e))); }, []);
  const byId = useMemo(() => new Map((movies || []).map(m => [m.id, m])), [movies]);
  const directors = useMemo(() => {
    const g = new Map<string, Movie[]>();
    for (const m of movies || []) for (const d of m.directors) { const k = shortName(d); (g.get(k) || g.set(k, []).get(k)!).push(m); }
    return [...g].filter(([, l]) => l.length >= 2).sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0], 'zh-CN'));
  }, [movies]);
  if (!movies) return <FillSkel error={error} />;
  const sel = directors.find(([d]) => d === who) || directors[0];

  return (
    <main className="mx-auto flex max-w-[1440px] flex-col gap-9 px-4 pb-10 pt-5 md:px-8 md:pt-6">
      {CHARTS.map(c => <ChartSection key={c.id} meta={c} chart={lists.charts?.find(x => x.id === c.id)} byId={byId} />)}

      <Section title="导演覆盖" note="片库里有两部以上的导演；完整作品表要接 TMDB 人物作品表">
        <div className="flex flex-wrap gap-2">
          {directors.slice(0, 40).map(([d, l]) => <button key={d} type="button" className="chip min-h-9 px-3 text-sm" aria-pressed={sel?.[0] === d} onClick={() => setWho(d)}>{d} <span className="font-mono text-xs text-muted">{l.length}</span></button>)}
        </div>
        {sel && <div className="grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(150px,1fr))]">
          {[...sel[1]].sort((a, b) => (a.year || 0) - (b.year || 0)).map(m => <MovieCard key={m.id} m={m} meta={`${m.year ?? ''} · ${score(m) ? score(m).toFixed(1) : '—'}`} />)}
          <div className="flex aspect-[2/3] flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-edge-3 p-3 text-center text-[13px] text-muted">
            {sel[0]} 的其他作品<Pending>TMDB 待接入</Pending>
          </div>
        </div>}
      </Section>
    </main>
  );
}
