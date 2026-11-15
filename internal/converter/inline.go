package converter

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
)

// ConvertInline はインライン要素を擬似 HTML に変換します (asciidoc.InlineConverter の実装)。
func (c *Converter) ConvertInline(n *asciidoc.Inline) string {
	c.loadTheme(n.Doc)
	switch n.Context {
	case "quoted":
		return c.inlineQuoted(n)
	case "anchor":
		return c.inlineAnchor(n)
	case "footnote":
		return c.inlineFootnote(n)
	case "image":
		return c.inlineImage(n)
	case "break":
		return n.Text + "<br>"
	case "button":
		content := c.theme.StrOr("button_content", "%s")
		return "<button>" + strings.Replace(content, "%s", n.Text, 1) + "</button>"
	case "kbd":
		if len(n.List) == 1 {
			return "<kbd>" + n.List[0] + "</kbd>"
		}
		parts := make([]string, len(n.List))
		for i, k := range n.List {
			parts[i] = "<kbd>" + k + "</kbd>"
		}
		return strings.Join(parts, c.theme.Str("kbd_separator_content"))
	case "menu":
		caret := c.theme.Str("menu_caret_content")
		menu := n.Attrs["menu"]
		item, hasItem := n.Attrs["menuitem"]
		switch {
		case len(n.List) > 0:
			parts := append(append([]string{menu}, n.List...), item)
			return "<menu>" + strings.Join(parts, caret) + "</menu>"
		case hasItem:
			return "<menu>" + menu + caret + item + "</menu>"
		}
		return "<menu>" + menu + "</menu>"
	case "callout":
		return c.inlineCallout(n)
	case "indexterm":
		return c.inlineIndexterm(n)
	}
	return n.Text
}

func (c *Converter) inlineQuoted(n *asciidoc.Inline) string {
	open, close := "", ""
	isTag, quotes := false, false
	switch n.Type {
	case "emphasis":
		open, close, isTag = "<em>", "</em>", true
	case "strong":
		open, close, isTag = "<strong>", "</strong>", true
	case "monospaced", "asciimath", "latexmath":
		open, close, isTag = "<code>", "</code>", true
	case "superscript":
		open, close, isTag = "<sup>", "</sup>", true
	case "subscript":
		open, close, isTag = "<sub>", "</sub>", true
	case "double":
		q := c.quotes()
		open, close, quotes = q[0], q[1], true
	case "single":
		q := c.quotes()
		open, close, quotes = q[2], q[3], true
	case "mark":
		open, close, isTag = "<mark>", "</mark>", true
	}
	inner := n.Text
	if quotes && len(inner) > 3 && strings.HasSuffix(inner, "...") && !strings.HasSuffix(inner[:len(inner)-3], "\\") {
		inner = inner[:len(inner)-3] + "&#8230;"
	}
	var out string
	if roles := n.Role(); roles != "" {
		if isTag {
			out = open[:len(open)-1] + ` class="` + roles + `">` + inner + close
		} else {
			out = `<span class="` + roles + `">` + open + inner + close + "</span>"
		}
	} else {
		out = open + inner + close
	}
	if n.ID != "" {
		return `<a id="` + c.deriveAnchorFromID(n.ID, "") + `">` + dummyText + "</a>" + out
	}
	return out
}

var uriBreakCharsRx = regexp.MustCompile(`(/|\?|&amp;|#)(.)`)

// breakableURI は URI の区切り文字の後ろに改行可能位置 (ゼロ幅スペース) を挿入します。
func breakableURI(uri string) string {
	scheme, address := "", uri
	if i := strings.Index(uri, "://"); i >= 0 {
		scheme, address = uri[:i+3], uri[i+3:]
	}
	if address == "" {
		return uri
	}
	for i := 0; i < 2; i++ {
		address = uriBreakCharsRx.ReplaceAllString(address, "${1}"+zwsp+"${2}")
	}
	return scheme + address
}

