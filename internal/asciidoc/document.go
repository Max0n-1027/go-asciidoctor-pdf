package asciidoc

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version は互換対象としている Asciidoctor のバージョンです ({asciidoctor-version} 属性に入ります)。
const Version = "2.0.23"

// セーフモード
const (
	SafeUnsafe = 0
	SafeSafe   = 1
	SafeServer = 10
	SafeSecure = 20
)

// InlineConverter はインライン要素を出力形式 (擬似 HTML) に変換します。
type InlineConverter interface {
	ConvertInline(n *Inline) string
}

// Inline はインライン要素です。
type Inline struct {
	// Context は "quoted" "anchor" "footnote" "image" "break" "kbd" "button" "menu" "callout" "indexterm" のいずれか
	Context string
	// Type は種類 (例: quoted の "strong"、anchor の "xref"、image の "icon")
	Type string
	Text string
	// HasText は明示的なテキストが指定されたかどうか (xref 用)
	HasText bool
	Target  string
	ID      string
	Attrs   map[string]string
	// List は kbd のキー、menu のサブメニュー、indexterm の語句
	List   []string
	Parent *Block
	Doc    *Document
}

// Role は role 属性を返します。
func (n *Inline) Role() string { return n.Attrs["role"] }

// Attr は属性値を返します。
func (n *Inline) Attr(name string) string { return n.Attrs[name] }

// HasAttr は属性が存在するかを返します。
func (n *Inline) HasAttr(name string) bool {
	_, ok := n.Attrs[name]
	return ok
}

// Author は著者情報です。
type Author struct {
	Name, FirstName, MiddleName, LastName, Initials, Email string
}

// Footnote は脚注です。
type Footnote struct {
	Index int
	ID    string
	Text  string
	Label string
}

// AttrEntry は本文中の属性定義 (再生用) です。
type AttrEntry struct {
	Name   string
	Value  string
	Negate bool
}

// Ref は相互参照の対象です。
type Ref struct {
	Block   *Block
	Inline  *Inline
	Reftext string
}

// Logger は警告などのメッセージの出力先です。
type Logger interface {
	Log(severity, msg string)
}

// Document は AsciiDoc 文書全体です。
type Document struct {
	*Block
	Attributes map[string]string
	Authors    []Author
	Refs       map[string]*Ref
	Footnotes  []*Footnote
	Inline     InlineConverter
	Logger     Logger
	BaseDir    string
	Safe       int
	// ParentDoc は入れ子文書 (AsciiDoc 形式の表セル) の親文書です。
	ParentDoc *Document
	// HasHeader は文書ヘッダー (タイトル) があるかどうか
	HasHeader bool
	// DocTitle は置換前の文書タイトル
	DocTitle string
	// Compat は compat-mode かどうか
	Compat bool

	locked           map[string]bool
	headerAttributes map[string]string
	callouts         *callouts
	indexterms       bool
	docTitleCache    *string
	nextIDs          map[string]int
	sourcemap        bool
	maxAttrValueSize int
}

// Block はブロックレベルの要素です。
type Block struct {
	Context string
	Style   string
	ID      string
	Attrs   map[string]string
	Options map[string]bool
	Level   int
	Parent  *Block
	Doc     *Document
	Blocks  []*Block
	Lines   []string
	Subs    []string
	// ContentModel は "compound" "simple" "verbatim" "raw" "empty"
	ContentModel string

	RawTitle string
	Caption  string
	Numeral  string

	// セクション用
	Sectname string
	Numbered bool
	// NumberedChapter は special セクションを章として番号付けする場合に true
	NumberedChapter bool
	Special         bool
	Index           int

	// リスト項目用
	Marker  string
	RawText string
	HasText bool
	// Terms は説明リスト項目の用語 (dlist の list_item)
	Terms []*Block
	// Desc は説明リスト項目の説明部分
	Desc *Block

	// 表用
	Table *Table

	// AttrEntries はこのブロックの直前に定義された属性 (変換時に再生する)
	AttrEntries []AttrEntry

	// 位置情報
	File   string
	Lineno int

	titleCache   *string
	contentCache *string
	textCache    *string

	nextSectionIndex   int
	nextSectionOrdinal int
}

