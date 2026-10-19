#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
mkdir -p "$ROOT/bin" "$ROOT/data/build-cache" "$ROOT/data/go-tmp" "$ROOT/log"
export GOCACHE="${GOCACHE:-$ROOT/data/build-cache}"
export GOTMPDIR="${GOTMPDIR:-$ROOT/data/go-tmp}"
export CGO_ENABLED=0
if ! command -v go >/dev/null 2>&1; then
  echo "需要 Go 1.24 或更新版本才能构建；运行编译产物不需要 Go。" >&2
  exit 1
fi
build() {
  target_os=$1
  target_arch=$2
  output=$3
  echo "构建 $target_os/$target_arch → bin/$output"
  (cd "$ROOT/src" && GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags='-s -w' -o "$ROOT/bin/$output" .)
}
case "${1:-}" in
  all)
    for target_os in darwin linux windows; do
      for target_arch in amd64 arm64; do
        suffix=''
        [ "$target_os" != windows ] || suffix=.exe
        build "$target_os" "$target_arch" "codex-session-viewer-$target_os-$target_arch$suffix"
      done
    done
    ;;
  '')
    target_os=$(go env GOHOSTOS)
    target_arch=$(go env GOHOSTARCH)
    suffix=''
    [ "$target_os" != windows ] || suffix=.exe
    build "$target_os" "$target_arch" "codex-session-viewer$suffix"
    ;;
  *) echo "用法: ./build.sh [all]" >&2; exit 2 ;;
esac
