// Data shapes served by the Go server (/api/*) and small fetch helpers.

export type Video = { path: string; size: number; dir?: string };

export type Movie = {
  id: number; title: string; enTitle?: string; originalTitle?: string; year: number | null; runtime: number | null;
  genres: string[]; countries: string[]; region: string; rating: number | null; overview: string;
  directors: string[]; cast: string[]; subLangs: string[]; subs: string[]; nasPaths: string[]; libraries: string[];
  added: number; poster: string | null; videos: Video[]; imdbId: string | null; imdbRating: number | null; imdbVotes: number | null;
};

export type BookFile = { id: string; path: string; format: string; size: number; textLayer?: string };

export type Book = {
  id: string; source: string; doubanId?: string; title: string; subtitle?: string; origTitle?: string;
  authors: string[]; translators: string[]; publisher?: string; year?: string; pages?: number; series?: string; isbn?: string | null;
  rating?: number; votes?: number; intro?: string; tags: string[]; category: string; subcategory?: string; clc?: string;
  folder: string; cover: string | null; added: number; files: BookFile[];
  progress?: { percentage: number; device: string; timestamp: number };
};

export type Hit = { kind: 'movies' | 'books'; id: number | string; score: number };
export type SearchResult = { q: string; ms: number; hits: Hit[]; error?: string };

export type Volume = { name: string; path: string; mounted: boolean; total?: number; free?: number };
export type Library = {
  moviesUpdated: number | null; booksUpdated: number | null;
  unmatchedMovies: { name: string; reason: string }[]; unmatchedBooks: { name: string; reason: string }[];
  volumes: Volume[];
};

export type Pending = { pending: boolean; source: string };

export type ChartItem = {
  rank: number; title: string; original?: string; year?: number; rating?: number; doubanId?: string; imdbId?: string; tmdbId?: number;
  poster?: string; movieId?: number; matchedBy?: 'imdb' | 'tmdb' | 'title'; suspect?: number; votes?: number; onNas?: string;
};
export type Chart = { id: string; name: string; source: string; note?: string; fetched: number; total: number; have: number; items: ChartItem[] };
/** /api/lists: chart coverage from data/lists.json, or a pending placeholder before `shelf lists` has run. */
export type Lists = { updated?: number; charts?: Chart[]; pending?: boolean; source?: string };

const memo = new Map<string, Promise<unknown>>();

async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url);
  const body = await r.json();
  if (!r.ok && !body?.pending) throw new Error(body?.error || `HTTP ${r.status}`);
  return body as T;
}

const once = <T,>(url: string) => () => {
  if (!memo.has(url)) memo.set(url, getJSON<T>(url));
  return memo.get(url) as Promise<T>;
};

export const getMovies = once<Movie[]>('/api/movies');
export const getBooks = once<Book[]>('/api/books');
export const getLibrary = once<Library>('/api/library');
export const getWatch = once<Pending & { items: unknown[] }>('/api/watch');
export const getLists = once<Lists>('/api/lists');

export const search = (q: string, kind: 'movies' | 'books' | 'all' = 'all') =>
  getJSON<SearchResult>(`/api/search?kind=${kind}&q=${encodeURIComponent(q)}`);
