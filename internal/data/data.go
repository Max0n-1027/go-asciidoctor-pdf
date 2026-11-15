// Package data は Asciidoctor PDF 由来の組み込みテーマとフォントをバイナリに埋め込みます。
//
// themes/ と fonts/ 以下のファイルは Asciidoctor PDF (MIT License) から取り込んだものです。
// フォントのライセンスは fonts/LICENSE-* を参照してください。
package data

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed themes/*.yml fonts/* fonts/icons/*/*
var files embed.FS

// FontsDirToken は組み込みフォントディレクトリを指す特別なパス接頭辞です。
const FontsDirToken = "GEM_FONTS_DIR"

// Theme は組み込みテーマ (例: "default") の YAML を返します。
func Theme(name string) ([]byte, bool) {
	b, err := files.ReadFile("themes/" + name + "-theme.yml")
	return b, err == nil
}

// ThemeNames は組み込みテーマ名の一覧を返します。
func ThemeNames() []string {
	entries, _ := fs.ReadDir(files, "themes")
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), "-theme.yml"))
	}
	sort.Strings(names)
	return names
}

// Font は組み込みフォントファイルを返します。
func Font(name string) ([]byte, bool) {
	b, err := files.ReadFile("fonts/" + name)
	return b, err == nil
}

// IconFonts はアイコンセット名とフォントファイル名の対応です (prawn-icon 同梱のフォント)。
var IconFonts = map[string]string{
	"fas": "fa-solid.ttf", "far": "fa-regular.ttf", "fab": "fa-brands.ttf",
	"fi": "foundation-icons.ttf", "pf": "paymentfont-webfont.ttf",
}

// IconFont はアイコンセットのフォントを返します。
func IconFont(set string) ([]byte, bool) {
	name, ok := IconFonts[set]
	if !ok {
		return nil, false
	}
	b, err := files.ReadFile("fonts/icons/" + set + "/" + name)
	return b, err == nil
}

// IconLegend はアイコンセットの名前とコードポイントの対応表 (YAML) を返します。
func IconLegend(set string) ([]byte, bool) {
	b, err := files.ReadFile("fonts/icons/" + set + "/" + set + ".yml")
	return b, err == nil
}

// File は組み込みデータを相対パスで返します。
func File(path string) ([]byte, bool) {
	b, err := files.ReadFile(path)
	return b, err == nil
}