func (c *Converter) inlineAnchor(n *asciidoc.Inline) string {
	doc := n.Doc
	target := n.Target
	switch n.Type {
	case "link":
		anchor := ""
		if n.ID != "" {
			anchor = `<a id="` + c.deriveAnchorFromID(n.ID, "") + `">` + dummyText + "</a>"
		}
		role := n.Role()
		classAttr := ""
		if role != "" {
			classAttr = ` class="` + role + `"`
		}
		text := n.Text
		bareTarget := target
		media := doc.AttrOr("media", "screen")
		if media != "screen" && strings.HasPrefix(target, "mailto:") {
			if target[7:] == text {
				if role != "" {
					role += " bare"
				} else {
					role = "bare"
				}
			}
			if doc.HasAttr("hide-uri-scheme") {
				bareTarget = target[7:]
			}
		}
		roles := strings.Fields(role)
		isBare := false
		noBreak := false
		for _, r := range roles {
			if r == "bare" {
				isBare = true
			}
			if r == "nobreak" {
				noBreak = true
			}
		}
		switch {
		case isBare:
			if !noBreak {
				text = breakableURI(text)
			}
			return anchor + `<a href="` + target + `"` + classAttr + ">" + text + "</a>"
		case doc.HasAttr("show-link-uri") || (media != "screen" && doc.AttrUnspecified("show-link-uri")):
			if doc.HasAttr("hide-uri-scheme") {
				if i := strings.Index(bareTarget, "://"); i >= 0 {
					bareTarget = bareTarget[i+3:]
				}
			}
			return anchor + `<a href="` + target + `"` + classAttr + ">" + text + `</a> [<font size="0.85em">` + breakableURI(bareTarget) + "</font>&#93;"
		}
		return anchor + `<a href="` + target + `"` + classAttr + ">" + text + "</a>"
	case "xref":
		if path, ok := n.Attrs["path"]; ok {
			text := n.Text
			if !n.HasText {
				text = path
			}
			return `<a href="` + target + `">` + text + "</a>"
		}
		if refid, ok := n.Attrs["refid"]; ok {
			text, hasText := n.Text, n.HasText
			anchor := ""
			if !hasText && !c.resolvingXref {
				if ref := doc.Ref(refid); ref != nil {
					c.resolvingXref = true
					xrefstyle := n.Attrs["xrefstyle"]
					if xrefstyle == "" {
						xrefstyle = doc.Attr("xrefstyle")
					}
					switch {
					case ref.Block != nil:
						text = ref.Block.Xreftext(xrefstyle)
						hasText = text != ""
					case ref.Inline != nil:
						if ref.Reftext != "" {
							text, hasText = ref.Reftext, true
							if ref.Inline.Type == "bibref" {
								text = "[" + text + "]"
							}
						} else if ref.Inline.Type == "bibref" {
							text, hasText = "["+refid+"]", true
						}
						if ref.Inline.Type == "bibref" && c.scratch == 0 && !c.bibrefRefs[refid] {
							c.bibrefRefs[refid] = true
							anchor = `<a id="` + c.deriveAnchorFromID("_bibref_ref_"+refid, "") + `">` + dummyText + "</a>"
						}
					}
					if strings.Contains(text, "<a") {
						text = dropAnchorRx.ReplaceAllString(text, "")
					}
					c.resolvingXref = false
				}
			}
			if !hasText {
				text = "[" + refid + "]"
			}
			out := anchor + `<a anchor="` + c.deriveAnchorFromID(refid, "") + `">` + text + "</a>"
			return strings.ReplaceAll(out, "]", "&#93;")
		}
		text := n.Text
		if !n.HasText {
			text = "[^top&#93;"
		}
		top := "top"
		if si := c.sectInfo[c.rootDoc(doc).Block]; si != nil {
			top = si.anchor
		} else {
			top = c.deriveAnchorFromID(c.rootDoc(doc).ID, "top")
		}
		return `<a anchor="` + top + `">` + text + "</a>"
	case "ref":
		return `<a id="` + c.deriveAnchorFromID(n.ID, "") + `">` + dummyText + "</a>"
	case "bibref":
		reftext := "[" + n.ID + "]"
		if n.Text != "" {
			reftext = "[" + n.Text + "]"
		}
		if c.bibrefRefs[n.ID] {
			reftext = `<a anchor="` + c.deriveAnchorFromID("_bibref_ref_"+n.ID, "") + `">` + reftext + "</a>"
		}
		return `<a id="` + c.deriveAnchorFromID(n.ID, "") + `">` + dummyText + "</a>" + reftext
	}
	c.logf("WARN", "unknown anchor type: %s", n.Type)
	return ""
}