// NewBlock はブロックを生成します。
func NewBlock(parent *Block, context string) *Block {
	b := &Block{Context: context, Parent: parent, Attrs: map[string]string{}, Options: map[string]bool{}, nextSectionOrdinal: 1}
	if parent != nil {
		b.Doc = parent.Doc
		if context != "section" {
			b.Level = parent.Level
		}
	}
	return b
}

// Attr は属性値を返します。fallback が true なら文書属性も参照します。
func (b *Block) Attr(name string) string { return b.Attrs[name] }

// HasAttr は属性の有無を返します。
func (b *Block) HasAttr(name string) bool {
	_, ok := b.Attrs[name]
	return ok
}

// AttrOr は属性値を返し、未定義なら def を返します。
func (b *Block) AttrOr(name, def string) string {
	if v, ok := b.Attrs[name]; ok {
		return v
	}
	return def
}

// AttrInherit はブロック属性、なければ文書属性を返します。
func (b *Block) AttrInherit(name string) (string, bool) {
	if v, ok := b.Attrs[name]; ok {
		return v, true
	}
	v, ok := b.Doc.Attributes[name]
	return v, ok
}

// Option はオプションが設定されているかを返します。
func (b *Block) Option(name string) bool { return b.Options[name] }

// Roles は role の一覧を返します。
func (b *Block) Roles() []string { return strings.Fields(b.Attrs["role"]) }

// HasRole は指定の role を持つかを返します。
func (b *Block) HasRole(name string) bool {
	for _, r := range b.Roles() {
		if r == name {
			return true
		}
	}
	return false
}

// HasTitle はタイトルの有無を返します。
func (b *Block) HasTitle() bool { return b.RawTitle != "" }

// Title は置換適用済みのタイトルを返します。
func (b *Block) Title() string {
	if b.titleCache != nil {
		return *b.titleCache
	}
	t := ""
	if b.RawTitle != "" {
		t = b.Doc.ApplySubs(b.RawTitle, TitleSubs, b)
	}
	b.titleCache = &t
	return t
}

// SetTitle はタイトルを設定します。
func (b *Block) SetTitle(raw string) {
	b.RawTitle = raw
	b.titleCache = nil
}

// CaptionedTitle はキャプション付きのタイトルを返します。
func (b *Block) CaptionedTitle() string { return b.Caption + b.Title() }

// Text はリスト項目のテキスト (置換適用済み) を返します。
func (b *Block) Text() string {
	if b.textCache != nil {
		return *b.textCache
	}
	t := ""
	if b.HasText {
		subs := b.Subs
		if subs == nil {
			subs = NormalSubs
		}
		t = b.Doc.ApplySubs(b.RawText, subs, b)
	}
	b.textCache = &t
	return t
}

// Source は生の内容を返します。
func (b *Block) Source() string { return strings.Join(b.Lines, "\n") }

// Content は置換適用済みの内容を返します (simple / verbatim / raw 用)。
func (b *Block) Content() string {
	if b.contentCache != nil {
		return *b.contentCache
	}
	c := b.Doc.ApplySubs(strings.Join(b.Lines, "\n"), b.Subs, b)
	b.contentCache = &c
	return c
}

// IsNested はリストが別のリストの中にあるかを返します。
func (b *Block) IsNested() bool {
	return b.Parent != nil && b.Parent.Context == "list_item"
}

// Sections は直下のセクションを返します。
func (b *Block) Sections() []*Block {
	var out []*Block
	for _, c := range b.Blocks {
		if c.Context == "section" {
			out = append(out, c)
		}
	}
	return out
}

