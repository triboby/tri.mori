// 首页: 接着看/接着读, 今晚给你, 新入库, 片单, 书架; scope tabs (全部/电影/少儿) and search results (?q=).
import { useEffect, useMemo, useState } from 'react';
import { getBooks, getMovies, search, type Book, type Movie, type SearchResult } from '../lib/api';
import { fmtDate, isKids, readPct, readestFile, readestHref, save, score, seededShuffle, store, today } from '../lib/util';
import { BOOK_STRIP, BookCard, BookCover, MovieCard, Pending, STRIP, Section } from './cards';
import { HomeSkel, PosterGrid, SectionSkel, Skeleton } from './Skeleton';

type Scope = '全部' | '电影' | '少儿';

export default function HomeApp() {
  const [movies, setMovies] = useState<Movie[] | null>(null);
  const [books, setBooks] = useState<Book[]>([]);
  const [error, setError] = useState<string>();
  const params = typeof location !== 'undefined' ? new URLSearchParams(location.search) : new URLSearchParams();
  const [scope, setScope] = useState<Scope>((params.get('scope') as Scope) || '全部');
  const q = params.get('q')?.trim() || '';
  const [lastVisit] = useState(() => store<number>('lastVisit', 0));

  useEffect(() => {
    const m = params.get('m');
    if (m) { location.replace(`/movie?id=${encodeURIComponent(m)}`); return; }
    Promise.all([getMovies(), getBooks()]).then(([ms, bs]) => { setMovies(ms); setBooks(bs); }).catch(e => setError(String(e.message || e)));
    const mark = () => save('lastVisit', Date.now());
    addEventListener('pagehide', mark);
    return () => removeEventListener('pagehide', mark);
  }, []);

  const pick = (s: Scope) => {
    setScope(s);
    const p = new URLSearchParams(location.search);
    s === '全部' ? p.delete('scope') : p.set('scope', s);
    history.replaceState(null, '', location.pathname + (p.size ? '?' + p : ''));
  };

  if (!movies) return <HomeSkel error={error} />;
  return (
    <main className="mx-auto flex max-w-[1440px] flex-col gap-9 px-4 pb-10 pt-5 md:px-8 md:pt-6">
      {q ? <SearchView q={q} movies={movies} books={books} /> : <>
        <nav aria-label="范围" className="-mb-3 flex flex-wrap gap-2">
          {(['全部', '电影', '少儿'] as Scope[]).map(s => <button key={s} type="button" className="chip min-h-10 text-sm" aria-pressed={scope === s} onClick={() => pick(s)}>{s}</button>)}
          <a href="/books" className="chip inline-flex min-h-10 items-center text-sm no-underline hover:text-text">书</a>
        </nav>
        {scope === '全部' ? <Overview movies={movies} books={books} lastVisit={lastVisit} /> : <Browse movies={scope === '少儿' ? movies.filter(isKids) : movies} kids={scope === '少儿'} />}
      </>}
    </main>
  );
}

function Overview({ movies, books, lastVisit }: { movies: Movie[]; books: Book[]; lastVisit: number }) {
  const reading = books.filter(b => b.progress && b.progress.percentage < 0.99).sort((a, b) => b.progress!.timestamp - a.progress!.timestamp).slice(0, 3);
  const skipped = store<number[]>('skipped', []);
  const tonight = useMemo(() => seededShuffle(movies.filter(m => score(m) >= 7.5 && !skipped.includes(m.id) && m.runtime && m.runtime <= 150), today()).slice(0, 8), [movies]);
  const byAdded = [...movies].sort((a, b) => b.added - a.added);
  const fresh = lastVisit ? byAdded.filter(m => m.added > lastVisit) : [];
  const recentBooks = [...books].sort((a, b) => b.added - a.added).slice(0, 12);

  return <>
    <Section title="接着看 · 接着读" note={<>KOReader / Readest 阅读进度 · 电影的 Plex 观看记录和 IINA 进度 <Pending /></>}>
      {reading.length ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-3.5">
          {reading.map(b => {
            const f = readestFile(b);
            return (
              <div key={b.id} className="card-panel grid grid-cols-[64px_minmax(0,1fr)] items-center gap-3.5 p-3">
                <BookCover b={b} />
                <div className="flex min-w-0 flex-col gap-1.5">
                  <div className="flex flex-wrap items-baseline gap-2"><strong className="text-[15px]">{b.title}</strong><span className="rounded border border-edge-2 px-1.5 font-mono text-[11px] text-muted">KOReader · {b.progress!.device || '设备'}</span></div>
                  <div className="h-1 overflow-hidden rounded-sm bg-edge"><div className="h-1 bg-accent" style={{ width: `${readPct(b)}%` }} /></div>
                  <span className="text-xs text-muted">读到 {readPct(b)}% · {fmtDate(b.progress!.timestamp * 1000)}</span>
                  {f && <div><a className="btn-pri min-h-8 text-[13px]" href={readestHref(f)}>在 Readest 中继续</a></div>}
                </div>
              </div>
            );
          })}
        </div>
      ) : <p className="m-0 text-sm text-muted">还没有阅读进度。KOReader 或 Readest 设好同步服务器（<code className="font-mono text-xs">/kosync</code>）后，读到哪里会出现在这里。</p>}
    </Section>

    <Section title="今晚给你" note="评分 7.5 以上、片长两个半小时内，每天换一批（按观看记录挑选待接入）" action={<a href="/pick">换个条件挑</a>}>
      <div className={STRIP}>{tonight.map(m => <MovieCard key={m.id} m={m} />)}</div>
    </Section>

    <Section title={fresh.length ? '你上次来之后新入库' : '最近入库'}
      note={fresh.length ? `${fresh.length} 部 · ${fmtDate(lastVisit)}以来` : '按 NAS 文件时间排序'}>
      <div className={STRIP}>{(fresh.length ? fresh : byAdded.slice(0, 14)).map(m => <MovieCard key={m.id} m={m} meta={fmtDate(m.added)} />)}</div>
    </Section>

    <Section title="片单" note="自动生成，显示片库覆盖了多少" action={<a href="/fill">导演覆盖与补片</a>}>
      <div className="flex flex-wrap items-center gap-3 rounded-xl border border-dashed border-edge-3 px-4 py-3.5 text-sm text-muted">
        <Pending />豆瓣 Top 250、IMDb Top 250、TMDB 人物作品表、Radarr 系列合集，接入后在这里显示「有 N / 共 M」。
      </div>
    </Section>

    <Section title="书架" note={`最近加入 · ${books.length} 本 · 点开用 Readest 读`} action={<a href="/books">全部书</a>}>
      <div className={BOOK_STRIP}>{recentBooks.map(b => <BookCard key={b.id} b={b} />)}</div>
    </Section>
  </>;
}

