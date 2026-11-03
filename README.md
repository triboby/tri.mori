<p align="center">
  <img src="web/public/mori-mark.svg" width="96" alt="Mori 森记" />
</p>

<h1 align="center">森记 Mori</h1>

<p align="center">
  <b>Your Personal Digital Garden · 让知识生长，让记忆留存</b><br />
  Collect · Connect · Grow
</p>

---

森记是一座跑在家里的个人数字花园：看过的电影、读过的书、想留住的片单和笔记，都在这里慢慢生长。
它把散落在 NAS 里的影音和电子书整理成一个安静、好找、好看的地方，自己托管，数据留在自己手里。

设计上追求 **Minimal · Natural · Calm**：没有广告和信息流，只有你的收藏。

## 模块

| 模块 | 说明 | 状态 |
| --- | --- | --- |
| 🎬 **Movies 片库** | 海报墙浏览、筛选和搜索 NAS 上的电影；自动补全海报、简介、演职员和字幕信息；少儿片库单独分区 | 可用 |
| 📚 **Books 书架** | 整理 NAS 上的电子书，补全封面和豆瓣元数据，按中图法两级分类；支持 OPDS 书目和 KOReader 阅读进度同步 | 可用 |
| 🗂 **Collections 片单** | 豆瓣 Top 250 等榜单与片库比对，看看还缺哪些片 | 部分可用 |
| 📝 **Notes 笔记** | 记录观影和阅读想法，与电影、书相互连接 | 规划中 |

## 特点

- **Collect 收集**：直接读取 NAS 上已有的文件夹，不搬家、不转码；元数据来自 TMDB、Radarr、豆瓣和 Open Library。
- **Connect 连接**：中英文混合的语义搜索（分词 + 向量检索），电影和书可以一起搜。
- **Grow 生长**：新片、新书放进 NAS，重建一次就出现在花园里；数据以文件形式存放，随时可迁移。
- **阅读器友好**：内置 OPDS 书目和兼容 koreader-sync-server 的进度同步，KOReader 等阅读器可直接接入。
- **轻量**：单个 Go 二进制提供全部页面和接口，空闲内存占用很小，按需启动。
- **明暗主题**：页面跟随系统切换浅色 / 深色。

## 快速开始

需要：Go、Node.js、一台能挂载 NAS 共享的机器，以及一个 [TMDB API Key](https://www.themoviedb.org/settings/api)。

```sh
git clone https://github.com/triboby/tri.mori.git mori && cd mori

npm install                                   # 搜索引擎 qmd 等依赖
(cd web && npm install && npm run build)      # 构建前端（嵌入到二进制中）
(cd go && go build -o bin/shelf .)            # 构建服务端

cp .env.example .env && chmod 600 .env        # 填入 TMDB_API_KEY
./rebuild.sh                                  # 扫描片库并建立搜索索引
./rebuild-books.sh                            # 扫描书库（可选）
./start.sh                                    # 启动服务
```

然后打开 `http://<主机地址>:8687`。片库、书库目录等配置项见 [CLAUDE.md](CLAUDE.md)。

### 接入 KOReader

- OPDS 书目：`http://<主机地址>:8687/opds`
- 进度同步（自定义服务器）：`http://<主机地址>:8687/kosync`，文档匹配方式保持默认的「二进制」

## 开发

架构、目录结构、命令、接口和运维细节都在 [CLAUDE.md](CLAUDE.md)。

## 致谢

[qmd](https://github.com/tobi/qmd) · [TMDB](https://www.themoviedb.org/) · [豆瓣](https://www.douban.com/) · [Open Library](https://openlibrary.org/) · [Radarr](https://radarr.video/) · [KOReader](https://koreader.rocks/) · [Astro](https://astro.build/)
