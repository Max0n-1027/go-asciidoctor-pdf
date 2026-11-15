// Package converter は AsciiDoc 文書を PDF に変換します。
// Asciidoctor PDF の Converter (converter.rb) と同じレイアウト規則に従うことを目指しています。
package converter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/data"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

type fontKey struct{ family, style string }

type fontSet struct {
	c       *Converter
	catalog theme.FontCatalog
	dirs    []string
	loaded  map[fontKey]*pdf.Font
	failed  map[fontKey]bool
	warned  map[string]bool
}

// 標準 14 フォント (AFM) は未対応のため、同梱フォントで代替する
var builtinSubstitutes = map[string][2]string{
	"Helvetica":   {"Noto Sans", "notosans"},
	"Times-Roman": {"Noto Serif", "notoserif"},
	"Courier":     {"M+ 1mn", "mplus1mn"},
}

func newFontSet(c *Converter, catalog theme.FontCatalog, fontsDir string) *fontSet {
	fs := &fontSet{c: c, catalog: theme.FontCatalog{}, loaded: map[fontKey]*pdf.Font{}, failed: map[fontKey]bool{}, warned: map[string]bool{}}
	for k, v := range catalog {
		fs.catalog[k] = v
	}
	sep := ";"
	if !strings.Contains(fontsDir, ";") && strings.Contains(fontsDir, ",") {
		sep = ","
	}
	for _, d := range strings.Split(fontsDir, sep) {
		if d = strings.TrimSpace(d); d != "" {
			fs.dirs = append(fs.dirs, d)
		}
	}
	return fs
}

func (fs *fontSet) read(path string) ([]byte, error) {
	if strings.HasPrefix(path, data.FontsDirToken) {
		name := strings.TrimLeft(path[len(data.FontsDirToken):], "/\\")
		if b, ok := data.Font(name); ok {
			return b, nil
		}
		return nil, fmt.Errorf("%s not found in bundled fonts", name)
	}
	if filepath.IsAbs(path) {
		return os.ReadFile(path)
	}
	for _, dir := range fs.dirs {
		if dir == data.FontsDirToken {
			if b, ok := data.Font(path); ok {
				return b, nil
			}
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, path)); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("%s not found in %s", path, strings.Join(fs.dirs, string(os.PathListSeparator)))
}

// get はファミリーとスタイル (normal / bold / italic / bold_italic) からフォントを返します。
func (fs *fontSet) get(family, style string) *pdf.Font {
	if style == "" {
		style = "normal"
	}
	key := fontKey{family, style}
	if f, ok := fs.loaded[key]; ok {
		return f
	}
	if fs.failed[key] {
		return nil
	}
	styles, ok := fs.catalog[family]
	if !ok {
		if _, isIcon := data.IconFonts[family]; isIcon {
			// アイコンフォントはスタイルに関係なく同じフォントを使う
			if f, loaded := fs.loaded[fontKey{family, "normal"}]; loaded {
				fs.loaded[key] = f
				return f
			}
			raw, _ := data.IconFont(family)
			f, err := pdf.ParseFont(raw)
			if err != nil {
				fs.c.logf("ERROR", "could not load icon font %s: %v", family, err)
				fs.failed[key] = true
				return nil
			}
			fs.c.pdf.RegisterFont(f)
			fs.loaded[fontKey{family, "normal"}] = f
			fs.loaded[key] = f
			return f
		}
	}
	if !ok {
		if sub, isBuiltin := builtinSubstitutes[family]; isBuiltin {
			if !fs.warned[family] {
				fs.warned[family] = true
				fs.c.logf("WARN", "built-in (AFM) font %s is not supported yet; substituting %s", family, sub[0])
			}
			styles = map[string]string{}
			for _, s := range []string{"normal", "bold", "italic", "bold_italic"} {
				name := s
				if s == "normal" {
					name = "regular"
				}
				styles[s] = data.FontsDirToken + "/" + sub[1] + "-" + name + "-subset.ttf"
			}
			fs.catalog[family] = styles
		} else {
			if !fs.warned[family] {
				fs.warned[family] = true
				fs.c.logf("ERROR", "font family %s is not registered in the theme font catalog", family)
			}
			fs.failed[key] = true
			return nil
		}
	}
	path, ok := styles[style]
	if !ok {
		path, ok = styles["normal"]
		if !ok {
			for _, p := range styles {
				path, ok = p, true
				break
			}
		}
	}
	if !ok {
		fs.failed[key] = true
		return nil
	}
	// 同じファイルを指す別スタイルはフォントを共有する
	for k, f := range fs.loaded {
		if k.family == family && fs.catalog[family][k.style] == path {
			fs.loaded[key] = f
			return f
		}
	}
	raw, err := fs.read(path)
	if err != nil {
		fs.c.logf("ERROR", "could not load font %s (%s): %v", family, style, err)
		fs.failed[key] = true
		return nil
	}
	f, err := pdf.ParseFont(raw)
	if err != nil {
		fs.c.logf("ERROR", "could not parse font %s (%s): %v", path, style, err)
		fs.failed[key] = true
		return nil
	}
	fs.c.pdf.RegisterFont(f)
	fs.loaded[key] = f
	return f
}

// styleName は太字・斜体の組み合わせからスタイル名を返します。
func styleName(bold, italic bool) string {
	switch {
	case bold && italic:
		return "bold_italic"
	case bold:
		return "bold"
	case italic:
		return "italic"
	}
	return "normal"
}

// フォントメトリクス (Prawn::Font と同じ定義。1/1000 em 単位の整数に切り捨ててから拡大する)
func ascender(f *pdf.Font, size float64) float64 {
	return float64(f.Scaled1000(f.Ascent)) / 1000 * size
}

func descender(f *pdf.Font, size float64) float64 {
	return -float64(f.Scaled1000(f.Descent)) / 1000 * size
}

func lineGap(f *pdf.Font, size float64) float64 {
	return float64(f.Scaled1000(f.LineGap)) / 1000 * size
}

func fontHeight(f *pdf.Font, size float64) float64 {
	return ascender(f, size) + descender(f, size) + lineGap(f, size)
}
