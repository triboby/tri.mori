// Skeleton placeholders sized like the real layouts; shown while /api data loads (and in the prerendered HTML).
import type { ReactNode } from 'react';
import { BOOK_STRIP, STRIP } from './cards';

const range = (n: number) => Array.from({ length: n }, (_, i) => i);
export const MOVIE_GRID = 'grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(136px,1fr))]';
export const BOOK_GRID = 'grid grid-cols-[repeat(auto-fill,minmax(100px,1fr))] gap-4 md:grid-cols-[repeat(auto-fill,minmax(116px,1fr))]';

export const Bone = ({ className = '' }: { className?: string }) => <div aria-hidden="true" className={`skel ${className}`} />;

export function Lines({ n = 3, className = '' }: { n?: number; className?: string }) {
  return <div className={`flex flex-col gap-2 ${className}`}>{range(n).map(i => <Bone key={i} className={`h-3.5 ${i === n - 1 ? 'w-3/5' : 'w-full'}`} />)}</div>;
}

export function PosterSkel() {
  return <div className="flex min-w-0 flex-col gap-1.5"><Bone className="aspect-[2/3] rounded-lg" /><Bone className="h-3.5 w-4/5" /><Bone className="h-3 w-1/2" /></div>;
}

export function BookSkel() {
  return <div className="flex min-w-0 flex-col gap-1.5"><Bone className="aspect-[3/4.3] rounded-[3px_8px_8px_3px]" /><Bone className="h-3.5 w-4/5" /><Bone className="h-3 w-1/2" /></div>;
}

export const PosterStrip = ({ n = 10 }: { n?: number }) => <div className={`${STRIP} overflow-hidden`}>{range(n).map(i => <PosterSkel key={i} />)}</div>;
export const BookStrip = ({ n = 12 }: { n?: number }) => <div className={`${BOOK_STRIP} overflow-hidden`}>{range(n).map(i => <BookSkel key={i} />)}</div>;
export const PosterGrid = ({ n = 18, className = MOVIE_GRID }: { n?: number; className?: string }) => <div className={className}>{range(n).map(i => <PosterSkel key={i} />)}</div>;
export const BookGrid = ({ n = 18 }: { n?: number }) => <div className={BOOK_GRID}>{range(n).map(i => <BookSkel key={i} />)}</div>;

export function SectionSkel({ children }: { children: ReactNode }) {
  return <section className="flex flex-col gap-3"><div className="flex items-baseline gap-3"><Bone className="h-5 w-28" /><Bone className="h-3.5 w-44" /></div>{children}</section>;
}

export const Chips = ({ n = 5, className = '' }: { n?: number; className?: string }) => <div className={`flex flex-wrap gap-2 ${className}`}>{range(n).map(i => <Bone key={i} className="h-9 w-16 rounded-lg" />)}</div>;

/** Wraps a skeleton so assistive tech hears one "loading" status instead of empty boxes; shows the error text when loading failed. */
export function Skeleton({ error, label = '加载中', children }: { error?: string; label?: string; children: ReactNode }) {
  if (error) return <p className="text-warn">加载出错：{error}</p>;
  return <div role="status" aria-busy="true" aria-label={label} className="contents">{children}</div>;
}

export function PageSkel({ error, children, className = 'gap-9' }: { error?: string; children: ReactNode; className?: string }) {
  return <main className={`mx-auto flex max-w-[1440px] flex-col px-4 pb-10 pt-5 md:px-8 md:pt-6 ${className}`}><Skeleton error={error}>{children}</Skeleton></main>;
}

export const HomeSkel = ({ error }: { error?: string }) => (
  <PageSkel error={error}>
    <Chips n={4} className="-mb-3" />
    <SectionSkel><div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-3.5">{range(2).map(i => <div key={i} className="card-panel grid grid-cols-[64px_minmax(0,1fr)] items-center gap-3.5 p-3"><Bone className="aspect-[3/4.3]" /><Lines n={3} /></div>)}</div></SectionSkel>
    <SectionSkel><PosterStrip /></SectionSkel>
    <SectionSkel><PosterStrip /></SectionSkel>
    <SectionSkel><BookStrip /></SectionSkel>
  </PageSkel>
);

