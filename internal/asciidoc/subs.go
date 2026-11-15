package asciidoc

import (
	"net/url"
	"path"
	"strconv"
	"strings"
)

// 置換グループ
var (
	NormalSubs   = []string{"specialcharacters", "quotes", "attributes", "replacements", "macros", "post_replacements"}
	HeaderSubs   = []string{"specialcharacters", "attributes"}
	TitleSubs    = NormalSubs
	ReftextSubs  = []string{"specialcharacters", "quotes", "replacements"}
	VerbatimSubs = []string{"specialcharacters", "callouts"}
	BasicSubs    = []string{"specialcharacters"}
	NoSubs       = []string{}
)

const (
	passStart = "\u0096"
	passEnd   = "\u0097"
	del       = "\u007f"
	can       = "\u0018"
)

var subAliases = map[string][]string{
	"none": {}, "normal": NormalSubs, "verbatim": VerbatimSubs, "specialchars": BasicSubs,
	"a": {"attributes"}, "m": {"macros"}, "n": NormalSubs, "p": {"post_replacements"}, "q": {"quotes"},
	"r": {"replacements"}, "c": {"specialcharacters"}, "v": VerbatimSubs,
	"specialcharacters": {"specialcharacters"}, "quotes": {"quotes"}, "attributes": {"attributes"},
	"replacements": {"replacements"}, "macros": {"macros"}, "post_replacements": {"post_replacements"},
	"callouts": {"callouts"}, "highlight": {"highlight"},
}

type passthru struct {
	text  string
	subs  []string
	typ   string
	attrs map[string]string
}

type subCtx struct {
	doc    *Document
	node   *Block
	passes []passthru
}

func hasSub(subs []string, name string) bool {
	for _, s := range subs {
		if s == name {
			return true
		}
	}
	return false
}

// ApplySubs はテキストに置換を適用します。
func (d *Document) ApplySubs(text string, subs []string, node *Block) string {
	if text == "" || len(subs) == 0 {
		return text
	}
	c := &subCtx{doc: d, node: node}
	return c.apply(text, subs)
}

func (c *subCtx) apply(text string, subs []string) string {
	if text == "" || len(subs) == 0 {
		return text
	}
	hasPass := false
	if hasSub(subs, "macros") {
		before := len(c.passes)
		text = c.extractPassthroughs(text)
		hasPass = len(c.passes) > before || len(c.passes) > 0
	}
	for _, typ := range subs {
		switch typ {
		case "specialcharacters":
			text = subSpecialChars(text)
		case "quotes":
			text = c.subQuotes(text)
		case "attributes":
			if strings.Contains(text, "{") {
				text = c.doc.SubAttributes(text)
			}
		case "replacements":
			text = subReplacements(text)
		case "macros":
			text = c.subMacros(text)
		case "highlight":
			// シンタックスハイライトは変換側で扱う
		case "callouts":
			text = c.subCallouts(text)
		case "post_replacements":
			text = c.subPostReplacements(text)
		}
	}
	if hasPass {
		text = c.restorePassthroughs(text)
	}
	return text
}

var specialCharsReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func subSpecialChars(text string) string {
	if !strings.ContainsAny(text, "&<>") {
		return text
	}
	return specialCharsReplacer.Replace(text)
}

func (c *subCtx) inline(n *Inline) string {
	n.Parent = c.node
	n.Doc = c.doc
	if n.Attrs == nil {
		n.Attrs = map[string]string{}
	}
	if c.doc.Inline == nil {
		return n.Text
	}
	return c.doc.Inline.ConvertInline(n)
}

// ---------------------------------------------------------------------------
// 引用 (quotes)

type quoteSub struct {
	typ         string
	constrained bool
	rx          *Rx
}

var quoteSubs = []quoteSub{
	{"strong", false, rx(`\\?(?:\[([^\]]+)\])?\*\*(.+?)\*\*`, "m")},
	{"strong", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?\*(\S|\S.*?\S)\*(?!`+cgWord+`)`, "m")},
	{"double", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?"`+"`"+`(\S|\S.*?\S)`+"`"+`"(?!`+cgWord+`)`, "m")},
	{"single", true, rx(`(^|[^`+ccWord+`;:`+"`"+`}])(?:\[([^\]]+)\])?'`+"`"+`(\S|\S.*?\S)`+"`"+`'(?!`+cgWord+`)`, "m")},
	{"monospaced", false, rx(`\\?(?:\[([^\]]+)\])?`+"``"+`(.+?)`+"``", "m")},
	{"monospaced", true, rx(`(^|[^`+ccWord+`;:"'`+"`"+`}])(?:\[([^\]]+)\])?`+"`"+`(\S|\S.*?\S)`+"`"+`(?![`+ccWord+`"'`+"`"+`])`, "m")},
	{"emphasis", false, rx(`\\?(?:\[([^\]]+)\])?__(.+?)__`, "m")},
	{"emphasis", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?_(\S|\S.*?\S)_(?!`+cgWord+`)`, "m")},
	{"mark", false, rx(`\\?(?:\[([^\]]+)\])?##(.+?)##`, "m")},
	{"mark", true, rx(`(^|[^`+ccWord+`&;:}])(?:\[([^\]]+)\])?#(\S|\S.*?\S)#(?!`+cgWord+`)`, "m")},
	{"superscript", false, rx(`\\?(?:\[([^\]]+)\])?\^(\S+?)\^`)},
	{"subscript", false, rx(`\\?(?:\[([^\]]+)\])?~(\S+?)~`)},
}

