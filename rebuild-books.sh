#!/bin/zsh
# Regenerate book docs from the NAS (+ Douban / Open Library), then refresh the qmd "books" collection.
set -euo pipefail
cd "${0:A:h}"
export PATH="$HOME/.nvm/versions/node/v24.13.0/bin:/opt/homebrew/bin:$PATH"
export QMD_EMBED_MODEL="hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf"
export NODE_USE_ENV_PROXY=1 NO_PROXY="localhost,127.0.0.1,192.168.0.0/16,.local,douban.com,doubanio.com"
QMD=(node node_modules/@tobilu/qmd/dist/cli/qmd.js --index movies)

go/bin/shelf gen-books "$@"
if ! "${QMD[@]}" collection list 2>/dev/null | grep -q '^books'; then
  "${QMD[@]}" collection add "$PWD/docs-books" --name books --mask '**/*.md'
  "${QMD[@]}" context add qmd://books/ "家庭 NAS 书库：每个文档是一本书的书名、作者、译者、出版信息、豆瓣简介和标签"
fi
"${QMD[@]}" update
"${QMD[@]}" embed
