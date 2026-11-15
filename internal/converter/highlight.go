package converter

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
)

type tokenStyle struct {
	fg, bg                  string
	bold, italic, underline bool
}

// rougeDefaultStyles は Asciidoctor PDF の Rouge 用既定テーマ (asciidoctor_pdf_default) の配色です。
var rougeDefaultStyles = map[chroma.TokenType]tokenStyle{
	chroma.Comment:               {fg: "888888"},
	chroma.CommentPreproc:        {fg: "CC0000", bold: true},
	chroma.CommentPreprocFile:    {fg: "CC0000", bold: true},
	chroma.CommentSpecial:        {fg: "CC0000", bold: true},
	chroma.Error:                 {fg: "A61717", bg: "E3D2D2"},
	chroma.GenericError:          {fg: "AA0000"},
	chroma.GenericHeading:        {fg: "333333"},
	chroma.GenericSubheading:     {fg: "666666"},
	chroma.GenericDeleted:        {fg: "000000", bg: "FFDDDD"},
	chroma.GenericInserted:       {fg: "000000", bg: "DDFFDD"},
	chroma.GenericEmph:           {italic: true},
	chroma.GenericStrong:         {bold: true},
	chroma.GenericOutput:         {fg: "888888"},
	chroma.GenericPrompt:         {fg: "555555"},
	chroma.GenericTraceback:      {fg: "AA0000"},
	chroma.Keyword:               {fg: "008800", bold: true},
	chroma.KeywordPseudo:         {fg: "008800"},
	chroma.KeywordType:           {fg: "888888", bold: true},
	chroma.LiteralNumber:         {fg: "0000DD", bold: true},
	chroma.LiteralString:         {fg: "DD2200"},
	chroma.LiteralStringEscape:   {fg: "0044DD"},
	chroma.LiteralStringInterpol: {fg: "3333BB"},
	chroma.LiteralStringOther:    {fg: "22BB22"},
	chroma.LiteralStringRegex:    {fg: "008800"},
	chroma.LiteralStringSymbol:   {fg: "AA6600"},
	chroma.NameAttribute:         {fg: "336699"},
	chroma.NameBuiltin:           {fg: "003388"},
	chroma.NameClass:             {fg: "BB0066", bold: true},
	chroma.NameConstant:          {fg: "003366", bold: true},
	chroma.NameDecorator:         {fg: "555555"},
	chroma.NameException:         {fg: "BB0066", bold: true},
	chroma.NameFunction:          {fg: "0066BB", bold: true},
	chroma.NameLabel:             {fg: "336699"},
	chroma.NameNamespace:         {fg: "BB0066", bold: true},
	chroma.NameProperty:          {fg: "336699", bold: true},
	chroma.NameTag:               {fg: "BB0066", bold: true},
	chroma.NameVariable:          {fg: "336699"},
	chroma.NameVariableGlobal:    {fg: "DD7700"},
	chroma.NameVariableInstance:  {fg: "3333BB"},
	chroma.OperatorWord:          {fg: "008800"},
}

// highlighterName は有効なシンタックスハイライターの名前を返します (無効なら空文字列)。
func highlighterName(doc *asciidoc.Document) string {
	switch name := doc.Attr("source-highlighter"); name {
	case "rouge", "coderay", "pygments":
		return name
	}
	return ""
}

func (c *Converter) styleForToken(style *chroma.Style, tt chroma.TokenType) tokenStyle {
	if style == nil {
		for _, cand := range []chroma.TokenType{tt, tt.SubCategory(), tt.Category()} {
			if s, ok := rougeDefaultStyles[cand]; ok {
				return s
			}
		}
		return tokenStyle{}
	}
	entry := style.Get(tt)
	var ts tokenStyle
	if entry.Colour.IsSet() {
		ts.fg = strings.ToUpper(strings.TrimPrefix(entry.Colour.String(), "#"))
	}
	if entry.Background.IsSet() {
		if bg := style.Get(chroma.Background).Background; !bg.IsSet() || bg != entry.Background {
			ts.bg = strings.ToUpper(strings.TrimPrefix(entry.Background.String(), "#"))
		}
	}
	ts.bold = entry.Bold == chroma.Yes
	ts.italic = entry.Italic == chroma.Yes
	ts.underline = entry.Underline == chroma.Yes
	return ts
}

var conumTailRx = regexp.MustCompile(`(?:(?://|#|--|;;) ?)?(\\)?<!?(--)?(\d+|\.)(--)?>$`)