// Reftext は reftext 属性 (置換適用済み) を返します。
func (b *Block) Reftext() string {
	if v, ok := b.Attrs["reftext"]; ok && v != "" {
		return b.Doc.ApplySubs(v, ReftextSubs, b)
	}
	return ""
}

// Sectnum はセクション番号を返します (例: "1.2.")。
func (b *Block) Sectnum(delimiter string, appendStr *string) string {
	app := delimiter
	if appendStr != nil {
		app = *appendStr
	}
	if b.Level > 1 && b.Parent != nil && b.Parent.Context == "section" {
		return b.Parent.Sectnum(delimiter, &delimiter) + b.Numeral + app
	}
	return b.Numeral + app
}

// Xreftext は相互参照で使う表示テキストを返します。
func (b *Block) Xreftext(xrefstyle string) string {
	if v := b.Reftext(); v != "" {
		return v
	}
	doc := b.Doc
	quote := func(format string) string {
		return strings.Replace(doc.subQuotes(format, b), "%s", b.Title(), 1)
	}
	if b.Context == "section" {
		emphasized := func() string {
			if b.Sectname == "chapter" || b.Sectname == "appendix" {
				return quote("_%s_")
			}
			return b.Title()
		}
		if xrefstyle == "" {
			return b.Title()
		}
		if !b.Numbered {
			return emphasized()
		}
		empty := ""
		comma := ","
		switch xrefstyle {
		case "full":
			var quoted string
			if b.Sectname == "chapter" || b.Sectname == "appendix" {
				quoted = quote("_%s_")
			} else {
				quoted = quote("\"`%s`\"")
			}
			if sig, ok := doc.Attributes[b.Sectname+"-refsig"]; ok {
				return sig + " " + b.Sectnum(".", &comma) + " " + quoted
			}
			return b.Sectnum(".", &comma) + " " + quoted
		case "short":
			if sig, ok := doc.Attributes[b.Sectname+"-refsig"]; ok {
				return sig + " " + b.Sectnum(".", &empty)
			}
			return b.Sectnum(".", &empty)
		}
		return emphasized()
	}
	if xrefstyle != "" && b.RawTitle != "" && b.Caption != "" {
		capAttr := captionAttributeNames[b.captionContext()]
		prefix, hasPrefix := doc.Attributes[capAttr]
		switch xrefstyle {
		case "full":
			quoted := quote("\"`%s`\"")
			if b.Numeral != "" && capAttr != "" && hasPrefix {
				return prefix + " " + b.Numeral + ", " + quoted
			}
			return strings.TrimSuffix(b.Caption, ". ") + ", " + quoted
		case "short":
			if b.Numeral != "" && capAttr != "" && hasPrefix {
				return prefix + " " + b.Numeral
			}
			return strings.TrimSuffix(b.Caption, ". ")
		}
	}
	return b.Title()
}

var captionAttributeNames = map[string]string{"example": "example-caption", "figure": "figure-caption", "listing": "listing-caption", "table": "table-caption"}

func (b *Block) captionContext() string {
	if b.Context == "image" {
		return "figure"
	}
	return b.Context
}

// assignCaption は必要に応じてキャプション (例: "Figure 1. ") を割り当てます。
func (b *Block) assignCaption(value string, hasValue bool) {
	if b.Caption != "" || b.RawTitle == "" {
		return
	}
	if hasValue {
		b.Caption = value
		return
	}
	if v, ok := b.Doc.Attributes["caption"]; ok {
		b.Caption = v
		return
	}
	ctx := b.captionContext()
	if attrName := captionAttributeNames[ctx]; attrName != "" {
		if prefix, ok := b.Doc.Attributes[attrName]; ok {
			b.Numeral = b.Doc.Counter(ctx+"-number", "")
			b.Caption = prefix + " " + b.Numeral + ". "
		}
	}
}

