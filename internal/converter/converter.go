package converter

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/data"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

// Version は移植元の Asciidoctor PDF のバージョンです (main ブランチ 1973bab 時点)。
const Version = "3.0.0.alpha.0"

// Block は AsciiDoc のブロックです。
type Block = asciidoc.Block

// Converter は AsciiDoc 文書を PDF に変換します。
type Converter struct {
	doc       *asciidoc.Document
	theme     *theme.Theme
	themesDir string
	pdf       *pdf.Document
	fonts     *fontSet
	// Log はメッセージの出力先です。
	Log func(severity, msg string)

	tagProps  map[string]props
	roleProps map[string]props

	// ページ寸法と余白 (上・右・下・左)
	pageW, pageH float64
	marginRecto  [4]float64
	marginVerso  [4]float64
	margin       [4]float64
	// 現在の描画領域
	left, width float64
	top, bottom float64
	y           float64
	page        int
	inkCount    []int

	// フォントの状態
	fontFamily          string
	fontSize            float64
	fontBold            bool
	fontItalic          bool
	fontColor           clr
	rootFontSize        float64
	fontScale           float64
	baseLineHeight      float64
	baseTextAlign       string
	textTransform       string
	textDecorationWidth float64
	kerning             bool
	fallbackFonts       []string
	cjkLineBreaks       bool

	scratch      int
	watchers     []*watcher
	missingChars map[rune]bool

	media            string
	ppbook           bool
	folioInverted    bool
	folioPhysical    bool
	pageBgColor      clr
	listNumerals     []listNumeral
	listBullets      []string
	bottomGutters    []map[*Block]float64
	renderedFootnote []*asciidoc.Footnote
	sectionIndent    [2]float64
	hasSectionIndent bool
	conumGlyphs      []rune
	tocMaxDigits     int
	sectInfo         map[*Block]*sectInfo
	numberedTitles   map[*Block][2]string
	images           map[string]*loadedImage
	bibrefRefs       map[string]bool
	resolvingXref    bool
	tocExtent        *extent
	index            *indexCatalog
	noRunning        map[string]map[int]bool
	jailDir          string
	now              time.Time
	floatBox         *floatBox
	columns          *columnBox
	svgWarned        map[string]bool
	pdfSources       map[string]*pdfSource
	pageBg           pageBackground
	// concealTop が true の間はページ先頭でも余白を入れる (conceal_page_top 相当)
	concealTop bool
	// lastLine は最後に描画した行の位置 (目次の点線リーダーの配置に使う)
	lastLine *linePos
}

type linePos struct {
	page  int
	right float64
	top   float64
}

type sectInfo struct {
	pageStart int // 1 始まり
	pageEnd   int
	anchor    string
	destPage  int // 0 始まり
	destY     float64
}

type listNumeral struct {
	kind  string // "int" "alpha" "roman" "greek" "none" "nil"
	n     int
	upper bool
	pad   bool
}

// New はコンバーターを生成します。
func New(log func(severity, msg string)) *Converter {
	if log == nil {
		log = func(string, string) {}
	}
	return &Converter{Log: log, missingChars: map[rune]bool{}, sectInfo: map[*Block]*sectInfo{}, numberedTitles: map[*Block][2]string{},
		images: map[string]*loadedImage{}, bibrefRefs: map[string]bool{}, svgWarned: map[string]bool{}, fontScale: 1, kerning: true,
		noRunning: map[string]map[int]bool{"header": {}, "footer": {}}}
}

func (c *Converter) logf(severity, format string, args ...any) {
	if c.scratch > 0 && severity != "ERROR" {
		return
	}
	c.Log(severity, fmt.Sprintf(format, args...))
}

// ---------------------------------------------------------------------------
// テーマ

func (c *Converter) loadTheme(doc *asciidoc.Document) *theme.Theme {
	if c.theme != nil {
		return c.theme
	}
	warn := func(msg string) { c.Log("WARN", msg) }
	root := doc
	for root.ParentDoc != nil {
		root = root.ParentDoc
	}
	docdir := root.Attr("docdir")
	themesDir := ""
	if v, ok := root.Attributes["pdf-themesdir"]; ok {
		themesDir = strings.ReplaceAll(v, "{docdir}", docdir)
	}
	name, hasName := root.Attributes["pdf-theme"]
	if !hasName {
		// 旧属性名との互換
		if v, ok := root.Attributes["pdf-style"]; ok {
			name, hasName = v, true
		}
		if v, ok := root.Attributes["pdf-stylesdir"]; ok && themesDir == "" {
			themesDir = strings.ReplaceAll(v, "{docdir}", docdir)
		}
	}
	var t *theme.Theme
	var err error
	switch {
	case hasName:
		t, err = theme.Load(name, themesDir, warn)
	case root.AttrOr("media", "screen") == "screen":
		t, err = theme.Load("", "", warn)
	default:
		t, err = theme.Load("default-for-print", "", warn)
	}
	if err != nil {
		if themesDir != "" {
			c.Log("ERROR", fmt.Sprintf("could not locate or load the pdf theme `%s' in %s because of %v; reverting to default theme", name, themesDir, err))
		} else {
			c.Log("ERROR", fmt.Sprintf("could not locate or load the built-in pdf theme `%s' because of %v; reverting to default theme", name, err))
		}
		t, _ = theme.Load("", "", warn)
	}
	c.themesDir = t.Dir
	c.prepareTheme(t)
	c.theme = t
	c.buildThemeSettings()
	return t
}

