package converter

import (
	"strconv"
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
)

var bullets = map[string]string{"disc": "•", "circle": "◦", "square": "▪", "none": ""}
var ballotBox = map[string]string{"checked": "☑", "unchecked": "☐"}

func (n listNumeral) String() string {
	switch n.kind {
	case "int":
		if n.pad && n.n > -10 && n.n < 10 {
			if n.n < 0 {
				return "-0" + strconv.Itoa(-n.n)
			}
			return "0" + strconv.Itoa(n.n)
		}
		return strconv.Itoa(n.n)
	case "alpha":
		// a, b, ... z, aa, ab ... (String#succ 相当)
		s := ""
		v := n.n
		for v > 0 {
			v--
			ch := byte('a' + v%26)
			if n.upper {
				ch = byte('A' + v%26)
			}
			s = string(ch) + s
			v /= 26
		}
		return s
	case "roman":
		r := toRoman(n.n)
		if !n.upper {
			r = strings.ToLower(r)
		}
		return r
	case "greek":
		return string(rune(0x3b1 + n.n - 1))
	}
	return ""
}

func (c *Converter) listLevel(node *Block) int {
	level := 1
	for p := node.Parent; p != nil; p = p.Parent {
		if p.Context == node.Context {
			level++
		}
	}
	return level
}

func (c *Converter) convertOlist(node *Block) {
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	var num listNumeral
	switch node.Style {
	case "decimal":
		num = listNumeral{kind: "int", n: 1, pad: true}
	case "loweralpha":
		num = listNumeral{kind: "alpha", n: 1}
	case "upperalpha":
		num = listNumeral{kind: "alpha", n: 1, upper: true}
	case "lowerroman":
		num = listNumeral{kind: "roman", n: 1}
	case "upperroman":
		num = listNumeral{kind: "roman", n: 1, upper: true}
	case "lowergreek":
		num = listNumeral{kind: "greek", n: 1}
	case "unstyled", "unnumbered", "no-bullet":
		num = listNumeral{kind: "nil"}
	case "none":
		num = listNumeral{kind: "none"}
	default:
		num = listNumeral{kind: "int", n: 1}
	}
	if num.kind != "nil" && num.kind != "none" {
		start, has := 0, false
		if v, ok := node.Attrs["start"]; ok {
			start, _ = strconv.Atoi(v)
			has = true
		} else if node.Option("reversed") {
			start, has = len(node.Blocks), true
		}
		if has && (start > 1 || (start < 1 && num.kind == "int")) {
			num.n = start
		}
	}
	c.listNumerals = append(c.listNumerals, num)
	c.convertList(node)
	c.listNumerals = c.listNumerals[:len(c.listNumerals)-1]
}

func (c *Converter) convertUlist(node *Block) {
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	bullet := ""
	if node.Option("checklist") {
		bullet = "checkbox"
	} else if node.Style != "" && node.Style != "ulist" {
		switch node.Style {
		case "bibliography":
			if c.theme.Has("ulist_marker_biblio_content") {
				if c.theme.Str("ulist_marker_biblio_content") != "" {
					bullet = "biblio"
				}
			} else {
				bullet = "square"
			}
		case "unstyled", "no-bullet":
		default:
			if _, ok := bullets[node.Style]; ok {
				bullet = node.Style
			} else {
				c.logf("WARN", "unknown unordered list style: %s", node.Style)
				bullet = "disc"
			}
		}
	} else {
		switch c.listLevel(node) {
		case 1:
			bullet = "disc"
		case 2:
			bullet = "circle"
		default:
			bullet = "square"
		}
	}
	c.listBullets = append(c.listBullets, bullet)
	c.convertList(node)
	c.listBullets = c.listBullets[:len(c.listBullets)-1]
}

