// Package theme は Asciidoctor PDF のテーマ (YAML) を読み込みます。
// 本家の Asciidoctor::PDF::ThemeLoader と同じ規則 (extends、変数展開、数式、単位変換) に従います。
package theme

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/data"
)

// BundledDir は組み込みテーマのディレクトリを表す擬似パスです。
const BundledDir = "\x00bundled"

// Theme は平坦化されたキー (例: base_font_size) で値を保持するテーマデータです。
type Theme struct {
	table map[string]any
	// Dir はテーマファイルのあるディレクトリ (組み込みテーマなら BundledDir)
	Dir string
	// Warn は警告メッセージの出力先です。
	Warn func(msg string)
}

// FontCatalog はフォントファミリー名 → スタイル → パスの対応表です。
type FontCatalog map[string]map[string]string

func newTheme() *Theme { return &Theme{table: map[string]any{}} }

// Has はキーが定義されているかどうか (nil 値を含む) を返します。
func (t *Theme) Has(key string) bool {
	_, ok := t.table[key]
	return ok
}

// Get は生の値を返します。
func (t *Theme) Get(key string) any { return t.table[key] }

// Set は値を設定します。
func (t *Theme) Set(key string, val any) { t.table[key] = val }

// SetDefault は値が未設定 (nil) の場合だけ設定します。
func (t *Theme) SetDefault(key string, val any) {
	if t.table[key] == nil {
		t.table[key] = val
	}
}

// Keys は定義済みのキーを返します。
func (t *Theme) Keys() []string {
	keys := make([]string, 0, len(t.table))
	for k := range t.table {
		keys = append(keys, k)
	}
	return keys
}

// Clone はテーマの複製を返します。
func (t *Theme) Clone() *Theme {
	c := &Theme{table: make(map[string]any, len(t.table)), Dir: t.Dir, Warn: t.Warn}
	for k, v := range t.table {
		c.table[k] = v
	}
	return c
}