func (c *Converter) prepareTheme(t *theme.Theme) {
	if t.Str("base_border_color") == "transparent" {
		t.Set("base_border_color", nil)
	}
	t.SetDefault("base_font_color", "000000")
	t.SetDefault("base_font_family", "Helvetica")
	t.SetDefault("base_font_style", "normal")
	t.SetDefault("page_numbering_start_at", "body")
	t.SetDefault("running_content_start_at", "body")
	t.SetDefault("heading_chapter_break_before", "always")
	t.SetDefault("heading_part_break_before", "always")
	for _, k := range []string{"heading_margin_page_top", "heading_margin_top", "heading_margin_bottom", "prose_text_indent", "prose_text_indent_inner",
		"prose_margin_bottom", "block_margin_bottom", "list_indent", "list_item_spacing", "description_list_term_spacing",
		"description_list_description_indent", "image_border_width", "callout_list_margin_top_after_code", "footnotes_item_spacing",
		"toc_indent", "toc_hanging_indent"} {
		t.SetDefault(k, 0.0)
	}
	borderColor := t.Get("base_border_color")
	if borderColor == nil {
		borderColor = "000000"
	}
	t.SetDefault("table_border_color", borderColor)
	t.SetDefault("table_border_width", 0.5)
	t.SetDefault("thematic_break_border_color", borderColor)
	t.SetDefault("code_linenum_font_color", "999999")
	t.SetDefault("role_unresolved_font_color", "FF0000")
	t.SetDefault("footnotes_margin_top", "auto")
	t.SetDefault("index_columns", 2.0)
	t.SetDefault("index_column_gap", t.Float("base_font_size", 12))
	t.SetDefault("kbd_separator_content", "+"+zwsp)
	t.SetDefault("menu_caret_content", nbsp+"› ")
	t.SetDefault("title_page_authors_delimiter", ", ")
}

// quotes は引用符 (二重の開き・閉じ、単一の開き・閉じ) を返します。
func (c *Converter) quotes() [4]string {
	q := [4]string{"&#8220;", "&#8221;", "&#8216;", "&#8217;"}
	if list := c.theme.List("quotes"); len(list) > 0 {
		for i := 0; i < 4 && i < len(list); i++ {
			if s, ok := list[i].(string); ok && s != "" {
				q[i] = s
			}
		}
	}
	return q
}

func (c *Converter) themeClr(key string) clr { return toClr(c.theme.Get(key)) }

// resolveThemeColor はテーマの色を解決します (transparent は透明用の代替色になります)。
func (c *Converter) resolveThemeColor(key string, fallback, transparent clr) clr {
	v := c.theme.Get(key)
	if v == nil {
		return fallback
	}
	col := toClr(v)
	if col.transparent {
		return transparent
	}
	return col
}

// expandPadding は余白の省略記法 (1〜4 要素) を上・右・下・左に展開します。
func expandPadding(v any) [4]float64 {
	num := func(x any) float64 {
		switch n := x.(type) {
		case float64:
			return n
		case string:
			return theme.StrToPt(n)
		}
		return 0
	}
	switch x := v.(type) {
	case nil:
		return [4]float64{}
	case []any:
		switch len(x) {
		case 0:
			return [4]float64{}
		case 1:
			a := num(x[0])
			return [4]float64{a, a, a, a}
		case 2:
			a, b := num(x[0]), num(x[1])
			return [4]float64{a, b, a, b}
		case 3:
			return [4]float64{num(x[0]), num(x[1]), num(x[2]), num(x[1])}
		default:
			return [4]float64{num(x[0]), num(x[1]), num(x[2]), num(x[3])}
		}
	default:
		a := num(v)
		return [4]float64{a, a, a, a}
	}
}

func (c *Converter) themePadding(key string) [4]float64 { return expandPadding(c.theme.Get(key)) }

// ---------------------------------------------------------------------------
// ページ管理

var pageSizes = map[string][2]float64{
	"A0": {2383.94, 3370.39}, "A1": {1683.78, 2383.94}, "A2": {1190.55, 1683.78}, "A3": {841.89, 1190.55},
	"A4": {595.28, 841.89}, "A5": {419.53, 595.28}, "A6": {297.64, 419.53}, "A7": {209.76, 297.64}, "A8": {147.40, 209.76},
	"B0": {2834.65, 4008.19}, "B1": {2004.09, 2834.65}, "B2": {1417.32, 2004.09}, "B3": {1000.63, 1417.32},
	"B4": {708.66, 1000.63}, "B5": {498.90, 708.66}, "B6": {354.33, 498.90},
	"C4": {649.13, 918.43}, "C5": {459.21, 649.13}, "C6": {323.15, 459.21},
	"EXECUTIVE": {521.86, 756.0}, "FOLIO": {612.0, 936.0}, "LEGAL": {612.0, 1008.0}, "LETTER": {612.0, 792.0}, "TABLOID": {792.0, 1224.0},
}

func (c *Converter) resolvePageSize() (float64, float64) {
	w, h := pageSizes["A4"][0], pageSizes["A4"][1]
	parseDim := func(s string) (float64, bool) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, false
		}
		v := theme.StrToPt(s)
		return v, v > 0
	}
	apply := func(spec any) bool {
		switch x := spec.(type) {
		case string:
			if sz, ok := pageSizes[strings.ToUpper(x)]; ok {
				w, h = sz[0], sz[1]
				return true
			}
			parts := strings.FieldsFunc(strings.Trim(x, "[] "), func(r rune) bool { return r == 'x' || r == ',' })
			if len(parts) == 2 {
				a, ok1 := parseDim(parts[0])
				b, ok2 := parseDim(parts[1])
				if ok1 && ok2 {
					w, h = a, b
					return true
				}
			}
		case []any:
			if len(x) >= 1 {
				dim := func(v any) (float64, bool) {
					switch n := v.(type) {
					case float64:
						return n, n > 0
					case string:
						return parseDim(n)
					}
					return 0, false
				}
				a, ok1 := dim(x[0])
				b, ok2 := a, ok1
				if len(x) > 1 {
					b, ok2 = dim(x[1])
				}
				if ok1 && ok2 {
					w, h = a, b
					return true
				}
			}
		}
		return false
	}
	if v, ok := c.doc.Attributes["pdf-page-size"]; !ok || !apply(v) {
		apply(c.theme.Get("page_size"))
	}
	layout := c.doc.AttrOr("pdf-page-layout", c.theme.Str("page_layout"))
	if (layout == "landscape" && h > w) || (layout != "landscape" && layout != "" && w > h) {
		w, h = h, w
	}
	return w, h
}

