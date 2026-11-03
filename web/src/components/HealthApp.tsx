// 片库体检: problems computed from movies.json + /api/library (unmatched entries, NAS space). Lists and links only.
import { useEffect, useMemo, useState } from 'react';
import { getLibrary, getMovies, type Library, type Movie } from '../lib/api';
import { fmtBytes, fmtTime, isKids, tech } from '../lib/util';
import { Pending } from './cards';
import { HealthSkel } from './Skeleton';

type Item = { kind: string; cat: string; tone: 'red' | 'amber' | 'blue'; text: string; href?: string; action?: string; movie?: Movie };
const TONE = { red: 'bg-warn-bg text-warn', amber: 'bg-[color-mix(in_srgb,var(--color-seed)_22%,transparent)] text-seed-ink', blue: 'bg-info-bg text-info' };
const FILTERS = ['全部', '字幕', '画质', '重复', '元数据', '未匹配'];
const HOST = typeof location !== 'undefined' ? location.hostname : 'localhost';

export default function HealthApp() {
  const [movies, setMovies] = useState<Movie[] | null>(null);
  const [lib, setLib] = useState<Library | null>(null);
  const [error, setError] = useState<string>();
  const [filter, setFilter] = useState('全部');
  useEffect(() => { Promise.all([getMovies(), getLibrary()]).then(([m, l]) => { setMovies(m); setLib(l); }).catch(e => setError(String(e.message || e))); }, []);

  const data = useMemo(() => {
    if (!movies || !lib) return null;
    const noSubs = movies.filter(m => !m.subLangs.length);
    const noZh = movies.filter(m => m.subLangs.length && !m.subLangs.some(l => l.includes('中文')));
    const low = movies.filter(m => { const h = tech(m).height; return h > 0 && h < 1080; });
    const unknownRes = movies.filter(m => m.videos.length && !tech(m).height);
    const dupes = movies.filter(m => m.videos.length > 1 || m.nasPaths.length > 1);
    const noVideo = movies.filter(m => !m.videos.length);
    const noPoster = movies.filter(m => !m.poster);
    const noRating = movies.filter(m => !m.imdbRating);
    const radarr = `http://${HOST}:7878`, bazarr = `http://${HOST}:6767`;
    const items: Item[] = [
      ...noSubs.map(m => ({ kind: '没字幕', cat: '字幕', tone: 'red' as const, text: `${m.title}（${m.year ?? '?'}）：没有外挂字幕`, href: bazarr, action: '去 Bazarr 搜', movie: m })),
      ...noZh.map(m => ({ kind: '无中字', cat: '字幕', tone: 'amber' as const, text: `${m.title}：外挂字幕只有 ${m.subLangs.join('、')}`, href: bazarr, action: '去 Bazarr 补中文', movie: m })),
      ...low.map(m => ({ kind: '画质', cat: '画质', tone: 'amber' as const, text: `${m.title}（${m.year ?? '?'}）：只有 ${tech(m).height}p`, href: radarr, action: '在 Radarr 搜索升级', movie: m })),
      ...unknownRes.map(m => ({ kind: '画质?', cat: '画质', tone: 'blue' as const, text: `${m.title}：文件名里看不出分辨率（${m.videos[0].path.split('/').pop()}）`, movie: m })),
      ...dupes.map(m => ({ kind: '重复', cat: '重复', tone: 'amber' as const, text: `${m.title}：${m.videos.length} 个视频文件${m.nasPaths.length > 1 ? `，分布在 ${m.nasPaths.length} 个文件夹` : ''}`, href: encodeURI(m.nasPaths[0]), action: '在 Finder 中对比', movie: m })),
      ...noVideo.map(m => ({ kind: '没视频', cat: '重复', tone: 'red' as const, text: `${m.title}：文件夹里没有视频文件`, href: encodeURI(m.nasPaths[0]), action: '在 Finder 中打开', movie: m })),
      ...noPoster.map(m => ({ kind: '缺海报', cat: '元数据', tone: 'blue' as const, text: `${m.title}：TMDB 没有海报`, movie: m })),
      ...noRating.map(m => ({ kind: '缺评分', cat: '元数据', tone: 'blue' as const, text: `${m.title}：没有 IMDb 评分`, movie: m })),
      ...lib.unmatchedMovies.map(u => ({ kind: '未匹配', cat: '未匹配', tone: 'blue' as const, text: `${u.name} — ${u.reason}`, href: radarr, action: '去 Radarr 手动匹配' })),
      ...lib.unmatchedBooks.map(u => ({ kind: '书未匹配', cat: '未匹配', tone: 'blue' as const, text: `${u.name} → ${u.reason}` })),
    ];
    const used = (lib2: string) => movies.filter(m => m.libraries.includes(lib2)).reduce((s, m) => s + m.videos.reduce((t, v) => t + (v.dir?.includes(lib2 === 'kids' ? '/video/' : '/store/') ? v.size : 0), 0), 0);
    return { items, tiles: [
      { n: noSubs.length, label: '没有外挂字幕', level: '需处理', color: 'bg-warn' },
      { n: low.length, label: '低于 1080p', level: '注意', color: 'bg-accent' },
      { n: dupes.length, label: '有多个版本', level: '注意', color: 'bg-accent' },
      { n: lib.unmatchedMovies.length + lib.unmatchedBooks.length, label: '未匹配（电影 + 书）', level: '待确认', color: 'bg-info' },
    ], moviesBytes: { store: used('main'), video: used('kids') } };
  }, [movies, lib]);

  if (!data || !lib) return <HealthSkel error={error} />;
  const shown = data.items.filter(i => filter === '全部' || i.cat === filter);
  return (
    <main className="mx-auto flex max-w-[1440px] flex-col gap-6 px-4 pb-10 pt-5 md:px-8 md:pt-6">
      <p className="m-0 text-sm text-muted">片库体检 · 数据更新于 {lib.moviesUpdated ? fmtTime(lib.moviesUpdated) : '—'}（随 rebuild）</p>
      <div className="grid grid-cols-2 gap-3.5 md:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">
        {data.tiles.map(t => (
          <div key={t.label} className="card-panel flex flex-col gap-1 px-4 py-3.5">
            <div className="flex items-center gap-2"><span className={`h-2.5 w-2.5 rounded-full ${t.color}`} /><span className="font-mono text-xs text-muted">{t.level}</span></div>
            <span className="font-mono text-[30px] tabular-nums">{t.n}</span>
            <span className="text-[13px] text-muted">{t.label}</span>
          </div>
        ))}
        <div className="card-panel flex flex-col gap-1 border-dashed px-4 py-3.5">
          <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-ok" /><span className="font-mono text-xs text-muted">进行中</span></div>
          <span className="font-mono text-[30px] text-muted">—</span>
          <span className="text-[13px] text-muted">Bazarr 排队 <Pending /></span>
        </div>
      </div>

      <section className="flex flex-col gap-3">
        <h2 className="m-0 text-lg">NAS 空间</h2>
        {lib.volumes.map(v => {
          const movieBytes = v.name === 'store' ? data.moviesBytes.store : data.moviesBytes.video;
          if (!v.mounted || !v.total) return <div key={v.name} className="text-[13px] text-muted">{v.name}（{v.path}）没有挂载</div>;
          const usedAll = v.total - (v.free || 0), other = Math.max(0, usedAll - movieBytes);
          const pct = (x: number) => `${Math.min(100, (x / v.total!) * 100).toFixed(1)}%`;
          return (
            <div key={v.name} className="flex flex-col gap-1.5">
              <div className="flex flex-wrap justify-between gap-3 text-[13px] text-muted">
                <span>{v.name}{v.name === 'video' ? '（少儿）' : ''} · 共 {fmtBytes(v.total)}</span>
                <span>电影 {fmtBytes(movieBytes)} · 其他 {fmtBytes(other)} · 空闲 {fmtBytes(v.free || 0)}</span>
              </div>
              <div className="flex h-3 overflow-hidden rounded-md bg-raise" role="img" aria-label={`${v.name} 已用 ${pct(usedAll)}`}>
                <div className={v.name === 'video' ? 'bg-kids' : 'bg-accent'} style={{ width: pct(movieBytes) }} /><div className="bg-bar" style={{ width: pct(other) }} />
              </div>
            </div>
          );
        })}
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="m-0 text-lg">待处理</h2>
          <div className="flex flex-wrap gap-1.5">{FILTERS.map(f => <button key={f} type="button" className="chip min-h-10 px-3.5 text-sm" aria-pressed={filter === f} onClick={() => setFilter(f)}>{f} {f === '全部' ? data.items.length : data.items.filter(i => i.cat === f).length}</button>)}</div>
        </div>
        <div className="flex flex-col gap-2">
          {shown.slice(0, 200).map((i, k) => (
            <div key={k} className="card-panel flex flex-wrap items-center gap-3.5 rounded-[10px] px-3.5 py-2.5">
              <span className={`w-[76px] flex-none rounded-[5px] px-1.5 py-0.5 text-center font-mono text-xs ${TONE[i.tone]}`}>{i.kind}</span>
              <span className="min-w-0 flex-[1_1_320px] text-sm">{i.movie ? <a className="text-text hover:text-accent" href={`/movie?id=${i.movie.id}`}>{i.text}</a> : i.text}{i.movie && isKids(i.movie) ? <span className="ml-2 text-xs text-kids">少儿</span> : null}</span>
              {i.href && <a className="btn whitespace-nowrap text-[13px]" href={i.href} target={i.href.startsWith('http') ? '_blank' : undefined} rel="noopener">{i.action}</a>}
            </div>
          ))}
          {shown.length > 200 && <p className="m-0 text-sm text-muted">还有 {shown.length - 200} 条没显示。</p>}
          {!shown.length && <p className="m-0 text-sm text-muted">这一类没有问题。</p>}
        </div>
        <p className="m-0 text-[13px] text-muted">这里只列问题和跳转，不会在页面上删除或改名文件。</p>
      </section>
    </main>
  );
}
