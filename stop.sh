#!/bin/zsh
# Stop Bazarr and the movie-shelf server.
pkill -f "bazarr.py -c" && echo "Bazarr stopped"
pkill -f "bazarr/main.py"
pid=$(lsof -ti tcp:${PORT:-8687} -sTCP:LISTEN)
[[ -n $pid ]] && kill $pid && echo "movie-shelf stopped"
true