func (c *Converter) resolvePageMargin(v any) ([4]float64, bool) {
	switch x := v.(type) {
	case nil:
		return [4]float64{}, false
	case string:
		s := strings.Trim(strings.TrimSpace(x), "[]")
		if s == "" {
			return [4]float64{}, false
		}
		var parts []any
		for _, p := range strings.Split(s, ",") {
			parts = append(parts, strings.TrimSpace(p))
		}
		return expandPadding(parts), true
	}
	return expandPadding(v), true
}

// pageSide は 1 始まりのページ番号が recto (右ページ) かどうかを返します。
func (c *Converter) isRecto(pgnum int) bool {
	recto := pgnum%2 == 1
	if c.folioInverted {
		return !recto
	}
	return recto
}

func (c *Converter) pageNumber() int { return c.page + 1 }

func (c *Converter) pageCount() int { return len(c.pdf.Pages) }

// startNewPage は新しいページを追加してそこへ移動します。
func (c *Converter) startNewPage() {
	c.startNewPageDiscretely()
	if c.pageBgColor.set && c.pageBgColor.hex != "FFFFFF" && c.scratch == 0 {
		g := pdf.NewG().Save().FillColor(c.pageBgColor.pdf()).Rect(0, 0, c.pageW, c.pageH).Fill().Restore()
		c.pdf.Pages[c.page].Add(g.Bytes())
	}
	c.applyPageBackground()
}

// goToPage は既存のページ (0 始まり) へ移動し、余白と描画領域を設定します。
func (c *Converter) goToPage(idx int) {
	indentL := c.left - c.margin[3]
	indentR := (c.pageW - c.margin[1]) - (c.left + c.width)
	if c.pageCount() == 0 || c.margin == ([4]float64{}) && c.width == 0 {
		indentL, indentR = 0, 0
	}
	c.page = idx
	if c.media == "prepress" {
		if c.isRecto(idx + 1) {
			c.margin = c.marginRecto
		} else {
			c.margin = c.marginVerso
		}
	} else {
		c.margin = c.marginRecto
	}
	c.top = c.pageH - c.margin[0]
	c.bottom = c.margin[2]
	c.left = c.margin[3] + indentL
	c.width = c.pageW - c.margin[1] - indentR - c.left
	c.y = c.top
}

type watcher struct {
	page  int
	armed bool
}

type stopSignal struct{ w *watcher }

// advancePage は次のページへ進みます (最終ページなら新しいページを作ります)。
func (c *Converter) advancePage() {
	for _, w := range c.watchers {
		if w.armed && w.page == c.page && c.inkCount[c.page] == 0 {
			panic(stopSignal{w})
		}
	}
	if col := c.columns; col != nil && col.cur < col.count-1 {
		// 段組み中は次の段へ進む
		col.cur++
		c.left = col.left + float64(col.cur)*(col.width+col.gap)
		c.y = col.top
		return
	}
	if c.page < len(c.pdf.Pages)-1 {
		c.goToPage(c.page + 1)
	} else {
		c.startNewPage()
	}
	if col := c.columns; col != nil {
		col.cur = 0
		col.top = c.top
		c.left, c.width = col.left, col.width
	}
}

func (c *Converter) inked() {
	if c.page < len(c.inkCount) {
		c.inkCount[c.page]++
	}
}

// draw は現在のページに描画内容を追加します (下書き中は記録のみ)。
func (c *Converter) draw(g *pdf.G) {
	if g.Len() == 0 {
		return
	}
	c.inked()
	if c.scratch == 0 {
		c.pdf.Pages[c.page].Add(g.Bytes())
	}
}

func (c *Converter) pageEmpty() bool { return c.inkCount[c.page] == 0 }

func (c *Converter) cursor() float64 { return c.y - c.bottom }

func (c *Converter) atPageTop() bool {
	if c.concealTop {
		return false
	}
	if c.columns != nil {
		return math.Abs(c.y-c.columns.top) < 1e-6
	}
	return math.Abs(c.y-c.top) < 1e-6
}

// columnBox は段組みの状態です。
type columnBox struct {
	count, cur  int
	left, width float64
	gap, top    float64
}

// withColumns は現在位置から段組みで fn を実行します。
func (c *Converter) withColumns(count int, gap float64, fn func()) {
	if count < 2 || c.columns != nil {
		fn()
		return
	}
	savedLeft, savedWidth := c.left, c.width
	colWidth := (c.width - gap*float64(count-1)) / float64(count)
	c.columns = &columnBox{count: count, left: c.left, width: colWidth, gap: gap, top: c.y}
	c.width = colWidth
	startPage := c.page
	fn()
	endY, endPage, endCol := c.y, c.page, c.columns.cur
	c.columns = nil
	c.left, c.width = savedLeft, savedWidth
	if endPage == startPage && endCol > 0 {
		// 複数段を使った場合はページ下端まで占有したものとして扱う
		c.y = c.bottom
	} else {
		c.y = endY
	}
}

func (c *Converter) atPageTopWithGap(gap float64) bool { return math.Abs(c.y+gap-c.top) < 1e-6 }

func (c *Converter) moveDown(n float64) { c.y -= n }

func (c *Converter) moveCursorTo(cursor float64) { c.y = c.bottom + cursor }

// marginSpace は余白を挿入します。ページ先頭では挿入せず、収まらなければ改ページします。
func (c *Converter) marginSpace(amount float64) float64 {
	if amount == 0 || c.atPageTop() {
		return 0
	}
	if c.cursor() > amount {
		c.moveDown(amount)
		return amount
	}
	c.advancePage()
	return 0
}

func (c *Converter) themeMargin(category, side string, next bool) {
	if !next {
		return
	}
	c.marginSpace(c.theme.Float(category+"_margin_"+side, 0))
}

func (c *Converter) indent(left, right float64, fn func()) {
	c.left += left
	c.width -= left + right
	fn()
	c.left -= left
	c.width += left + right
}

// float は実行後に位置 (ページとカーソル) を元に戻します。
func (c *Converter) float(fn func()) {
	page, y := c.page, c.y
	fn()
	if c.page != page {
		c.goToPage(page)
	}
	c.y = y
}