// Str は文字列値を返します。数値は文字列化されます。
func (t *Theme) Str(key string) string {
	switch v := t.table[key].(type) {
	case string:
		return v
	case float64:
		return FormatNumber(v)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// StrOr は文字列値を返し、未設定なら def を返します。
func (t *Theme) StrOr(key, def string) string {
	if t.table[key] == nil {
		return def
	}
	return t.Str(key)
}

// Num は数値を返します。数値でなければ ok は false です。
func (t *Theme) Num(key string) (float64, bool) {
	switch v := t.table[key].(type) {
	case float64:
		return v, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// Float は数値を返し、数値でなければ def を返します。
func (t *Theme) Float(key string, def float64) float64 {
	if v, ok := t.Num(key); ok {
		return v
	}
	return def
}

// List は配列値を返します。スカラーなら 1 要素の配列にします。
func (t *Theme) List(key string) []any {
	switch v := t.table[key].(type) {
	case nil:
		return nil
	case []any:
		return v
	default:
		return []any{v}
	}
}

// FontCatalog はフォントカタログを返します。
func (t *Theme) FontCatalog() FontCatalog {
	c, _ := t.table["font_catalog"].(FontCatalog)
	return c
}

// FontFallbacks はフォールバックフォントのファミリー名を返します。
func (t *Theme) FontFallbacks() []string {
	v, _ := t.table["font_fallbacks"].([]string)
	return v
}

// FormatNumber は数値を Ruby の to_s に近い形で文字列化します。
func FormatNumber(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

var deprecatedCategoryKeys = map[string]string{"blockquote": "quote", "key": "kbd", "literal": "codespan", "outline_list": "list"}

var deprecatedKeys = func() map[string]string {
	m := map[string]string{"table_caption_side": "table_caption_end"}
	for _, prefix := range strings.Fields("base heading heading_h1 heading_h2 heading_h3 heading_h4 heading_h5 heading_h6 title_page abstract abstract_title admonition_label sidebar_title toc_title") {
		m[prefix+"_align"] = prefix + "_text_align"
	}
	return m
}()

var paddingBottomHackKeys = map[string]bool{"example_padding": true, "quote_padding": true, "sidebar_padding": true, "verse_padding": true}

var (
	variableRx         = regexp.MustCompile(`\$([a-z0-9_-]+)`)
	loneVariableRx     = regexp.MustCompile(`^\$([a-z0-9_-]+)$`)
	multiplyDivideOpRx = regexp.MustCompile(`(-?\d+(?:\.\d+)?) +([*/^]) +(-?\d+(?:\.\d+)?)`)
	addSubtractOpRx    = regexp.MustCompile(`(-?\d+(?:\.\d+)?) +([+-]) +(-?\d+(?:\.\d+)?)`)
	precisionFuncRx    = regexp.MustCompile(`^(round|floor|ceil)\(`)
	measurementHintRx  = regexp.MustCompile(`\d(in|mm|cm|p[txc])`)
	insetMeasurementRx = regexp.MustCompile(`(^| |\()(-?\d+(?:\.\d+)?)(in|mm|cm|p[txc])($| |\))`)
	relativeUnitsRx    = regexp.MustCompile(`(\d)(r?em) `)
	leadingFloatRx     = regexp.MustCompile(`^\s*-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?`)
	roleAlignKeyRx     = regexp.MustCompile(`(?:_text)?_align$`)
)

// ToPt は単位付きの数値を pt に変換します。
func ToPt(n float64, units string) float64 {
	switch units {
	case "in":
		return n * 72
	case "mm":
		return n * (72 / 25.4)
	case "cm":
		return n * (720 / 25.4)
	case "px":
		return n * 0.75
	case "pc":
		return n * 12
	}
	return n
}

var measurementValueRx = regexp.MustCompile(`(\d+|\d*\.\d+)(in|mm|cm|p[txc])?$`)

// StrToPt は "0.5in" のような文字列を pt に変換します。
func StrToPt(val string) float64 {
	if m := measurementValueRx.FindStringSubmatch(val); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return ToPt(n, m[2])
	}
	f, _ := strconv.ParseFloat(val, 64)
	return f
}

func resolveMeasurementValues(s string) string {
	if !measurementHintRx.MatchString(s) {
		return s
	}
	// 後読み・先読みの代わりに、隣接する一致を取りこぼさないよう変化がなくなるまで繰り返す
	for {
		out := insetMeasurementRx.ReplaceAllStringFunc(s, func(m string) string {
			sm := insetMeasurementRx.FindStringSubmatch(m)
			n, _ := strconv.ParseFloat(sm[2], 64)
			return sm[1] + rubyFloat(ToPt(n, sm[3])) + sm[4]
		})
		if out == s {
			return out
		}
		s = out
	}
}

// rubyFloat は Float#to_s 相当の文字列を返します (整数値でも ".0" を付ける)。
func rubyFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// Load はテーマを読み込みます。name が空なら既定のテーマを読み込みます。
func Load(name, dir string, warn func(string)) (*Theme, error) {
	if warn == nil {
		warn = func(string) {}
	}
	l := &loader{warn: warn, loaded: map[string]bool{}}
	path, themeDir := resolveThemeFile(name, dir)
	if themeDir == BundledDir && strings.TrimSuffix(filepath.Base(path), "-theme.yml") == "base" {
		t, err := loadBase()
		if err != nil {
			return nil, err
		}
		t.Warn = warn
		return t, nil
	}
	t := newTheme()
	t.table["base_font_size"] = 12.0
	t, err := l.loadFile(path, themeDir, t, themeDir)
	if err != nil {
		return nil, err
	}
	if themeDir != BundledDir {
		t.SetDefault("base_text_align", "left")
		t.SetDefault("base_line_height", 1.0)
		t.SetDefault("base_font_color", "000000")
		codespan := t.StrOr("codespan_font_family", "Courier")
		t.SetDefault("code_font_family", codespan)
		t.SetDefault("conum_font_family", codespan)
		if hf := t.Str("heading_font_family"); hf != "" {
			t.SetDefault("abstract_title_font_family", hf)
			t.SetDefault("sidebar_title_font_family", hf)
		}
	}
	t.Dir = themeDir
	t.Warn = warn
	return t, nil
}

func loadBase() (*Theme, error) {
	raw, _ := data.Theme("base")
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	t := newTheme()
	for k, v := range m {
		t.table[k] = normalizeYAML(v)
	}
	t.Dir = BundledDir
	return t, nil
}

func normalizeYAML(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeYAML(e)
		}
		return out
	}
	return v
}

// resolveThemeFile はテーマ名からファイルパスとテーマディレクトリを決定します。
func resolveThemeFile(name, dir string) (string, string) {
	if strings.HasSuffix(name, ".yml") {
		if dir != "" && dir != BundledDir {
			dir = absPath(dir)
			if filepath.IsAbs(name) {
				return name, dir
			}
			return filepath.Join(dir, name), dir
		}
		p := absPath(name)
		return p, filepath.Dir(p)
	}
	if name == "" {
		name = "default"
	}
	if dir == "" || dir == BundledDir {
		return name + "-theme.yml", BundledDir
	}
	dir = absPath(dir)
	return filepath.Join(dir, name+"-theme.yml"), dir
}

func absPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

type loader struct {
	warn   func(string)
	loaded map[string]bool
}

func isBundledName(name string) bool {
	_, ok := data.Theme(name)
	return ok
}

func (l *loader) loadFile(path, fileDir string, t *Theme, themeDir string) (*Theme, error) {
	var raw []byte
	if fileDir == BundledDir {
		b, ok := data.Theme(strings.TrimSuffix(filepath.Base(path), "-theme.yml"))
		if !ok {
			return nil, fmt.Errorf("no such built-in theme: %s", path)
		}
		raw = b
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	if fileDir != BundledDir {
		raw = quoteHexColors(raw)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	l.loaded[fileDir+"\x00"+path] = true
	var doc *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = resolveAlias(root.Content[0])
	}
	if doc == nil || doc.Kind != yaml.MappingNode {
		return t, nil
	}
	// extends を先に処理する
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "extends" {
			continue
		}
		var list []string
		ext := resolveAlias(doc.Content[i+1])
		if ext.Kind == yaml.SequenceNode {
			for _, n := range ext.Content {
				list = append(list, n.Value)
			}
		} else if ext.Kind == yaml.ScalarNode && ext.Tag != "!!null" {
			list = append(list, ext.Value)
		}
		for _, extendPath := range list {
			force := false
			if strings.HasSuffix(extendPath, " !important") {
				force = true
				extendPath = strings.TrimSuffix(extendPath, " !important")
			}
			if extendPath == "base" {
				if !l.loaded["base"] || force {
					l.loaded["base"] = true
					base, err := loadBase()
					if err != nil {
						return nil, err
					}
					for k, v := range base.table {
						t.table[k] = v
					}
				}
				continue
			}
			var p, d string
			switch {
			case isBundledName(extendPath):
				p, d = resolveThemeFile(extendPath, BundledDir)
			case strings.HasPrefix(extendPath, "./"):
				if fileDir == BundledDir {
					p, d = resolveThemeFile(strings.TrimPrefix(extendPath, "./"), BundledDir)
				} else {
					p, d = resolveThemeFile(extendPath, fileDir)
				}
			default:
				p, d = resolveThemeFile(extendPath, themeDir)
			}
			key := d + "\x00" + p
			if !l.loaded[key] || force {
				var err error
				if t, err = l.loadFile(p, d, t, d); err != nil {
					return nil, err
				}
			}
		}
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		if key == "extends" {
			continue
		}
		l.processEntry(key, doc.Content[i+1], t, true)
	}
	return t, nil
}

var hexColorEntryRx = regexp.MustCompile(`^( *\S+): +(["']?)(#)?([0-9a-fA-F]{3,6})(["']?) *(?:#.*)?$`)

// quoteHexColors は「key: #RRGGBB」や「key: 000000」のような色指定を引用符付きに書き換えます。
// (引用符なしの # は YAML のコメント、数字だけの値は数値として解釈されてしまうため)
func quoteHexColors(raw []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i, line := range lines {
		m := hexColorEntryRx.FindStringSubmatch(line)
		if m == nil || m[2] != m[5] || m[4] == "null" {
			continue
		}
		if m[3] == "#" || strings.HasSuffix(m[1], "color") {
			lines[i] = m[1] + ": '" + m[4] + "'"
		} else {
			lines[i] = m[1] + ": " + m[4]
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// nodeValue は YAML ノードを Go の値に変換します。rawStrings が true ならスカラーを文字列のまま返します。
func nodeValue(n *yaml.Node, rawStrings bool) any {
	n = resolveAlias(n)
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		tag := n.ShortTag()
		if tag == "!!null" {
			return nil
		}
		if rawStrings || n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return n.Value
		}
		switch tag {
		case "!!int", "!!float":
			var f float64
			if err := n.Decode(&f); err == nil {
				return f
			}
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err == nil {
				return b
			}
		}
		return n.Value
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = nodeValue(c, rawStrings)
		}
		return out
	case yaml.MappingNode:
		out := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = nodeValue(n.Content[i+1], rawStrings)
		}
		return out
	}
	return nil
}

func (l *loader) processEntry(key string, n *yaml.Node, t *Theme, normalizeKey bool) {
	n = resolveAlias(n)
	if normalizeKey && strings.Contains(key, "-") {
		key = strings.ReplaceAll(key, "-", "_")
	}
	isMap := n != nil && n.Kind == yaml.MappingNode
	switch {
	case key == "font":
		if isMap {
			for i := 0; i+1 < len(n.Content); i += 2 {
				sub := n.Content[i].Value
				if sub == "catalog" || sub == "fallbacks" {
					l.processEntry(key+"_"+sub, n.Content[i+1], t, false)
				}
			}
		}
	case key == "font_catalog":
		if !isMap {
			t.table[key] = nil
			return
		}
		catalog := FontCatalog{}
		merge := false
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "merge" {
				if b, ok := nodeValue(n.Content[i+1], false).(bool); ok && b {
					merge = true
				}
			}
		}
		if merge {
			if prev, ok := t.table[key].(FontCatalog); ok {
				for name, styles := range prev {
					catalog[name] = styles
				}
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			name := n.Content[i].Value
			if name == "merge" {
				continue
			}
			sn := resolveAlias(n.Content[i+1])
			styles := map[string]string{}
			add := func(style, path string) {
				path = fmt.Sprint(l.expandVars(path, t))
				switch style {
				case "*":
					for _, s := range []string{"normal", "bold", "italic", "bold_italic"} {
						styles[s] = path
					}
				case "regular":
					styles["normal"] = path
				default:
					styles[style] = path
				}
			}
			if sn.Kind == yaml.ScalarNode {
				add("*", sn.Value)
			} else if sn.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(sn.Content); j += 2 {
					add(sn.Content[j].Value, resolveAlias(sn.Content[j+1]).Value)
				}
			}
			catalog[name] = styles
		}
		t.table[key] = catalog
	case key == "font_fallbacks":
		var list []string
		if n != nil && n.Kind == yaml.SequenceNode {
			for _, c := range n.Content {
				list = append(list, fmt.Sprint(l.expandVars(resolveAlias(c).Value, t)))
			}
		}
		t.table[key] = list
	case strings.HasPrefix(key, "admonition_icon_"):
		if isMap {
			m := map[string]any{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k2 := strings.ReplaceAll(n.Content[i].Value, "-", "_")
				if strings.HasSuffix(k2, "_color") {
					m[k2] = toColor(l.evaluate(nodeValue(n.Content[i+1], true), t, false))
				} else {
					m[k2] = l.evaluate(nodeValue(n.Content[i+1], false), t, true)
				}
			}
			t.table[key] = m
		}
	case isMap:
		if rekey, ok := deprecatedCategoryKeys[key]; ok {
			l.warn(fmt.Sprintf("the %s theme category is deprecated; use the %s category instead", strings.ReplaceAll(key, "_", "-"), strings.ReplaceAll(rekey, "_", "-")))
			key = rekey
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			sub := n.Content[i].Value
			if key != "role" && strings.Contains(sub, "-") {
				sub = strings.ReplaceAll(sub, "-", "_")
			}
			l.processEntry(key+"_"+sub, n.Content[i+1], t, false)
		}
	default:
		rekey, deprecated := deprecatedKeys[key]
		if !deprecated && strings.HasPrefix(key, "role_") && strings.HasSuffix(key, "_align") {
			rekey, deprecated = roleAlignKeyRx.ReplaceAllString(key, "_text_align"), true
		}
		switch {
		case deprecated:
			t.table[rekey] = l.evaluate(nodeValue(n, false), t, false)
		case paddingBottomHackKeys[key]:
			val := l.evaluate(nodeValue(n, false), t, true)
			if arr, ok := val.([]any); ok && len(arr) >= 3 {
				if toFloat(arr[0]) >= 0 && toFloat(arr[2]) <= 0 {
					arr[2] = arr[0]
				}
			}
			t.table[key] = val
		case strings.HasSuffix(key, "_color"):
			val := nodeValue(n, true)
			arr, isArr := val.([]any)
			if (key == "table_border_color" && isArr) || (key == "table_grid_color" && isArr && len(arr) == 2) {
				out := make([]any, len(arr))
				for i, e := range arr {
					out[i] = toColor(l.evaluate(e, t, false))
				}
				t.table[key] = out
			} else {
				if isArr {
					// CMYK / RGB 配列は数値として解釈する
					val = nodeValue(n, false)
				}
				t.table[key] = toColor(l.evaluate(val, t, false))
			}
		case strings.HasSuffix(key, "_content") || key == "kbd_separator":
			if key == "kbd_separator" {
				l.warn("the kbd-separator theme key is deprecated; use the kbd-separator-content key instead")
				key = "kbd_separator_content"
			}
			v := nodeValue(n, true)
			if v == nil {
				t.table[key] = ""
			} else {
				t.table[key] = fmt.Sprint(l.expandVars(fmt.Sprint(v), t))
			}
		default:
			t.table[key] = l.evaluate(nodeValue(n, false), t, true)
		}
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if m := leadingFloatRx.FindString(x); m != "" {
			f, _ := strconv.ParseFloat(strings.TrimSpace(m), 64)
			return f
		}
	}
	return 0
}

