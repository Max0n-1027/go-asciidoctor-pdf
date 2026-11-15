#!/usr/bin/env bash
# 各 OS / アーキテクチャ向けの配布物 (アーカイブと SHA256SUMS) を dist/ に作ります。
#
# 使い方: scripts/dist.sh [バージョン]
#   バージョンを省略すると git describe の結果を使います (先頭の v は取り除きます)。
#   対象は環境変数 TARGETS で変更できます (例: TARGETS="linux/amd64 windows/amd64")。
#
# 必要なコマンド: go, git, tar, zip, sha256sum
set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
version="${version#v}"
name=asciidoctor-pdf
targets="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}"

rm -rf dist
mkdir -p dist/.licenses

# バイナリに含まれる第三者の成果物のライセンス文を集める
cp licenses/* dist/.licenses/
cp internal/data/fonts/LICENSE-* dist/.licenses/
for set in internal/data/fonts/icons/*/; do
  set="$(basename "$set")"
  cp "internal/data/fonts/icons/$set/LICENSE" "dist/.licenses/LICENSE-icons-$set"
done
# リンクされる Go モジュール (標準ライブラリと本体を除く)
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' ./cmd/asciidoctor-pdf |
  sort -u |
  while read -r path dir; do
    found=
    for f in "$dir"/LICENSE* "$dir"/COPYING*; do
      [ -f "$f" ] || continue
      dest="LICENSE-go-${path//\//_}"
      [ "$(basename "$f")" = LICENSE ] || dest="$dest-$(basename "$f")"
      cp "$f" "dist/.licenses/$dest"
      found=1
    done
    if [ -z "$found" ]; then
      echo "license file not found for Go module: $path" >&2
      exit 1
    fi
  done

for target in $targets; do
  goos="${target%/*}"
  goarch="${target#*/}"
  pkg="${name}_${version}_${goos}_${goarch}"
  dir="dist/$pkg"
  exe="$name"
  [ "$goos" = windows ] && exe="$name.exe"
  echo "building $pkg" >&2
  mkdir -p "$dir"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.toolVersion=$version" -o "$dir/$exe" ./cmd/asciidoctor-pdf
  cp README.md LICENSE "$dir/"
  cp -r dist/.licenses "$dir/licenses"
  if [ "$goos" = windows ]; then
    (cd dist && zip -qr "$pkg.zip" "$pkg")
  else
    tar -C dist -czf "dist/$pkg.tar.gz" "$pkg"
  fi
  rm -rf "$dir"
done

rm -rf dist/.licenses
(cd dist && sha256sum -- *.tar.gz *.zip > SHA256SUMS)
ls -l dist >&2