func (c *Converter) convertList(node *Block) {
	t := c.theme
	if node.HasTitle() {
		c.inkCaption(node.Title(), captionOpts{category: "list"})
	}
	o := prose{}
	if a := resolveTextAlignFromRole(node.Roles()); a != "" {
		o.align = a
	} else if node.Style == "bibliography" {
		o.align = "left"
	} else if a := t.Str("list_text_align"); a != "" {
		o.align = a
	}
	lm := c.calcLineMetrics(c.baseLineHeight)
	listIndent := t.Float("list_indent", 0)
	noMarker := false
	switch node.Context {
	case "ulist":
		noMarker = c.listBullets[len(c.listBullets)-1] == ""
	case "olist":
		noMarker = c.listNumerals[len(c.listNumerals)-1].kind == "nil"
	case "dlist":
		noMarker = len(c.listNumerals) == 0
	}
	if noMarker {
		if node.Style == "unstyled" || (node.Style == "bibliography" && t.Has("ulist_marker_biblio_content") && t.Str("ulist_marker_biblio_content") == "") {
			listIndent = 0
		} else if listIndent > 0 {
			sample := "1.x"
			if node.Context == "ulist" {
				sample = "•x"
			}
			listIndent = maxF(listIndent-c.renderedWidthOfString(sample), 0)
		}
	}
	c.indent(listIndent, 0, func() {
		for _, item := range node.Blocks {
			c.allocateSpaceForListItem(lm)
			c.convertListItem(item, node, o)
		}
	})
	if !node.IsNested() {
		c.themeMargin("prose", "bottom", nextEnclosedBlock(node, false) != nil)
	}
}

func (c *Converter) allocateSpaceForListItem(lm lineMetrics) {
	if !c.atPageTop() && c.cursor() < lm.height+lm.leading+lm.padTop {
		c.advancePage()
	}
}

func (c *Converter) convertListItem(node, list *Block, o prose) {
	t := c.theme
	markerColor := c.fontColor
	if v := t.Get("list_marker_font_color"); v != nil {
		markerColor = toClr(v)
	}
	markerFamily, markerSize, markerLineHeight := c.fontFamily, c.fontSize, c.baseLineHeight
	markerStyle := ""
	marker, hasMarker := "", false
	junction := node
	styleFrom := func(prefixes ...string) {
		for _, prefix := range prefixes {
			if v := t.Get(prefix + "_font_color"); v != nil {
				markerColor = toClr(v)
				break
			}
		}
		for _, prefix := range prefixes {
			if v := t.Str(prefix + "_font_family"); v != "" {
				markerFamily = v
				break
			}
		}
		for _, prefix := range prefixes {
			if v := t.Get(prefix + "_font_size"); v != nil {
				markerSize = c.resolveSize(v, c.fontSize)
				break
			}
		}
		for _, prefix := range prefixes {
			if v := t.Str(prefix + "_font_style"); v != "" {
				markerStyle = v
				break
			}
		}
		for _, prefix := range prefixes {
			if v, ok := t.Num(prefix + "_line_height"); ok {
				markerLineHeight = v
				break
			}
		}
	}
	switch list.Context {
	case "dlist":
		junction = node.Desc
		if n := len(c.listNumerals); n > 0 {
			marker, hasMarker = c.listNumerals[n-1].String()+".", true
			c.listNumerals[n-1].n++
		}
	case "olist":
		if n := len(c.listNumerals); n > 0 {
			cur := &c.listNumerals[n-1]
			switch cur.kind {
			case "nil":
			case "none":
				marker, hasMarker = "", true
			default:
				marker, hasMarker = cur.String()+".", true
				if list.Option("reversed") {
					cur.n--
				} else {
					cur.n++
				}
				styleFrom("olist_marker")
			}
		}
	default:
		if n := len(c.listBullets); n > 0 && c.listBullets[n-1] != "" {
			typ := c.listBullets[n-1]
			if typ == "checkbox" {
				if node.HasAttr("checkbox") {
					typ = "unchecked"
					if node.HasAttr("checked") {
						typ = "checked"
					}
					marker, hasMarker = t.StrOr("ulist_marker_"+typ+"_content", ballotBox[typ]), true
				}
			} else {
				marker, hasMarker = t.StrOr("ulist_marker_"+typ+"_content", bullets[typ]), true
			}
			if hasMarker {
				styleFrom("ulist_marker_"+typ, "ulist_marker")
			}
		}
	}
	if hasMarker && marker != "" {
		gap := c.renderedWidthOfString("x")
		saved := c.saveFont()
		c.fontFamily, c.fontSize = markerFamily, markerSize
		if markerStyle != "" {
			c.setFontStyle(markerStyle)
		}
		to := textOpts{align: "right", charSpacing: -0.5, singleLine: true}
		frags := []frag{{text: marker, color: markerColor}}
		// 幅は字間調整なしで測り、描画時だけ字間を詰める (本家と同じ)
		width := c.widthOfFragments(frags, textOpts{})
		lm := c.calcLineMetrics(markerLineHeight)
		to.leading = lm.leading
		start := c.left - width - gap
		c.float(func() {
			if c.media == "prepress" && c.cursor() < lm.height {
				c.advancePage()
			}
			c.drawTextBox(frags, start, c.y-lm.padTop, width, 0, "top", to)
		})
		c.restoreFont(saved)
	}
	o.marginBottom = fptr(0)
	o.normalizeLineHeight = true
	compound := false
	if junction != nil {
		// 入れ子のリストだけを持つ項目は単純な項目として扱う
		simple := len(junction.Blocks) == 0 || (len(junction.Blocks) == 1 && (junction.Blocks[0].Context == "ulist" || junction.Blocks[0].Context == "olist"))
		if !simple {
			compound = true
		} else if nextEnclosedBlock(junction, true) != nil {
			o.marginBottom = fptr(t.Float("list_item_spacing", 0))
		}
	}
	if compound {
		o.marginBottom = nil
	}
	c.traverseListItem(node, list.Context, o)
}