func (c *Converter) rootDoc(doc *asciidoc.Document) *asciidoc.Document {
	for doc.ParentDoc != nil {
		doc = doc.ParentDoc
	}
	return doc
}

func (c *Converter) inlineFootnote(n *asciidoc.Inline) string {
	if idxStr, ok := n.Attrs["index"]; ok {
		index, _ := strconv.Atoi(idxStr)
		var fn *asciidoc.Footnote
		for _, cand := range n.Doc.AllFootnotes() {
			if cand.Index == index {
				fn = cand
				break
			}
		}
		if fn != nil {
			anchor := ""
			if n.Type != "xref" {
				anchor = `<a id="_footnoteref_` + idxStr + `">` + dummyText + "</a>"
			}
			label := strconv.Itoa(index - len(c.renderedFootnote))
			for _, r := range c.renderedFootnote {
				if r == fn {
					label = fn.Label
					if label == "" {
						label = idxStr
					}
					break
				}
			}
			return `<sup class="wj">` + anchor + `<a anchor="_footnotedef_` + idxStr + `">[` + label + "]</a></sup>"
		}
	}
	if n.Type == "xref" {
		return `<sup class="wj"><font color="` + c.theme.Str("role_unresolved_font_color") + `">[` + n.Text + "]</font></sup>"
	}
	c.logf("WARN", "unknown footnote type: %s", n.Type)
	return ""
}

func (c *Converter) inlineCallout(n *asciidoc.Inline) string {
	num, _ := strconv.Atoi(n.Text)
	result := c.conumGlyphSafe(num)
	if family := c.theme.Str("conum_font_family"); family != "" && family != c.fontFamily {
		result = `<font name="` + family + `">` + result + "</font>"
	}
	if color := c.themeClr("conum_font_color"); color.set {
		result = `<font color="` + color.String() + `">` + result + "</font>"
	}
	return result
}

func (c *Converter) conumGlyphSafe(n int) string {
	if c.conumGlyphs == nil {
		// 変換開始前 (パース中) に呼ばれた場合の既定値
		if n >= 1 && n <= 20 {
			return string(rune(0x2460 + n - 1))
		}
		return "(" + strconv.Itoa(n) + ")"
	}
	return c.conumGlyph(n)
}

func encodeQuotes(s string) string { return strings.ReplaceAll(s, `"`, "&quot;") }

func (c *Converter) inlineImage(n *asciidoc.Inline) string {
	var img string
	alt := n.Attrs["alt"]
	if n.Type == "icon" {
		img = c.inlineIcon(n)
	} else {
		path := c.resolveImagePath(n.Parent, n.Target, true)
		li := c.loadImage(path, n.Target)
		if li == nil {
			img = "[" + alt + "&#93;"
		} else {
			classAttr, fitAttr := "", ""
			if role := n.Role(); role != "" {
				classAttr = ` class="` + role + `"`
			}
			if fit := n.Attrs["fit"]; fit != "" {
				fitAttr = ` fit="` + fit + `"`
			}
			width := li.w * 0.75
			if w, ok := c.explicitImageWidth(n.Attrs, 0, li, false); ok {
				width = w
			}
			img = `<img src="` + path + `" format="` + li.format + `" alt="` + encodeQuotes(alt) + `" width="` + fmt.Sprintf("%.4f", width) + `"` + classAttr + fitAttr + ">"
		}
	}
	if link, ok := n.Attrs["link"]; ok && link != "" {
		if link[0] == '#' {
			return `<a anchor="` + link[1:] + `">` + img + "</a>"
		}
		return `<a href="` + link + `">` + img + "</a>"
	}
	return img
}

// ---------------------------------------------------------------------------
// 索引

type indexTerm struct {
	name     string
	dests    []indexDest
	subterms map[string]*indexTerm
}

type indexDest struct {
	anchor string
	page   int
}

type indexCatalog struct {
	categories      map[string]*indexTerm
	anchors         map[string]*indexDest
	sequence        int
	startPageNumber int
}

func newIndexCatalog() *indexCatalog {
	return &indexCatalog{categories: map[string]*indexTerm{}, anchors: map[string]*indexDest{}, startPageNumber: 1}
}

func (ic *indexCatalog) empty() bool { return len(ic.categories) == 0 }

func (ic *indexCatalog) nextAnchorName() string {
	ic.sequence++
	return fmt.Sprintf("__indexterm-%d", ic.sequence)
}

