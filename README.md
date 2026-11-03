# 森记 Mori

Your Personal Digital Garden · Collect · Connect · Grow

森记是家里 NAS 上的个人数字花园：电影（片库）、书（书架）和片单都在这里，后续加入笔记和收藏。风格追求 Minimal / Natural / Calm。
本仓库由原 `movie-shelf` 迁移而来，服务端口、路径、`/opds`、`/kosync` 接口均保持不变。

## 从源码构建

```sh
npm install                       # qmd（搜索索引）等 Node 依赖
(cd web && npm install && npm run build)   # 前端构建到 go/webdist，会被嵌入二进制
(cd go && go build -o bin/shelf .)
cp .env.example .env              # 填入 TMDB_API_KEY，chmod 600
```

`data/`、`cache/`、`logs/`、`docs/`、`docs-books/`、`go/bin/`、`go/webdist/` 以及 kosync 数据库都是本机生成的数据，不进仓库。

## 片库（Movies）

家庭 NAS 电影货架页 + 搜索。数据来自 NAS 片源（只读）、Radarr 和 TMDB，搜索引擎是 [qmd](https://github.com/tobi/qmd)。

片源在 `gen.mjs` 的 `SOURCES` 里配置，每部电影带 `libraries` 字段：

| library | 目录 | 说明 |
| --- | --- | --- |
| `main` | `/Volumes/store/movie`（`NAS_DIR` 可覆盖） | 主片库，会写 NAS `poster.jpg` |
| `kids` | `/Volumes/video/movie`（`KIDS_NAS_DIR` 可覆盖） | 少儿片库（Plex「少儿电影」、Radarr 根目录 + `kids` 标签），不写 NAS 海报 |

页面「少儿」标签和推荐页「少儿」一行按 `libraries` 包含 `kids` 筛选。

- 页面：<http://192.168.50.253:8687>（手动启动：`./start.sh`，见「服务管理」）
- qmd 索引：`~/.cache/qmd/movies.sqlite`（独立于默认 index），嵌入模型 Qwen3-Embedding-0.6B
- 搜索：每次 `/api/search` 起一个 `qmd query --no-rerank` 子进程，搜完即退出（约 1.3 秒/次，服务空闲内存约 75MB）；并发搜索会排队，同一时间最多加载一个模型

## 重建（新增电影后跑这一条）

```sh
./rebuild.sh            # 生成 docs/*.md + data/movies.json，然后 qmd update + embed
./rebuild.sh --rescan   # 同上，但重新扫描所有 NAS 文件夹（字幕变化时用）
./rebuild.sh --no-nas-posters   # 不往 NAS 写 poster.jpg
```

默认每次生成都会把 TMDB w780 海报写成 `main` 片源电影文件夹里的 `poster.jpg`：已有 `poster.jpg` / `folder.jpg` / `cover.jpg`
的文件夹跳过（列在 `logs/nas-posters-skipped.txt`），不会覆盖。新建的每个文件都记在 `logs/nas-posters-<日期>.txt`，
撤销：`while read f; do rm "$f"; done < logs/nas-posters-<日期>.txt`。
注意：Plex 开启了"本地媒体资源"时会开始使用这些海报。

NAS 文件夹扫描结果缓存在 `cache/scan.json`，TMDB 响应缓存在 `cache/tmdb/`，海报在 `cache/posters/`。
页面会自动读取新的 `data/movies.json`，无需重启服务。

## 书架（`/books`、`/all`）

NAS 布局（`organize-books.mjs` 整理，和 `store/movie` 一样平铺）：`/Volumes/store/book/<书名 - 作者>/` 里放书文件
（多格式同名，完全相同的副本标「(重复)」）、`cover.jpg` 和 Calibre 兼容的 `metadata.opf`。分类写在 OPF 的
`home:category`（大类，参考中图法共 15 个）和 `home:subcategory`（细类），版权页能读到 CIP 的另有 `home:clc` 中图分类号；
分类表和每本书的归属在 `recategorize-books.mjs` 里维护。**NAS 上的 metadata.opf / cover.jpg 是权威数据**：生成时优先读取，
有 OPF 的书不再查豆瓣；手改 OPF 后跑 `./rebuild-books.sh` 即可。新放进来、还没有 OPF 的书走下面的流程：

元数据来源：epub OPF / mobi·azw3 EXTH 元数据和封面 → 豆瓣（有 ISBN 先按 ISBN，
否则 suggest 搜书名并校验作者，每次请求间隔约 3 秒）→ 英文书回退 Open Library。同一豆瓣条目的不同格式合并成一本。

```sh
./rebuild-books.sh            # 生成 docs-books/*.md + data/books.json + cache/covers/，然后 qmd update + embed（collection books）
./rebuild-books.sh --rescan   # 重新读取所有书文件（默认按 size+mtime 复用 cache/book-scan.json）
./rebuild-books.sh --offline  # 不查豆瓣 / Open Library，只用文件内元数据和文件名
```

豆瓣 / Open Library 响应缓存在 `cache/bookmeta/`；豆瓣开始拒绝请求（403）时本次运行会停止查询豆瓣，
稍后重跑即可补齐。未匹配的文件列在 `unmatched-books.md`。正在复制中（0 字节）的文件会跳过。

### KOReader

- OPDS 书目：`http://192.168.50.253:8687/opds`（最近添加 / 豆瓣高分 / 全部 / 分类 / 搜索）。下载的是 NAS 原文件，不转换。
- 进度同步（kosync，兼容 koreader-sync-server API）：自定义服务器 `http://192.168.50.253:8687/kosync`，
  数据在 `data/kosync.sqlite`。文档匹配方式保持默认的「二进制」（partial MD5），书架页据此显示阅读进度。
  `KOSYNC_REGISTRATION=off` 可关闭新用户注册。

## 文件

| 路径 | 作用 |
| --- | --- |
| `gen.mjs` | 扫描 NAS → 匹配 Radarr / TMDB → 写 `docs/<tmdbId>.md`、`data/movies.json`、`unmatched.md`、NAS `poster.jpg` |
| `gen-books.mjs` | 扫描 NAS 书库 → 豆瓣 / Open Library → 写 `docs-books/<id>.md`、`data/books.json`、`cache/covers/`、`unmatched-books.md` |
| `go/bin/shelf serve` | 端口 8687（Node 版 `server.mjs` 已停用）：`/`、`/books`、`/all`、`/api/movies`、`/api/books`、`/api/search?q=&kind=movies\|books\|all`（jieba 分词 lex + 向量，无 rerank）、`/opds`、`/kosync`、`/books/file/<md5>` |
| `public/index.html` | 电影货架页面 |
| `organize-books.mjs` | 书库整理：默认只生成 `book-reorg-plan.md` 预演；`--apply` 执行并写回滚日志 `logs/book-reorg-<时间>.tsv`；`--rollback <日志>` 撤销 |
| `recategorize-books.mjs` | 两级分类：默认生成 `book-recategorize-plan.md`；`--apply` 先备份全部 OPF 到 `logs/opf-backup-<时间>/` 再改写；`--rollback <备份目录>` 还原 |
| `public/books.html` | 书架 / 全部页面 |
| `public/shelf.css` | 两个页面共用样式 |
| `lib/seg.mjs` | jieba 中文分词（文档和查询共用） |
| `unmatched.md` | 未能高置信度匹配的 NAS 条目 |
| `go/bin/shelf lists` | 补片页榜单：抓豆瓣电影 Top 250（榜单缓存一个月，`--refresh` 强制重抓），经 TMDB 匹配片库（含少儿库），写 `data/lists.json`；`rebuild.sh` 末尾会调用，失败不影响重建 |
| `.env` | `TMDB_API_KEY`（chmod 600，勿提交） |

## 模型下载（国内源）

嵌入模型 `Qwen3-Embedding-0.6B-Q8_0.gguf` 放在 `~/.cache/qmd/models/hf_Qwen_Qwen3-Embedding-0.6B-Q8_0.gguf`。
HuggingFace 走代理很慢时，从 ModelScope 直连下载（约 6MB/s），然后核对 sha256：

```sh
aria2c -x4 -s4 -o hf_Qwen_Qwen3-Embedding-0.6B-Q8_0.gguf -d ~/.cache/qmd/models \
  https://modelscope.cn/models/Qwen/Qwen3-Embedding-0.6B-GGUF/resolve/master/Qwen3-Embedding-0.6B-Q8_0.gguf
# sha256 06507c7b42688469c4e7298b0a1e16deff06caf291cf0a5b278c308249c3e439
```

ModelScope 上也有 `ggml-org/Qwen3-Reranker-0.6B-Q8_0-GGUF`；qmd 的查询扩展模型 `tobil/qmd-query-expansion-1.7B-gguf` 没有国内镜像。

## 服务管理

不开机自启，需要时手动启停（Bazarr 字幕下载 + 片库网页）：

```sh
./start.sh            # 未运行时后台启动 Bazarr（:6767）和片库（:8687）
./start.sh --rebuild  # 同上，并重建一次片库
./stop.sh             # 停止两者
./restart-server.sh   # 只重启片库并等待 /api/health
tail -f logs/server.log
```

片库服务由 Go 二进制 `go/bin/shelf serve` 提供（前端 `web/` 构建后嵌入二进制），重建脚本调用 `go/bin/shelf gen` / `gen-books`。
旧的 Node 版（`server.mjs`、`gen*.mjs`、`public/`、`node_modules`）仍保留，`node_modules` 里的 qmd 也仍被使用。
回退到 Node 版：

```sh
kill $(lsof -ti tcp:8687 -sTCP:LISTEN)
QMD_EMBED_MODEL="hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf" \
  PORT=8687 nohup ~/.nvm/versions/node/v24.13.0/bin/node server.mjs >> logs/server.log 2>> logs/server.err.log < /dev/null &!
```