// assignNumeral はセクションに番号を割り当てます。
func (b *Block) assignNumeral(section *Block) {
	section.Index = b.nextSectionIndex
	b.nextSectionIndex++
	if !section.Numbered {
		return
	}
	doc := b.Doc
	switch {
	case section.Sectname == "appendix":
		section.Numeral = doc.Counter("appendix-number", "A")
		if caption, ok := doc.Attributes["appendix-caption"]; ok && caption != "" {
			section.Caption = caption + " " + section.Numeral + ": "
		} else {
			section.Caption = section.Numeral + ". "
		}
	case section.Sectname == "chapter" || section.NumberedChapter:
		section.Numeral = doc.Counter("chapter-number", "1")
	default:
		if section.Sectname == "part" {
			section.Numeral = intToRoman(b.nextSectionOrdinal)
		} else {
			section.Numeral = strconv.Itoa(b.nextSectionOrdinal)
		}
		b.nextSectionOrdinal++
	}
}

func intToRoman(n int) string {
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var b strings.Builder
	for i, v := range vals {
		for n >= v {
			b.WriteString(syms[i])
			n -= v
		}
	}
	return b.String()
}

// Options はパース時のオプションです。
type Options struct {
	// Attributes は API / CLI から渡された属性 ("name" → 値。"name!" で未定義化、値末尾 "@" でソフト設定)
	Attributes map[string]string
	BaseDir    string
	Safe       int
	Doctype    string
	Backend    string
	// InputFile は入力ファイルのパス (標準入力なら空)
	InputFile string
	Logger    Logger
	Inline    InlineConverter
	Sourcemap bool
	// Now は現在時刻 (テスト用に差し替え可能)
	Now time.Time
}

type nopLogger struct{}

func (nopLogger) Log(string, string) {}

func newDocument(opts Options) *Document {
	doc := &Document{
		Attributes: map[string]string{},
		Refs:       map[string]*Ref{},
		Inline:     opts.Inline,
		Logger:     opts.Logger,
		Safe:       opts.Safe,
		locked:     map[string]bool{},
		callouts:   newCallouts(),
		nextIDs:    map[string]int{},
		sourcemap:  opts.Sourcemap,
	}
	if doc.Logger == nil {
		doc.Logger = nopLogger{}
	}
	doc.Block = NewBlock(nil, "document")
	doc.Block.Doc = doc
	doc.ContentModel = "compound"
	return doc
}

var safeModeNames = map[int]string{SafeUnsafe: "unsafe", SafeSafe: "safe", SafeServer: "server", SafeSecure: "secure"}