func (ic *indexCatalog) store(names []string, anchor string) {
	if len(names) == 0 || names[0] == "" {
		return
	}
	initial := strings.ToUpper(string([]rune(names[0])[:1]))
	if initial[0] < 'A' || initial[0] > 'Z' {
		initial = "@"
	}
	cat := ic.categories[initial]
	if cat == nil {
		cat = &indexTerm{name: initial, subterms: map[string]*indexTerm{}}
		ic.categories[initial] = cat
	}
	cur := cat
	for i, name := range names {
		if i >= 3 {
			break
		}
		next := cur.subterms[name]
		if next == nil {
			next = &indexTerm{name: name, subterms: map[string]*indexTerm{}}
			cur.subterms[name] = next
		}
		cur = next
	}
	cur.dests = append(cur.dests, indexDest{anchor: anchor})
}

func (ic *indexCatalog) linkDestToPage(anchor string, page int) {
	for _, cat := range ic.categories {
		var walk func(t *indexTerm)
		walk = func(t *indexTerm) {
			for i := range t.dests {
				if t.dests[i].anchor == anchor {
					t.dests[i].page = page
				}
			}
			for _, s := range t.subterms {
				walk(s)
			}
		}
		walk(cat)
	}
}

func (c *Converter) inlineIndexterm(n *asciidoc.Inline) string {
	visible := n.Type == "visible"
	if c.scratch > 0 || c.index == nil {
		if visible {
			return n.Text
		}
		return ""
	}
	name := c.index.nextAnchorName()
	anchor := `<a id="` + name + `" type="indexterm">` + dummyText + "</a>"
	if visible {
		c.index.store([]string{sanitize(n.Text)}, name)
		return anchor + n.Text
	}
	terms := make([]string, len(n.List))
	for i, t := range n.List {
		terms[i] = sanitize(t)
	}
	c.index.store(terms, name)
	return anchor
}

func (c *Converter) indexPageLabel(page int) string {
	virtual := page - (c.index.startPageNumber - 1)
	if virtual < 1 {
		return strings.ToLower(toRoman(page))
	}
	return strconv.Itoa(virtual)
}

func sortedTermNames(terms map[string]*indexTerm) []string {
	names := make([]string, 0, len(terms))
	for name := range terms {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.ToLower(names[i]), strings.ToLower(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	return names
}

func (c *Converter) convertIndexSection(node *Block) {
	t := c.theme
	columns := int(t.Float("index_columns", 1))
	c.withColumns(columns, t.Float("index_column_gap", 0), func() {
		var keys []string
		for k := range c.index.categories {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		termSpacing := t.Float("description_list_term_spacing", 0)
		needed := termSpacing + 2*c.heightOfTypesetText("A", prose{})
		for _, k := range keys {
			cat := c.index.categories[k]
			if needed > c.cursor() {
				c.advancePage()
			}
			o := prose{align: "left", plain: true, marginBottom: &termSpacing}
			switch t.Str("description_list_term_font_style") {
			case "bold":
				o.bold = true
			case "italic":
				o.italic = true
			case "bold_italic":
				o.bold, o.italic = true, true
			}
			c.inkProse(cat.name, o)
			for _, name := range sortedTermNames(cat.subterms) {
				c.convertIndexTerm(cat.subterms[name])
			}
			if margin := t.Float("prose_margin_bottom", 0); margin > c.cursor() {
				c.advancePage()
			} else {
				c.moveDown(margin)
			}
		}
	})
}

func (c *Converter) convertIndexTerm(term *indexTerm) {
	frags := []frag{{text: term.name}}
	link := frag{}
	link.apply(c.tagProps["link"])
	for _, d := range term.dests {
		if d.page == 0 {
			continue
		}
		frags = append(frags, frag{text: ", "})
		f := link
		f.text, f.anchor = c.indexPageLabel(d.page), d.anchor
		frags = append(frags, f)
	}
	subIndent := c.theme.Float("description_list_description_indent", 0)
	c.typesetFragments(frags, c.calcLineMetrics(c.baseLineHeight), prose{align: "left", hangingIndent: subIndent * 2})
	if len(term.subterms) > 0 {
		c.indent(subIndent, 0, func() {
			for _, name := range sortedTermNames(term.subterms) {
				c.convertIndexTerm(term.subterms[name])
			}
		})
	}
}