func (l *loader) evaluate(expr any, t *Theme, doMath bool) any {
	switch v := expr.(type) {
	case string:
		ev := l.expandVars(v, t)
		if doMath {
			return evaluateMath(ev)
		}
		return ev
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = l.evaluate(e, t, doMath)
		}
		return out
	}
	return expr
}

// expandVars は $var 形式の変数参照を展開します。単独の変数参照は元の型のまま返します。
func (l *loader) expandVars(expr string, t *Theme) any {
	idx := strings.IndexByte(expr, '$')
	if idx < 0 {
		return expr
	}
	if idx == 0 {
		if m := loneVariableRx.FindStringSubmatch(expr); m != nil {
			return l.resolveVar(t, expr, m[1])
		}
	} else if idx == 1 && expr[0] == '-' {
		if m := loneVariableRx.FindStringSubmatch(expr[1:]); m != nil {
			val := l.resolveVar(t, expr[1:], m[1])
			if f, ok := val.(float64); ok {
				return -f
			}
			return "-" + stringify(val)
		}
	}
	return variableRx.ReplaceAllStringFunc(expr, func(ref string) string {
		return stringify(l.resolveVar(t, ref, ref[1:]))
	})
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return FormatNumber(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = stringify(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(v)
}

func (l *loader) resolveVar(t *Theme, ref, name string) any {
	name = strings.ReplaceAll(name, "-", "_")
	if t.Has(name) {
		return t.table[name]
	}
	for old, repl := range deprecatedCategoryKeys {
		if strings.HasPrefix(name, old+"_") {
			if cand := repl + name[len(old):]; t.Has(cand) {
				return t.table[cand]
			}
		}
	}
	if repl, ok := deprecatedKeys[name]; ok && t.Has(repl) {
		return t.table[repl]
	}
	l.warn("unknown variable reference in PDF theme: " + ref)
	return ref
}

func evaluateMath(v any) any {
	expr, ok := v.(string)
	if !ok {
		return v
	}
	original := expr
	expr = resolveMeasurementValues(expr)
	units := ""
	if strings.Contains(expr, "em ") {
		if m := relativeUnitsRx.FindStringSubmatchIndex(expr); m != nil {
			units = expr[m[4]:m[5]]
			expr = expr[:m[4]] + expr[m[5]:]
		}
	}
	for strings.ContainsAny(expr, "*/^") {
		result := multiplyDivideOpRx.ReplaceAllStringFunc(expr, func(m string) string {
			sm := multiplyDivideOpRx.FindStringSubmatch(m)
			a, _ := strconv.ParseFloat(sm[1], 64)
			b, _ := strconv.ParseFloat(sm[3], 64)
			switch sm[2] {
			case "*":
				return rubyFloat(a * b)
			case "/":
				return rubyFloat(a / b)
			}
			return rubyFloat(math.Pow(a, b))
		})
		if result == expr {
			break
		}
		expr = result
	}
	for strings.ContainsAny(expr, "+-") {
		result := addSubtractOpRx.ReplaceAllStringFunc(expr, func(m string) string {
			sm := addSubtractOpRx.FindStringSubmatch(m)
			a, _ := strconv.ParseFloat(sm[1], 64)
			b, _ := strconv.ParseFloat(sm[3], 64)
			if sm[2] == "+" {
				return rubyFloat(a + b)
			}
			return rubyFloat(a - b)
		})
		if result == expr {
			break
		}
		expr = result
	}
	var num float64
	isNum := false
	if strings.HasSuffix(expr, ")") {
		if m := precisionFuncRx.FindStringSubmatch(expr); m != nil {
			inner := toFloat(expr[len(m[1])+1 : len(expr)-1])
			switch m[1] {
			case "round":
				num = math.Round(inner)
			case "floor":
				num = math.Floor(inner)
			case "ceil":
				num = math.Ceil(inner)
			}
			isNum = true
		}
	}
	if !isNum {
		if expr == original {
			// 変化がなければ元の値のまま (数値リテラル文字列は数値化しない)
			return original
		}
		num = toFloat(expr)
	}
	if units != "" {
		return FormatNumber(num) + units
	}
	return num
}

// toColor は色の値を正規化します。戻り値は "RRGGBB" 文字列、"transparent"、CMYK 配列 ([]float64)、または nil です。
func toColor(v any) any {
	var value string
	switch x := v.(type) {
	case nil:
		return nil
	case []float64:
		return x
	case []any:
		switch len(x) {
		case 4:
			out := make([]float64, 4)
			for i, e := range x {
				switch n := e.(type) {
				case float64:
					if n <= 1 {
						n *= 100
					}
					out[i] = n
				default:
					out[i] = toFloat(strings.TrimSuffix(fmt.Sprint(e), "%"))
				}
			}
			if out[0] == 0 && out[1] == 0 && out[2] == 0 && out[3] == 0 {
				return "FFFFFF"
			}
			if out[0] == 100 && out[1] == 100 && out[2] == 100 && out[3] == 100 {
				return "000000"
			}
			return out
		case 3:
			var b strings.Builder
			for _, e := range x {
				fmt.Fprintf(&b, "%02X", int(toFloat(e)))
			}
			return b.String()
		default:
			for _, e := range x {
				value += stringify(e)
			}
		}
	case string:
		if x == "transparent" {
			return x
		}
		value = x
	case float64:
		value = FormatNumber(x)
	default:
		value = fmt.Sprint(v)
	}
	value = strings.TrimPrefix(value, "#")
	switch {
	case len(value) == 6:
	case len(value) == 3:
		value = string([]byte{value[0], value[0], value[1], value[1], value[2], value[2]})
	case len(value) > 6:
		value = value[:6]
	default:
		value = strings.Repeat("0", 6-len(value)) + value
	}
	return strings.ToUpper(value)
}
