#!/bin/zsh
# Restarts the shelf server on $PORT (default 8687) (nohup, not launchd): stop the listener, wait for the port to free, start, wait for /api/health.
set -uo pipefail
cd "${0:A:h}"
export PATH="$HOME/.nvm/versions/node/v24.13.0/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin"
export QMD_EMBED_MODEL="hf:Qwen/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q8_0.gguf" PORT=${PORT:-8687}
[[ -x go/bin/shelf ]] || { echo "go/bin/shelf not found" >&2; exit 1; }
pid=$(lsof -ti tcp:$PORT -sTCP:LISTEN)
[[ -n $pid ]] && kill $pid
for i in {1..50}; do lsof -ti tcp:$PORT -sTCP:LISTEN >/dev/null || break; sleep 0.2; done
nohup go/bin/shelf serve >> logs/server.log 2>> logs/server.err.log < /dev/null &!
for i in {1..30}; do sleep 0.5; curl -sf -m 2 localhost:$PORT/api/health && echo && exit 0; done
echo "server did not come up; see logs/server.err.log" >&2; exit 1
