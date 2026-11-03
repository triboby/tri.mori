import { Jieba } from '@node-rs/jieba';
import { dict } from '@node-rs/jieba/dict.js';

const jieba = Jieba.withDict(dict);

export const hasCJK = s => /[぀-ヿ㐀-鿿가-힯]/.test(s || '');

/** Segment text into words (search mode), dropping punctuation and whitespace. */
export function segment(text, { search = true } = {}) {
  const words = search ? jieba.cutForSearch(text, true) : jieba.cut(text, true);
  return words.map(w => w.trim()).filter(w => /[\p{L}\p{N}]/u.test(w));
}