// extractConums はソースの各行末からコールアウト番号を取り除き、行番号との対応を返します。
func extractConums(source string) (string, map[int][]int) {
	if !strings.Contains(source, "<") {
		return source, nil
	}
	mapping := map[int][]int{}
	auto := 0
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "<") {
			continue
		}
		var nums []string
		cur := line
		for {
			m := conumTailRx.FindStringSubmatchIndex(cur)
			if m == nil || (m[4] >= 0) != (m[8] >= 0) {
				break
			}
			if m[2] >= 0 {
				// エスケープされたコールアウトはバックスラッシュだけ取り除く
				cur = cur[:m[2]] + cur[m[3]:]
				break
			}
			nums = append([]string{cur[m[6]:m[7]]}, nums...)
			cur = cur[:m[0]]
			if trimmed := strings.TrimRight(cur, " "); trimmed != cur && conumTailRx.MatchString(trimmed) {
				cur = trimmed
			}
		}
		if len(nums) > 0 {
			for _, n := range nums {
				if n == "." {
					auto++
					mapping[i] = append(mapping[i], auto)
				} else {
					v, _ := strconv.Atoi(n)
					mapping[i] = append(mapping[i], v)
				}
			}
			cur = strings.TrimRight(cur, " ")
		}
		lines[i] = cur
	}
	if len(mapping) == 0 {
		return strings.Join(lines, "\n"), nil
	}
	return strings.Join(lines, "\n"), mapping
}

// highlightSource はソースコードをトークンに分けて色付けした断片を返します。
// 2 番目の戻り値はコードブロックの背景色の上書き (スタイルが指定する場合) です。
func (c *Converter) highlightSource(node *Block) ([]frag, clr, bool) {
	doc := node.Doc
	name := highlighterName(doc)
	if name == "" || node.Style != "source" {
		return nil, clr{}, false
	}
	var subs []string
	callouts := false
	for _, s := range node.Subs {
		switch s {
		case "specialcharacters", "highlight":
		case "callouts":
			callouts = true
		default:
			subs = append(subs, s)
		}
	}
	source := node.Source()
	if len(subs) > 0 {
		source = doc.ApplySubs(source, subs, node)
	}
	source = strings.ReplaceAll(source, "\t", "    ")
	var conums map[int][]int
	if callouts {
		source, conums = extractConums(source)
	}
	if source == "" {
		return []frag{}, clr{}, true
	}
	var lexer chroma.Lexer
	if lang := node.Attr("language"); lang != "" {
		lexer = lexers.Get(lang)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	var style *chroma.Style
	styleAttr := doc.Attr(name + "-style")
	if styleAttr != "" && styleAttr != "asciidoctor_pdf_default" {
		if s, ok := styles.Registry[strings.ToLower(styleAttr)]; ok {
			style = s
		}
	}
	var bgOverride clr
	if style != nil {
		if bg := style.Get(chroma.Background).Background; bg.IsSet() {
			bgOverride = hexClr(strings.ToUpper(strings.TrimPrefix(bg.String(), "#")))
		}
	}
	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return nil, clr{}, false
	}
	linenums := node.Option("linenums") || node.HasAttr("linenums")
	start := 1
	if v, err := strconv.Atoi(node.Attr("start")); err == nil && v > 0 {
		start = v
	}
	numLines := strings.Count(source, "\n") + 1
	pad := len(strconv.Itoa(start + numLines - 1))
	linenumColor := hexClr("888888")
	var frags []frag
	lineIdx := 0
	startOfLine := true
	beginLine := func() {
		if linenums {
			num := strconv.Itoa(start + lineIdx)
			frags = append(frags, frag{text: strings.Repeat(nbsp, pad-len(num)) + num + " ", color: linenumColor})
			startOfLine = false
		}
	}
	_ = numLines
	endLine := func() {
		if nums := conums[lineIdx]; len(nums) > 0 {
			glyphs := make([]string, len(nums))
			for i, n := range nums {
				glyphs[i] = c.conumGlyph(n)
			}
			f := frag{text: strings.Join(glyphs, " ")}
			if col := c.themeClr("conum_font_color"); col.set {
				f.color = col
			}
			if family := c.theme.Str("conum_font_family"); family != "" {
				f.family = family
			}
			frags = append(frags, frag{text: " "}, f)
		}
	}
	needNum := true
	for tok := it(); tok != chroma.EOF; tok = it() {
		ts := c.styleForToken(style, tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				if needNum {
					beginLine()
				}
				endLine()
				frags = append(frags, frag{text: "\n"})
				lineIdx++
				startOfLine = true
				needNum = true
			}
			if part == "" {
				continue
			}
			if needNum {
				beginLine()
				needNum = false
			}
			if startOfLine && part[0] == ' ' {
				part = nbsp + part[1:]
			}
			startOfLine = false
			f := frag{text: part}
			if strings.TrimSpace(part) != "" {
				if ts.fg != "" {
					f.color = hexClr(ts.fg)
				}
				if ts.bg != "" {
					f.bg = hexClr(ts.bg)
				}
				f.bold, f.italic, f.underline = ts.bold, ts.italic, ts.underline
			}
			frags = append(frags, f)
		}
	}
	// 字句解析器が末尾に付ける改行を取り除く
	for len(frags) > 0 && frags[len(frags)-1].text == "\n" {
		frags = frags[:len(frags)-1]
		lineIdx--
	}
	endLine()
	return frags, bgOverride, true
}