func (d *Document) initAttributes(opts Options) {
	a := d.Attributes
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
		if sde := os.Getenv("SOURCE_DATE_EPOCH"); sde != "" {
			if secs, err := strconv.ParseInt(sde, 10, 64); err == nil {
				now = time.Unix(secs, 0).UTC()
			}
		}
	}
	a["asciidoctor"] = ""
	a["asciidoctor-version"] = Version
	a["safe-mode-name"] = safeModeNames[d.Safe]
	a["safe-mode-"+safeModeNames[d.Safe]] = ""
	a["safe-mode-level"] = strconv.Itoa(d.Safe)
	a["max-include-depth"] = "64"
	a["allow-uri-read"] = ""
	delete(a, "allow-uri-read")
	if home, err := os.UserHomeDir(); err == nil && d.Safe < SafeServer {
		a["user-home"] = home
	} else {
		a["user-home"] = "."
	}
	// 既定値
	defaults := map[string]string{
		"sectids": "", "toc-placement": "auto", "prewrap": "", "attribute-undefined": "drop-line", "attribute-missing": "skip",
		"iconfont-remote": "", "caution-caption": "Caution", "important-caption": "Important", "note-caption": "Note",
		"tip-caption": "Tip", "warning-caption": "Warning", "example-caption": "Example", "figure-caption": "Figure",
		"table-caption": "Table", "toc-title": "Table of Contents", "section-refsig": "Section", "part-refsig": "Part",
		"chapter-refsig": "Chapter", "appendix-caption": "Appendix", "appendix-refsig": "Appendix",
		"untitled-label": "Untitled", "version-label": "Version", "last-update-label": "Last updated",
	}
	for k, v := range defaults {
		a[k] = v
	}
	// 組み込みの文字参照属性
	for k, v := range intrinsicAttributes {
		a[k] = v
	}
	backend := opts.Backend
	if backend == "" {
		backend = "pdf"
	}
	a["backend"] = backend
	a["backend-"+backend] = ""
	a["basebackend"] = "html"
	a["basebackend-html"] = ""
	a["filetype"] = backend
	a["filetype-"+backend] = ""
	a["outfilesuffix"] = "." + backend
	a["htmlsyntax"] = "html"
	// 入力ファイルに由来する属性
	localDate := now.Format("2006-01-02")
	localTime := now.Format("15:04:05 -0700")
	a["localdate"] = localDate
	a["localtime"] = localTime
	a["localdatetime"] = localDate + " " + localTime
	a["localyear"] = strconv.Itoa(now.Year())
	docTime := now
	if opts.InputFile != "" {
		abs, _ := filepath.Abs(opts.InputFile)
		a["docfile"] = abs
		a["docdir"] = filepath.Dir(abs)
		ext := filepath.Ext(abs)
		a["docfilesuffix"] = ext
		a["docname"] = strings.TrimSuffix(filepath.Base(abs), ext)
		if os.Getenv("SOURCE_DATE_EPOCH") == "" {
			if st, err := os.Stat(abs); err == nil {
				docTime = st.ModTime()
			}
		}
	} else if d.BaseDir != "" {
		a["docdir"] = d.BaseDir
	}
	docDate := docTime.Format("2006-01-02")
	a["docdate"] = docDate
	a["doctime"] = docTime.Format("15:04:05 -0700")
	a["docdatetime"] = docDate + " " + a["doctime"]
	a["docyear"] = strconv.Itoa(docTime.Year())
	doctype := opts.Doctype
	if doctype == "" {
		doctype = "article"
	}
	a["doctype"] = doctype
	a["doctype-"+doctype] = ""

	// API / CLI から渡された属性 (上書き・ロック)
	for key, val := range opts.Attributes {
		soft := false
		if strings.HasSuffix(key, "@") {
			key, soft = strings.TrimSuffix(key, "@"), true
		} else if strings.HasSuffix(val, "@") {
			val, soft = strings.TrimSuffix(val, "@"), true
		}
		negate := false
		if strings.HasPrefix(key, "!") {
			key, negate = key[1:], true
		} else if strings.HasSuffix(key, "!") {
			key, negate = strings.TrimSuffix(key, "!"), true
		}
		key = strings.ToLower(key)
		if negate {
			delete(a, key)
			if key == "doctype" || key == "backend" {
				continue
			}
		} else {
			if key == "doctype" {
				delete(a, "doctype-"+a["doctype"])
				a["doctype-"+val] = ""
			}
			a[key] = val
		}
		if !soft {
			d.locked[key] = true
		}
	}
	if _, ok := a["max-attribute-value-size"]; ok {
		d.maxAttrValueSize, _ = strconv.Atoi(a["max-attribute-value-size"])
	}
	d.Compat = d.HasAttr("compat-mode")
}

var intrinsicAttributes = map[string]string{
	"startsb": "[", "endsb": "]", "vbar": "|", "caret": "^", "asterisk": "*", "tilde": "~",
	"plus": "&#43;", "backslash": "\\", "backtick": "`", "blank": "", "empty": "", "sp": " ",
	"two-colons": "::", "two-semicolons": ";;", "nbsp": "&#160;", "deg": "&#176;", "zwsp": "&#8203;",
	"quot": "&#34;", "apos": "&#39;", "lsquo": "&#8216;", "rsquo": "&#8217;", "ldquo": "&#8220;",
	"rdquo": "&#8221;", "wj": "&#8288;", "brvbar": "&#166;", "pp": "&#43;&#43;", "cpp": "C&#43;&#43;",
	"cxx": "C&#43;&#43;", "amp": "&", "lt": "<", "gt": ">",
}

