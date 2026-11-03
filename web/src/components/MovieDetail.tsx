// 详情与「在哪播放」: /movie?id=<tmdbId>.
import { useEffect, useMemo, useState } from 'react';
import { getBooks, getMovies, search, type Book, type Movie } from '../lib/api';
import { copyText, fmtSize, iinaURL, isKids, ratingLabel, score, shortName, smbURL, tech } from '../lib/util';
import { BOOK_STRIP, BookCard, MovieCard, Pending, Poster, STRIP } from './cards';
import { BookStrip, DetailSkel, Skeleton } from './Skeleton';

/** Library films sharing a director, then most genres, ranked by rating. */
function similar(m: Movie, all: Movie[]) {
  const dirs = new Set(m.directors.map(shortName));
  return all.filter(x => x.id !== m.id).map(x => {
    const shared = x.genres.filter(g => m.genres.includes(g)).length;
    const sameDir = x.directors.some(d => dirs.has(shortName(d)));
    return { x, s: (sameDir ? 10 : 0) + shared * 2 + (x.region === m.region ? 1 : 0) + score(x) / 10 };
  }).filter(r => r.s >= 4).sort((a, b) => b.s - a.s).slice(0, 10).map(r => r.x);
}

export default function MovieDetail() {
  const [all, setAll] = useState<Movie[] | null>(null);
  const [error, setError] = useState<string>();
  const id = typeof location !== 'undefined' ? Number(new URLSearchParams(location.search).get('id')) : 0;
  useEffect(() => { getMovies().then(setAll).catch(e => setError(String(e.message || e))); }, []);
  const m = all?.find(x => x.id === id);
  useEffect(() => { if (m) document.title = `${m.title} · 森记 Mori`; }, [m]);
  const sims = useMemo(() => (m && all ? similar(m, all) : []), [m, all]);
  if (!all) return <DetailSkel error={error} />;
  if (!m) return <main className="mx-auto max-w-[1440px] px-4 py-6 md:px-8"><p>片库里没有这部电影。<a href="/">回首页</a></p></main>;
  const r = ratingLabel(m);
  const t = tech(m);
  const orig = [...new Set([m.originalTitle, m.enTitle])].filter(x => x && x !== m.title);

  return (
    <main>
      <section className="bg-[radial-gradient(90%_140%_at_75%_0%,#1d3348_0%,#0b0d12_70%)]">
        <div className="mx-auto flex max-w-[1440px] flex-wrap items-end gap-6 px-4 py-6 md:gap-8 md:px-8 md:py-8">
          <Poster m={m} big className="w-36 flex-none md:w-[220px]" />
          <div className="flex min-w-0 flex-[1_1_420px] flex-col gap-2.5">
            <h1 className="m-0 font-serif text-[28px] font-black leading-tight md:text-[38px]">{m.title}
              <span className="ml-2 font-sans text-base font-normal text-muted md:text-lg">{[orig[0], m.year].filter(Boolean).join(' · ')}</span></h1>
            <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-sm text-muted">
              {r && <span><strong className="font-mono text-accent">{r.value}</strong> {r.href ? <a href={r.href} target="_blank" rel="noopener" className="text-muted">{r.label}</a> : r.label}</span>}
              {m.imdbRating && m.rating && <span><strong className="font-mono text-accent">{m.rating.toFixed(1)}</strong> <a className="text-muted" href={`https://www.themoviedb.org/movie/${m.id}?language=zh-CN`} target="_blank" rel="noopener">TMDB</a></span>}
              {m.runtime && <span>{m.runtime} 分钟</span>}
              {m.genres.length > 0 && <span>{m.genres.join(' · ')}</span>}
              {m.directors.length > 0 && <span>导演 {m.directors.map(shortName).join('、')}</span>}
              {isKids(m) && <span className="text-kids">少儿库</span>}
            </div>
            <div className="flex flex-wrap items-center gap-2.5 text-[13px] text-muted">你的评分和观看记录 <Pending>Plex 待接入</Pending></div>
            <p className="m-0 max-w-[62ch] text-soft">{m.overview || '暂无简介'}</p>
            <div className="flex flex-wrap gap-1.5">{t.tags.map(x => <span key={x} className="rounded border border-edge-3 px-1.5 py-0.5 font-mono text-xs">{x}</span>)}</div>
          </div>
        </div>
      </section>

      <div className="mx-auto flex max-w-[1440px] flex-wrap gap-8 px-4 pb-10 pt-2 md:px-8">
        <div className="flex min-w-0 flex-[999_1_640px] flex-col gap-7">
          <section className="card-panel p-4">
            <h2 className="m-0 mb-2.5 font-mono text-xs font-medium tracking-[.06em] text-muted">演职员</h2>
            <dl className="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
              <dt className="text-muted">导演</dt><dd className="m-0">{m.directors.join('、') || '—'}</dd>
              <dt className="text-muted">主演</dt><dd className="m-0">{m.cast.join('、') || '—'}</dd>
              <dt className="text-muted">地区</dt><dd className="m-0">{m.countries.join(' / ') || '—'}（{m.region}）</dd>
            </dl>
          </section>
          {sims.length > 0 && <section>
            <h2 className="m-0 mb-3 text-lg">片库里相近的 <span className="text-[13px] font-normal text-muted">同导演、同类型，按评分排</span></h2>
            <div className={STRIP}>{sims.map(x => <MovieCard key={x.id} m={x} />)}</div>
          </section>}
          <RelatedBooks m={m} />
        </div>

        <aside className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
          <WherePlay m={m} />
          <section className="card-panel p-4">
            <h2 className="m-0 mb-2.5 font-mono text-xs font-medium tracking-[.06em] text-muted">字幕</h2>
            {m.subLangs.length ? <div className="flex flex-col gap-2 text-sm">
              <div className="flex justify-between gap-2"><span>{m.subLangs.join('、')}</span><span className="font-mono text-xs text-ok">外挂 {m.subs.length} 个</span></div>
              <details className="text-xs text-muted"><summary className="cursor-pointer">文件列表</summary>{m.subs.map(s => <div key={s} className="break-all">{s.split('/').pop()}</div>)}</details>
            </div> : <p className="m-0 text-sm text-muted">没有外挂字幕（可能有内封字幕）。</p>}
          </section>
          <section className="card-panel p-4">
            <h2 className="m-0 mb-2.5 font-mono text-xs font-medium tracking-[.06em] text-muted">台词跳转</h2>
            <p className="m-0 text-sm text-muted"><Pending /> 按台词搜索并跳到对应时间点，需要先给字幕建索引。</p>
          </section>
          <section className="card-panel p-4 text-sm">
            <h2 className="m-0 mb-2.5 font-mono text-xs font-medium tracking-[.06em] text-muted">NAS</h2>
            {m.nasPaths.map(p => <div key={p} className="break-all"><a href={encodeURI(p)}>{p}</a></div>)}
            <div className="mt-2 text-muted">TMDB <a href={`https://www.themoviedb.org/movie/${m.id}?language=zh-CN`} target="_blank" rel="noopener">{m.id}</a></div>
          </section>
        </aside>
      </div>
    </main>
  );
}