func (c *Converter) indentSection(fn func()) {
	if c.hasSectionIndent {
		c.indent(c.sectionIndent[0], c.sectionIndent[1], fn)
	} else {
		fn()
	}
}

func (c *Converter) outdentSection(enabled bool, fn func()) {
	if enabled && c.hasSectionIndent {
		c.indent(-c.sectionIndent[0], -c.sectionIndent[1], fn)
	} else {
		fn()
	}
}

// ---------------------------------------------------------------------------
// 下書き実行 (dry run)

type extent struct {
	curPage  int
	curY     float64
	fromPage int
	fromY    float64
	toPage   int
	toY      float64
}

func (e *extent) singlePage() bool { return e.fromPage == e.toPage }

type snapshot struct {
	c        Converter
	numPages int
	ink      []int
	numerals []listNumeral
	bullets  []string
}

func (c *Converter) snapshot() *snapshot {
	s := &snapshot{c: *c, numPages: len(c.pdf.Pages)}
	s.ink = append([]int(nil), c.inkCount...)
	s.numerals = append([]listNumeral(nil), c.listNumerals...)
	s.bullets = append([]string(nil), c.listBullets...)
	return s
}

func (c *Converter) restore(s *snapshot) {
	pdfDoc := c.pdf
	*c = s.c
	c.pdf = pdfDoc
	c.pdf.Pages = c.pdf.Pages[:s.numPages]
	c.inkCount = append([]int(nil), s.ink...)
	c.listNumerals = append([]listNumeral(nil), s.numerals...)
	c.listBullets = append([]string(nil), s.bullets...)
}

func (c *Converter) runWatched(w *watcher, fn func()) (restart bool) {
	defer func() {
		if r := recover(); r != nil {
			if sig, ok := r.(stopSignal); ok && sig.w == w {
				restart = true
				return
			}
			panic(r)
		}
	}()
	fn()
	return false
}

// dryRun は fn を下書き実行して占有範囲を測ります。
// 戻り値は開始までに進めるページ数、開始カーソル位置 (y)、終了までのページ数差、終了 y です。
func (c *Converter) dryRun(keepTogether bool, fn func()) (advance int, fromY float64, pages int, toY float64) {
	snap := c.snapshot()
	for {
		c.scratch = snap.c.scratch + 1
		if keepTogether || advance > 0 {
			// 新しいページの先頭から測る
			c.pdf.AddPage(c.pageW, c.pageH)
			c.inkCount = append(c.inkCount, 0)
			c.goToPage(len(c.pdf.Pages) - 1)
		}
		startPage := c.page
		fromY = c.y
		w := &watcher{page: c.page, armed: !c.atPageTop()}
		c.watchers = append(c.watchers, w)
		restart := c.runWatched(w, fn)
		if restart {
			c.restore(snap)
			advance++
			continue
		}
		pages = c.page - startPage
		toY = c.y
		break
	}
	c.restore(snap)
	return
}

// arrangeBlock はブロックの占有範囲を測ってから fn を実行します。
// 下書き中は範囲 (extent) なしで実行されます。
func (c *Converter) arrangeBlock(node *Block, fn func(ext *extent)) {
	keepTogether := node != nil && node.Option("unbreakable") && !c.atPageTop()
	measure := func() { fn(nil) }
	advance, fromY, pages, toY := c.dryRun(keepTogether, measure)
	if keepTogether && advance == 0 && pages > 0 {
		// 1 ページに収まらないブロックは分割を許して測り直す
		keepTogether = false
		advance, fromY, pages, toY = c.dryRun(false, measure)
	}
	ext := &extent{curPage: c.page, curY: c.y}
	switch {
	case advance > 0:
		for i := 0; i < advance; i++ {
			c.advancePage()
		}
	case keepTogether:
		// ページ先頭から測った高さが現在のページの残りに収まらなければ次ページへ送る
		height := fromY - toY
		if height > c.cursor() {
			c.advancePage()
		}
		toY = c.y - height
	}
	ext.fromPage, ext.fromY = c.page, c.y
	ext.toPage, ext.toY = c.page+pages, toY
	if c.scratch > 0 {
		fn(nil)
	} else {
		fn(ext)
	}
}

// ---------------------------------------------------------------------------
// フォント

func (c *Converter) currentFont() *pdf.Font {
	return c.fonts.get(c.fontFamily, styleName(c.fontBold, c.fontItalic))
}

func (c *Converter) setFontStyle(style string) {
	switch style {
	case "bold":
		c.fontBold, c.fontItalic = true, false
	case "italic":
		c.fontBold, c.fontItalic = false, true
	case "bold_italic":
		c.fontBold, c.fontItalic = true, true
	case "normal_italic":
		c.fontBold, c.fontItalic = false, true
	case "normal":
		c.fontBold, c.fontItalic = false, false
	}
}

type fontState struct {
	family       string
	size         float64
	bold, italic bool
	color        clr
	lineHeight   float64
	transform    string
	kerning      bool
}

func (c *Converter) saveFont() fontState {
	return fontState{c.fontFamily, c.fontSize, c.fontBold, c.fontItalic, c.fontColor, c.baseLineHeight, c.textTransform, c.kerning}
}

func (c *Converter) restoreFont(s fontState) {
	c.fontFamily, c.fontSize, c.fontBold, c.fontItalic = s.family, s.size, s.bold, s.italic
	c.fontColor, c.baseLineHeight, c.textTransform, c.kerning = s.color, s.lineHeight, s.transform, s.kerning
}