func (c *Converter) traverseListItem(node *Block, listType string, o prose) {
	if listType == "dlist" {
		// qanda
		spacing := c.theme.Float("description_list_term_spacing", 0)
		for _, term := range node.Terms {
			to := o
			to.marginBottom = &spacing
			c.inkProse("<em>"+term.Text()+"</em>", to)
		}
		if desc := node.Desc; desc != nil {
			if desc.HasText {
				c.inkProse(desc.Text(), o)
			}
			c.traverse(desc)
		}
		return
	}
	if text := node.Text(); text == "" {
		if len(node.Blocks) == 0 {
			c.inkProse(dummyText, o)
		}
	} else {
		c.inkProse(text, o)
	}
	c.traverse(node)
}

func (c *Converter) convertColist(node *Block) {
	t := c.theme
	if !c.atPageTop() {
		if prev := prevSibling(node); prev != nil && (prev.Context == "listing" || prev.Context == "literal") {
			c.marginSpace(t.Float("callout_list_margin_top_after_code", 0))
		}
	}
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	c.listNumerals = append(c.listNumerals, listNumeral{kind: "int", n: 1})
	spacing := t.Float("list_item_spacing", 0)
	if v, ok := t.Num("callout_list_item_spacing"); ok {
		spacing = v
	}
	o := prose{normalizeLineHeight: true}
	if a := resolveTextAlignFromRole(node.Roles()); a != "" {
		o.align = a
	} else if a := t.Str("list_text_align"); a != "" {
		o.align = a
	}
	c.themeFont("callout_list", -1, func() {
		var lm lineMetrics
		c.themeFont("conum", -1, func() { lm = c.calcLineMetrics(c.baseLineHeight) })
		for i, item := range node.Blocks {
			c.allocateSpaceForListItem(lm)
			io := o
			io.marginBottom = &spacing
			if i == len(node.Blocks)-1 {
				io.marginBottom = fptr(0)
			}
			c.convertColistItem(item, io)
		}
	})
	c.listNumerals = c.listNumerals[:len(c.listNumerals)-1]
	c.themeMargin("prose", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) conumGlyph(n int) string {
	if n >= 1 && n <= len(c.conumGlyphs) {
		return string(c.conumGlyphs[n-1])
	}
	return "(" + strconv.Itoa(n) + ")"
}

func (c *Converter) convertColistItem(node *Block, o prose) {
	idx := c.listNumerals[len(c.listNumerals)-1].n
	c.listNumerals[len(c.listNumerals)-1].n++
	var markerWidth float64
	c.themeFont("conum", -1, func() {
		marker := c.conumGlyph(idx)
		markerWidth = c.renderedWidthOfString(marker + "x")
		color := c.fontColor
		if v := c.theme.Get("callout_list_marker_font_color"); v != nil {
			color = toClr(v)
		}
		c.float(func() {
			lm := c.calcLineMetrics(c.baseLineHeight)
			c.drawTextBox([]frag{{text: marker, color: color}}, c.left, c.y-lm.padTop, markerWidth, 0, "top", textOpts{align: "center", leading: lm.leading})
		})
	})
	c.indent(markerWidth, 0, func() { c.traverseListItem(node, "colist", o) })
}

func (c *Converter) convertDlist(node *Block) {
	t := c.theme
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	switch node.Style {
	case "unordered", "ordered":
		c.convertDlistAsList(node)
		return
	case "qanda":
		c.listNumerals = append(c.listNumerals, listNumeral{kind: "int", n: 1})
		c.convertList(node)
		c.listNumerals = c.listNumerals[:len(c.listNumerals)-1]
		return
	case "horizontal":
		c.convertHorizontalDlist(node)
		return
	}
	if node.HasTitle() {
		c.inkCaption(node.Title(), captionOpts{category: "description_list"})
	}
	termSpacing := t.Float("description_list_term_spacing", 0)
	var termHeight float64
	c.themeFont("description_list_term", -1, func() { termHeight = c.heightOfTypesetText("A", prose{}) })
	proseHeight := c.heightOfTypesetText("A", prose{})
	for _, item := range node.Blocks {
		desc := item.Desc
		nlines := float64(len(item.Terms))
		if desc != nil && desc.HasText {
			nlines++
		}
		need := nlines*termHeight + (nlines-1)*termSpacing
		if desc != nil && !desc.HasText && len(desc.Blocks) > 0 {
			need += termSpacing + proseHeight
		}
		if !c.atPageTop() && c.cursor() < need {
			c.advancePage()
		}
		c.themeFont("description_list_term", -1, func() {
			for i, term := range item.Terms {
				top := 0.0
				if i > 0 {
					top = termSpacing
				}
				c.inkProse(term.Text(), prose{marginTop: &top, marginBottom: fptr(0), align: "left", normalizeLineHeight: true, inheritStyles: true})
			}
		})
		if desc != nil {
			c.indent(t.Float("description_list_description_indent", 0), 0, func() {
				c.marginSpace(termSpacing)
				o := prose{normalizeLineHeight: true}
				if nextEnclosedBlock(desc, true) == nil {
					o.marginBottom = fptr(0)
				}
				c.traverseListItem(desc, "dlist_desc", o)
			})
		}
	}
	if !node.IsNested() {
		c.themeMargin("prose", "bottom", nextEnclosedBlock(node, false) != nil)
	}
}

// convertDlistAsList は [unordered] / [ordered] スタイルの説明リストを通常のリストとして描画します。
func (c *Converter) convertDlistAsList(node *Block) {
	ctx := "ulist"
	if node.Style == "ordered" {
		ctx = "olist"
		c.listNumerals = append(c.listNumerals, listNumeral{kind: "int", n: 1})
	} else {
		c.listBullets = append(c.listBullets, "disc")
	}
	list := asciidoc.NewBlock(node.Parent, ctx)
	list.Doc = node.Doc
	stack := node.HasRole("stack")
	stop := ":"
	if stack {
		stop = ""
	}
	if v, ok := node.Attrs["subject-stop"]; ok {
		stop = v
	}
	for _, it := range node.Blocks {
		subject := ""
		if len(it.Terms) > 0 {
			subject = it.Terms[0].Text()
		}
		item := asciidoc.NewBlock(list, "list_item")
		item.Doc = node.Doc
		item.HasText = true
		item.Subs = []string{}
		if dd := it.Desc; dd != nil {
			plain := sanitize(subject)
			s := stop
			if strings.HasSuffix(plain, ".") || strings.HasSuffix(plain, "?") || strings.HasSuffix(plain, "!") || strings.HasSuffix(plain, ":") || strings.HasSuffix(plain, ";") {
				s = ""
			}
			text := "<strong>" + subject + s + "</strong>"
			if dd.HasText {
				if stack {
					text += "<br>"
				} else {
					text += " "
				}
				text += dd.Text()
			}
			item.RawText = text
			for _, b := range dd.Blocks {
				item.Blocks = append(item.Blocks, b)
			}
		} else {
			item.RawText = "<strong>" + subject + "</strong>"
		}
		list.Blocks = append(list.Blocks, item)
	}
	c.convertList(list)
	if ctx == "olist" {
		c.listNumerals = c.listNumerals[:len(c.listNumerals)-1]
	} else {
		c.listBullets = c.listBullets[:len(c.listBullets)-1]
	}
	c.themeMargin("prose", "bottom", !node.IsNested() && nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertHorizontalDlist(node *Block) {
	t := c.theme
	if node.HasTitle() {
		c.inkCaption(node.Title(), captionOpts{category: "description_list"})
	}
	maxTermWidth := 0.0
	var termHeight float64
	var termPad [4]float64
	c.themeFont("description_list_term", -1, func() {
		termHeight = c.heightOfTypesetText("A", prose{})
		lm := c.calcLineMetrics(c.baseLineHeight)
		termPad = [4]float64{lm.padTop, 10, lm.padBottom, 10}
		for _, item := range node.Blocks {
			for _, term := range item.Terms {
				text := term.Text()
				if c.textTransform != "" {
					text = transformText(text, c.textTransform)
				}
				if w := c.widthOfFragments(c.proseFrags(text, prose{inheritStyles: true}), textOpts{}); w > maxTermWidth {
					maxTermWidth = w
				}
			}
		}
	})
	proseHeight := c.heightOfTypesetText("A", prose{})
	sidePad := termPad[1] + termPad[3]
	maxTermWidth += sidePad
	termColWidth := minF(maxTermWidth, c.width*0.5-sidePad)
	termSpacing := t.Float("description_list_term_spacing", 0)
	for _, item := range node.Blocks {
		need := (termSpacing+termHeight)*float64(len(item.Terms)) - termSpacing
		if item.Desc != nil && proseHeight > need {
			need = proseHeight
		}
		if !c.atPageTop() && c.cursor() < need {
			c.advancePage()
		}
		initialY, initialPage := c.y, c.page
		c.indent(termPad[3], c.width-termColWidth, func() {
			c.themeFont("description_list_term", -1, func() {
				for i, term := range item.Terms {
					top := 0.0
					if i > 0 {
						top = termSpacing
					}
					c.inkProse(term.Text(), prose{marginTop: &top, marginBottom: fptr(0), align: "left", normalizeLineHeight: true, inheritStyles: true})
				}
			})
		})
		if desc := item.Desc; desc != nil {
			afterY, afterPage := c.y, c.page
			if c.page > initialPage {
				c.goToPage(initialPage)
			}
			c.y = initialY
			c.indent(termColWidth+10, 10, func() {
				o := prose{normalizeLineHeight: true}
				if nextEnclosedBlock(desc, true) == nil {
					o.marginBottom = fptr(0)
				}
				c.traverseListItem(desc, "dlist_desc", o)
			})
			if c.page < afterPage {
				c.goToPage(afterPage)
				c.y = afterY
			} else if c.page == afterPage && c.y > afterY {
				c.y = afterY
			}
		}
	}
	if !node.IsNested() {
		c.themeMargin("prose", "bottom", nextEnclosedBlock(node, false) != nil)
	}
}