function WherePlay({ m }: { m: Movie }) {
  const [status, setStatus] = useState('在这台 Mac 上用 IINA 直接打开 NAS 上的文件；客厅投屏接入 Plex 后可用。');
  const v = m.videos[0];
  const dest = 'flex min-h-12 w-full items-center justify-between gap-2.5 rounded-[10px] border px-3.5 text-left text-[15px] no-underline';
  return (
    <section className="card-panel p-4">
      <h2 className="m-0 mb-2.5 font-mono text-xs font-medium tracking-[.06em] text-muted">在哪播放</h2>
      {v ? <div className="flex flex-col gap-2">
        <a className={`${dest} border-accent bg-accent-bg text-accent hover:text-accent-hi`} href={iinaURL(v)} onClick={() => setStatus('已交给 IINA；如果没有反应，确认 NAS 的 store / video 共享已挂载。')}>
          <span>这台 Mac</span><span className="font-mono text-[11px] text-muted">IINA · NAS 直读</span></a>
        <button type="button" className={`${dest} cursor-not-allowed border-edge bg-chip text-muted`} disabled>
          <span>客厅 Apple TV</span><span className="font-mono text-[11px]">Plex · 待接入</span></button>
        <button type="button" className={`${dest} border-edge bg-chip text-text hover:border-accent`}
          onClick={async () => setStatus((await copyText(smbURL(v))) ? '已复制 SMB 地址，在 VidHub / Infuse 里粘贴打开。' : smbURL(v))}>
          <span>手机 / iPad</span><span className="font-mono text-[11px] text-muted">复制 SMB 地址</span></button>
        {m.videos.length > 1 && <div className="mt-1 flex flex-col gap-1 text-[13px]">
          <span className="text-muted">共 {m.videos.length} 个文件：</span>
          {m.videos.map((x, i) => <a key={x.path} href={iinaURL(x)} title={x.path}>文件 {i + 1} · {fmtSize(x.size)}</a>)}
        </div>}
      </div> : <p className="m-0 text-sm text-muted">NAS 上没有找到视频文件。</p>}
      <p className="m-0 mt-3 text-[13px] text-muted" role="status">{status}</p>
    </section>
  );
}

function RelatedBooks({ m }: { m: Movie }) {
  const [books, setBooks] = useState<Book[] | null>(null);
  const [state, setState] = useState<'idle' | 'loading' | 'done' | string>('idle');
  const run = async () => {
    setState('loading');
    try {
      const [all, res] = await Promise.all([getBooks(), search([m.title, ...m.genres.slice(0, 2)].join(' '), 'books')]);
      const byId = new Map(all.map(b => [b.id, b]));
      setBooks(res.hits.slice(0, 8).map(h => byId.get(h.id as string)).filter(Boolean) as Book[]);
      setState('done');
    } catch (e) { setState(String((e as Error).message || e)); }
  };
  return (
    <section>
      <h2 className="m-0 mb-3 text-lg">书架上相关的书 <span className="text-[13px] font-normal text-muted">按片名和类型做语义搜索</span></h2>
      {state === 'idle' && <button type="button" className="btn" onClick={run}>找找看</button>}
      {state === 'loading' && <Skeleton label="搜索中"><BookStrip n={8} /></Skeleton>}
      {state === 'done' && (books?.length ? <div className={BOOK_STRIP}>{books.map(b => <BookCard key={b.id} b={b} />)}</div> : <p className="m-0 text-sm text-muted">没有找到相关的书。</p>)}
      {!['idle', 'loading', 'done'].includes(state) && <p className="m-0 text-sm text-warn">搜索出错：{state}</p>}
    </section>
  );
}
