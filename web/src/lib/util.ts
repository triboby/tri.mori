// Formatting and derived facts shared by the pages.
import type { Book, BookFile, Movie, Video } from './api';

export const score = (m: Movie) => m.imdbRating ?? m.rating ?? 0;
export const isKids = (m: Movie) => m.libraries?.includes('kids');
export const fmtSize = (b: number) => (b >= 1e9 ? (b / 1e9).toFixed(1) + ' GB' : b >= 1e6 ? Math.round(b / 1e6) + ' MB' : Math.max(1, Math.round(b / 1e3)) + ' KB');
export const fmtBytes = (b: number) => (b >= 1e12 ? (b / 1e12).toFixed(1) + ' TB' : Math.round(b / 1e9) + ' GB');
export const shortName = (p: string) => p.replace(/（.*）/, '');
export const posterURL = (m: Movie) => (m.poster ? `/posters/${m.poster}` : null);
export const coverURL = (b: Book) => (b.cover ? `/covers/${b.cover}` : null);

export function ratingLabel(m: Movie) {
  if (m.imdbRating) return { label: 'IMDb', value: m.imdbRating.toFixed(1), href: m.imdbId ? `https://www.imdb.com/title/${m.imdbId}/` : undefined };
  if (m.rating) return { label: 'TMDB', value: m.rating.toFixed(1), href: `https://www.themoviedb.org/movie/${m.id}` };
  return null;
}

const nasFileURL = (v: Video) => 'file://' + `${v.dir || '/Volumes/store/movie'}/${v.path}`.split('/').map(encodeURIComponent).join('/');
/** Opens the NAS file in IINA on this Mac (the share is mounted under /Volumes). */
export const iinaURL = (v: Video) => 'iina://weblink?url=' + encodeURIComponent(nasFileURL(v));
/** smb:// URL of a video for players on other devices (VidHub, Infuse); /Volumes/<share>/… maps to the NAS share. */
export const smbURL = (v: Video) => `smb://192.168.50.94/${(v.dir || '/Volumes/store/movie').replace(/^\/Volumes\//, '')}/${v.path}`;

const IOS = typeof navigator !== 'undefined' && (/iPhone|iPad/i.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1));
export const MOBILE = typeof navigator !== 'undefined' && (IOS || /Android/i.test(navigator.userAgent));
/** Readest opens a book by its KOReader partial-MD5 file id: universal link on phones, readest:// on desktop. */
export const readestHref = (f: BookFile) => (MOBILE ? `https://web.readest.com/o/book/${f.id}` : `readest://book/${f.id}`);
export const READEST_FMT = ['epub', 'azw3', 'mobi', 'pdf', 'fb2', 'cbz', 'txt'];
export const readestFile = (b: Book) => b.files.filter(f => READEST_FMT.includes(f.format)).sort((x, y) => READEST_FMT.indexOf(x.format) - READEST_FMT.indexOf(y.format))[0];
export const bookFileHref = (f: BookFile) => `/books/file/${f.id}/${encodeURIComponent(f.path.split('/').pop() || '')}`;
export const BOOK_SMB = 'smb://192.168.50.94/store/book/';
export const readPct = (b: Book) => (b.progress ? Math.round(b.progress.percentage * 100) : null);

/** Resolution, source, HDR, audio and codec parsed from the (Radarr-named) video file name. */
export function tech(m: Movie) {
  const v = m.videos[0];
  if (!v) return { tags: [] as string[], height: 0 };
  const n = v.path.split('/').pop() || '';
  const pick = (re: RegExp) => n.match(re)?.[0];
  const res = pick(/\b(2160|1080|720|576|480)p\b/i) || (/\b4k\b|uhd/i.test(n) ? '2160p' : undefined);
  const tags = [
    pick(/remux|blu-?ray|web-?dl|webrip|hdtv|dvdrip|bdrip/i), res, pick(/\b(hdr10\+?|dv|dovi|hdr)\b/i),
    pick(/(truehd|dts-hd ma|dts-x|dts|eac3|ddp|ac3|aac|flac|opus)(\s?\d\.\d)?/i), pick(/\b(x265|x264|hevc|avc|av1|h\.?26[45])\b/i),
    fmtSize(v.size), m.subLangs.some(l => l.includes('中文')) ? '中字' : undefined,
  ].filter(Boolean) as string[];
  return { tags, height: res ? parseInt(res) : 0 };
}

/** Deterministic shuffle seeded by a string (today's date), so picks change once a day. */
export function seededShuffle<T>(list: T[], seed: string): T[] {
  let h = 2166136261;
  for (const c of seed) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
  const rand = () => ((h = Math.imul(h ^ (h >>> 15), 2246822507) ^ Math.imul(h ^ (h >>> 13), 3266489909)) >>> 0) / 4294967296;
  const out = [...list];
  for (let i = out.length - 1; i > 0; i--) { const j = Math.floor(rand() * (i + 1)); [out[i], out[j]] = [out[j], out[i]]; }
  return out;
}

export const today = () => new Date().toLocaleDateString('sv');

export function store<T>(key: string, fallback: T): T {
  try { const v = localStorage.getItem(key); return v ? (JSON.parse(v) as T) : fallback; } catch { return fallback; }
}
export function save(key: string, v: unknown) {
  try { localStorage.setItem(key, JSON.stringify(v)); } catch {}
}

export const fmtDate = (ms: number) => { const d = new Date(ms); return `${d.getMonth() + 1} 月 ${d.getDate()} 日`; };
export const fmtTime = (ms: number) => { const d = new Date(ms); return `${fmtDate(ms)} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`; };

export async function copyText(s: string) {
  try { await navigator.clipboard.writeText(s); return true; } catch { return false; }
}
