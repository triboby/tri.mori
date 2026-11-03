// 书架 (/books) and 全部 (/all): reading progress, categories, top rated, search, and a detail drawer (?b=<id>).
import { useEffect, useMemo, useState } from 'react';
import { getBooks, getMovies, search, type Book, type Movie, type SearchResult } from '../lib/api';
import { BOOK_SMB, bookFileHref, copyText, fmtDate, fmtSize, READEST_FMT, readPct, readestFile, readestHref, save, store } from '../lib/util';
import { BOOK_STRIP, BookCard, BookCover, MovieCard, Section } from './cards';
import { BookGrid, BooksSkel, SectionSkel, Skeleton } from './Skeleton';

type Tab = '在读' | '分类' | '细类' | '高分' | '最近';

export default function BooksApp() {
  const all = typeof location !== 'undefined' && location.pathname === '/all';
  const params = typeof location !== 'undefined' ? new URLSearchParams(location.search) : new URLSearchParams();
  const q = params.get('q')?.trim() || '';
  const [books, setBooks] = useState<Book[] | null>(null);
  const [movies, setMovies] = useState<Movie[]>([]);
  const [error, setError] = useState<string>();
  const [tab, setTab] = useState<Tab>(() => store('bookTab', '分类'));
  const [open, setOpen] = useState<string | null>(params.get('b'));
  useEffect(() => {
    Promise.all([getBooks(), all || q ? getMovies() : Promise.resolve([])]).then(([b, m]) => { setBooks(b); setMovies(m); }).catch(e => setError(String(e.message || e)));
    const pop = () => setOpen(new URLSearchParams(location.search).get('b'));
    addEventListener('popstate', pop);
    return () => removeEventListener('popstate', pop);
  }, []);
  useEffect(() => save('bookTab', tab), [tab]);
  const byId = useMemo(() => new Map((books || []).map(b => [b.id, b])), [books]);

  const show = (id: string | null) => {
    const p = new URLSearchParams(location.search);
    id ? p.set('b', id) : p.delete('b');
    history.pushState(null, '', location.pathname + (p.size ? '?' + p : ''));
    setOpen(id);
  };
  const onClick = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest('a[href^="/books?b="]') as HTMLAnchorElement | null;
    if (a && !e.metaKey && !e.ctrlKey) { e.preventDefault(); show(new URL(a.href).searchParams.get('b')); }
  };

  if (!books) return <BooksSkel error={error} />;
  const reading = books.filter(b => b.progress && b.progress.percentage < 0.99).sort((a, b) => b.progress!.timestamp - a.progress!.timestamp);
  const cats = new Map<string, Book[]>();
  for (const b of books) (cats.get(b.category) || cats.set(b.category, []).get(b.category)!).push(b);
  const catRows = [...cats].sort((a, b) => b[1].length - a[1].length);
  const rows: [string, Book[]][] = tab === '在读' ? [['在读', reading]]
    : tab === '分类' ? catRows
      : tab === '细类' ? catRows.flatMap(([c, l]) => { const s = new Map<string, Book[]>(); for (const b of l) (s.get(b.subcategory || '其他') || s.set(b.subcategory || '其他', []).get(b.subcategory || '其他')!).push(b); return [...s].sort((a, b) => b[1].length - a[1].length).map(([k, v]) => [`${c} · ${k}`, v] as [string, Book[]]); })
        : tab === '高分' ? [['豆瓣 8.5 分以上', books.filter(b => (b.rating || 0) >= 8.5).sort((a, b) => (b.rating || 0) - (a.rating || 0))]]
          : [['最近加入', [...books].sort((a, b) => b.added - a.added)]];

  return (
    <main className="mx-auto flex max-w-[1440px] flex-col gap-8 px-4 pb-10 pt-5 md:px-8 md:pt-6" onClick={onClick}>
      {q ? <BookSearch q={q} all={all} books={byId} movies={movies} /> : <>
        <div className="flex flex-wrap items-center gap-2">
          <span className="mr-1 font-mono text-xs text-muted">{books.length} 本 · {all ? '书和电影' : '书'}</span>
          {(['在读', '分类', '细类', '高分', '最近'] as Tab[]).map(t => <button key={t} type="button" className="chip min-h-9 px-3 text-sm" aria-pressed={tab === t} onClick={() => setTab(t)}>{t}{t === '在读' && reading.length ? ` ${reading.length}` : ''}</button>)}
          <a href={all ? '/books' : '/all'} className="ml-auto text-sm">{all ? '只看书' : '书和电影一起搜'}</a>
        </div>
        {rows.filter(([, l]) => l.length || tab === '在读').map(([name, list]) => (
          <Section key={name} title={name} note={`${list.length} 本`}>
            {list.length ? <div className={rows.length === 1 ? 'grid grid-cols-[repeat(auto-fill,minmax(100px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(116px,1fr))]' : BOOK_STRIP}>
              {list.map(b => <BookCard key={b.id} b={b} />)}</div>
              : <p className="m-0 text-sm text-muted">没有在读的书。KOReader 设好进度同步（自定义服务器 <code className="font-mono text-xs">http://192.168.50.58:8686/kosync</code>）后会显示在这里。</p>}
          </Section>
        ))}
        <section className="card-panel p-4 text-sm text-muted">
          <h2 className="m-0 mb-2 font-mono text-xs font-medium tracking-[.06em]">在 KOReader 里</h2>
          OPDS 书目 <code className="font-mono text-text">http://192.168.50.58:8686/opds</code> · 进度同步 <code className="font-mono text-text">http://192.168.50.58:8686/kosync</code>（文档匹配保持默认的「二进制」）。
        </section>
      </>}
      {open && byId.get(open) && <BookDrawer b={byId.get(open)!} onClose={() => show(null)} />}
    </main>
  );
}

