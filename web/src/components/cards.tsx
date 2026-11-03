// Poster / book cards and section chrome shared by the pages.
import type { ReactNode } from 'react';
import type { Book, Movie } from '../lib/api';
import { coverURL, posterURL, ratingLabel, readPct } from '../lib/util';

export function Poster({ m, className = '', big = false }: { m: Movie; className?: string; big?: boolean }) {
  const src = posterURL(m);
  const r = ratingLabel(m);
  return (
    <div className={`relative aspect-[2/3] overflow-hidden rounded-xl bg-edge ring-1 ring-black/5 dark:ring-white/10 ${className}`}>
      {src ? <img src={src} alt="" loading="lazy" className="h-full w-full object-cover" />
        : <div className="flex h-full flex-col justify-between p-2.5 text-[15px] font-semibold leading-tight text-soft"><Sprout className="h-6 w-6 opacity-70" />{m.title}</div>}
      {r && !big && <span className="absolute right-1.5 top-1.5 rounded-md bg-black/55 px-1.5 py-0.5 font-mono text-[11px] text-[#E2C27E] backdrop-blur-sm">{r.value}</span>}
    </div>
  );
}

export function MovieCard({ m, meta }: { m: Movie; meta?: string }) {
  return (
    <a href={`/movie?id=${m.id}`} className="group flex min-w-0 flex-col gap-1.5 text-text no-underline hover:text-text">
      <Poster m={m} className="transition duration-200 group-hover:-translate-y-1 group-hover:shadow-[0_8px_20px_-8px_rgba(31,42,34,.35)] group-hover:ring-accent/60" />
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
    <div className={`relative aspect-[3/4.3] overflow-hidden rounded-[4px_10px_10px_4px] bg-edge ring-1 ring-black/5 dark:ring-white/10 ${className}`}>
      {src ? <img src={src} alt="" loading="lazy" className="h-full w-full object-cover" />
        : <div className="flex h-full flex-col justify-end p-2 text-sm font-semibold leading-tight text-soft">{b.title}<span className="mt-1 font-mono text-[10px] font-normal text-muted">{b.authors[0]}</span></div>}
    </div>
  );
}

export function BookCard({ b, href }: { b: Book; href?: string }) {
  const p = readPct(b);
  return (
    <a href={href ?? `/books?b=${encodeURIComponent(b.id)}`} className="group flex min-w-0 flex-col gap-1.5 text-text no-underline hover:text-text">
      <BookCover b={b} className="transition duration-200 group-hover:-translate-y-1 group-hover:shadow-[0_8px_20px_-8px_rgba(31,42,34,.35)] group-hover:ring-accent/60" />
      {p != null && <div className="h-1 overflow-hidden rounded-sm bg-edge"><div className="h-1 bg-accent" style={{ width: `${p}%` }} /></div>}
      <span className="text-[13px] leading-snug"><span className="line-clamp-2">{b.title}</span><span className="line-clamp-1 text-[11px] text-muted">{b.authors[0]}</span></span>
    </a>
  );
}

export function Section({ title, note, action, children }: { title: string; note?: ReactNode; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="m-0 text-lg font-semibold tracking-tight">{title}</h2>
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

/** The Mori sprout mark for empty and loading states (colours come from the global .mori-mark styles). */
export function Sprout({ className = '' }: { className?: string }) {
  return (
    <svg className={`mori-mark ${className}`} viewBox="0 0 64 64" fill="none" aria-hidden="true">
      <path d="M30.323 34.37V55.51" className="stem" strokeWidth="3.43" strokeLinecap="round" />
      <path d="M30.323 32.655A17.632 17.632 0 0 1 11.303 18.836A17.632 17.632 0 0 1 30.323 32.655Z" className="leaf-s" />
      <path d="M30.323 34.775A28.53 28.53 0 0 1 52.683 4A28.53 28.53 0 0 1 30.323 34.775Z" className="leaf-l" />
      <circle cx="30.323" cy="55.51" r="4.49" className="seed" />
    </svg>
  );
}

/** Empty state: a small sprout and a quiet line of text. */
export function Empty({ children }: { children: ReactNode }) {
  return <div className="flex items-center gap-3 rounded-2xl border border-dashed border-edge-2 bg-edge/40 px-4 py-4 text-sm text-muted"><Sprout className="h-7 w-7 shrink-0" />{children}</div>;
}

export const Pending = ({ children = '待接入' }: { children?: ReactNode }) => <span className="pending">{children}</span>;
