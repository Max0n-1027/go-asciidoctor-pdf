# go-asciidoctor-pdf

[![CI](https://github.com/Max0n-1027/go-asciidoctor-pdf/actions/workflows/ci.yml/badge.svg)](https://github.com/Max0n-1027/go-asciidoctor-pdf/actions/workflows/ci.yml)

[Asciidoctor PDF](https://github.com/asciidoctor/asciidoctor-pdf) 互換の AsciiDoc → PDF コンバーターを Go で実装したものです。
Ruby も外部コマンドも不要で、単一バイナリとして Linux / macOS / Windows で動作します。

## インストール

[Releases](https://github.com/Max0n-1027/go-asciidoctor-pdf/releases) から OS とアーキテクチャに合ったアーカイブを
ダウンロードして展開し、`asciidoctor-pdf` (Windows では `asciidoctor-pdf.exe`) を PATH の通った場所に置いてください。
Linux / macOS / Windows それぞれの amd64 と arm64 向けを用意しています。`SHA256SUMS` でファイルを検証できます。

macOS のバイナリは署名していないため、ブラウザでダウンロードした場合は初回実行前に次のコマンドで隔離属性を外してください。

```bash
xattr -d com.apple.quarantine asciidoctor-pdf
```

Go の環境がある場合は `go install` でも入れられます。Go 1.26.8 以降が必要です (Go 1.21 以降なら、必要なツールチェーンが自動で取得されます)。

```bash
go install github.com/Max0n-1027/go-asciidoctor-pdf/cmd/asciidoctor-pdf@latest
```

## ビルド

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/asciidoctor-pdf ./cmd/asciidoctor-pdf
```

他の OS 向けには `GOOS` / `GOARCH` を指定します (例: `GOOS=windows GOARCH=amd64`)。
テーマ・フォント・アイコンはすべてバイナリに埋め込まれます。

リリースと同じ配布物 (全プラットフォームのアーカイブと `SHA256SUMS`) は次のコマンドで `dist/` に作れます。

```bash
scripts/dist.sh
```

## CI とリリース

- **CI** (`.github/workflows/ci.yml`): main への push とプルリクエストごとに、gofmt / go vet / go.mod の整合性を確認し、
  Linux・macOS・Windows でテストと実際の変換を行います。配布物のビルドも確認し、成果物 `dist` として 7 日間保存します。
- **リリース** (`.github/workflows/release.yml`): `v` で始まるタグを push すると、テストの後で全プラットフォームの配布物を作り、
  GitHub リリースに添付します。リリースノートは自動生成で、`v0.2.0-rc.1` のようにハイフンを含むタグはプレリリースになります。

```bash
git tag v0.1.0
```

```bash
git push origin v0.1.0
```

## 使い方

本家の `asciidoctor-pdf` コマンドと同じオプションを受け付けます。

```bash
bin/asciidoctor-pdf document.adoc
```

```bash
bin/asciidoctor-pdf -a pdf-theme=default-with-font-fallbacks -a scripts=cjk -o out.pdf document.adoc
```

主なオプション: `-a name=value`、`-o FILE` (`-` で標準出力)、`-D DIR`、`-B DIR`、`-d DOCTYPE`、`-S SAFE_MODE`、
`--theme THEME`、`-n`、`--failure-level LEVEL`、`-q`、`-v`、`-t`、`-V`。入力に `-` を指定すると標準入力を読みます。

日本語の文書では、組み込みの `default-with-font-fallbacks` テーマ (M+ 1p フォールバック) と `scripts=cjk` を指定してください。

## 構成

| パッケージ | 役割 |
| --- | --- |
| `cmd/asciidoctor-pdf` | コマンドライン |
| `internal/asciidoc` | AsciiDoc パーサーと置換処理 (Asciidoctor の Parser / Substitutors の移植) |
| `internal/converter` | レイアウトエンジン (Asciidoctor PDF の converter.rb の移植) |
| `internal/theme` | テーマ (YAML) ローダー |
| `internal/pdf` | PDF ライター、PDF リーダー (ページ取り込み用)、TrueType の解析とサブセット化 |
| `internal/svg` | 簡易 SVG レンダラー |
| `internal/data` | 埋め込みのテーマ・フォント |

## 互換性の検証方法

`testdata/examples/` は本家の `examples/` です。本家リポジトリに含まれる出力 PDF と、`pdftotext -bbox` で得た単語の座標を
ページごとに突き合わせて検証しています。現時点で `basic-example.adoc` と `chronicles-example.adoc` (17 ページ) は、
全ページでテキストの内容と位置が本家の出力と一致します (差は 0.02pt 以内)。
表紙画像・ページ背景画像 (PDF を含む) は、本家 spec の参照 PDF と描画結果を画素単位で比較して一致を確認しています。

```bash
go test ./...
```

## 対応状況

対応済み:

- 文書ヘッダー、属性、include、条件付きディレクティブ、セクション (番号付け・付録・部・章)、相互参照、脚注、索引
- 段落、リスト各種 (チェックリスト・コールアウト・説明リスト)、注記、コード、引用、サイドバー、例、表 (結合・AsciiDoc セル・ヘッダー繰り返し)
- インライン書式、リンク、kbd / btn / menu、アイコン (Font Awesome など)、インライン画像
- タイトルページ、目次、しおり、ページラベル、ランニングヘッダー / フッター、prepress 用の左右マージン
- PDF の取り込み: `image::file.pdf[page=2]` / `pages="1..3,5"`、PDF の表紙・裏表紙 (`front-cover-image` / `back-cover-image`)、
  PDF のページ背景 (`page-background-image(-recto|-verso)`)。xref ストリーム、オブジェクトストリーム、
  空パスワードで開ける暗号化 PDF (RC4 / AES-128 / AES-256)、相互参照表の壊れた PDF にも対応
- ページ背景画像・表紙画像 (PNG / JPEG / GIF / SVG、`fit` / `position` / `pdfwidth` 指定)
- テーマ (`extends`、変数、数式、単位)、カスタムフォント、フォールバックフォント
- 画像 (PNG / JPEG / GIF / SVG)、回り込み、キャプション
- シンタックスハイライト (`source-highlighter` が rouge / coderay / pygments のとき [chroma](https://github.com/alecthomas/chroma) で色付け)

未対応・制限事項:

- PDF 標準 14 フォント (Helvetica など。`base` テーマが使用) は同梱フォントで代替します
- 透かし (`page-foreground-image`)
- 取り込んだ PDF ページ内の、元の文書の他ページを指すリンクと注釈のポップアップは取り込みません。フォーム (AcroForm) は動作しません
- SVG のテキスト・グラデーション (単色で近似)・クリッピング
- ハイライトのトークン分割は chroma によるため、Rouge などと配色が一部異なります。`highlight` 属性 (行の強調) は未対応です
- ハイフネーション (`hyphens`)、数式 (stem) の組版、ページの段組み (`page-columns`)、`optimize`、pdfmark
- Ruby 製の拡張 (`-r`)、asciidoctor-diagram などの拡張機能
- 1 ページを超える高さの表セル、索引の see / see also、manpage doctype の専用処理

## ライセンス

本プロジェクトは [MIT License](LICENSE) です。

`internal/asciidoc` は [Asciidoctor](https://github.com/asciidoctor/asciidoctor) の、`internal/converter` と `internal/theme` は
[Asciidoctor PDF](https://github.com/asciidoctor/asciidoctor-pdf) の実装を Go に移植したものです (いずれも MIT License)。
元のライセンス文は [licenses/](licenses/) にあります。

## 同梱している第三者の成果物

- `internal/data/themes/`、`internal/data/fonts/` (Noto Serif / Noto Sans / M+ / Noto Emoji のサブセット、アイコンの旧名対応表):
  Asciidoctor PDF (MIT License) より。フォントのライセンスは `internal/data/fonts/LICENSE-*` を参照
- `internal/data/fonts/icons/`: [prawn-icon](https://github.com/jessedoyle/prawn-icon) 同梱の Font Awesome Free、Foundation Icons、PaymentFont。
  各ディレクトリの `LICENSE` を参照
- `testdata/examples/`: Asciidoctor PDF のサンプル (MIT License)
- `testdata/pdf/`: Asciidoctor PDF のテスト用 PDF (MIT License) と、それを Ghostscript で変換・加工したもの
