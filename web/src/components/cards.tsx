// Poster / book cards and section chrome shared by the pages.
import type { ReactNode } from 'react';
import type { Book, Movie } from '../lib/api';
import { coverURL, posterURL, ratingLabel, readPct } from '../lib/util';

export function Poster({ m, className = '', big = false }: { m: Movie; className?: string; big?: boolean }) {
  const src = posterURL(m);
  const r = ratingLabel(m);
  return (
    <div className={`relative aspect-[2/3] overflow-hidden rounded-lg bg-raise shadow-[inset_0_0_0_1px_rgba(255,255,255,.08)] ${className}`}>
      {src ? <img src={src} alt="" loading="lazy" className="h-full w-full object-cover" />
        : <div className="flex h-full items-end bg-gradient-to-b from-edge to-ink p-2.5 font-serif text-[15px] font-black leading-tight text-white">{m.title}</div>}
      {r && !big && <span className="absolute right-1.5 top-1.5 rounded bg-black/70 px-1.5 py-0.5 font-mono text-[11px] text-accent">{r.value}</span>}
    </div>
  );
}

export function MovieCard({ m, meta }: { m: Movie; meta?: string }) {
  return (
    <a href={`/movie?id=${m.id}`} className="group flex min-w-0 flex-col gap-1.5 text-text no-underline hover:text-text">
      <Poster m={m} className="transition group-hover:shadow-[inset_0_0_0_2px_var(--color-accent)]" />
      <span className="text-[13px] leading-snug">
        <span className="line-clamp-2">{m.title}</span>
        <span className="font-mono text-[11px] text-muted">{meta ?? [m.year, m.runtime && `${m.runtime} 分钟`].filter(Boolean).join(' · ')}</span>
      </span>
    </a>
  );
}

export function BookCover({ b, className = '' }: { b: Book; className?: string }) {
  const src = coverURL(b);
  return (
    <div className={`relative aspect-[3/4.3] overflow-hidden rounded-[3px_8px_8px_3px] bg-raise shadow-[inset_0_0_0_1px_rgba(255,255,255,.08)] ${className}`}>
      {src ? <img src={src} alt="" loading="lazy" className="h-full w-full object-cover" />
        : <div className="flex h-full flex-col justify-end bg-gradient-to-b from-edge to-ink p-2 font-serif text-sm font-black leading-tight text-white">{b.title}<span className="mt-1 font-mono text-[10px] font-normal text-white/75">{b.authors[0]}</span></div>}
    </div>
  );
}

export function BookCard({ b, href }: { b: Book; href?: string }) {
  const p = readPct(b);
  return (
    <a href={href ?? `/books?b=${encodeURIComponent(b.id)}`} className="group flex min-w-0 flex-col gap-1.5 text-text no-underline hover:text-text">
      <BookCover b={b} className="transition group-hover:shadow-[inset_0_0_0_2px_var(--color-accent)]" />
      {p != null && <div className="h-1 overflow-hidden rounded-sm bg-edge"><div className="h-1 bg-accent" style={{ width: `${p}%` }} /></div>}
      <span className="text-[13px] leading-snug"><span className="line-clamp-2">{b.title}</span><span className="line-clamp-1 text-[11px] text-muted">{b.authors[0]}</span></span>
    </a>
  );
}

export function Section({ title, note, action, children }: { title: string; note?: ReactNode; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="m-0 text-lg font-bold">{title}</h2>
        {note && <span className="text-[13px] text-muted">{note}</span>}
        {action && <span className="ml-auto text-[13px]">{action}</span>}
      </div>
      {children}
    </section>
  );
}

/** Horizontal poster rows: movie posters and (narrower) book covers. */
export const STRIP = 'strip auto-cols-[128px] md:auto-cols-[136px]';
export const BOOK_STRIP = 'strip auto-cols-[104px] md:auto-cols-[112px]';

export const Pending = ({ children = '待接入' }: { children?: ReactNode }) => <span className="pending">{children}</span>;