// compat-mode 用の引用規則
var compatQuoteSubs = []quoteSub{
	{"strong", false, rx(`\\?(?:\[([^\]]+)\])?\*\*(.+?)\*\*`, "m")},
	{"strong", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?\*(\S|\S.*?\S)\*(?!`+cgWord+`)`, "m")},
	{"double", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?`+"``"+`(\S|\S.*?\S)''(?!`+cgWord+`)`, "m")},
	{"single", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?`+"`"+`(\S|\S.*?\S)'(?!`+cgWord+`)`, "m")},
	{"monospaced", false, rx(`\\?(?:\[([^\]]+)\])?\+\+(.+?)\+\+`, "m")},
	{"monospaced", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?\+(\S|\S.*?\S)\+(?!`+cgWord+`)`, "m")},
	{"emphasis", false, rx(`\\?(?:\[([^\]]+)\])?__(.+?)__`, "m")},
	{"emphasis", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?_(\S|\S.*?\S)_(?!`+cgWord+`)`, "m")},
	{"emphasis", true, rx(`(^|[^`+ccWord+`;:}])(?:\[([^\]]+)\])?'(\S|\S.*?\S)'(?!`+cgWord+`)`, "m")},
	{"mark", false, rx(`\\?(?:\[([^\]]+)\])?##(.+?)##`, "m")},
	{"mark", true, rx(`(^|[^`+ccWord+`&;:}])(?:\[([^\]]+)\])?#(\S|\S.*?\S)#(?!`+cgWord+`)`, "m")},
	{"superscript", false, rx(`\\?(?:\[([^\]]+)\])?\^(\S+?)\^`)},
	{"subscript", false, rx(`\\?(?:\[([^\]]+)\])?~(\S+?)~`)},
}

func (d *Document) subQuotes(text string, node *Block) string {
	c := &subCtx{doc: d, node: node}
	return c.subQuotes(text)
}

func (c *subCtx) subQuotes(text string) string {
	sniff := "*_`#^~"
	list := quoteSubs
	if c.doc.Compat {
		sniff = "*_`#^~+'"
		list = compatQuoteSubs
	}
	if !strings.ContainsAny(text, sniff) {
		return text
	}
	for _, qs := range list {
		qs := qs
		text = qs.rx.Gsub(text, func(m *M) string { return c.convertQuotedText(m, qs.typ, qs.constrained) })
	}
	return text
}

func (c *subCtx) convertQuotedText(m *M, typ string, constrained bool) string {
	all := m.All()
	unescapedAttrs := ""
	if strings.HasPrefix(all, "\\") {
		if constrained && m.Has(2) {
			unescapedAttrs = "[" + m.S(2) + "]"
		} else {
			return all[1:]
		}
	}
	if constrained {
		if unescapedAttrs != "" {
			return unescapedAttrs + c.inline(&Inline{Context: "quoted", Type: typ, Text: m.S(3)})
		}
		var attrs map[string]string
		id := ""
		if m.Has(2) {
			attrs = c.parseQuotedTextAttributes(m.S(2))
			id = attrs["id"]
			if typ == "mark" {
				typ = "unquoted"
			}
		}
		return m.S(1) + c.inline(&Inline{Context: "quoted", Type: typ, Text: m.S(3), ID: id, Attrs: attrs})
	}
	var attrs map[string]string
	id := ""
	if m.Has(1) {
		attrs = c.parseQuotedTextAttributes(m.S(1))
		id = attrs["id"]
		if typ == "mark" {
			typ = "unquoted"
		}
	}
	return c.inline(&Inline{Context: "quoted", Type: typ, Text: m.S(2), ID: id, Attrs: attrs})
}

func (c *subCtx) parseQuotedTextAttributes(str string) map[string]string {
	if strings.Contains(str, "{") {
		str = c.doc.SubAttributes(str)
	}
	if i := strings.Index(str, ","); i >= 0 {
		str = str[:i]
	}
	str = strings.TrimSpace(str)
	attrs := map[string]string{}
	if str == "" {
		return attrs
	}
	if strings.HasPrefix(str, ".") || strings.HasPrefix(str, "#") {
		before, after, _ := strings.Cut(str, "#")
		dots := func(s string) string { return strings.TrimLeft(strings.ReplaceAll(s, ".", " "), " ") }
		if after == "" {
			if len(before) > 1 {
				attrs["role"] = dots(before)
			}
		} else {
			id, roles, _ := strings.Cut(after, ".")
			if id != "" {
				attrs["id"] = id
			}
			switch {
			case roles == "":
				if len(before) > 1 {
					attrs["role"] = dots(before)
				}
			case len(before) > 1:
				attrs["role"] = dots(before + "." + roles)
			default:
				attrs["role"] = strings.ReplaceAll(roles, ".", " ")
			}
		}
		return attrs
	}
	attrs["role"] = str
	return attrs
}

// ---------------------------------------------------------------------------
// 属性参照 (attributes)

// SubAttributes は {name} 形式の属性参照を展開します。
func (d *Document) SubAttributes(text string) string {
	return d.subAttributesOpts(text, "")
}