function BookSearch({ q, all, books, movies }: { q: string; all: boolean; books: Map<string, Book>; movies: Movie[] }) {
  const [res, setRes] = useState<SearchResult | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { search(q, all ? 'all' : 'books').then(r => (r.error ? setError(r.error) : setRes(r))).catch(e => setError(String(e.message || e))); }, [q]);
  const mById = useMemo(() => new Map(movies.map(m => [m.id, m])), [movies]);
  if (!res) return <Skeleton error={error} label="搜索中"><SectionSkel><BookGrid n={12} /></SectionSkel></Skeleton>;
  const hits = res.hits.map(h => (h.kind === 'movies' ? { m: mById.get(h.id as number) } : { b: books.get(h.id as string) })).filter(x => x.m || x.b);
  return (
    <Section title={`「${q}」`} note={`${hits.length} 个结果 · ${(res.ms / 1000).toFixed(2)} 秒`}>
      {hits.length ? <div className="grid grid-cols-[repeat(auto-fill,minmax(100px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(116px,1fr))]">
        {hits.map(x => (x.m ? <MovieCard key={'m' + x.m.id} m={x.m} /> : <BookCard key={'b' + x.b!.id} b={x.b!} />))}</div>
        : <p className="text-muted">没有找到。</p>}
    </Section>
  );
}

function BookDrawer({ b, onClose }: { b: Book; onClose: () => void }) {
  const [copied, setCopied] = useState('');
  const f = readestFile(b);
  const p = readPct(b);
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    addEventListener('keydown', key);
    return () => removeEventListener('keydown', key);
  }, []);
  const row = (k: string, v?: string | number | null) => (v ? <><dt className="text-muted">{k}</dt><dd className="m-0">{v}</dd></> : null);
  return (
    <>
      <div className="fixed inset-0 z-40 bg-[#1f2a22]/40 backdrop-blur-[2px]" onClick={onClose} />
      <aside role="dialog" aria-modal="true" aria-label={b.title} className="fixed inset-y-0 right-0 z-50 flex w-full max-w-[520px] flex-col gap-4 overflow-y-auto border-l border-line bg-ink p-5 pb-[calc(20px+env(safe-area-inset-bottom))]">
        <button type="button" className="btn self-end" onClick={onClose} aria-label="关闭">关闭</button>
        <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-4">
          <BookCover b={b} />
          <div className="flex min-w-0 flex-col gap-1">
            <h2 className="m-0 text-[22px] font-semibold leading-tight tracking-tight">{b.title}</h2>
            {[b.subtitle, b.origTitle].filter(Boolean).map(x => <div key={x} className="text-sm text-muted">{x}</div>)}
            <div className="text-sm">{b.authors.join('、')}{b.translators.length ? <span className="text-muted"> · 译 {b.translators.join('、')}</span> : null}</div>
            {b.rating ? <a className="text-sm" href={b.doubanId ? `https://book.douban.com/subject/${b.doubanId}/` : undefined} target="_blank" rel="noopener">豆瓣 {b.rating.toFixed(1)}</a> : null}
            <div className="text-xs text-muted">{b.category}{b.subcategory ? ` / ${b.subcategory}` : ''}{b.clc ? ` · 中图 ${b.clc}` : ''}</div>
          </div>
        </div>
        {p != null && <div className="card-panel flex flex-col gap-1.5 p-3 text-sm"><div className="h-1 overflow-hidden rounded-sm bg-edge"><div className="h-1 bg-accent" style={{ width: `${p}%` }} /></div>读到 {p}% · {b.progress!.device} · {fmtDate(b.progress!.timestamp * 1000)}</div>}
        {f && <a className="btn-pri min-h-11 justify-center" href={readestHref(f)}>{p != null ? '在 Readest 中继续' : '在 Readest 中阅读'}</a>}
        <p className="m-0 whitespace-pre-line text-sm text-soft">{b.intro || '暂无简介'}</p>
        <dl className="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
          {row('出版社', b.publisher)}{row('出版年', b.year)}{row('页数', b.pages)}{row('丛书', b.series)}{row('ISBN', b.isbn)}
          {b.tags.length ? <><dt className="text-muted">标签</dt><dd className="m-0">{b.tags.join('、')}</dd></> : null}
        </dl>
        <section className="flex flex-col gap-2">
          <h3 className="m-0 font-mono text-xs font-medium tracking-[.06em] text-muted">文件</h3>
          {b.files.map(x => (
            <div key={x.id} className="card-panel flex flex-wrap items-center gap-2 p-2.5 text-sm">
              <span className="font-mono text-xs uppercase">{x.format}</span><span className="text-muted">{fmtSize(x.size)}</span>
              <span className="ml-auto flex flex-wrap gap-2">
                {READEST_FMT.includes(x.format) && <a href={readestHref(x)}>Readest</a>}
                <a href={bookFileHref(x)}>下载</a>
                <button type="button" className="cursor-pointer text-accent hover:text-accent-hi" onClick={async () => setCopied((await copyText(BOOK_SMB + x.path)) ? x.id : '')}>{copied === x.id ? '已复制' : '复制 NAS 路径'}</button>
              </span>
            </div>
          ))}
        </section>
      </aside>
    </>
  );
}