// HasAttr は文書属性が定義されているかを返します。
func (d *Document) HasAttr(name string) bool {
	_, ok := d.Attributes[name]
	return ok
}

// Attr は文書属性の値を返します。
func (d *Document) Attr(name string) string { return d.Attributes[name] }

// AttrOr は文書属性の値を返し、未定義なら def を返します。
func (d *Document) AttrOr(name, def string) string {
	if v, ok := d.Attributes[name]; ok {
		return v
	}
	return def
}

// AttrIs は文書属性が指定の値かどうかを返します。
func (d *Document) AttrIs(name, val string) bool {
	v, ok := d.Attributes[name]
	return ok && v == val
}

// AttrUnspecified は属性が未定義かつロックもされていない (明示的に unset されていない) かを返します。
func (d *Document) AttrUnspecified(name string) bool {
	if _, ok := d.Attributes[name]; ok {
		return false
	}
	return !d.locked[name]
}

// AttributeLocked は属性が API / CLI によってロックされているかを返します。
func (d *Document) AttributeLocked(name string) bool { return d.locked[name] }

// SetAttr は文書属性を設定します (ロックされていれば false)。
func (d *Document) SetAttr(name, value string) bool {
	if d.locked[name] {
		return false
	}
	if d.maxAttrValueSize > 0 && len(value) > d.maxAttrValueSize {
		value = value[:d.maxAttrValueSize]
	}
	d.Attributes[name] = value
	switch name {
	case "doctype":
		// doctype-* 属性の更新はヘッダー解析後に行う
	case "compat-mode":
		d.Compat = true
	case "leveloffset":
	}
	return true
}

// UnsetAttr は文書属性を削除します (ロックされていれば false)。
func (d *Document) UnsetAttr(name string) bool {
	if d.locked[name] {
		return false
	}
	delete(d.Attributes, name)
	if name == "compat-mode" {
		d.Compat = false
	}
	return true
}

// Doctype は文書タイプを返します。
func (d *Document) Doctype() string { return d.AttrOr("doctype", "article") }

// Counter はカウンターを進めて現在値を返します。
func (d *Document) Counter(name, seed string) string {
	if d.ParentDoc != nil {
		return d.ParentDoc.Counter(name, seed)
	}
	var next string
	if cur := d.Attributes[name]; cur != "" {
		next = nextCounterValue(cur)
	} else if seed != "" {
		next = seed
	} else {
		next = "1"
	}
	if !d.locked[name] {
		d.Attributes[name] = next
	}
	return next
}

func nextCounterValue(cur string) string {
	if n, err := strconv.Atoi(cur); err == nil {
		return strconv.Itoa(n + 1)
	}
	// 文字の連番 (a → b、z → aa は Ruby の String#succ に準拠)
	r := []rune(cur)
	for i := len(r) - 1; i >= 0; i-- {
		switch {
		case r[i] == 'z':
			r[i] = 'a'
		case r[i] == 'Z':
			r[i] = 'A'
		case r[i] == '9':
			r[i] = '0'
		default:
			r[i]++
			return string(r)
		}
		if i == 0 {
			first := r[0]
			if first == '0' {
				first = '1'
			}
			return string(first) + string(r)
		}
	}
	return string(r)
}

// Register は ID を参照カタログに登録します。既に登録済みなら false を返します。
func (d *Document) Register(id string, ref *Ref) bool {
	if d.ParentDoc != nil {
		return d.ParentDoc.Register(id, ref)
	}
	if _, exists := d.Refs[id]; exists {
		return false
	}
	d.Refs[id] = ref
	return true
}

