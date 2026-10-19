#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PIDFILE="$ROOT/data/server.pid"
if [ ! -f "$PIDFILE" ]; then echo "codex-session-viewer 未运行。"; exit 0; fi
SERVER_PID=$(cat "$PIDFILE")
case "$SERVER_PID" in ''|*[!0-9]*|0|1) echo "无效 PID 文件，拒绝停止。" >&2; exit 1 ;; esac
if ! kill -0 "$SERVER_PID" 2>/dev/null; then rm -f "$PIDFILE"; echo "已清理过期 PID。"; exit 0; fi
COMMAND=$(ps -p "$SERVER_PID" -o command= 2>/dev/null || true)
case "$COMMAND" in
  "$ROOT/bin/codex-session-viewer"|"$ROOT/bin/codex-session-viewer "*|"$ROOT/bin/session-lens"|"$ROOT/bin/session-lens "*) ;;
  *) echo "PID $SERVER_PID 不属于本项目，拒绝停止。" >&2; exit 1 ;;
esac
kill -TERM "$SERVER_PID"
tries=0
while kill -0 "$SERVER_PID" 2>/dev/null && [ "$tries" -lt 100 ]; do
  state=$(ps -p "$SERVER_PID" -o stat= 2>/dev/null || true)
  case "$state" in *Z*) break ;; esac
  sleep 0.1
  tries=$((tries + 1))
done
if [ "$tries" -ge 100 ]; then echo "仍在退出中，保留 PID 文件，请稍后再次查看状态。" >&2; exit 1; fi
rm -f "$PIDFILE" "$ROOT/data/server.port"
echo "codex-session-viewer 已停止。"
