#!/bin/zsh
# Start Bazarr and the movie-shelf server in the background if they are not running.
# Usage: ./start.sh [--rebuild]
cd "${0:A:h}"
export PORT=${PORT:-8687}
BAZARR=~/Documents/Github/bazarr

if pgrep -f "bazarr.py -c $BAZARR/data" >/dev/null; then
  echo "Bazarr already running"
else
  (cd $BAZARR && nohup .venv/bin/python bazarr.py -c $BAZARR/data -p 6767 --no-update > data/log/stdout.log 2>&1 &)
  echo "Bazarr started: http://localhost:6767"
fi

if lsof -ti tcp:$PORT -sTCP:LISTEN >/dev/null; then
  echo "movie-shelf already running"
else
  export PATH="$HOME/.nvm/versions/node/v24.13.0/bin:/opt/homebrew/bin:$PATH"
  QMD_EMBED_MODEL="hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf" \
    nohup go/bin/shelf serve >> logs/server.log 2>> logs/server.err.log < /dev/null &!
  echo "movie-shelf started: http://localhost:$PORT"
fi

if [[ "$1" == "--rebuild" ]]; then ./rebuild.sh; fi