// Ref は ID に対応する参照対象を返します。
func (d *Document) Ref(id string) *Ref {
	if d.ParentDoc != nil {
		return d.ParentDoc.Ref(id)
	}
	return d.Refs[id]
}

// ResolveRefID は reftext から ID を逆引きします。
func (d *Document) ResolveRefID(text string) string {
	if d.ParentDoc != nil {
		return d.ParentDoc.ResolveRefID(text)
	}
	for id, ref := range d.Refs {
		var xt string
		switch {
		case ref.Block != nil:
			xt = ref.Block.Xreftext("")
		default:
			xt = ref.Reftext
		}
		if xt == text {
			return id
		}
	}
	return ""
}

// RegisterFootnote は脚注を登録します。
func (d *Document) RegisterFootnote(fn *Footnote) {
	if d.ParentDoc != nil {
		d.ParentDoc.RegisterFootnote(fn)
		return
	}
	d.Footnotes = append(d.Footnotes, fn)
}

// AllFootnotes は文書の脚注を返します。
func (d *Document) AllFootnotes() []*Footnote {
	if d.ParentDoc != nil {
		return d.ParentDoc.AllFootnotes()
	}
	return d.Footnotes
}

// Warn は警告を出力します。
func (d *Document) Warn(msg string) { d.Logger.Log("WARN", msg) }

// Error はエラーを出力します。
func (d *Document) Error(msg string) { d.Logger.Log("ERROR", msg) }

// Info は情報メッセージを出力します。
func (d *Document) Info(msg string) { d.Logger.Log("INFO", msg) }

// Title は置換適用済みの文書タイトルを返します。
func (d *Document) Title() string {
	if d.docTitleCache != nil {
		return *d.docTitleCache
	}
	t := ""
	if d.DocTitle != "" {
		t = d.ApplySubs(d.DocTitle, TitleSubs, d.Block)
	}
	d.docTitleCache = &t
	return t
}

// ResolvedDocTitle は表示用の文書タイトルを返します (title 属性 → ヘッダー → 最初のセクション)。
func (d *Document) ResolvedDocTitle() string {
	if v, ok := d.Attributes["title"]; ok && v != "" {
		return d.ApplySubs(v, TitleSubs, d.Block)
	}
	if t := d.Title(); t != "" {
		return t
	}
	if secs := d.Sections(); len(secs) > 0 {
		return secs[0].Title()
	}
	if v, ok := d.Attributes["untitled-label"]; ok {
		return v
	}
	return ""
}

// SaveHeaderAttributes はヘッダー解析直後の属性を保存します。
func (d *Document) saveHeaderAttributes() {
	d.headerAttributes = make(map[string]string, len(d.Attributes))
	for k, v := range d.Attributes {
		d.headerAttributes[k] = v
	}
}

// RestoreAttributes は属性をヘッダー解析直後の状態に戻します (変換開始前に呼びます)。
func (d *Document) RestoreAttributes() {
	if d.headerAttributes == nil {
		return
	}
	d.callouts.rewind()
	d.Attributes = make(map[string]string, len(d.headerAttributes))
	for k, v := range d.headerAttributes {
		d.Attributes[k] = v
	}
	d.Compat = d.HasAttr("compat-mode")
}

// Playback はブロックに記録された属性定義を再生します。
func (d *Document) Playback(b *Block) {
	for _, e := range b.AttrEntries {
		if e.Negate {
			delete(d.Attributes, e.Name)
			if e.Name == "compat-mode" {
				d.Compat = false
			}
		} else {
			d.Attributes[e.Name] = e.Value
			if e.Name == "compat-mode" {
				d.Compat = true
			}
		}
	}
}

func (d *Document) location(file string, lineno int) string {
	if file == "" {
		file = "<stdin>"
	}
	return fmt.Sprintf("%s: line %d", file, lineno)
}
