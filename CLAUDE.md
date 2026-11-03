# CLAUDE.md

Implementation notes for Mori / 森记. The user-facing intro is in `README.md`; keep that file product-level and put
technical detail here.

> This repo is public. Do not commit personal data, LAN IPs, hostnames, API keys or library contents.
> Use placeholders such as `<host>` and `<nas>` in docs.

## Overview

Mori (formerly `movie-shelf`) is a self-hosted personal digital garden for a home NAS: movies (片库), books (书架),
collections (补片 / charts) and, later, notes. Brand: Minimal / Natural / Calm; Collect · Connect · Grow.
Ports, paths and the `/opds`, `/kosync` APIs were kept unchanged from movie-shelf.

- **Server**: one Go binary, `go/bin/shelf`, serves all pages and APIs (default port `8687`) and also contains the
  generators as subcommands.
- **Frontend**: Astro + React + Tailwind in `web/`, built statically into `go/webdist/` and embedded into the binary.
- **Search**: [qmd](https://github.com/tobi/qmd) (Node, from `node_modules`), jieba segmentation + vectors, no rerank.
- **Legacy Node version** (`server.mjs`, `gen*.mjs`, `organize-books.mjs`, `recategorize-books.mjs`, `public/`,
  `lib/seg.mjs`) is kept for rollback; the Node `node_modules` (qmd) is still used by the Go version.

Service management is on demand: nothing is started at boot (no launchd), and the user prefers it that way.

## Build

```sh
npm install                                   # qmd (search index) and other Node deps
(cd web && npm install && npm run build)      # icons + Astro build to go/webdist (embedded into the binary)
(cd go && go build -o bin/shelf .)
cp .env.example .env                          # TMDB_API_KEY, chmod 600
```

`web/` `npm run build` first runs `scripts/icons.mjs`, which renders `web/public/mori-favicon.svg` into
`favicon.ico`, `apple-touch-icon.png`, `icon-*.png` and `site.webmanifest` (all gitignored). `npm run dev` serves
Astro on :4321.

After a frontend or Go change: rebuild the web bundle, rebuild the binary, then `./restart-server.sh`.

Not in git (generated locally): `data/`, `cache/`, `logs/`, `docs/`, `docs-books/`, `go/bin/`, `go/webdist/`,
`*.sqlite` (including the kosync DB), `unmatched*.md`, `book-*-plan.md`.

## Layout

| Path | Purpose |
| --- | --- |
| `go/main.go` | subcommand dispatch |
| `go/server.go`, `go/extra.go` | HTTP server, page routes, embedded webdist, placeholder APIs |
| `go/gen.go` | movie generator (`shelf gen`) |
| `go/genbooks.go` | book generator (`shelf gen-books`); `genbooks_compat_test.go` checks parity with the Node version |
| `go/organize.go`, `go/recategorize.go` | book library reorg / categorisation |
| `go/lists.go` | chart coverage for `/fill` (`shelf lists`) |
| `go/search.go` | `/api/search` via a qmd child process |
| `go/opds.go`, `go/kosync.go` | OPDS catalog, KOReader progress sync |
| `go/jieba`, `go/icu`, `go/jsre.go`, `go/jsx.go` | Chinese segmentation and JS-compatible helpers (match Node output) |
| `web/src/pages` | `index` (movies), `books`, `pick`, `movie`, `fill`, `health` |
| `web/src/components` | React islands (`HomeApp`, `BooksApp`, `PickApp`, `FillApp`, `MovieDetail`, `HealthApp`, …) |
| `web/public` | brand assets: `mori-mark.svg`, `mori-mark-reverse.svg`, `mori-favicon.svg` |
| `*.sh` | `start.sh`, `stop.sh`, `restart-server.sh`, `rebuild.sh`, `rebuild-books.sh`, `apply-book-reorg.sh` |
| `export-embedded-subs.py` | export embedded subtitle tracks |
| `.env` | `TMDB_API_KEY` (chmod 600, never commit) |

## `shelf` subcommands

```
shelf serve                                        # HTTP server (default when no args)
shelf gen [--rescan] [--no-nas-posters]            # movies: NAS → Radarr / TMDB → docs/, data/movies.json
shelf gen-books [--rescan] [--offline]             # books: NAS → Douban / Open Library → docs-books/, data/books.json
shelf organize-books [--apply | --rollback <log>]
shelf recategorize-books [--apply | --rollback <backup>]
shelf lists [--refresh]                            # Douban Top 250 coverage → data/lists.json
shelf seg [--search]                               # jieba-segment stdin, one JSON array per line
```

## HTTP routes

- Pages: `/`, `/books`, `/all`, `/pick`, `/movie`, `/fill`, `/health`
- `/api/movies`, `/api/books`, `/api/library`, `/api/lists`, `/api/health`
- `/api/search?q=&kind=movies|books|all` (jieba lex + vector, no rerank)
- Placeholders (return `pending: true`): `/api/watch` (Plex history), `/api/play` (Plex cast), `/api/radarr/add`
- `/opds` (recent / Douban top / all / categories / search), `/books/file/<md5>/<name>`, `/covers/<file>`
- `/kosync` (koreader-sync-server compatible: `/healthcheck`, `/users/create`, `/users/auth`, `/syncs/progress`)

## Environment variables

`PORT` (8687), `HOST`, `TMDB_API_KEY`, `NAS_DIR` (default `/Volumes/store/movie`), `KIDS_NAS_DIR`
(default `/Volumes/video/movie`), `BOOK_DIR`, `KOSYNC_DB` (default `data/kosync.sqlite`),
`KOSYNC_REGISTRATION=off` (disable sign-up), `QMD_EMBED_MODEL`, `QMD_INDEX`, `NODE_BIN`, `SHELF_ROOT`,
`SHELF_COMPAT_DIR`.

Known hardcoding: the NAS SMB base (`smb://<nas>/…`) is a literal in `go/gen.go`, `go/genbooks.go`,
`web/src/lib/util.ts` and the legacy Node files. `web/src/components/BooksApp.tsx` still shows the old
OPDS / kosync URL in its help text.

## Movies (片库)

Sources are configured in `go/gen.go` (`gen.mjs` `SOURCES` for Node); each movie has a `libraries` field:

| library | directory | notes |
| --- | --- | --- |
| `main` | `/Volumes/store/movie` (`NAS_DIR`) | main library; writes NAS `poster.jpg` |
| `kids` | `/Volumes/video/movie` (`KIDS_NAS_DIR`) | kids library (Plex kids section, Radarr root + `kids` tag); no NAS posters |

The 少儿 tab and the 少儿 row on the pick page filter on `libraries` containing `kids`.

Rebuild after adding movies:

```sh
./rebuild.sh                     # shelf gen → docs/*.md + data/movies.json, then qmd update + embed, then shelf lists
./rebuild.sh --rescan            # rescan every NAS folder (e.g. subtitles changed)
./rebuild.sh --no-nas-posters    # do not write poster.jpg to the NAS
```

- By default each run writes the TMDB w780 poster as `poster.jpg` into `main` movie folders. Folders that already
  have `poster.jpg` / `folder.jpg` / `cover.jpg` are skipped (listed in `logs/nas-posters-skipped.txt`) and never
  overwritten. New files are logged in `logs/nas-posters-<date>.txt`; undo with
  `while read f; do rm "$f"; done < logs/nas-posters-<date>.txt`. Plex uses them when "local media assets" is on.
- Caches: NAS scan `cache/scan.json`, TMDB `cache/tmdb/`, posters `cache/posters/`. Unmatched folders →
  `unmatched.md`.
- The server picks up a new `data/movies.json` without restarting.
- `shelf lists` failing does not fail the rebuild; `/fill` keeps the previous `data/lists.json`.

## Books (书架)

NAS layout (flat, like the movie library): `<BOOK_DIR>/<书名 - 作者>/` holding the book files (same name, several
formats; exact duplicates marked「(重复)」), `cover.jpg` and a Calibre-compatible `metadata.opf`.

- Categories live in the OPF: `home:category` (15 top-level, based on 中图法), `home:subcategory`, and `home:clc`
  when a CIP number was found. The category table and per-book assignments are maintained in `recategorize-books.mjs`.
- **The NAS `metadata.opf` / `cover.jpg` are the source of truth.** The generator reads them first and skips Douban
  for books that have an OPF. After editing an OPF by hand, run `./rebuild-books.sh`.
- New books without an OPF: epub OPF / mobi·azw3 EXTH metadata and cover → Douban (ISBN first, otherwise suggest
  search by title with author check, ~3 s between requests) → Open Library for English books. Formats of the same
  Douban entry are merged into one book.

```sh
./rebuild-books.sh               # docs-books/*.md + data/books.json + cache/covers/, then qmd update + embed (collection books)
./rebuild-books.sh --rescan      # re-read every book file (default reuses cache/book-scan.json by size+mtime)
./rebuild-books.sh --offline     # no Douban / Open Library, only embedded metadata and file names
```

Douban / Open Library responses are cached in `cache/bookmeta/`. If Douban starts returning 403, the run stops
querying it; re-run later to fill in. Unmatched files → `unmatched-books.md`. Files still copying (0 bytes) are skipped.

Library maintenance (both default to a dry-run plan file):

- `organize-books`: writes `book-reorg-plan.md`; `--apply` executes and logs `logs/book-reorg-<time>.tsv`;
  `--rollback <log>` undoes.
- `recategorize-books`: writes `book-recategorize-plan.md`; `--apply` backs up every OPF to
  `logs/opf-backup-<time>/` first; `--rollback <dir>` restores.

### KOReader

- OPDS: `http://<host>:8687/opds`. Downloads are the original NAS files, no conversion.
- Progress sync: custom server `http://<host>:8687/kosync`, data in `data/kosync.sqlite`. Document matching stays on
  the default "binary" (partial MD5); the books page uses it to show reading progress.

## Search

- Each `/api/search` spawns `qmd query --no-rerank` and exits afterwards (~1.3 s per query, ~75 MB idle server
  memory). Concurrent searches queue; at most one model is loaded at a time.
- qmd index: `~/.cache/qmd/movies.sqlite` (separate from the default index); collections `movies` and `books`.
- Embedding model: Qwen3-Embedding-0.6B (`QMD_EMBED_MODEL=hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf`).
- Documents and queries share the same jieba segmentation (`go/jieba`, legacy `lib/seg.mjs`).

Model download from a mainland-China mirror when HuggingFace is slow:

```sh
aria2c -x4 -s4 -o hf_Qwen_Qwen3-Embedding-0.6B-Q8_0.gguf -d ~/.cache/qmd/models \
  https://modelscope.cn/models/Qwen/Qwen3-Embedding-0.6B-GGUF/resolve/master/Qwen3-Embedding-0.6B-Q8_0.gguf
# sha256 06507c7b42688469c4e7298b0a1e16deff06caf291cf0a5b278c308249c3e439
```

ModelScope also has `ggml-org/Qwen3-Reranker-0.6B-Q8_0-GGUF`; qmd's query-expansion model
`tobil/qmd-query-expansion-1.7B-gguf` has no mirror.

## Running the service

On demand, not at boot (Bazarr subtitles + the web server):

```sh
./start.sh             # start Bazarr (:6767) and Mori (:8687) in the background if not running
./start.sh --rebuild   # same, then rebuild the movie library once
./stop.sh              # stop both
./restart-server.sh    # restart only Mori and wait for /api/health
tail -f logs/server.log
```

The scripts are zsh and assume macOS paths (nvm Node, Homebrew, a local Bazarr checkout).

### Rollback to the Node server

```sh
kill $(lsof -ti tcp:8687 -sTCP:LISTEN)
QMD_EMBED_MODEL="hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf" \
  PORT=8687 nohup node server.mjs >> logs/server.log 2>> logs/server.err.log < /dev/null &!
```

## Working on this repo

- The live deployment is a checkout on the home server, and other sessions push to `main` from there. Make changes
  on a branch and open a PR rather than pushing to `main`.
- Branding / UI copy: product name 森记 Mori; tagline "Your Personal Digital Garden · 让知识生长，让记忆留存".