// themeFont はテーマのカテゴリに対応するフォント設定を適用して fn を実行します。
// level が 0 以上なら見出しレベル (h1〜h6) 用のキーも参照します。
func (c *Converter) themeFont(category string, level int, fn func()) {
	t := c.theme
	saved := c.saveFont()
	var family, style, transform string
	var size any
	var color any
	var lineHeight, kern any
	scale := false
	if level >= 0 {
		hcat := fmt.Sprintf("%s_h%d", category, level)
		pick := func(suffix string) any {
			if v := t.Get(hcat + suffix); v != nil {
				return v
			}
			return t.Get(category + suffix)
		}
		family, _ = pick("_font_family").(string)
		if family == "" {
			family = t.Str("base_font_family")
		}
		if size = pick("_font_size"); size == nil {
			size = c.rootFontSize
			scale = true
		} else if _, isStr := size.(string); !isStr {
			scale = true
		}
		style, _ = pick("_font_style").(string)
		color = pick("_font_color")
		kern = pick("_font_kerning")
		lineHeight = pick("_line_height")
		transform, _ = pick("_text_transform").(string)
	} else {
		family = t.Str(category + "_font_family")
		if family == "" {
			family = c.fontFamily
		}
		if size = t.Get(category + "_font_size"); size == nil {
			size = c.fontSize
		} else if _, isStr := size.(string); !isStr {
			scale = true
		}
		style = t.Str(category + "_font_style")
		color = t.Get(category + "_font_color")
		kern = t.Get(category + "_font_kerning")
		lineHeight = t.Get(category + "_line_height")
		transform = t.Str(category + "_text_transform")
	}
	if color != nil {
		c.fontColor = toClr(color)
	}
	if k, ok := kern.(string); ok {
		c.kerning = k != "none"
	}
	if lh, ok := lineHeight.(float64); ok {
		c.baseLineHeight = lh
	}
	if transform != "" {
		if transform == "none" {
			c.textTransform = ""
		} else {
			c.textTransform = transform
		}
	}
	c.fontFamily = family
	c.fontSize = c.resolveSize(size, c.fontSize)
	if style != "" {
		c.setFontStyle(style)
	}
	if scale {
		c.fontSize *= c.fontScale
	}
	fn()
	c.restoreFont(saved)
}

type fontCat struct {
	name  string
	level int
}

func (c *Converter) themeFontCascade(cats []fontCat, fn func()) {
	seen := map[fontCat]bool{}
	var uniq []fontCat
	for _, cat := range cats {
		if !seen[cat] {
			seen[cat] = true
			uniq = append(uniq, cat)
		}
	}
	var run func(i int)
	run = func(i int) {
		if i == len(uniq) {
			fn()
			return
		}
		c.themeFont(uniq[i].name, uniq[i].level, func() { run(i + 1) })
	}
	run(0)
}

type lineMetrics struct {
	height, leading, padTop, padBottom float64
}

func (c *Converter) calcLineMetrics(lineHeight float64) lineMetrics {
	f := c.currentFont()
	gap := 0.0
	if f != nil {
		gap = lineGap(f, c.fontSize)
	}
	h := lineHeight * c.fontSize
	leading := h - c.fontSize
	return lineMetrics{height: h, leading: leading, padTop: leading/2 + gap, padBottom: leading / 2}
}

// ---------------------------------------------------------------------------
// テキストの配置

type prose struct {
	margin, marginTop, marginBottom *float64
	align                           string
	lineHeight                      float64
	transform                       *string
	anchor                          string
	anchorSelf                      bool
	noNormalize                     bool
	// styles は現在のフォントスタイルを断片に継承させるか
	inheritStyles       bool
	bold, italic        bool
	underline, strike   bool
	decoColor           clr
	decoWidth           float64
	color               clr
	hangingIndent       float64
	indentParagraphs    float64
	bottomGutter        float64
	normalizeLineHeight bool
	singleLine          bool
	plain               bool
	charSpacing         float64
	noKerning           bool
	firstLine           *firstLineOpts
	// size は配置時だけ適用するフォントサイズ (autofit 用)
	size float64
}

func fptr(v float64) *float64 { return &v }

func (c *Converter) proseFrags(s string, o prose) []frag {
	var inherited *frag
	if o.inheritStyles || o.bold || o.italic || o.underline || o.strike {
		inherited = &frag{bold: o.bold, italic: o.italic, underline: o.underline, strike: o.strike, decoColor: o.decoColor, decoWidth: o.decoWidth}
		if o.inheritStyles {
			inherited.bold = inherited.bold || c.fontBold
			inherited.italic = inherited.italic || c.fontItalic
		}
	}
	if c.cjkLineBreaks {
		s = insertCJKBreaks(s)
	}
	if o.plain {
		f := frag{text: s}
		if inherited != nil {
			f = *inherited
			f.text = s
		}
		return []frag{f}
	}
	return c.parseText(s, !o.noNormalize, inherited)
}

func isCJK(r rune) bool {
	return r == 0x3000 || (r >= 0x30a0 && r <= 0x30ff) || (r >= 0x3040 && r <= 0x309f) || (r >= 0x4e00 && r <= 0x9fff) ||
		(r >= 0x3400 && r <= 0x4dbf) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xff00 && r <= 0xffef) || (r >= 0x20000 && r <= 0x2ffff)
}

// insertCJKBreaks は CJK 文字の前にゼロ幅スペースを挿入します (scripts=cjk)。
func insertCJKBreaks(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag && isCJK(r):
			b.WriteString(zwsp)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c *Converter) proseTextOpts(o prose, lm lineMetrics) textOpts {
	align := o.align
	if align == "" {
		align = c.baseTextAlign
	}
	return textOpts{align: align, leading: lm.leading, normalizeLineHeight: o.normalizeLineHeight, charSpacing: o.charSpacing,
		singleLine: o.singleLine, bottomGutter: o.bottomGutter, indentFirst: o.indentParagraphs, noKerning: o.noKerning}
}

// inkProse は文章を現在位置に配置します (Asciidoctor PDF の ink_prose 相当)。
func (c *Converter) inkProse(s string, o prose) {
	top, bottom := 0.0, c.theme.Float("prose_margin_bottom", 0)
	if o.margin != nil {
		top, bottom = *o.margin, *o.margin
	} else {
		if o.marginTop != nil {
			top = *o.marginTop
		}
		if o.marginBottom != nil {
			bottom = *o.marginBottom
		}
	}
	transform := c.textTransform
	if o.transform != nil {
		transform = *o.transform
	}
	if transform != "" && transform != "none" {
		s = transformText(s, transform)
	}
	if o.anchorSelf {
		s = "<a>" + s + "</a>"
	} else if o.anchor != "" {
		s = `<a anchor="` + o.anchor + `">` + s + "</a>"
	}
	c.marginSpace(top)
	lh := o.lineHeight
	if lh == 0 {
		lh = c.baseLineHeight
	}
	lm := c.calcLineMetrics(lh)
	frags := c.proseFrags(s, o)
	c.typesetFragments(frags, lm, o)
	c.marginSpace(bottom)
}

