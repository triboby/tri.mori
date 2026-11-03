#!/bin/zsh
# Applies logs/book-reorg-plan.tsv to /Volumes/store/book (moves + metadata.opf/cover.jpg sidecars, rollback log),
# then rebuilds the book index from the new layout. Rollback: node organize-books.mjs --rollback logs/book-reorg-<stamp>.tsv
set -euo pipefail
cd "${0:A:h}"
export PATH="$HOME/.nvm/versions/node/v24.13.0/bin:/opt/homebrew/bin:$PATH"
node organize-books.mjs --apply
./rebuild-books.sh
