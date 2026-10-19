#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
mkdir -p "$ROOT/data" "$ROOT/log"
LOCK="$ROOT/data/restart.lock"
if ! mkdir "$LOCK" 2>/dev/null; then echo "另一个启动操作正在运行；若已中断，请删除 data/restart.lock。" >&2; exit 1; fi
trap 'rmdir "$LOCK" 2>/dev/null || true' EXIT HUP INT TERM
EXE="$ROOT/bin/codex-session-viewer"
if [ ! -x "$EXE" ]; then "$ROOT/build.sh"; fi
if ! command -v curl >/dev/null 2>&1; then echo "restart.sh 健康检查需要 curl。也可直接运行 bin/codex-session-viewer。" >&2; exit 1; fi
"$ROOT/stop.sh"
rm -f "$ROOT/data/server.port"
if [ -f "$ROOT/log/server.log" ] && [ "$(wc -c < "$ROOT/log/server.log" | tr -d ' ')" -gt 10485760 ]; then mv -f "$ROOT/log/server.log" "$ROOT/log/server.log.1"; fi
cd "$ROOT"
nohup "$EXE" --data-dir "$ROOT/data" --port "${PORT:-10002}" "$@" >> "$ROOT/log/server.log" 2>&1 < /dev/null &
SERVER_PID=$!
printf '%s\n' "$SERVER_PID" > "$ROOT/data/server.pid"
tries=0
while [ "$tries" -lt 60 ]; do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then echo "启动失败，查看 log/server.log" >&2; rm -f "$ROOT/data/server.pid"; exit 1; fi
  if [ -f "$ROOT/data/server.port" ]; then
    SERVER_PORT=$(cat "$ROOT/data/server.port")
    case "$SERVER_PORT" in ''|*[!0-9]*) SERVER_PORT=0 ;; esac
    if curl --noproxy '*' -fsS --max-time 1 "http://127.0.0.1:$SERVER_PORT/api/health" 2>/dev/null | grep -Eq '"pid":'"$SERVER_PID"'([,}])'; then
      echo "codex-session-viewer 已启动 · PID $SERVER_PID · http://127.0.0.1:$SERVER_PORT"
      echo "日志: $ROOT/log/server.log"
      exit 0
    fi
  fi
  tries=$((tries + 1))
  sleep 0.2
done
echo "启动超时，查看 log/server.log；进程可能仍在运行。" >&2
exit 1