func (d *Document) subAttributesOpts(text, attributeMissing string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	drop, dropLine, dropEmptyLine := false, false, false
	text = attributeReferenceRx.Gsub(text, func(m *M) string {
		if m.S(1) == "\\" || m.S(4) == "\\" {
			return "{" + m.S(2) + "}"
		}
		if m.Has(3) {
			args := strings.SplitN(m.S(2), ":", 3)
			directive := args[0]
			args = args[1:]
			switch directive {
			case "set":
				value := ""
				if len(args) > 1 {
					value = args[1]
				}
				name := args[0]
				ok := false
				if strings.HasPrefix(name, "!") {
					d.UnsetAttr(strings.ToLower(name[1:]))
				} else if strings.HasSuffix(name, "!") {
					d.UnsetAttr(strings.ToLower(strings.TrimSuffix(name, "!")))
				} else {
					ok = d.SetAttr(strings.ToLower(name), value)
				}
				if ok || d.AttrOr("attribute-undefined", "drop-line") != "drop-line" {
					drop, dropEmptyLine = true, true
					return del
				}
				drop, dropLine = true, true
				return can
			case "counter2":
				seed := ""
				if len(args) > 1 {
					seed = args[1]
				}
				d.Counter(args[0], seed)
				drop, dropEmptyLine = true, true
				return del
			default:
				seed := ""
				if len(args) > 1 {
					seed = args[1]
				}
				return d.Counter(args[0], seed)
			}
		}
		key := strings.ToLower(m.S(2))
		if v, ok := d.Attributes[key]; ok {
			return v
		}
		if v, ok := intrinsicAttributes[key]; ok {
			return v
		}
		missing := attributeMissing
		if missing == "" {
			missing = d.AttrOr("attribute-missing", "skip")
		}
		switch missing {
		case "drop":
			drop, dropEmptyLine = true, true
			return del
		case "drop-line":
			d.Info("dropping line containing reference to missing attribute: " + key)
			drop, dropLine = true, true
			return can
		case "warn":
			d.Warn("skipping reference to missing attribute: " + key)
		}
		return m.All()
	})
	if !drop {
		return text
	}
	if dropEmptyLine {
		for strings.Contains(text, del+del) {
			text = strings.ReplaceAll(text, del+del, del)
		}
		lines := strings.Split(text, "\n")
		out := lines[:0]
		for _, line := range lines {
			if line == del || (dropLine && strings.Contains(line, can)) {
				continue
			}
			out = append(out, line)
		}
		return strings.ReplaceAll(strings.Join(out, "\n"), del, "")
	}
	if strings.Contains(text, "\n") {
		lines := strings.Split(text, "\n")
		out := lines[:0]
		for _, line := range lines {
			if !strings.Contains(line, can) {
				out = append(out, line)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// ---------------------------------------------------------------------------
// 文字置換 (replacements)

type replacement struct {
	rx      *Rx
	repl    string
	restore string // "none" "leading" "bounding"
}

var replacements = []replacement{
	{rx(`\\?\(C\)`), "&#169;", "none"},
	{rx(`\\?\(R\)`), "&#174;", "none"},
	{rx(`\\?\(TM\)`), "&#8482;", "none"},
	{rx(`(?: |\n|^|\\)--(?: |\n|$)`), "&#8201;&#8212;&#8201;", "none"},
	{rx(`(` + cgWord + `)\\?--(?=` + cgWord + `)`), "&#8212;&#8203;", "leading"},
	{rx(`\\?\.\.\.`), "&#8230;&#8203;", "none"},
	{rx("\\\\?`'"), "&#8217;", "none"},
	{rx(`(` + cgAlnum + `)\\?'(?=` + cgAlpha + `)`), "&#8217;", "leading"},
	{rx(`\\?-&gt;`), "&#8594;", "none"},
	{rx(`\\?=&gt;`), "&#8658;", "none"},
	{rx(`\\?&lt;-`), "&#8592;", "none"},
	{rx(`\\?&lt;=`), "&#8656;", "none"},
	{rx(`\\?(&)amp;((?:[a-zA-Z][a-zA-Z]+\d{0,2}|#\d\d\d{0,4}|#x[\da-fA-F][\da-fA-F][\da-fA-F]{0,3});)`), "", "bounding"},
}

var replaceableTextRx = rx(`[&']|--|\.\.\.|\([CRT]M?\)`)

func subReplacements(text string) string {
	if !replaceableTextRx.Test(text) {
		return text
	}
	for _, r := range replacements {
		r := r
		text = r.rx.Gsub(text, func(m *M) string {
			captured := m.All()
			if strings.Contains(captured, "\\") {
				return strings.Replace(captured, "\\", "", 1)
			}
			switch r.restore {
			case "none":
				return r.repl
			case "bounding":
				return m.S(1) + r.repl + m.S(2)
			}
			return m.S(1) + r.repl
		})
	}
	return text
}

// ---------------------------------------------------------------------------
// 後置換 (post_replacements)

func (c *subCtx) subPostReplacements(text string) string {
	hardbreaks := c.doc.HasAttr("hardbreaks-option") || c.doc.HasAttr("hardbreaks")
	if c.node != nil && c.node.HasAttr("hardbreaks-option") {
		hardbreaks = true
	}
	if hardbreaks {
		lines := strings.Split(text, "\n")
		if len(lines) < 2 {
			return text
		}
		last := lines[len(lines)-1]
		out := make([]string, 0, len(lines))
		for _, line := range lines[:len(lines)-1] {
			line = strings.TrimSuffix(line, " +")
			out = append(out, c.inline(&Inline{Context: "break", Type: "line", Text: line}))
		}
		out = append(out, last)
		return strings.Join(out, "\n")
	}
	if strings.Contains(text, "+") && strings.Contains(text, " +") {
		return hardLineBreakRx.Gsub(text, func(m *M) string {
			return c.inline(&Inline{Context: "break", Type: "line", Text: m.S(1)})
		})
	}
	return text
}

// ---------------------------------------------------------------------------
// コールアウト (callouts)

func (c *subCtx) subCallouts(text string) string {
	autonum := 0
	return calloutSourceRx.Gsub(text, func(m *M) string {
		if m.Has(2) {
			return strings.Replace(m.All(), "\\", "", 1)
		}
		num := m.S(4)
		if num == "." {
			autonum++
			num = strconv.Itoa(autonum)
		}
		attrs := map[string]string{}
		if m.Has(1) {
			attrs["guard"] = m.S(1)
		}
		return c.inline(&Inline{Context: "callout", Text: num, ID: c.doc.callouts.readNextID(), Attrs: attrs})
	})
}

// ---------------------------------------------------------------------------
// パススルー

var singlePlusInlinePassRx = rx(`^(\\)?\+(\S|\S.*?\S)\+$`, "m")

func (c *subCtx) addPass(p passthru) string {
	c.passes = append(c.passes, p)
	return passStart + strconv.Itoa(len(c.passes)-1) + passEnd
}

func (c *subCtx) extractPassthroughs(text string) string {
	compat := c.doc.Compat
	if strings.Contains(text, "++") || strings.Contains(text, "$$") || strings.Contains(text, "ss:") {
		text = inlinePassMacroRx.Gsub(text, func(m *M) string {
			preceding := ""
			if m.Has(4) {
				boundary := m.S(4)
				if compat && boundary == "++" {
					prefix := m.S(1) + m.S(3)
					if m.Has(2) {
						prefix = m.S(1) + "[" + m.S(2) + "]" + m.S(3)
					}
					return prefix + "++" + c.extractPassthroughs(m.S(5)) + "++"
				}
				var attrs map[string]string
				oldBehavior := false
				escapeCount := len(m.S(3))
				if m.Has(2) {
					attrlist := m.S(2)
					switch {
					case escapeCount > 0:
						return m.S(1) + "[" + attrlist + "]" + strings.Repeat("\\", escapeCount-1) + boundary + m.S(5) + boundary
					case m.S(1) == "\\":
						preceding = "[" + attrlist + "]"
					case boundary == "++" && attrlist == "x-":
						oldBehavior = true
						attrs = map[string]string{}
					case boundary == "++" && strings.HasSuffix(attrlist, " x-"):
						oldBehavior = true
						attrs = c.parseQuotedTextAttributes(attrlist[:len(attrlist)-3])
					default:
						attrs = c.parseQuotedTextAttributes(attrlist)
					}
				} else if escapeCount > 0 {
					return strings.Repeat("\\", escapeCount-1) + boundary + m.S(5) + boundary
				}
				subs := BasicSubs
				if boundary == "+++" {
					subs = NoSubs
				}
				switch {
				case attrs != nil && oldBehavior:
					return preceding + c.addPass(passthru{text: m.S(5), subs: NormalSubs, typ: "monospaced", attrs: attrs})
				case attrs != nil:
					return preceding + c.addPass(passthru{text: m.S(5), subs: subs, typ: "unquoted", attrs: attrs})
				}
				return preceding + c.addPass(passthru{text: m.S(5), subs: subs})
			}
			// pass:[]
			if m.S(6) == "\\" {
				return m.All()[1:]
			}
			content := normalizeText(m.S(8), false, true)
			if m.Has(7) {
				return c.addPass(passthru{text: content, subs: resolvePassSubs(m.S(7))})
			}
			return c.addPass(passthru{text: content})
		})
	}
	if !compat && (strings.Contains(text, "+") || strings.Contains(text, "-]")) {
		text = inlinePassRx.Gsub(text, func(m *M) string {
			preceding := m.S(1)
			attrlist, hasAttrlist := "", false
			if m.Has(4) {
				attrlist, hasAttrlist = m.S(4), true
			} else if m.Has(3) {
				attrlist, hasAttrlist = m.S(3), true
			}
			escaped := m.Has(5)
			quotedText := m.S(6)
			formatMark := m.S(7)
			content := m.S(8)
			oldBehavior := false
			if hasAttrlist {
				if attrlist == "x-" {
					oldBehavior = true
					hasAttrlist = false
				} else if strings.HasSuffix(attrlist, " x-") {
					oldBehavior = true
					attrlist = attrlist[:len(attrlist)-3]
				}
			}
			var attrs map[string]string
			if hasAttrlist {
				switch {
				case formatMark == "`" && !oldBehavior:
					esc := ""
					if escaped {
						esc = "\\"
					}
					return c.extractInnerPassthrough(content, preceding+"["+attrlist+"]"+esc)
				case escaped:
					return preceding + "[" + attrlist + "]" + quotedText[1:]
				case preceding == "\\":
					preceding = "[" + attrlist + "]"
				default:
					attrs = c.parseQuotedTextAttributes(attrlist)
				}
			} else if formatMark == "`" && !oldBehavior {
				esc := ""
				if escaped {
					esc = "\\"
				}
				return c.extractInnerPassthrough(content, preceding+esc)
			} else if escaped {
				return preceding + quotedText[1:]
			}
			switch {
			case attrs != nil && oldBehavior:
				subs := NormalSubs
				if formatMark == "`" {
					subs = BasicSubs
				}
				return preceding + c.addPass(passthru{text: content, subs: subs, attrs: attrs, typ: "monospaced"})
			case attrs != nil:
				return preceding + c.addPass(passthru{text: content, subs: BasicSubs, attrs: attrs, typ: "unquoted"})
			case oldBehavior:
				subs := NormalSubs
				if formatMark == "`" {
					subs = BasicSubs
				}
				return preceding + c.addPass(passthru{text: content, subs: subs, attrs: map[string]string{}, typ: "monospaced"})
			}
			return preceding + c.addPass(passthru{text: content, subs: BasicSubs})
		})
	}
	if strings.Contains(text, ":") && (strings.Contains(text, "stem:") || strings.Contains(text, "math:")) {
		text = inlineStemMacroRx.Gsub(text, func(m *M) string {
			if strings.HasPrefix(m.All(), "\\") {
				return m.All()[1:]
			}
			typ := m.S(1)
			if typ == "stem" {
				typ = "asciimath"
				if v := c.doc.Attr("stem"); v == "latexmath" || v == "latex" || v == "tex" {
					typ = "latexmath"
				}
			}
			content := normalizeText(m.S(3), false, true)
			if typ == "latexmath" && strings.HasPrefix(content, "$") && strings.HasSuffix(content, "$") && len(content) >= 2 {
				content = content[1 : len(content)-1]
			}
			subs := BasicSubs
			if m.Has(2) {
				subs = resolvePassSubs(m.S(2))
			}
			return c.addPass(passthru{text: content, subs: subs, typ: typ})
		})
	}
	return text
}

func (c *subCtx) extractInnerPassthrough(text, pre string) string {
	if strings.HasSuffix(text, "+") && (strings.HasPrefix(text, "+") || strings.HasPrefix(text, "\\+")) {
		if m := singlePlusInlinePassRx.Match(text); m != nil {
			if m.Has(1) {
				return pre + "`+" + m.S(2) + "+`"
			}
			return pre + "`" + c.addPass(passthru{text: m.S(2), subs: BasicSubs}) + "`"
		}
	}
	return pre + "`" + text + "`"
}

func (c *subCtx) restorePassthroughs(text string) string {
	if !strings.Contains(text, passStart) {
		return text
	}
	return passSlotRx.Gsub(text, func(m *M) string {
		idx, _ := strconv.Atoi(m.S(1))
		if idx < 0 || idx >= len(c.passes) {
			c.doc.Error("unresolved passthrough detected: " + text)
			return "??pass??"
		}
		p := c.passes[idx]
		subbed := c.apply(p.text, p.subs)
		if p.typ != "" {
			subbed = c.inline(&Inline{Context: "quoted", Type: p.typ, Text: subbed, ID: p.attrs["id"], Attrs: p.attrs})
		}
		if strings.Contains(subbed, passStart) {
			return c.restorePassthroughs(subbed)
		}
		return subbed
	})
}

func normalizeText(text string, normalizeWhitespace, unescapeBrackets bool) string {
	if text == "" {
		return text
	}
	if normalizeWhitespace {
		text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	}
	if unescapeBrackets && strings.Contains(text, "]") {
		text = strings.ReplaceAll(text, "\\]", "]")
	}
	return text
}

func resolvePassSubs(spec string) []string {
	return ResolveSubs(spec, "inline", nil)
}

// ResolveSubs は subs 属性の指定 (例: "attributes+" "-callouts" "quotes,macros") を置換リストに解決します。
func ResolveSubs(spec, kind string, defaults []string) []string {
	if spec == "" {
		return nil
	}
	spec = strings.ReplaceAll(spec, " ", "")
	var candidates []string
	modifiers := strings.ContainsAny(spec, "+-")
	if modifiers {
		candidates = append(candidates, defaults...)
	}
	remove := func(names []string) {
		out := candidates[:0]
		for _, c := range candidates {
			if !hasSub(names, c) {
				out = append(out, c)
			}
		}
		candidates = out
	}
	for _, key := range strings.Split(spec, ",") {
		if key == "" {
			continue
		}
		op := ""
		if modifiers {
			switch {
			case strings.HasPrefix(key, "+"):
				op, key = "append", key[1:]
			case strings.HasPrefix(key, "-"):
				op, key = "remove", key[1:]
			case strings.HasSuffix(key, "+"):
				op, key = "prepend", strings.TrimSuffix(key, "+")
			}
		}
		resolved, ok := subAliases[key]
		if !ok || (kind == "inline" && len(key) > 1 && (key == "verbatim" || key == "v")) {
			if kind == "inline" && (key == "verbatim" || key == "v") {
				resolved = BasicSubs
			} else if !ok {
				continue
			}
		}
		switch op {
		case "append":
			candidates = append(candidates, resolved...)
		case "prepend":
			candidates = append(append([]string{}, resolved...), candidates...)
		case "remove":
			remove(resolved)
		default:
			candidates = append(candidates, resolved...)
		}
	}
	// 重複を除く
	seen := map[string]bool{}
	out := []string{}
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// マクロ (macros)

func basenameNoExt(target string) string {
	base := path.Base(strings.ReplaceAll(target, "\\", "/"))
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return base
}

func (c *subCtx) extractAttributesFromText(text string) (string, map[string]string) {
	attrlist := strings.ReplaceAll(text, "\n", " ")
	attrs := c.doc.ParseAttributes(attrlist, nil, c.node, nil)
	if resolved, ok := attrs["1"]; ok {
		if resolved == attrlist {
			return text, map[string]string{}
		}
		return resolved, attrs
	}
	return "", attrs
}

func splitSimpleCSV(str string) []string {
	if str == "" {
		return nil
	}
	if strings.Contains(str, "\"") {
		var values []string
		var accum strings.Builder
		quoteOpen := false
		for _, ch := range str {
			switch ch {
			case ',':
				if quoteOpen {
					accum.WriteRune(ch)
				} else {
					values = append(values, strings.TrimSpace(accum.String()))
					accum.Reset()
				}
			case '"':
				quoteOpen = !quoteOpen
			default:
				accum.WriteRune(ch)
			}
		}
		return append(values, strings.TrimSpace(accum.String()))
	}
	parts := strings.Split(str, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func (c *subCtx) subMacros(text string) string {
	doc := c.doc
	foundSquareBracket := strings.Contains(text, "[")
	foundColon := strings.Contains(text, ":")
	foundMacroish := foundSquareBracket && foundColon
	foundMacroishShort := foundMacroish && strings.Contains(text, ":[")

	if doc.HasAttr("experimental") {
		if foundMacroishShort && (strings.Contains(text, "kbd:") || strings.Contains(text, "btn:")) {
			text = inlineKbdBtnMacroRx.Gsub(text, func(m *M) string {
				if m.Has(1) {
					return m.All()[1:]
				}
				if m.S(2) == "kbd" {
					keys := strings.TrimSpace(m.S(3))
					if strings.Contains(keys, "]") {
						keys = strings.ReplaceAll(keys, "\\]", "]")
					}
					var list []string
					delimIdx := -1
					if len(keys) > 1 {
						ci := strings.Index(keys[1:], ",")
						pi := strings.Index(keys[1:], "+")
						switch {
						case ci >= 0 && pi >= 0:
							delimIdx = 1 + min(ci, pi)
						case ci >= 0:
							delimIdx = 1 + ci
						case pi >= 0:
							delimIdx = 1 + pi
						}
					}
					if delimIdx >= 0 {
						delim := keys[delimIdx : delimIdx+1]
						if strings.HasSuffix(keys, delim) {
							list = strings.Split(keys[:len(keys)-1], delim)
							for i := range list {
								list[i] = strings.TrimSpace(list[i])
							}
							list[len(list)-1] += delim
						} else {
							for _, k := range strings.Split(keys, delim) {
								list = append(list, strings.TrimSpace(k))
							}
						}
					} else {
						list = []string{keys}
					}
					return c.inline(&Inline{Context: "kbd", List: list})
				}
				return c.inline(&Inline{Context: "button", Text: normalizeText(m.S(3), true, true)})
			})
		}
		if foundMacroish && strings.Contains(text, "menu:") {
			text = inlineMenuMacroRx.Gsub(text, func(m *M) string {
				if strings.HasPrefix(m.All(), "\\") {
					return m.All()[1:]
				}
				menu := m.S(1)
				var submenus []string
				attrs := map[string]string{"menu": menu}
				if m.Has(2) {
					items := m.S(2)
					if strings.Contains(items, "]") {
						items = strings.ReplaceAll(items, "\\]", "]")
					}
					delim := ""
					if strings.Contains(items, "&gt;") {
						delim = "&gt;"
					} else if strings.Contains(items, ",") {
						delim = ","
					}
					if delim != "" {
						for _, it := range strings.Split(items, delim) {
							submenus = append(submenus, strings.TrimSpace(it))
						}
						attrs["menuitem"] = submenus[len(submenus)-1]
						submenus = submenus[:len(submenus)-1]
					} else {
						attrs["menuitem"] = strings.TrimRight(items, " \t\n")
					}
				}
				return c.inline(&Inline{Context: "menu", Attrs: attrs, List: submenus})
			})
		}
		if strings.Contains(text, "\"") && strings.Contains(text, "&gt;") {
			text = inlineMenuRx.Gsub(text, func(m *M) string {
				if strings.HasPrefix(m.All(), "\\") {
					return m.All()[1:]
				}
				var parts []string
				for _, it := range strings.Split(m.S(1), "&gt;") {
					parts = append(parts, strings.TrimSpace(it))
				}
				attrs := map[string]string{"menu": parts[0]}
				submenus := parts[1:]
				if len(submenus) > 0 {
					attrs["menuitem"] = submenus[len(submenus)-1]
					submenus = submenus[:len(submenus)-1]
				}
				return c.inline(&Inline{Context: "menu", Attrs: attrs, List: submenus})
			})
		}
	}

	if foundMacroish && (strings.Contains(text, "image:") || strings.Contains(text, "icon:")) {
		text = inlineImageMacroRx.Gsub(text, func(m *M) string {
			all := m.All()
			if strings.HasPrefix(all, "\\") {
				return all[1:]
			}
			typ, posattrs := "image", []string{"alt", "width", "height"}
			if strings.HasPrefix(all, "icon:") {
				typ, posattrs = "icon", []string{"size"}
			}
			target := m.S(1)
			attrs := doc.ParseAttributes(normalizeText(m.S(2), false, true), posattrs, c.node, nil)
			if typ != "icon" {
				if v, ok := doc.Attributes["imagesdir"]; ok {
					attrs["imagesdir"] = v
				}
			}
			if _, ok := attrs["alt"]; !ok {
				alt := strings.NewReplacer("_", " ", "-", " ").Replace(basenameNoExt(target))
				attrs["alt"] = alt
				attrs["default-alt"] = alt
			}
			return c.inline(&Inline{Context: "image", Type: typ, Target: target, Attrs: attrs})
		})
	}

	if (strings.Contains(text, "((") && strings.Contains(text, "))")) || (foundMacroishShort && strings.Contains(text, "dexterm")) {
		text = inlineIndextermMacroRx.Gsub(text, func(m *M) string {
			all := m.All()
			switch m.S(1) {
			case "indexterm":
				if strings.HasPrefix(all, "\\") {
					return all[1:]
				}
				attrlist := normalizeText(m.S(2), true, true)
				n := &Inline{Context: "indexterm", Attrs: map[string]string{}}
				if strings.Contains(attrlist, "=") {
					attrs := doc.ParseAttributes(attrlist, nil, c.node, nil)
					if primary, ok := attrs["1"]; ok {
						n.List = []string{primary}
						n.Attrs = attrs
					} else {
						n.List = []string{attrlist}
					}
				} else {
					n.List = splitSimpleCSV(attrlist)
				}
				return c.inline(n)
			case "indexterm2":
				if strings.HasPrefix(all, "\\") {
					return all[1:]
				}
				term := normalizeText(m.S(2), true, true)
				n := &Inline{Context: "indexterm", Type: "visible", Attrs: map[string]string{}}
				if strings.Contains(term, "=") {
					attrs := doc.ParseAttributes(term, nil, c.node, nil)
					if primary, ok := attrs["1"]; ok {
						term = primary
						n.Attrs = attrs
					}
				}
				n.Text = term
				return c.inline(n)
			}
			encl := m.S(3)
			visible := true
			before, after := "", ""
			wrap := false
			if strings.HasPrefix(all, "\\") {
				if strings.HasPrefix(encl, "(") && strings.HasSuffix(encl, ")") {
					encl = encl[1 : len(encl)-1]
					before, after, wrap = "(", ")", true
				} else {
					return all[1:]
				}
			} else if strings.HasPrefix(encl, "(") {
				if strings.HasSuffix(encl, ")") {
					encl, visible = encl[1:len(encl)-1], false
				} else {
					encl, before, after, wrap = encl[1:], "(", "", true
				}
			} else if strings.HasSuffix(encl, ")") {
				encl, before, after, wrap = encl[:len(encl)-1], "", ")", true
			}
			n := &Inline{Context: "indexterm", Attrs: map[string]string{}}
			terms := normalizeText(encl, true, false)
			if strings.Contains(terms, ";&") {
				if strings.Contains(terms, " &gt;&gt; ") {
					var see string
					terms, see, _ = strings.Cut(terms, " &gt;&gt; ")
					n.Attrs["see"] = see
				} else if strings.Contains(terms, " &amp;&gt; ") {
					parts := strings.Split(terms, " &amp;&gt; ")
					terms = parts[0]
					n.Attrs["see-also"] = strings.Join(parts[1:], "\x1f")
				}
			}
			if visible {
				n.Type = "visible"
				n.Text = terms
			} else {
				n.List = splitSimpleCSV(terms)
			}
			subbed := c.inline(n)
			if wrap {
				return before + subbed + after
			}
			return subbed
		})
	}

	if foundColon && strings.Contains(text, "://") {
		text = inlineLinkRx.Gsub(text, func(m *M) string {
			all := m.All()
			g1 := m.S(1)
			if m.Has(2) && !m.Has(5) {
				// <https://example.org> 形式
				if strings.HasPrefix(g1, "\\") {
					return all[1:]
				}
				if strings.HasPrefix(m.S(3), "\\") {
					return g1 + all[len(g1)+1:]
				}
				if !m.Has(6) {
					return all
				}
				target := m.S(3) + m.S(6)
				linkText := target
				if doc.HasAttr("hide-uri-scheme") {
					linkText = uriSniffRx.Sub(target, func(*M) string { return "" })
				}
				return c.inline(&Inline{Context: "anchor", Type: "link", Text: linkText, Target: target, Attrs: map[string]string{"role": "bare"}})
			}
			if strings.HasPrefix(m.S(3), "\\") {
				return g1 + all[len(g1)+1:]
			}
			prefix, suffix := g1, ""
			target := m.S(3)
			if m.Has(4) {
				target += m.S(4)
			} else {
				target += m.S(7)
			}
			linkText, hasText := "", false
			if m.Has(5) {
				if prefix == "link:" {
					prefix = ""
				}
				if m.S(5) != "" {
					linkText, hasText = m.S(5), true
				}
			} else {
				switch prefix {
				case "link:", "\"", "'":
					return all
				}
				switch m.S(8) {
				case ";", ":":
					last := m.S(8)
					target = target[:len(target)-1]
					if strings.HasSuffix(target, ")") {
						target = target[:len(target)-1]
						suffix = ")" + last
					} else {
						suffix = last
					}
					if target == m.S(3) {
						return all
					}
				}
			}
			var attrs map[string]string
			n := &Inline{Context: "anchor", Type: "link"}
			bare := false
			if hasText {
				if strings.Contains(linkText, "]") {
					linkText = strings.ReplaceAll(linkText, "\\]", "]")
				}
				if !doc.Compat && strings.Contains(linkText, "=") {
					linkText, attrs = c.extractAttributesFromText(linkText)
					n.ID = attrs["id"]
				}
				if strings.HasSuffix(linkText, "^") {
					linkText = linkText[:len(linkText)-1]
					if attrs == nil {
						attrs = map[string]string{}
					}
					if _, ok := attrs["window"]; !ok {
						attrs["window"] = "_blank"
					}
				}
				if linkText == "" {
					bare = true
				}
			} else {
				bare = true
			}
			if bare {
				linkText = target
				if doc.HasAttr("hide-uri-scheme") {
					linkText = uriSniffRx.Sub(target, func(*M) string { return "" })
				}
				if attrs == nil {
					attrs = map[string]string{}
				}
				if role, ok := attrs["role"]; ok {
					attrs["role"] = "bare " + role
				} else {
					attrs["role"] = "bare"
				}
			}
			n.Text, n.Target, n.Attrs = linkText, target, attrs
			return prefix + c.inline(n) + suffix
		})
	}

	if foundMacroish && (strings.Contains(text, "link:") || strings.Contains(text, "ilto:")) {
		text = inlineLinkMacroRx.Gsub(text, func(m *M) string {
			all := m.All()
			if strings.HasPrefix(all, "\\") {
				return all[1:]
			}
			mailto := m.Has(1)
			target := m.S(2)
			mailtoText := ""
			if mailto {
				mailtoText = target
				target = "mailto:" + target
			}
			var attrs map[string]string
			n := &Inline{Context: "anchor", Type: "link"}
			linkText := m.S(3)
			if linkText != "" {
				if strings.Contains(linkText, "]") {
					linkText = strings.ReplaceAll(linkText, "\\]", "]")
				}
				if mailto {
					if !doc.Compat && strings.Contains(linkText, ",") {
						linkText, attrs = c.extractAttributesFromText(linkText)
						n.ID = attrs["id"]
						if subject, ok := attrs["2"]; ok {
							target += "?subject=" + url.PathEscape(subject)
							if body, ok := attrs["3"]; ok {
								target += "&amp;body=" + url.PathEscape(body)
							}
						}
					}
				} else if !doc.Compat && strings.Contains(linkText, "=") {
					linkText, attrs = c.extractAttributesFromText(linkText)
					n.ID = attrs["id"]
				}
				if strings.HasSuffix(linkText, "^") {
					linkText = linkText[:len(linkText)-1]
					if attrs == nil {
						attrs = map[string]string{}
					}
					if _, ok := attrs["window"]; !ok {
						attrs["window"] = "_blank"
					}
				}
			}
			if linkText == "" {
				if mailto {
					linkText = mailtoText
				} else {
					linkText = target
					if doc.HasAttr("hide-uri-scheme") {
						if stripped := uriSniffRx.Sub(target, func(*M) string { return "" }); stripped != "" {
							linkText = stripped
						}
					}
					if attrs == nil {
						attrs = map[string]string{}
					}
					if role, ok := attrs["role"]; ok {
						attrs["role"] = "bare " + role
					} else {
						attrs["role"] = "bare"
					}
				}
			}
			n.Text, n.Target, n.Attrs = linkText, target, attrs
			return c.inline(n)
		})
	}

	if strings.Contains(text, "@") {
		text = inlineEmailRx.Gsub(text, func(m *M) string {
			all := m.All()
			if m.Has(1) {
				if m.S(1) == "\\" {
					return all[1:]
				}
				return all
			}
			return c.inline(&Inline{Context: "anchor", Type: "link", Text: all, Target: "mailto:" + all})
		})
	}

	if foundSquareBracket && c.node != nil && c.node.Context == "list_item" && c.node.Parent != nil && c.node.Parent.Style == "bibliography" {
		text = inlineBiblioAnchorRx.Sub(text, func(m *M) string {
			return c.inline(&Inline{Context: "anchor", Type: "bibref", ID: m.S(1), Text: m.S(2)})
		})
	}

	if (foundSquareBracket && strings.Contains(text, "[[")) || (foundMacroish && strings.Contains(text, "or:")) {
		text = inlineAnchorRx.Gsub(text, func(m *M) string {
			if m.Has(1) {
				return m.All()[1:]
			}
			id, reftext := m.S(2), m.S(3)
			if !m.Has(2) {
				id, reftext = m.S(4), m.S(5)
				if strings.Contains(reftext, "]") {
					reftext = strings.ReplaceAll(reftext, "\\]", "]")
				}
			}
			return c.inline(&Inline{Context: "anchor", Type: "ref", ID: id, Text: reftext})
		})
	}

	if (strings.Contains(text, "&") && strings.Contains(text, ";&l")) || (foundMacroish && strings.Contains(text, "xref:")) {
		text = inlineXrefMacroRx.Gsub(text, func(m *M) string {
			all := m.All()
			if strings.HasPrefix(all, "\\") {
				return all[1:]
			}
			attrs := map[string]string{}
			refid := ""
			linkText, hasText := "", false
			macro := false
			if m.Has(1) {
				refid = m.S(1)
				if strings.Contains(refid, ",") {
					var lt string
					refid, lt, _ = strings.Cut(refid, ",")
					if lt = strings.TrimLeft(lt, " \t\n"); lt != "" {
						linkText, hasText = lt, true
					}
				}
			} else {
				macro = true
				refid = m.S(2)
				if m.Has(3) {
					linkText, hasText = m.S(3), true
					if strings.Contains(linkText, "]") {
						linkText = strings.ReplaceAll(linkText, "\\]", "]")
					}
					if !doc.Compat && strings.Contains(linkText, "=") {
						linkText, attrs = c.extractAttributesFromText(linkText)
						hasText = linkText != ""
					}
				}
			}
			var pathStr, fragment, target string
			hasPath, hasTarget, src2src := false, false, false
			hashIdx := strings.Index(refid, "#")
			switch {
			case doc.Compat:
				fragment = refid
			case hashIdx >= 0 && (hashIdx == 0 || refid[hashIdx-1] != '&'):
				if hashIdx > 0 {
					if len(refid)-1-hashIdx > 0 {
						pathStr, fragment = refid[:hashIdx], refid[hashIdx+1:]
					} else {
						pathStr = refid[:len(refid)-1]
					}
					hasPath = true
					if macro {
						if strings.HasSuffix(pathStr, ".adoc") {
							pathStr = pathStr[:len(pathStr)-5]
							src2src = true
						} else if path.Ext(pathStr) == "" {
							src2src = true
						}
					} else if ext := path.Ext(pathStr); ext == ".adoc" || ext == ".asciidoc" || ext == ".asc" || ext == ".ad" || ext == ".txt" {
						pathStr = pathStr[:len(pathStr)-len(ext)]
						src2src = true
					} else {
						src2src = true
					}
				} else {
					target, fragment, hasTarget = refid, refid[1:], true
				}
			case macro:
				if strings.HasSuffix(refid, ".adoc") {
					pathStr, hasPath, src2src = refid[:len(refid)-5], true, true
				} else if path.Ext(refid) != "" && !strings.Contains(path.Ext(refid), " ") {
					pathStr, hasPath = refid, true
				} else {
					fragment = refid
				}
			default:
				fragment = refid
			}
			switch {
			case hasTarget:
				refid = fragment
			case hasPath:
				if src2src && doc.Attr("docname") == pathStr {
					if fragment != "" {
						refid, target = fragment, "#"+fragment
					} else {
						refid, target = "", "#"
					}
					hasPath = false
				} else {
					refid = pathStr
					suffix := ""
					if src2src {
						suffix = doc.AttrOr("relfilesuffix", doc.AttrOr("outfilesuffix", ".pdf"))
					}
					pathStr = doc.Attr("relfileprefix") + pathStr + suffix
					if fragment != "" {
						refid, target = refid+"#"+fragment, pathStr+"#"+fragment
					} else {
						target = pathStr
					}
				}
			case doc.Compat:
				refid, target = fragment, "#"+fragment
			case doc.Ref(fragment) != nil:
				refid, target = fragment, "#"+fragment
			case strings.Contains(fragment, " ") || strings.ToLower(fragment) != fragment:
				if resolved := doc.ResolveRefID(fragment); resolved != "" {
					fragment, refid, target = resolved, resolved, "#"+resolved
				} else {
					refid, target = fragment, "#"+fragment
				}
			default:
				refid, target = fragment, "#"+fragment
			}
			if hasPath {
				attrs["path"] = pathStr
			}
			if fragment != "" {
				attrs["fragment"] = fragment
			}
			if refid != "" {
				attrs["refid"] = refid
			}
			n := &Inline{Context: "anchor", Type: "xref", Target: target, Attrs: attrs}
			if hasText {
				n.Text, n.HasText = linkText, true
			}
			return c.inline(n)
		})
	}

	if foundMacroish && strings.Contains(text, "tnote") {
		text = inlineFootnoteMacroRx.Gsub(text, func(m *M) string {
			all := m.All()
			if strings.HasPrefix(all, "\\") {
				return all[1:]
			}
			id, content := "", ""
			hasContent := false
			if m.Has(1) {
				if !m.Has(3) {
					return all
				}
				id, content, hasContent = strings.Cut(m.S(3), ",")
			} else {
				id = m.S(2)
				content, hasContent = m.S(3), m.Has(3)
			}
			n := &Inline{Context: "footnote", Attrs: map[string]string{}}
			register := func() {
				content = c.restorePassthroughs(normalizeText(content, true, true))
				index, _ := strconv.Atoi(doc.Counter("footnote-number", ""))
				doc.RegisterFootnote(&Footnote{Index: index, ID: id, Text: content})
				n.Attrs["index"] = strconv.Itoa(index)
			}
			if id != "" {
				var existing *Footnote
				for _, fn := range doc.AllFootnotes() {
					if fn.ID == id {
						existing = fn
						break
					}
				}
				switch {
				case existing != nil:
					n.Attrs["index"] = strconv.Itoa(existing.Index)
					content = existing.Text
					n.Type, n.Target = "xref", id
				case hasContent && content != "":
					register()
					n.Type, n.ID = "ref", id
				default:
					doc.Warn("invalid footnote reference: " + id)
					n.Type, n.Target, content = "xref", id, id
				}
			} else if hasContent && content != "" {
				register()
			} else {
				return all
			}
			n.Text = content
			return c.inline(n)
		})
	}
	return text
}
