#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ ! -f "$ROOT/data/server.pid" ]; then echo "codex-session-viewer 未运行。"; exit 1; fi
SERVER_PID=$(cat "$ROOT/data/server.pid")
case "$SERVER_PID" in ''|*[!0-9]*|0|1) echo "无效 PID 文件。"; exit 1 ;; esac
if ! kill -0 "$SERVER_PID" 2>/dev/null; then echo "codex-session-viewer 未运行（过期 PID ${SERVER_PID}）。"; exit 1; fi
COMMAND=$(ps -p "$SERVER_PID" -o command= 2>/dev/null || true)
case "$COMMAND" in "$ROOT/bin/codex-session-viewer"|"$ROOT/bin/codex-session-viewer "*|"$ROOT/bin/session-lens"|"$ROOT/bin/session-lens "*) ;; *) echo "PID 已被其他进程使用。"; exit 1 ;; esac
SERVER_PORT=$(cat "$ROOT/data/server.port" 2>/dev/null || true)
case "$SERVER_PORT" in ''|*[!0-9]*) echo "进程 $SERVER_PID 正在启动；端口尚未就绪。"; exit 1 ;; esac
if curl --noproxy '*' -fsS --max-time 2 "http://127.0.0.1:$SERVER_PORT/api/health" 2>/dev/null | grep -Eq '"pid":'"$SERVER_PID"'([,}])'; then
  echo "codex-session-viewer 运行正常 · PID $SERVER_PID · http://127.0.0.1:$SERVER_PORT"
else echo "PID $SERVER_PID 存在，但健康检查未通过。"; exit 1; fi
