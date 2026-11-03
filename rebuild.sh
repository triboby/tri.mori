#!/bin/zsh
# Regenerate movie docs from NAS/Radarr/TMDB, then refresh the qmd "movies" index.
set -euo pipefail
cd "${0:A:h}"
export PATH="$HOME/.nvm/versions/node/v24.13.0/bin:/opt/homebrew/bin:$PATH"
export QMD_EMBED_MODEL="hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf"
export NODE_USE_ENV_PROXY=1 NO_PROXY="localhost,127.0.0.1,192.168.0.0/16,.local"
QMD=(node node_modules/@tobilu/qmd/dist/cli/qmd.js --index movies)

go/bin/shelf gen "$@"
if ! "${QMD[@]}" collection list 2>/dev/null | grep -q '^movies'; then
  "${QMD[@]}" collection add "$PWD/docs" --name movies --mask '**/*.md'
  "${QMD[@]}" context add qmd://movies/ "家庭 NAS 片库：每个文档是一部电影的中英文标题、简介、类型、关键词、导演和演员"
fi
"${QMD[@]}" update
"${QMD[@]}" embed
# Chart coverage for /fill (charts cached for a month); a failure here does not fail the rebuild.
NO_PROXY="$NO_PROXY,douban.com,doubanio.com" go/bin/shelf lists || echo "shelf lists failed; /fill keeps the previous data/lists.json" >&2