func (c *Converter) typesetFragments(frags []frag, lm lineMetrics, o prose) {
	to := c.proseTextOpts(o, lm)
	saved := c.fontColor
	if o.color.set {
		c.fontColor = o.color
	}
	savedSize := c.fontSize
	if o.size > 0 {
		c.fontSize = o.size
	}
	switch {
	case o.hangingIndent > 0:
		to.indentFirst = -o.hangingIndent
		c.indent(o.hangingIndent, 0, func() { c.fillText(frags, to, lm.padTop) })
	case o.firstLine != nil:
		c.fillTextFirstLine(frags, to, lm.padTop, *o.firstLine)
	default:
		c.fillText(frags, to, lm.padTop)
	}
	c.fontSize = savedSize
	c.fontColor = saved
	c.moveDown(lm.padBottom)
}

// heightOfTypesetText は文章を配置したときの高さを返します。
func (c *Converter) heightOfTypesetText(s string, o prose) float64 {
	lh := o.lineHeight
	if lh == 0 {
		lh = c.baseLineHeight
	}
	lm := c.calcLineMetrics(lh)
	transform := c.textTransform
	if o.transform != nil {
		transform = *o.transform
	}
	if transform != "" && transform != "none" {
		s = transformText(s, transform)
	}
	h := c.heightOfFragments(c.proseFrags(s, o), c.width, c.proseTextOpts(o, lm)) + lm.padTop
	if !o.singleLine {
		h += lm.padBottom
	}
	return h
}

func (c *Converter) renderedWidthOfString(s string) float64 {
	return c.widthOfFragments([]frag{{text: s}}, textOpts{})
}

// padBox は内側の余白を取って fn を実行します。
func (c *Converter) padBox(padding [4]float64, node *Block, fn func()) {
	gutter := map[*Block]float64{}
	if node != nil {
		last := node
		if node.ContentModel == "compound" {
			last = nil
			if n := len(node.Blocks); n > 0 && node.Blocks[n-1].Context == "paragraph" {
				last = node.Blocks[n-1]
			}
		}
		if last != nil {
			gutter[last] = padding[2]
		}
	}
	c.bottomGutters = append(c.bottomGutters, gutter)
	c.moveDown(padding[0])
	c.left += padding[3]
	c.width -= padding[3] + padding[1]
	fn()
	if !c.atPageTop() {
		if c.cursor() > padding[2] {
			c.moveDown(padding[2])
		} else {
			c.advancePage()
		}
	}
	c.bottomGutters = c.bottomGutters[:len(c.bottomGutters)-1]
	c.left -= padding[3]
	c.width += padding[3] + padding[1]
}

func (c *Converter) bottomGutter(node *Block) float64 {
	if n := len(c.bottomGutters); n > 0 {
		return c.bottomGutters[n-1][node]
	}
	return 0
}

// ---------------------------------------------------------------------------
// 移動先 (destination)

// ID がなければ "__anchor-" + def を返します (def も空なら空文字列)。
func (c *Converter) deriveAnchorFromID(value, def string) string {
	if value == "" {
		if def == "" {
			return ""
		}
		return "__anchor-" + def
	}
	// PDF の名前付き移動先は ASCII に限定する
	ascii := true
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return value
	}
	var b strings.Builder
	b.WriteString("0x")
	for i := 0; i < len(value); i++ {
		fmt.Fprintf(&b, "%02x", value[i])
	}
	return b.String()
}

func (c *Converter) addDest(name string, x, y float64) {
	if c.scratch > 0 || name == "" {
		return
	}
	c.pdf.SetDest(name, pdf.Dest{Page: c.page, X: x, Y: y})
}

func (c *Converter) addDestForBlock(node *Block, anchor string) {
	if c.scratch > 0 {
		return
	}
	if anchor == "" {
		anchor = c.deriveAnchorFromID(node.ID, "")
	}
	if anchor == "" {
		return
	}
	y := c.y
	if c.atPageTop() {
		y = c.pageH
	}
	x := c.left
	if x < 0 {
		x = 0
	}
	c.pdf.SetDest(anchor, pdf.Dest{Page: c.page, X: x, Y: y})
	if node.Context == "section" || node.Context == "document" {
		if si := c.sectInfo[node]; si != nil {
			si.destPage, si.destY = c.page, y
		}
	}
}

func (c *Converter) addInlineDest(f *frag, x, top float64) {
	if c.scratch > 0 || f.name == "" {
		return
	}
	if f.destType == "indexterm" && c.index != nil {
		c.index.linkDestToPage(f.name, c.pageNumber())
	}
	c.pdf.SetDest(f.name, pdf.Dest{Page: c.page, X: x, Y: top})
}

// ---------------------------------------------------------------------------
// ブロックの兄弟関係

func siblingsOf(b *Block) []*Block {
	p := b.Parent
	if p == nil {
		return nil
	}
	if p.Context == "dlist" {
		var out []*Block
		for _, item := range p.Blocks {
			out = append(out, item.Terms...)
			if item.Desc != nil {
				out = append(out, item.Desc)
			}
		}
		return out
	}
	return p.Blocks
}

func indexOf(list []*Block, b *Block) int {
	for i, x := range list {
		if x == b {
			return i
		}
	}
	return -1
}

func prevSibling(b *Block) *Block {
	if b.Parent == nil {
		return nil
	}
	if i := indexOf(b.Parent.Blocks, b); i > 0 {
		return b.Parent.Blocks[i-1]
	}
	return nil
}