const groupBy = <T,>(list: T[], key: (x: T) => string[]) => {
  const g = new Map<string, T[]>();
  for (const x of list) for (const k of key(x)) (g.get(k) || g.set(k, []).get(k)!).push(x);
  return [...g];
};

function Browse({ movies, kids }: { movies: Movie[]; kids: boolean }) {
  const [group, setGroup] = useState(() => store<string>('browseGroup', '地区'));
  const [sort, setSort] = useState(() => store<string>('sortBy', 'default'));
  useEffect(() => { save('browseGroup', group); save('sortBy', sort); }, [group, sort]);
  const sorted = (l: Movie[]) => (sort === 'rating' ? [...l].sort((a, b) => score(b) - score(a) || (b.imdbVotes || 0) - (a.imdbVotes || 0)) : [...l].sort((a, b) => b.added - a.added));
  const rows: [string, Movie[]][] = group === '地区' ? (['华语', '日韩', '欧美'].map(r => [r, movies.filter(m => m.region === r)] as [string, Movie[]]))
    : group === '类型' ? groupBy(movies, m => m.genres).filter(([, l]) => l.length >= 3).sort((a, b) => b[1].length - a[1].length)
      : group === '年代' ? groupBy(movies, m => [m.year ? `${Math.floor(m.year / 10) * 10} 年代` : '年份未知']).sort((a, b) => b[0].localeCompare(a[0]))
        : [['全部', movies]];
  return <>
    <div className="flex flex-wrap items-center gap-2">
      <span className="font-mono text-xs text-muted">{kids ? '少儿库' : '全部电影'} {movies.length} 部 · 分组</span>
      {['地区', '类型', '年代', '不分组'].map(g => <button key={g} type="button" className="chip min-h-9 px-3 text-sm" aria-pressed={group === g} onClick={() => setGroup(g)}>{g}</button>)}
      <select aria-label="排序" value={sort} onChange={e => setSort(e.target.value)} className="ml-auto min-h-9 rounded-lg border border-edge-2 bg-chip px-2 text-sm text-text">
        <option value="default">按入库时间</option><option value="rating">按评分</option>
      </select>
    </div>
    {rows.filter(([, l]) => l.length).map(([name, list]) => (
      <Section key={name} title={name} note={`${list.length} 部`}>
        {group === '不分组' || rows.length === 1
          ? <div className="grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(136px,1fr))]">{sorted(list).map(m => <MovieCard key={m.id} m={m} />)}</div>
          : <div className={STRIP}>{sorted(list).map(m => <MovieCard key={m.id} m={m} />)}</div>}
      </Section>
    ))}
  </>;
}

function SearchView({ q, movies, books }: { q: string; movies: Movie[]; books: Book[] }) {
  const [res, setRes] = useState<SearchResult | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { search(q, 'all').then(r => (r.error ? setError(r.error) : setRes(r))).catch(e => setError(String(e.message || e))); }, [q]);
  const mById = useMemo(() => new Map(movies.map(m => [m.id, m])), [movies]);
  const bById = useMemo(() => new Map(books.map(b => [b.id, b])), [books]);
  if (!res) return <Skeleton error={error} label="搜索中"><SectionSkel><PosterGrid n={12} /></SectionSkel></Skeleton>;
  const ms = res.hits.filter(h => h.kind === 'movies').map(h => mById.get(h.id as number)).filter(Boolean) as Movie[];
  const bs = res.hits.filter(h => h.kind === 'books').map(h => bById.get(h.id as string)).filter(Boolean) as Book[];
  const grid = 'grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(136px,1fr))]';
  return <>
    <Section title={`「${q}」`} note={`电影 ${ms.length} 部 · 书 ${bs.length} 本 · ${(res.ms / 1000).toFixed(2)} 秒`} action={<a href="/pick">按条件挑</a>}>
      {ms.length ? <div className={grid}>{ms.map(m => <MovieCard key={m.id} m={m} />)}</div> : <p className="m-0 text-muted">片库里没有相关的电影。</p>}
    </Section>
    {bs.length > 0 && <Section title="书架里" note={`${bs.length} 本`}><div className={BOOK_STRIP}>{bs.map(b => <BookCard key={b.id} b={b} />)}</div></Section>}
  </>;
}
