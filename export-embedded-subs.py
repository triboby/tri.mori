"""Export embedded Simplified Chinese text subtitle tracks to <video>.zh.srt next to the video.

Reads candidates from logs/embedded-zh-candidates.json, processes smallest videos first,
never overwrites, skips folders that already have a Chinese external subtitle, and appends
every created file to logs/embedded-subs-export-<date>.txt. Safe to re-run (resumable).
"""
import datetime
import json
import os
import re
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.abspath(__file__))
NAS = '/Volumes/store/movie'
LOG = os.path.join(ROOT, 'logs', f'embedded-subs-export-{datetime.date.today().isoformat()}.txt')
SIMP = re.compile('[们这说时过还对没为国会发么个来话样边认问间东车门]')
TRAD = re.compile('[們這說時過還對沒為國會發麼個來話樣邊認問間東車門]')
ZH_SUB = re.compile(r'\.(zh|chs|chi|zho|sc|cn|chs&eng|简体|简中)[.\-_ &]|[一-鿿]', re.I)
SUB_EXT = ('.srt', '.ass', '.ssa')


def log(msg):
    print(f'{datetime.datetime.now():%H:%M:%S} {msg}', flush=True)


def has_chinese_external(folder, stem):
    for f in os.listdir(folder):
        if f.lower().endswith(SUB_EXT) and ZH_SUB.search(f):
            return f
    return None


def main():
    cands = json.load(open(os.path.join(ROOT, 'logs', 'embedded-zh-candidates.json')))
    cands.sort(key=lambda c: c['size'])
    done = written = 0
    for c in cands:
        video = os.path.join(NAS, c['video'])
        folder, stem = os.path.dirname(video), os.path.splitext(video)[0]
        target = stem + '.zh.srt'
        if os.path.exists(target):
            log(f'skip (exists) {c["title"]}')
            continue
        existing = has_chinese_external(folder, stem)
        if existing:
            log(f'skip (has {existing}) {c["title"]}')
            continue
        with tempfile.TemporaryDirectory() as tmp:
            out = os.path.join(tmp, 'sub.srt')
            t0 = datetime.datetime.now()
            r = subprocess.run(['nice', '-n', '15', 'ffmpeg', '-nostdin', '-v', 'error', '-i', video,
                                '-map', f'0:{c["track"]}', '-c:s', 'srt', out], capture_output=True, text=True)
            secs = (datetime.datetime.now() - t0).seconds
            if r.returncode or not os.path.exists(out) or os.path.getsize(out) < 1000:
                log(f'FAIL {c["title"]} ({secs}s): {r.stderr.strip()[:200]}')
                continue
            text = open(out, encoding='utf-8', errors='replace').read()
            s, t = len(SIMP.findall(text)), len(TRAD.findall(text))
            if t > s or s < 20:
                log(f'reject {c["title"]}: simp={s} trad={t}')
                continue
            fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
            with os.fdopen(fd, 'w', encoding='utf-8') as f:
                f.write(text)
            with open(LOG, 'a', encoding='utf-8') as f:
                f.write(f'{datetime.datetime.now().isoformat(timespec="seconds")}\t{target}\ttrack {c["track"]} {c["label"]}\n')
            written += 1
            log(f'wrote {c["title"]} ({secs}s, {c["size"] / 1e9:.1f}GB) simp={s}')
        done += 1
    log(f'EXPORT_DONE written={written}')


if __name__ == '__main__':
    sys.exit(main())