func nextSibling(b *Block) *Block {
	if b.Parent == nil {
		return nil
	}
	if i := indexOf(b.Parent.Blocks, b); i >= 0 && i+1 < len(b.Parent.Blocks) {
		return b.Parent.Blocks[i+1]
	}
	return nil
}

// nextEnclosedBlock は同じ囲みの中で次に来るブロックを返します (余白を入れるかどうかの判定に使います)。
func nextEnclosedBlock(b *Block, descend bool) *Block {
	if b == nil || b.Context == "document" || b.Parent == nil {
		return nil
	}
	parent := b.Parent
	listItem := b.Context == "list_item"
	if listItem && descend && len(b.Blocks) > 0 {
		return b.Blocks[0]
	}
	siblings := siblingsOf(b)
	if len(siblings) > 0 && b != siblings[len(siblings)-1] {
		if b.Context == "open" && b.Style == "table-container" {
			return nextEnclosedBlock(parent, false)
		}
		if i := indexOf(siblings, b); i >= 0 && i+1 < len(siblings) {
			return siblings[i+1]
		}
		return nil
	}
	switch {
	case parent.Context == "list_item" || (parent.Context == "open" && parent.Style != "abstract") || parent.Context == "section":
		return nextEnclosedBlock(parent, false)
	case listItem && parent.Parent != nil && parent.Parent.Context == "list_item":
		return nextEnclosedBlock(parent.Parent, false)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 変換のエントリーポイント

// Convert は文書を PDF に変換します。
func (c *Converter) Convert(doc *asciidoc.Document) *pdf.Document {
	c.doc = doc
	c.initPDF(doc)
	c.convertDocument(doc)
	return c.pdf
}

// promotePrefaceBlock は書籍の preamble (preface-title 付き) を序文セクションに昇格させます。
func promotePrefaceBlock(doc *asciidoc.Document) {
	if doc.Doctype() != "book" || len(doc.Blocks) < 2 {
		return
	}
	pre, next := doc.Blocks[0], doc.Blocks[1]
	if pre.Context != "preamble" || pre.RawTitle == "" || next.Context != "section" {
		return
	}
	if len(pre.Blocks) > 0 && pre.Blocks[0].Style == "abstract" {
		return
	}
	preface := asciidoc.NewBlock(doc.Block, "section")
	preface.Level = next.Level
	preface.ContentModel = "compound"
	preface.Special = true
	preface.Sectname = "preface"
	preface.Attrs["1"], preface.Attrs["style"] = "preface", "preface"
	preface.SetTitle(pre.RawTitle)
	if doc.HasAttr("sectids") {
		preface.ID = asciidoc.GenerateSectionID(preface.Title(), doc)
		doc.Register(preface.ID, &asciidoc.Ref{Block: preface})
	}
	if len(pre.Blocks) > 0 && pre.Blocks[0].Option("notitle") {
		preface.Options["notitle"] = true
		if first := pre.Blocks[0]; first.Context == "paragraph" && first.Attr("role") == "" {
			first.Attrs["role"] = "lead"
		}
	}
	preface.Blocks = pre.Blocks
	for _, b := range preface.Blocks {
		b.Parent = preface
	}
	doc.Blocks[0] = preface
}

func (c *Converter) initPDF(doc *asciidoc.Document) {
	promotePrefaceBlock(doc)
	if doc.AttrUnspecified("outline") {
		doc.Attributes["outline"] = ""
	}
	if doc.AttrUnspecified("outline-title") {
		doc.Attributes["outline-title"] = ""
	}
	if doc.AttrUnspecified("pagenums") {
		doc.Attributes["pagenums"] = ""
	}
	t := c.loadTheme(doc)
	c.pdf = pdf.New()
	c.pdf.Compress = doc.HasAttr("compress")
	if v := doc.Attr("pdf-version"); v != "" {
		c.pdf.Version = v
	}
	c.now = time.Now()
	c.buildPDFInfo(doc)
	fontsDir := data.FontsDirToken
	if v, ok := doc.Attributes["pdf-fontsdir"]; ok {
		fontsDir = strings.ReplaceAll(v, "{docdir}", doc.Attr("docdir"))
	} else if t.Dir != theme.BundledDir {
		fontsDir = t.Dir + ";" + data.FontsDirToken
	}
	c.fonts = newFontSet(c, t.FontCatalog(), fontsDir)
	c.media = doc.AttrOr("media", "screen")
	folio := doc.AttrOr("pdf-folio-placement", map[bool]string{true: "physical", false: "virtual"}[c.media == "prepress"])
	c.folioPhysical = strings.HasPrefix(folio, "physical")
	c.folioInverted = strings.HasSuffix(folio, "-inverted")
	c.pageW, c.pageH = c.resolvePageSize()
	m, ok := c.resolvePageMargin(doc.Attributes["pdf-page-margin"])
	if _, has := doc.Attributes["pdf-page-margin"]; !has || !ok {
		if m, ok = c.resolvePageMargin(t.Get("page_margin")); !ok {
			m = [4]float64{36, 36, 36, 36}
		}
	}
	c.marginRecto, c.marginVerso = m, m
	if c.media == "prepress" {
		c.ppbook = doc.Doctype() == "book"
		if v, ok := t.Num("page_margin_outer"); ok {
			c.marginRecto[1], c.marginVerso[3] = v, v
		}
		if v, ok := t.Num("page_margin_inner"); ok {
			c.marginRecto[3], c.marginVerso[1] = v, v
		}
	}
	c.margin = c.marginRecto
	c.left = c.margin[3]
	c.width = c.pageW - c.margin[1] - c.margin[3]
	c.top = c.pageH - c.margin[0]
	c.bottom = c.margin[2]
	c.y = c.top
	c.pageBgColor = c.resolveThemeColor("page_background_color", hexClr("FFFFFF"), hexClr("FFFFFF"))
	c.kerning = t.Str("base_font_kerning") != "none"
	c.fallbackFonts = t.FontFallbacks()
	c.rootFontSize = t.Float("base_font_size", 12)
	c.fontScale = 1
	c.fontColor = c.themeClr("base_font_color")
	c.textDecorationWidth = t.Float("base_text_decoration_width", 0)
	c.baseTextAlign = t.StrOr("base_text_align", "left")
	if v := doc.Attr("text-align"); v == "left" || v == "center" || v == "right" || v == "justify" {
		c.baseTextAlign = v
	}
	c.baseLineHeight = t.Float("base_line_height", 1)
	c.cjkLineBreaks = doc.AttrIs("scripts", "cjk")
	c.fontFamily = t.Str("base_font_family")
	c.fontSize = c.rootFontSize
	c.setFontStyle(t.StrOr("base_font_style", "normal"))
	c.bottomGutters = []map[*Block]float64{{}}
	glyphs := t.StrOr("conum_glyphs", "circled")
	switch glyphs {
	case "circled":
		for r := rune(0x2460); r <= 0x2473; r++ {
			c.conumGlyphs = append(c.conumGlyphs, r)
		}
	case "filled":
		for r := rune(0x2776); r <= 0x277f; r++ {
			c.conumGlyphs = append(c.conumGlyphs, r)
		}
		for r := rune(0x24eb); r <= 0x24f4; r++ {
			c.conumGlyphs = append(c.conumGlyphs, r)
		}
	default:
		for _, part := range strings.Split(glyphs, ",") {
			from, to, isRange := strings.Cut(strings.TrimSpace(part), "-")
			get := func(s string) rune {
				if strings.HasPrefix(s, `\u`) {
					n, _ := strconv.ParseInt(s[2:], 16, 32)
					return rune(n)
				}
				for _, r := range s {
					return r
				}
				return 0
			}
			a := get(from)
			b := a
			if isRange {
				b = get(to)
			}
			for r := a; r <= b && r != 0; r++ {
				c.conumGlyphs = append(c.conumGlyphs, r)
			}
		}
	}
	if v := t.Get("section_indent"); v != nil {
		c.hasSectionIndent = true
		if list, ok := v.([]any); ok && len(list) >= 2 {
			c.sectionIndent = [2]float64{toF(list[0]), toF(list[1])}
		} else {
			f := toF(v)
			c.sectionIndent = [2]float64{f, f}
		}
	}
	c.tocMaxDigits = 3
	if v, ok := doc.Attributes["toc-max-pagenum-digits"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.tocMaxDigits = n
		}
	}
	if doc.Safe >= asciidoc.SafeSafe {
		c.jailDir = doc.BaseDir
	}
	c.index = newIndexCatalog()
}

func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		return theme.StrToPt(x)
	}
	return 0
}