export const PickSkel = ({ error }: { error?: string }) => (
  <PageSkel error={error} className="gap-4">
    <div className="flex items-center gap-3"><Bone className="h-8 w-20" /><Bone className="h-4 w-24" /><Bone className="ml-auto h-9 w-28 rounded-lg" /></div>
    <div className="hidden flex-col gap-2.5 md:flex">{[2, 3, 14, 9, 4, 4, 3, 2].map((n, i) => <div key={i} className="flex gap-2"><Bone className="h-9 w-9 shrink-0 opacity-50" /><Chips n={n} /></div>)}</div>
    <PosterGrid n={24} className="grid grid-cols-[repeat(auto-fill,minmax(104px,1fr))] gap-x-3 gap-y-4 md:grid-cols-[repeat(auto-fill,minmax(140px,1fr))] md:gap-x-4" />
  </PageSkel>
);

export const DetailSkel = ({ error }: { error?: string }) => (
  <main>
    <Skeleton error={error}>
      <section className="bg-[radial-gradient(90%_140%_at_75%_0%,#1d3348_0%,#0b0d12_70%)]">
        <div className="mx-auto flex max-w-[1440px] flex-wrap items-end gap-6 px-4 py-6 md:gap-8 md:px-8 md:py-8">
          <Bone className="aspect-[2/3] w-36 flex-none rounded-lg md:w-[220px]" />
          <div className="flex min-w-0 flex-[1_1_420px] flex-col gap-3"><Bone className="h-9 w-2/3 md:h-11" /><Bone className="h-4 w-1/2" /><Lines n={4} className="max-w-[62ch]" /><Chips n={5} /></div>
        </div>
      </section>
      <div className="mx-auto flex max-w-[1440px] flex-wrap gap-8 px-4 pb-10 pt-2 md:px-8">
        <div className="flex min-w-0 flex-[999_1_640px] flex-col gap-7"><div className="card-panel p-4"><Lines n={3} /></div><SectionSkel><PosterStrip n={8} /></SectionSkel></div>
        <div className="card-panel flex min-w-0 flex-[1_1_320px] flex-col gap-3 p-4"><Bone className="h-4 w-24" /><Bone className="h-10 w-full rounded-lg" /><Bone className="h-10 w-full rounded-lg" /><Lines n={2} /></div>
      </div>
    </Skeleton>
  </main>
);

export const BooksSkel = ({ error }: { error?: string }) => (
  <PageSkel error={error} className="gap-8">
    <Chips n={6} />
    <SectionSkel><BookStrip /></SectionSkel>
    <SectionSkel><BookStrip /></SectionSkel>
    <SectionSkel><BookStrip /></SectionSkel>
  </PageSkel>
);

export const HealthSkel = ({ error }: { error?: string }) => (
  <PageSkel error={error} className="gap-6">
    <Bone className="h-4 w-64" />
    <div className="grid grid-cols-2 gap-3.5 md:grid-cols-[repeat(auto-fill,minmax(220px,1fr))]">{range(6).map(i => <div key={i} className="card-panel flex flex-col gap-2 px-4 py-3.5"><Bone className="h-3 w-16" /><Bone className="h-8 w-14" /><Bone className="h-3 w-24" /></div>)}</div>
    <SectionSkel>{range(2).map(i => <div key={i} className="flex flex-col gap-1.5"><Bone className="h-3.5 w-full max-w-md" /><Bone className="h-3 w-full" /></div>)}</SectionSkel>
    <SectionSkel><div className="flex flex-col gap-2">{range(6).map(i => <div key={i} className="card-panel flex items-center gap-3.5 rounded-[10px] px-3.5 py-2.5"><Bone className="h-5 w-[76px]" /><Bone className="h-4 flex-1" /><Bone className="h-8 w-20 rounded-lg" /></div>)}</div></SectionSkel>
  </PageSkel>
);

export const FillSkel = ({ error }: { error?: string }) => (
  <PageSkel error={error} className="gap-8">
    {range(2).map(i => <SectionSkel key={i}><Bone className="h-2.5 w-full max-w-xl rounded-full" /><div className="flex flex-col gap-2">{range(6).map(j => <div key={j} className="card-panel flex items-center gap-3 rounded-[10px] p-2"><Bone className="h-4 w-8" /><Bone className="aspect-[2/3] w-10 rounded" /><Bone className="h-4 flex-1" /><Bone className="h-6 w-20" /></div>)}</div></SectionSkel>)}
  </PageSkel>
);
