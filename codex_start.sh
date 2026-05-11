#!/bin/bash

cd "$(dirname "$(readlink -f "$0")")" || exit 1

pkill -f codex2api-linux-amd64 2>/dev/null
pkill -9 -f codex2api-watchdog 2>/dev/null

for i in $(seq 1 35); do
    pidof codex2api-linux-amd64 > /dev/null 2>&1 || break
    sleep 1
done

if pidof codex2api-linux-amd64 > /dev/null 2>&1; then
    pkill -9 -f codex2api-linux-amd64 2>/dev/null
    sleep 1
fi

export DATABASE_DRIVER=sqlite
export DATABASE_PATH=./codex2api.db
export CACHE_DRIVER=memory

export GOMEMLIMIT=3000MiB
export GOMAXPROCS=2

chmod +x codex2api-linux-amd64

nohup ./codex2api-linux-amd64 > /dev/null 2>&1 &
echo "codex2api started, pid: $!"

SCRIPT_PATH="$(readlink -f "$0")"
SCRIPT_DIR="$(dirname "$SCRIPT_PATH")"
nohup bash -c "
    # codex2api-watchdog
    cd '$SCRIPT_DIR'
    while true; do
        sleep 10
        if ! pidof codex2api-linux-amd64 > /dev/null 2>&1; then
            echo \"[\$(date '+%Y-%m-%d %H:%M:%S')] codex2api-linux-amd64 已停止，重新执行 codex_start.sh\" >> codex-watchdog.log
            exec bash '$SCRIPT_PATH'
        fi
    done
" > /dev/null 2>&1 &
echo "watchdog started, pid: $!"