func pdfDate(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return fmt.Sprintf("(D:%s%s%02d'%02d')", t.Format("20060102150405"), sign, off/3600, off%3600/60)
}

func (c *Converter) buildPDFInfo(doc *asciidoc.Document) {
	info := &c.pdf.Info
	add := func(key, val string) { *info = append(*info, pdf.InfoEntry{Key: key, Value: val}) }
	if title := doc.ResolvedDocTitle(); title != "" {
		add("Title", sanitize(title))
	}
	author := ""
	switch {
	case doc.AttributeLocked("author") && !doc.AttributeLocked("authors"):
		author = sanitize(doc.Attr("author"))
	case doc.HasAttr("authors"):
		author = sanitize(doc.Attr("authors"))
	case doc.HasAttr("author"):
		author = sanitize(doc.Attr("author"))
	}
	if author != "" {
		add("Author", author)
	}
	if doc.HasAttr("subject") {
		add("Subject", sanitize(doc.Attr("subject")))
	}
	if doc.HasAttr("keywords") {
		add("Keywords", sanitize(doc.Attr("keywords")))
	}
	if doc.HasAttr("publisher") {
		add("Creator", sanitize(doc.Attr("publisher")))
	} else {
		add("Creator", author)
	}
	producer := ""
	if doc.HasAttr("producer") {
		producer = sanitize(doc.Attr("producer"))
	}
	if doc.HasAttr("reproducible") {
		if producer == "" {
			producer = "go-asciidoctor-pdf"
		}
		add("Producer", producer)
		return
	}
	if producer == "" {
		producer = "go-asciidoctor-pdf (Asciidoctor PDF " + Version + " compatible)"
	}
	add("Producer", producer)
	parse := func(s string) time.Time {
		for _, layout := range []string{"2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 MST", "2006-01-02 15:04:05", "2006-01-02"} {
			if tm, err := time.Parse(layout, s); err == nil {
				return tm
			}
		}
		return c.now
	}
	*info = append(*info, pdf.InfoEntry{Key: "ModDate", Value: pdfDate(parse(doc.Attr("docdatetime"))), Raw: true})
	*info = append(*info, pdf.InfoEntry{Key: "CreationDate", Value: pdfDate(parse(doc.Attr("localdatetime"))), Raw: true})
}

// resolveAssetPath はテーマや文書からの相対パスを解決します。
func (c *Converter) themeAsset(path string) string {
	if filepath.IsAbs(path) || c.themesDir == theme.BundledDir || c.themesDir == "" {
		return path
	}
	return filepath.Join(c.themesDir, path)
}

// floatBox は回り込み画像の横にある、文章を流し込める領域です。
type floatBox struct {
	page                int
	top, bottom         float64
	left, width, height float64
	gapY                float64
}

func (c *Converter) initFloatBox(blockWidth, blockHeight float64, floatTo string) {
	gap := [2]float64{12, 6}
	if list := c.theme.List("image_float_gap"); len(list) > 0 {
		gap[0], gap[1] = toF(list[0]), toF(list[0])
		if len(list) > 1 {
			gap[1] = toF(list[1])
		}
	}
	floatW, floatH := blockWidth+gap[0], blockHeight+gap[1]
	left := c.left
	if floatTo != "right" {
		left += floatW
	}
	top := c.y + blockHeight
	height := minF(top-c.bottom, floatH)
	c.y = top
	c.floatBox = &floatBox{page: c.page, top: top, bottom: top - height, left: left, width: c.width - floatW, height: height, gapY: gap[1]}
}
