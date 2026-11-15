package converter

import (
	"strconv"
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
)

// traverse はブロックの子要素 (または内容) を変換します。
func (c *Converter) traverse(node *Block) {
	c.traverseOpts(node, prose{})
}

func (c *Converter) traverseOpts(node *Block, o prose) {
	if len(node.Blocks) > 0 {
		for _, child := range node.Blocks {
			c.convertBlock(child)
		}
		return
	}
	if node.ContentModel != "compound" && node.ContentModel != "empty" && len(node.Lines) > 0 {
		o.marginBottom = fptr(0)
		if a := resolveTextAlignFromRole(node.Roles()); a != "" {
			o.align = a
		}
		o.bottomGutter = c.bottomGutter(node)
		c.inkProse(node.Content(), o)
	}
}

func resolveTextAlignFromRole(roles []string) string {
	for i := len(roles) - 1; i >= 0; i-- {
		switch roles[i] {
		case "text-left":
			return "left"
		case "text-center":
			return "center"
		case "text-right":
			return "right"
		case "text-justify":
			return "justify"
		}
	}
	return ""
}

func (c *Converter) convertBlock(node *Block) {
	node.Doc.Playback(node)
	switch node.Context {
	case "section":
		c.convertSection(node)
	case "preamble":
		c.convertPreamble(node)
	case "paragraph":
		c.convertParagraph(node, nil)
	case "admonition":
		c.convertAdmonition(node)
	case "listing", "literal":
		c.convertCode(node)
	case "example":
		c.convertExample(node)
	case "open":
		c.convertOpen(node)
	case "quote", "verse":
		c.convertQuoteOrVerse(node)
	case "sidebar":
		c.convertSidebar(node)
	case "ulist":
		c.convertUlist(node)
	case "olist":
		c.convertOlist(node)
	case "dlist":
		c.convertDlist(node)
	case "colist":
		c.convertColist(node)
	case "image":
		c.convertImage(node, imageOpts{relativeToImagesdir: true})
	case "table":
		c.convertTable(node)
	case "page_break":
		c.convertPageBreak(node)
	case "thematic_break":
		c.convertThematicBreak(node)
	case "toc":
		c.convertToc(node, "macro")
	case "floating_title":
		c.convertFloatingTitle(node)
	case "pass":
		c.themeFont("code", -1, func() {
			c.typesetFragments([]frag{{text: guardIndentation(node.Content()), color: c.themeClr("base_font_color")}}, c.calcLineMetrics(c.baseLineHeight), prose{})
		})
		c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
	case "stem":
		c.convertStem(node)
	case "video", "audio":
		c.convertMedia(node)
	default:
		c.logf("WARN", "missing convert handler for %s node in pdf backend", node.Context)
	}
}

// ---------------------------------------------------------------------------
// 見出し・セクション

type headingOpts struct {
	align      string
	level      int
	outdent    bool
	margin     *float64
	lineHeight float64
}

func (c *Converter) headingKey(level int, suffix string) any {
	if level > 0 {
		if v := c.theme.Get("heading_h" + strconv.Itoa(level) + suffix); v != nil {
			return v
		}
	}
	return c.theme.Get("heading" + suffix)
}

func (c *Converter) headingNum(level int, suffix string) float64 {
	if f, ok := c.headingKey(level, suffix).(float64); ok {
		return f
	}
	return 0
}

// inkHeading は見出しを配置します (theme_font :heading の中から呼びます)。
func (c *Converter) inkHeading(s string, o headingOpts) {
	t := c.theme
	hcat := "heading_h" + strconv.Itoa(o.level)
	var top float64
	if o.margin != nil {
		top = *o.margin
	} else if c.atPageTop() {
		if v := c.headingNum(o.level, "_margin_page_top"); v > 0 {
			c.moveDown(v)
		}
	} else {
		top = c.headingNum(o.level, "_margin_top")
	}
	bottom := c.headingNum(o.level, "_margin_bottom")
	if o.margin != nil {
		bottom = *o.margin
	}
	if c.textTransform != "" {
		s = transformText(s, c.textTransform)
	}
	c.outdentSection(o.outdent, func() {
		c.marginSpace(top)
		startY, startPage := c.y, c.page
		c.padBox(expandPadding(t.Get(hcat+"_padding")), nil, func() {
			po := prose{align: o.align, inheritStyles: true}
			if d, _ := c.headingKey(o.level, "_text_decoration").(string); d != "" {
				po.underline, po.strike = d == "underline", d == "line-through"
				po.decoColor = toClr(c.headingKey(o.level, "_text_decoration_color"))
				po.decoWidth = c.headingNum(o.level, "_text_decoration_width")
			}
			lh := o.lineHeight
			if lh == 0 {
				lh = c.baseLineHeight
			}
			lm := c.calcLineMetrics(lh)
			c.typesetFragments(c.proseFrags(s, po), lm, po)
		})
		if bw, ok := t.Num(hcat + "_border_width"); ok && bw > 0 {
			bc := c.resolveThemeColor(hcat+"_border_color", c.themeClr("base_border_color"), clr{})
			if bc.set && c.page == startPage {
				c.fillAndStroke(c.left, startY, c.width, startY-c.y, clr{}, bc, t.Get(hcat+"_border_width"), t.Str(hcat+"_border_style"), 0)
			}
		}
		c.marginSpace(bottom)
	})
}

// arrangeHeading は見出しが孤立しないよう、必要なら改ページします。
func (c *Converter) arrangeHeading(node *Block, title string, o headingOpts) {
	fits := true
	c.themeFont("heading", o.level, func() {
		if c.fontSize > c.cursor() {
			fits = false
			return
		}
		spaceBelow := 0.0
		hasContent := len(node.Blocks) > 0
		if node.Context != "section" {
			hasContent = nextSibling(node) != nil
		}
		minAfter, numeric := c.theme.Get("heading_min_height_after").(float64)
		if !numeric && c.theme.Str("heading_min_height_after") == "auto" {
			minAfter = c.fontSize * c.baseLineHeight
		}
		if minAfter > 0 && hasContent {
			spaceBelow = minAfter + c.headingNum(o.level, "_margin_bottom")
		}
		pad := expandPadding(c.theme.Get("heading_h" + strconv.Itoa(o.level) + "_padding"))
		c.indent(pad[3], pad[1], func() {
			text := title
			h := c.heightOfTypesetText(text, prose{inheritStyles: true}) + pad[0] + pad[2] + c.headingNum(o.level, "_margin_top") + spaceBelow
			fits = c.cursor() >= h
		})
	})
	if !fits {
		c.advancePage()
	}
}

func (c *Converter) convertSection(sect *Block) {
	t := c.theme
	doc := sect.Doc
	sectname := sect.Sectname
	if sectname == "index" && c.index.empty() {
		c.numberedTitles[sect] = [2]string{"", ""}
		return
	}
	title := c.numberedTitle(sect, true)
	sep := sect.Attr("separator")
	if sep == "" {
		sep = doc.Attr("title-separator")
	}
	if sep != "" && strings.Contains(title, sep+" ") {
		i := strings.LastIndex(title, sep+" ")
		title = title[:i] + "\n<em class=\"subtitle\">" + title[i+len(sep)+1:] + "</em>"
	}
	hlevel := sect.Level + 1
	align := c.baseTextAlign
	if v, _ := c.headingKey(hlevel, "_text_align").(string); v != "" {
		align = v
	}
	part := sectname == "part"
	chapterlike := !part && (sectname == "chapter" || (doc.Doctype() == "book" && sect.Level == 1))
	hidden := sect.Option("notitle")
	o := headingOpts{align: align, level: hlevel, outdent: !(part || chapterlike)}
	startedNew := false
	startNew := func() {
		if !c.atPageTop() {
			c.startNewPage()
		}
		if c.ppbook && !c.isRecto(c.pageNumber()) && !sect.Option("nonfacing") {
			c.startNewPage()
		}
		startedNew = true
	}
	firstOfPart := sect.Parent != nil && sect.Parent.Sectname == "part" && len(sect.Parent.Sections()) > 0 && sect.Parent.Sections()[0] == sect && sect.Parent.Blocks[0] == sect
	if part {
		if t.Str("heading_part_break_before") == "always" {
			startNew()
		}
	} else if chapterlike {
		if (t.Str("heading_chapter_break_before") == "always" && !(t.Str("heading_part_break_after") == "avoid" && firstOfPart)) ||
			(t.Str("heading_part_break_after") == "always" && firstOfPart) {
			startNew()
		}
	}
	if !hidden && !startedNew && !c.atPageTop() {
		c.arrangeHeading(sect, title, o)
	}
	si := c.sectInfo[sect]
	if si == nil {
		si = &sectInfo{}
		c.sectInfo[sect] = si
	}
	si.pageStart = c.pageNumber()
	si.anchor = c.deriveAnchorFromID(sect.ID, strconv.Itoa(si.pageStart)+"-"+strconv.Itoa(int(c.y+0.9999)))
	c.addDestForBlock(sect, si.anchor)
	if c.scratch == 0 {
		si.destPage, si.destY = c.page, c.destY()
	}
	if !hidden {
		c.themeFont("heading", hlevel, func() { c.inkHeading(title, o) })
	}
	if sectname == "index" {
		c.outdentSection(true, func() { c.convertIndexSection(sect) })
	} else {
		c.traverse(sect)
	}
	if chapterlike {
		c.outdentSection(true, func() { c.inkFootnotes(sect) })
	}
	si.pageEnd = c.pageNumber()
}

func (c *Converter) convertFloatingTitle(node *Block) {
	hlevel := node.Level + 1
	align := resolveTextAlignFromRole(node.Roles())
	if align == "" {
		align = c.baseTextAlign
		if v, _ := c.headingKey(hlevel, "_text_align").(string); v != "" {
			align = v
		}
	}
	o := headingOpts{align: align, level: hlevel, outdent: node.Parent != nil && node.Parent.Context == "section"}
	title := node.Title()
	if !c.atPageTop() && nextSibling(node) != nil {
		c.arrangeHeading(node, title, o)
	}
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	c.themeFont("heading", hlevel, func() { c.inkHeading(title, o) })
}

func (c *Converter) convertPreamble(node *Block) {
	if len(node.Blocks) > 0 {
		first := node.Blocks[0]
		if first.Context == "paragraph" && len(node.Doc.Sections()) > 0 && first.Attr("role") == "" {
			first.Attrs["role"] = "lead"
		}
	}
	c.traverse(node)
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
	c.convertToc(node, "preamble")
}

func (c *Converter) convertToc(node *Block, placement string) {
	if c.tocExtent != nil {
		return
	}
	doc := c.doc
	if !doc.AttrIs("toc-placement", placement) || !doc.HasAttr("toc") || len(doc.Sections()) == 0 {
		return
	}
	isBook := doc.Doctype() == "book"
	if isBook {
		if !c.atPageTop() {
			c.startNewPage()
		}
		if c.ppbook && !c.isRecto(c.pageNumber()) {
			c.startNewPage()
		}
	}
	if placement == "macro" {
		id := node.ID
		if id == "" {
			id = "toc"
		}
		c.addDest(id, 0, c.destY())
	}
	c.tocExtent = c.allocateToc(doc, c.resolveTocLevels(doc), isBook || doc.HasAttr("title-page"))
	if placement == "macro" {
		for p := c.tocExtent.fromPage; p <= c.tocExtent.toPage; p++ {
			if node.Option("noheader") {
				c.noRunning["header"][p+1] = true
			}
			if node.Option("nofooter") {
				c.noRunning["footer"][p+1] = true
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 段落・キャプション

func (c *Converter) roleCats(roles []string) []fontCat {
	var cats []fontCat
	for _, r := range roles {
		cats = append(cats, fontCat{"role_" + r, -1})
	}
	return cats
}

func (c *Converter) convertParagraph(node *Block, base *prose) {
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	t := c.theme
	o := prose{marginBottom: fptr(0)}
	if base != nil {
		o = *base
	}
	roles := node.Roles()
	align := c.baseTextAlign
	if a := resolveTextAlignFromRole(roles); a != "" {
		o.align, align = a, a
	} else {
		for i := len(roles) - 1; i >= 0; i-- {
			if a := t.Str("role_" + roles[i] + "_text_align"); a != "" {
				o.align, align = a, a
				break
			}
		}
	}
	if align == "left" || align == "justify" {
		if indent := t.Float("prose_text_indent", 0); indent > 0 {
			o.indentParagraphs = indent
		} else if indent := t.Float("prose_text_indent_inner", 0); indent > 0 {
			if prev := prevSibling(node); prev != nil && prev.Context == "paragraph" {
				o.indentParagraphs = indent
			}
		}
	}
	o.bottomGutter = c.bottomGutter(node)
	next := nextEnclosedBlock(node, false)
	content := node.Content()
	var cats []fontCat
	for _, r := range roles {
		if !strings.HasPrefix(r, "text-") {
			cats = append(cats, fontCat{"role_" + r, -1})
		}
	}
	insertMarginBottom := func() {
		if inner, ok := t.Num("prose_margin_inner"); ok && next != nil && next.Context == "paragraph" {
			c.marginSpace(inner)
		} else {
			c.themeMargin("prose", "bottom", next != nil)
		}
	}
	if fb := c.floatBox; fb != nil {
		if prev := prevSibling(node); prev != nil && prev.Context == "open" && prev.HasRole("float-group") {
			c.y = fb.bottom
			c.floatBox = nil
		} else {
			c.inkParagraphInFloatBox(content, fb, o, cats, next, insertMarginBottom)
			return
		}
	}
	if node.HasTitle() {
		c.inkCaption(node.Title(), captionOpts{})
	}
	if len(cats) > 0 {
		c.themeFontCascade(cats, func() { c.inkProse(content, o) })
	} else {
		c.inkProse(content, o)
	}
	insertMarginBottom()
}

// inkParagraphInFloatBox は回り込み画像の横に段落を流し込みます。
func (c *Converter) inkParagraphInFloatBox(content string, fb *floatBox, o prose, cats []fontCat, next *Block, insertMarginBottom func()) {
	c.floatBox = nil
	var lm lineMetrics
	var overflow []frag
	var to textOpts
	startY := c.y
	startPage := fb.page
	blockBottom := fb.bottom + fb.gapY
	endY, drawn := 0.0, false
	boxHeight := 0.0
	run := func(fn func()) {
		if len(cats) > 0 {
			c.themeFontCascade(cats, fn)
		} else {
			fn()
		}
	}
	run(func() {
		lm = c.calcLineMetrics(c.baseLineHeight)
		f := c.currentFont()
		finalGap := lm.padBottom
		if f != nil {
			finalGap += descender(f, c.fontSize)
		}
		lineHeightLength := lm.height + lm.leading + lm.padTop
		boxHeight = minF(c.cursor(), fb.height-(fb.top-startY)+lineHeightLength)
		transform := c.textTransform
		text := content
		if transform != "" {
			text = transformText(text, transform)
		}
		frags := c.proseFrags(text, o)
		to = c.proseTextOpts(o, lm)
		toks := c.tokenize(frags, to)
		lines := c.wrapLines(toks, to, func(first bool) float64 {
			if first {
				return fb.width - to.indentFirst
			}
			return fb.width
		})
		top := startY - lm.padTop
		baseline := 0.0
		n := 0
		for n < len(lines) {
			ln := &lines[n]
			var need float64
			if n == 0 {
				need = ln.asc + ln.desc
			} else {
				need = baseline + ln.desc + ln.lineH + to.leading
			}
			if need > boxHeight+0.0001 {
				break
			}
			if n == 0 {
				baseline = ln.asc
			} else {
				baseline += ln.lineH + to.leading
			}
			x, w := fb.left, fb.width
			if ln.first && to.indentFirst != 0 {
				x += to.indentFirst
				w -= to.indentFirst
			}
			if n < len(lines)-1 || !ln.paraEnd {
				// 続きがある行は段落の途中として扱う
			}
			c.drawLine(ln, x, top-baseline, w, to)
			n++
		}
		if n > 0 {
			drawn = true
			endY = top - baseline - finalGap
			c.y = endY
		}
		if n < len(lines) {
			_, overflow = splitFrags(frags, consumedRunes(toks, lines, n))
			if n == 0 {
				overflow = frags
			}
		}
	})
	if len(overflow) == 0 {
		switch {
		case next != nil && next.Context == "paragraph":
			insertMarginBottom()
			if c.page == startPage && c.y > startY-boxHeight {
				c.floatBox = fb
			}
		case endY > blockBottom:
			c.y = blockBottom
			c.themeMargin("block", "bottom", next != nil)
		default:
			insertMarginBottom()
		}
		return
	}
	oo := prose{align: o.align}
	if !drawn {
		oo.indentParagraphs = o.indentParagraphs
		if startY > fb.bottom {
			c.y = fb.bottom
		}
	}
	run(func() { c.typesetFragments(overflow, lm, oo) })
	insertMarginBottom()
}

type captionOpts struct {
	category   string
	bottom     bool
	blockAlign string
	blockWidth float64
	maxWidth   any
}

func (c *Converter) captionKey(category, suffix string) any {
	if category != "" {
		if v := c.theme.Get(category + "_caption" + suffix); v != nil {
			return v
		}
	}
	return c.theme.Get("caption" + suffix)
}

// inkCaption はキャプションを配置します。
func (c *Converter) inkCaption(s string, o captionOpts) {
	outside, _ := c.captionKey(o.category, "_margin_outside").(float64)
	inside, _ := c.captionKey(o.category, "_margin_inside").(float64)
	container := c.width
	indentBy := [2]float64{}
	align := c.baseTextAlign
	if a, _ := c.captionKey(o.category, "_align").(string); a != "" {
		if a == "inherit" {
			if o.blockAlign != "" {
				align = o.blockAlign
			}
		} else {
			align = a
		}
	}
	textAlign := align
	if a, _ := c.captionKey(o.category, "_text_align").(string); a != "" && a != "inherit" {
		textAlign = a
	}
	blockAlign := o.blockAlign
	if o.maxWidth != nil && o.maxWidth != "none" {
		maxWidth := container
		switch mw := o.maxWidth.(type) {
		case string:
			if strings.HasPrefix(mw, "fit-content") {
				bw := o.blockWidth
				if bw == 0 {
					bw = container
				}
				maxWidth = bw
				if strings.HasSuffix(mw, "%)") {
					pct, _ := strconv.ParseFloat(strings.TrimSuffix(mw[12:], "%)"), 64)
					narrowed := bw * pct / 100
					if delta := bw - narrowed; delta > 0 {
						switch align {
						case "right":
							indentBy[0] += delta
						case "center":
							indentBy[0] += delta / 2
							indentBy[1] += delta / 2
						default:
							indentBy[1] += delta
						}
					}
				}
			} else if strings.HasSuffix(mw, "%") {
				pct, _ := strconv.ParseFloat(strings.TrimSuffix(mw, "%"), 64)
				maxWidth = minF(pct/100*container, container)
				blockAlign = align
			} else {
				maxWidth = minF(toF(mw), container)
				blockAlign = align
			}
		case float64:
			maxWidth = minF(mw, container)
			blockAlign = align
		}
		if rem := container - maxWidth; rem > 0 {
			switch blockAlign {
			case "right":
				indentBy[0] += rem
			case "center":
				indentBy[0] += rem / 2
				indentBy[1] += rem / 2
			default:
				indentBy[1] += rem
			}
		}
	}
	cats := []fontCat{{"caption", -1}}
	if o.category != "" {
		cats = append(cats, fontCat{o.category + "_caption", -1})
	}
	c.themeFontCascade(cats, func() {
		top, bottom := outside, inside
		if o.bottom {
			top, bottom = inside, outside
		}
		po := prose{marginTop: &top, marginBottom: &bottom, align: textAlign, noNormalize: true, normalizeLineHeight: true, inheritStyles: true}
		if d, _ := c.captionKey(o.category, "_text_decoration").(string); d != "" {
			po.underline, po.strike = d == "underline", d == "line-through"
		}
		if bg := toClr(c.captionKey(o.category, "_background_color")); bg.set && c.scratch == 0 {
			h := c.heightOfTypesetText(s, po)
			y := c.y
			if !c.atPageTop() {
				y -= top
			}
			c.fillRect(c.left, y, container, h, bg)
		}
		c.indent(indentBy[0], indentBy[1], func() { c.inkProse(s, po) })
	})
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// 塗りと枠線

func (c *Converter) fillRect(x, top, w, h float64, col clr) {
	if !col.set || h <= 0 {
		return
	}
	g := pdf.NewG().Save().FillColor(col.pdf()).Rect(x, top-h, w, h).Fill().Restore()
	c.draw(g)
}

// fillAndStroke は矩形 (左上が x, top) を塗り、枠線を引きます。
// borderWidth は数値または [上, 右, 下, 左] の配列です。
func (c *Converter) fillAndStroke(x, top, w, h float64, fill, stroke clr, borderWidth any, style string, radius float64) {
	if h <= 0 || w <= 0 {
		return
	}
	g := pdf.NewG()
	widths, isArray := borderWidth.([]any)
	if isArray {
		radius = 0
	}
	if fill.set {
		g.Save().FillColor(fill.pdf()).RoundedRect(x, top-h, w, h, radius).Fill().Restore()
	}
	if stroke.set {
		if isArray {
			sw := expandPadding(widths)
			if len(widths) == 2 {
				sw = [4]float64{toF(widths[0]), toF(widths[1]), toF(widths[0]), toF(widths[1])}
			}
			line := func(x1, y1, x2, y2, lw float64) {
				if lw <= 0 {
					return
				}
				g.Save().StrokeColor(stroke.pdf()).LineWidth(lw)
				applyLineStyle(g, style, lw)
				g.MoveTo(x1, y1).LineTo(x2, y2).Stroke().Restore()
			}
			line(x-sw[3]/2, top, x+w+sw[1]/2, top, sw[0])
			line(x+w, top+sw[0]/2, x+w, top-h-sw[2]/2, sw[1])
			line(x-sw[3]/2, top-h, x+w+sw[1]/2, top-h, sw[2])
			line(x, top+sw[0]/2, x, top-h-sw[2]/2, sw[3])
		} else if lw := toF(borderWidth); lw > 0 {
			g.Save().StrokeColor(stroke.pdf())
			if style == "double" {
				single := lw / 3
				off := single * 2
				g.LineWidth(single).RoundedRect(x, top-h, w, h, radius).Stroke()
				g.RoundedRect(x+off, top-h+off, w-off*2, h-off*2, radius).Stroke()
			} else {
				g.LineWidth(lw)
				applyLineStyle(g, style, lw)
				g.RoundedRect(x, top-h, w, h, radius).Stroke()
			}
			g.Restore()
		}
	}
	c.draw(g)
}

// eachPage は範囲に含まれる各ページで fn を呼びます (top と height はそのページでの範囲)。
func (c *Converter) eachPage(ext *extent, fn func(top, height float64, first, last bool)) {
	c.float(func() {
		for p := ext.fromPage; p <= ext.toPage; p++ {
			first, last := p == ext.fromPage, p == ext.toPage
			if !first {
				c.advancePage()
			}
			top := c.y
			if first {
				top = ext.fromY
			}
			bottom := c.bottom
			if last {
				bottom = ext.toY
			}
			fn(top, top-bottom, first, last)
		}
	})
}

// themeFillAndStrokeBlock はブロックの背景と枠線を描きます (複数ページにまたがる場合は分割します)。
func (c *Converter) themeFillAndStrokeBlock(category string, ext *extent, captionNode *Block, borderWidthOverride any, hasOverride bool, bgOverride clr) {
	t := c.theme
	caption := func() {
		if captionNode != nil && captionNode.HasTitle() {
			c.inkCaption(captionNode.CaptionedTitle(), captionOpts{category: category})
		}
	}
	if ext == nil {
		caption()
		return
	}
	borderWidth := t.Get(category + "_border_width")
	if hasOverride {
		borderWidth = borderWidthOverride
	}
	bw := 0.0
	hasRadius := true
	switch v := borderWidth.(type) {
	case []any:
		if len(v) > 0 {
			bw = toF(v[0])
		}
		hasRadius = false
	default:
		bw = toF(v)
	}
	bg := bgOverride
	if !bg.set {
		bg = c.resolveThemeColor(category+"_background_color", clr{}, clr{})
	}
	if bw <= 0 && !bg.set {
		caption()
		return
	}
	borderColor := c.resolveThemeColor(category+"_border_color", c.themeClr("base_border_color"), c.pageBgColor)
	radius := 0.0
	if hasRadius {
		radius = t.Float(category+"_border_radius", 0) + bw
	}
	gapColor, shift := c.pageBgColor, 0.0
	if bw > 0 {
		if borderColor.String() == c.pageBgColor.String() {
			shift = bw * 0.5
		} else if bg.set && bg.String() != borderColor.String() {
			gapColor = bg
		}
	} else {
		bw = 0.5
		shift = bw * 0.5
		borderWidth = nil
	}
	caption()
	ext.fromPage, ext.fromY = c.page, c.y
	if ext.toPage < ext.fromPage {
		ext.toPage = ext.fromPage
	}
	style := t.StrOr(category+"_border_style", "solid")
	c.eachPage(ext, func(top, height float64, first, last bool) {
		c.fillAndStroke(c.left, top, c.width, height, bg, borderColor, borderWidth, style, t.Float(category+"_border_radius", 0))
		// ページをまたぐ箇所の枠線を破線で打ち消す
		gap := func(y float64) {
			g := pdf.NewG().Save().StrokeColor(gapColor.pdf()).LineWidth(bw*1.2).Dash(bw*1.2*4, bw*1.2*4)
			g.MoveTo(c.left+radius, y).LineTo(c.left+c.width-radius, y).Stroke().Restore()
			c.draw(g)
		}
		if !first {
			gap(top - shift)
		}
		if !last {
			gap(top - height + shift)
		}
	})
}

// ---------------------------------------------------------------------------
// 囲みブロック

func (c *Converter) convertExample(node *Block) {
	if node.Option("collapsible") {
		c.convertCollapsible(node)
		return
	}
	captionBottom := c.theme.Str("example_caption_end") == "bottom"
	c.arrangeBlock(node, func(ext *extent) {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		capNode := node
		if captionBottom {
			capNode = nil
		}
		c.themeFillAndStrokeBlock("example", ext, capNode, nil, false, clr{})
		c.padBox(c.themePadding("example_padding"), node, func() {
			c.themeFont("example", -1, func() { c.traverse(node) })
		})
	})
	if captionBottom && node.HasTitle() {
		c.inkCaption(node.CaptionedTitle(), captionOpts{category: "example", bottom: true})
	}
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertCollapsible(node *Block) {
	marker := "▼ "
	title := marker + "Details"
	if node.HasTitle() {
		title = marker + node.Title()
	}
	var indentBy float64
	c.themeFont("caption", -1, func() { indentBy = c.renderedWidthOfString(marker) })
	body := func() {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		c.inkCaption(title, captionOpts{})
		c.indent(indentBy, 0, func() { c.traverse(node) })
	}
	if !c.atPageTop() && (node.ID != "" || node.Option("unbreakable")) {
		c.arrangeBlock(node, func(*extent) { body() })
	} else {
		body()
	}
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertOpen(node *Block) {
	if node.Style == "abstract" {
		c.convertAbstract(node)
		return
	}
	body := func() {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		if node.HasTitle() {
			c.inkCaption(node.Title(), captionOpts{})
		}
		c.traverse(node)
	}
	if !c.atPageTop() && (node.HasTitle() || node.ID != "" || node.Option("unbreakable")) {
		c.arrangeBlock(node, func(*extent) { body() })
	} else {
		body()
	}
}

func (c *Converter) convertAbstract(node *Block) {
	t := c.theme
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	c.outdentSection(true, func() {
		c.padBox(c.themePadding("abstract_padding"), nil, func() {
			if node.HasTitle() {
				c.themeFont("abstract_title", -1, func() {
					top, bottom := t.Float("heading_margin_top", 0), t.Float("heading_margin_bottom", 0)
					c.inkProse(node.Title(), prose{align: t.StrOr("abstract_title_text_align", c.baseTextAlign), marginTop: &top, marginBottom: &bottom,
						lineHeight: t.Float("heading_line_height", t.Float("base_line_height", 1)), inheritStyles: true})
				})
			}
			c.themeFont("abstract", -1, func() {
				o := prose{align: t.StrOr("abstract_text_align", c.baseTextAlign), marginBottom: fptr(0)}
				var fl *firstLineOpts
				if style := t.Str("abstract_first_line_font_style"); style != "" && style != styleName(c.fontBold, c.fontItalic) {
					fl = &firstLineOpts{hasStyle: true}
					switch style {
					case "normal":
					case "normal_italic":
						fl.italic = true
					default:
						fl.bold = c.fontBold || strings.Contains(style, "bold")
						fl.italic = c.fontItalic || strings.Contains(style, "italic")
					}
				}
				if v := t.Get("abstract_first_line_font_color"); v != nil {
					if fl == nil {
						fl = &firstLineOpts{}
					}
					fl.color = toClr(v)
				}
				if v := t.Str("abstract_first_line_text_transform"); v != "" {
					if fl == nil {
						fl = &firstLineOpts{}
					}
					fl.transform = v
				}
				o.firstLine = fl
				if len(node.Blocks) > 0 {
					for _, child := range node.Blocks {
						if child.Context == "paragraph" {
							child.Doc.Playback(child)
							po := o
							c.convertParagraph(child, &po)
							o.firstLine = nil
						} else {
							c.convertBlock(child)
						}
					}
				} else if len(node.Lines) > 0 {
					if a := resolveTextAlignFromRole(node.Roles()); a != "" {
						o.align = a
					}
					c.inkProse(node.Content(), o)
				}
			})
		})
	})
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertSidebar(node *Block) {
	t := c.theme
	c.arrangeBlock(node, func(ext *extent) {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		if ext != nil {
			c.themeFillAndStrokeBlock("sidebar", ext, nil, nil, false, clr{})
		}
		c.padBox(c.themePadding("sidebar_padding"), node, func() {
			if node.HasTitle() {
				c.themeFont("sidebar_title", -1, func() {
					align := c.baseTextAlign
					for _, k := range []string{"sidebar_title_text_align", "heading_text_align"} {
						if v := t.Str(k); v != "" {
							align = v
							break
						}
					}
					bottom := t.Float("heading_margin_bottom", 0)
					c.inkProse(node.Title(), prose{align: align, marginBottom: &bottom, lineHeight: t.Float("heading_line_height", t.Float("base_line_height", 1)), inheritStyles: true})
				})
			}
			c.themeFont("sidebar", -1, func() { c.traverse(node) })
		})
	})
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertQuoteOrVerse(node *Block) {
	t := c.theme
	category := node.Context
	leftWidth := t.Float(category+"_border_left_width", 0)
	var borderColor clr
	var bwOverride any
	hasOverride := false
	if leftWidth > 0 {
		borderColor = c.resolveThemeColor(category+"_border_color", c.themeClr("base_border_color"), clr{})
		if !borderColor.set {
			leftWidth = 0
		}
		bwOverride, hasOverride = t.Get(category+"_border_width"), true
	} else if t.Float(category+"_border_width", 0) == 0 {
		bwOverride, hasOverride = nil, true
	}
	attribution := node.Attr("attribution")
	citetitle := node.Attr("citetitle")
	c.arrangeBlock(node, func(ext *extent) {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		c.themeFillAndStrokeBlock(category, ext, node, bwOverride, hasOverride, clr{})
		if ext != nil && leftWidth > 0 {
			ext.fromPage, ext.fromY = c.page, c.y
			c.eachPage(ext, func(top, height float64, _, _ bool) {
				x := c.left + leftWidth*0.5
				g := pdf.NewG().Save().StrokeColor(borderColor.pdf()).LineWidth(leftWidth).MoveTo(x, top).LineTo(x, top-height).Stroke().Restore()
				c.draw(g)
			})
		}
		gutterNode := node
		if attribution != "" {
			gutterNode = nil
		}
		c.padBox(c.themePadding(category+"_padding"), gutterNode, func() {
			c.themeFont(category, -1, func() {
				if category == "quote" {
					c.traverse(node)
				} else {
					align := resolveTextAlignFromRole(node.Roles())
					if align == "" {
						align = "left"
					}
					o := prose{noNormalize: true, align: align, marginBottom: fptr(0)}
					if attribution == "" {
						o.bottomGutter = c.bottomGutter(node)
					}
					c.inkProse(guardIndentation(node.Content()), o)
				}
			})
			if attribution != "" {
				c.themeMargin("block", "bottom", true)
				c.themeFont(category+"_cite", -1, func() {
					text := "— " + attribution
					if citetitle != "" {
						text += ", " + citetitle
					}
					c.inkProse(text, prose{align: "left", noNormalize: true, marginBottom: fptr(0)})
				})
			}
		})
	})
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertAdmonition(node *Block) {
	t := c.theme
	typ := node.Attr("name")
	labelAlign := t.StrOr("admonition_label_text_align", "center")
	labelValign := t.StrOr("admonition_label_vertical_align", "middle")
	labelMinWidth := t.Float("admonition_label_min_width", 0)
	labelText := sanitize(node.Attr("textlabel"))
	var labelWidth float64
	labelCats := []fontCat{{"admonition_label", -1}, {"admonition_label_" + typ, -1}}
	// icons=font の場合はフォントアイコン、それ以外は画像アイコン (あれば) を使う
	var icon admonitionIcon
	iconSet, iconGlyphStr, useIcon := "", "", false
	var iconImage *loadedImage
	if icons, has := node.Doc.Attributes["icons"]; has {
		icon = c.admonitionIconData(typ)
		if icons == "font" && !node.HasAttr("icon") {
			if s, g, ok := resolveIcon(icon.name); ok {
				iconSet, iconGlyphStr, useIcon = s, g, true
			} else {
				c.logf("WARN", "%s is not a valid icon name", icon.name)
			}
		} else {
			name := typ
			if v := node.Attr("icon"); v != "" {
				name = v
			}
			path := c.resolveIconImagePath(node.Doc, name)
			if iconImage = c.loadImage(path, name); iconImage == nil {
				c.logf("WARN", "admonition icon image for %s not found or not readable: %s", strings.ToUpper(typ), path)
			}
		}
	}
	if useIcon || iconImage != nil {
		labelWidth = icon.size
	} else {
		c.themeFontCascade(labelCats, func() {
			if c.textTransform != "" {
				labelText = transformPlain(labelText, c.textTransform)
			}
			labelWidth = c.renderedWidthOfString(labelText)
		})
	}
	if labelMinWidth > labelWidth {
		labelWidth = labelMinWidth
	}
	cpad := c.themePadding("admonition_padding")
	lpad := cpad
	if t.Get("admonition_label_padding") != nil {
		lpad = c.themePadding("admonition_label_padding")
	}
	c.arrangeBlock(node, func(ext *extent) {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		if ext != nil {
			c.themeFillAndStrokeBlock("admonition", ext, nil, nil, false, clr{})
		}
		c.padBox([4]float64{0, cpad[1], 0, lpad[3]}, nil, func() {
			if ext != nil {
				ext.fromPage, ext.fromY = c.page, c.y
				labelHeight := c.cursor()
				if ext.singlePage() {
					labelHeight = ext.fromY - ext.toY
				}
				ruleWidth := t.Float("admonition_column_rule_width", 0)
				ruleColor := c.resolveThemeColor("admonition_column_rule_color", c.themeClr("base_border_color"), clr{})
				if ruleWidth > 0 && ruleColor.set {
					c.eachPage(ext, func(top, height float64, _, _ bool) {
						x := c.left + labelWidth + lpad[1]
						g := pdf.NewG().Save().StrokeColor(ruleColor.pdf()).LineWidth(ruleWidth)
						applyLineStyle(g, t.Str("admonition_column_rule_style"), ruleWidth)
						g.MoveTo(x, top).LineTo(x, top-height).Stroke().Restore()
						c.draw(g)
					})
				}
				if useIcon || iconImage != nil {
					c.float(func() {
						c.inkAdmonitionIcon(icon, iconSet, iconGlyphStr, iconImage, labelWidth, labelHeight, labelAlign, labelValign)
					})
				}
				c.float(func() {
					if useIcon || iconImage != nil {
						return
					}
					c.themeFontCascade(labelCats, func() {
						lm := c.calcLineMetrics(1)
						frags := []frag{{text: labelText, bold: c.fontBold, italic: c.fontItalic}}
						// ラベルが列幅を超える場合は縮小する
						if w := c.widthOfFragments(frags, textOpts{}); w > labelWidth+0.0001 {
							c.fontSize *= labelWidth / w
							lm = c.calcLineMetrics(1)
						}
						h := c.heightOfFragments(frags, labelWidth, textOpts{leading: lm.leading}) + lm.padTop + lm.padBottom
						top := c.y
						switch labelValign {
						case "middle", "center":
							if d := (labelHeight - h) * 0.5; d > 0 {
								top -= d
							}
						case "bottom":
							if d := labelHeight - h; d > 0 {
								top -= d
							}
						}
						c.drawTextBox(frags, c.left, top-lm.padTop, labelWidth, 0, "top", textOpts{align: labelAlign, leading: lm.leading})
					})
				})
			}
			c.padBox([4]float64{cpad[0], 0, cpad[2], labelWidth + lpad[1] + cpad[3]}, node, func() {
				if node.HasTitle() {
					c.inkCaption(node.Title(), captionOpts{category: "admonition"})
				}
				c.themeFont("admonition", -1, func() { c.traverse(node) })
			})
		})
	})
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

// guardIndentation は行頭の空白が取り除かれないよう、行頭の空白 1 つを NBSP に置き換えます。
func guardIndentation(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(s, "\t") {
		s = strings.ReplaceAll(s, "\t", "    ")
	}
	if s[0] == ' ' {
		s = nbsp + s[1:]
	}
	if strings.Contains(s, "\n ") {
		s = strings.ReplaceAll(s, "\n ", "\n"+nbsp)
	}
	return s
}

func (c *Converter) convertCode(node *Block) {
	t := c.theme
	var frags []frag
	var bgOverride clr
	source := ""
	c.themeFont("code", -1, func() {
		if hl, bg, ok := c.highlightSource(node); ok {
			frags, bgOverride = hl, bg
			source = node.Source()
			return
		}
		source = guardIndentation(node.Content())
		if strings.ContainsAny(source, "<&") {
			frags = c.parseText(source, false, nil)
		} else {
			frags = []frag{{text: source}}
		}
	})
	adjustedSize := 0.0
	if node.Option("autofit") || node.Doc.HasAttr("autofit-option") {
		c.themeFont("code", -1, func() {
			pad := c.themePadding("code_padding")
			actual := c.widthOfFragments(frags, textOpts{})
			if avail := c.width - pad[1] - pad[3]; actual > avail {
				adjustedSize = float64(int(avail*c.fontSize/actual*10000)) / 10000
				minSize := t.Get("code_font_size_min")
				if minSize == nil {
					minSize = t.Get("base_font_size_min")
				}
				if minSize != nil {
					if m := c.resolveSize(minSize, c.fontSize); adjustedSize < m {
						adjustedSize = m
					}
				}
			}
		})
	}
	toggled := false
	if !node.Option("unbreakable") {
		if minLines, ok := t.Num("code_breakable_min_lines"); ok && float64(strings.Count(source, "\n")+1) < minLines {
			node.Options["unbreakable"] = true
			toggled = true
		}
	}
	captionBottom := t.Str("code_caption_end") == "bottom"
	c.arrangeBlock(node, func(ext *extent) {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		capNode := node
		if captionBottom {
			capNode = nil
		}
		c.themeFillAndStrokeBlock("code", ext, capNode, nil, false, bgOverride)
		c.padBox(c.themePadding("code_padding"), node, func() {
			c.themeFont("code", -1, func() {
				o := prose{align: "left", bottomGutter: c.bottomGutter(node), size: adjustedSize}
				if v := t.Get("code_font_color"); v != nil {
					o.color = toClr(v)
				}
				c.typesetFragments(frags, c.calcLineMetrics(c.baseLineHeight), o)
			})
		})
	})
	if toggled {
		delete(node.Options, "unbreakable")
	}
	if captionBottom && node.HasTitle() {
		c.inkCaption(node.CaptionedTitle(), captionOpts{category: "code", bottom: true})
	}
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertStem(node *Block) {
	c.arrangeBlock(node, func(ext *extent) {
		if node.ID != "" {
			c.addDestForBlock(node, "")
		}
		c.themeFillAndStrokeBlock("code", ext, node, nil, false, clr{})
		c.padBox(c.themePadding("code_padding"), node, func() {
			c.themeFont("code", -1, func() {
				frags := c.parseText(guardIndentation(node.Content()), false, nil)
				c.typesetFragments(frags, c.calcLineMetrics(c.baseLineHeight), prose{align: "left", bottomGutter: c.bottomGutter(node)})
			})
		})
	})
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertMedia(node *Block) {
	target := node.Attr("target")
	if node.Context == "video" {
		switch node.Attr("poster") {
		case "youtube":
			target = "https://www.youtube.com/watch?v=" + target
		case "vimeo":
			target = "https://vimeo.com/" + target
		}
	}
	label := map[string]string{"video": "► ", "audio": "► "}[node.Context]
	c.themeFont(node.Context, -1, func() {
		c.inkProse(label+`<a href="`+target+`">`+target+`</a> <em>(`+node.Context+`)</em>`, prose{noNormalize: true, margin: fptr(0), singleLine: true})
	})
	if node.HasTitle() {
		c.inkCaption(node.Title(), captionOpts{category: node.Context, bottom: true})
	}
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) convertPageBreak(node *Block) {
	if c.atPageTop() && !node.Option("always") {
		return
	}
	c.advancePage()
}

func (c *Converter) convertThematicBreak(node *Block) {
	t := c.theme
	pad := c.themePadding("thematic_break_padding")
	if t.Get("thematic_break_padding") == nil {
		m := t.Float("thematic_break_margin_top", 0)
		pad = [4]float64{m, 0, m, 0}
	}
	c.padBox(pad, nil, func() {
		if col := c.resolveThemeColor("thematic_break_border_color", clr{}, clr{}); col.set {
			lw := t.Float("thematic_break_border_width", 0.5)
			g := pdf.NewG().Save().StrokeColor(col.pdf()).LineWidth(lw)
			applyLineStyle(g, t.Str("thematic_break_border_style"), lw)
			g.MoveTo(c.left, c.y).LineTo(c.left+c.width, c.y).Stroke().Restore()
			c.draw(g)
		}
	})
	if nextEnclosedBlock(node, false) != nil {
		// ページ先頭でも余白を入れる (conceal_page_top 相当)
		amount := t.Float("block_margin_bottom", 0)
		if c.cursor() > amount {
			c.moveDown(amount)
		} else {
			c.advancePage()
		}
	}
}

// ---------------------------------------------------------------------------
// 脚注

func (c *Converter) inkFootnotes(node *Block) {
	doc := c.doc
	all := doc.AllFootnotes()
	if len(all) <= len(c.renderedFootnote) {
		return
	}
	fns := all[len(c.renderedFootnote):]
	t := c.theme
	if node.Context == "document" || (len(doc.Blocks) > 0 && node == doc.Blocks[len(doc.Blocks)-1]) {
		c.themeMargin("block", "bottom", true)
	}
	valignBottom := t.Str("footnotes_margin_top") == "auto"
	if !valignBottom {
		c.themeMargin("footnotes", "top", true)
	}
	offset := len(c.renderedFootnote)
	sectXref := ""
	if node.Context == "section" {
		sectXref = node.Xreftext(doc.Attr("xrefstyle"))
	}
	body := func() {
		c.themeFont("footnotes", -1, func() {
			if title := doc.Attr("footnotes-title"); title != "" {
				c.inkCaption(title, captionOpts{category: "footnotes"})
			}
			spacing := t.Float("footnotes_item_spacing", 0)
			for _, fn := range fns {
				label := strconv.Itoa(fn.Index - offset)
				if sectXref != "" {
					fn.Label = label + " - " + sectXref
				}
				idx := strconv.Itoa(fn.Index)
				c.inkProse(`<a id="_footnotedef_`+idx+`">`+dummyText+`</a><strong><a anchor="_footnoteref_`+idx+`">`+label+`.</a></strong> `+fn.Text, prose{marginBottom: &spacing})
			}
		})
	}
	if valignBottom {
		_, fromY, pages, toY := c.dryRun(false, body)
		if pages == 0 {
			if delta := c.cursor() - (fromY - toY) - 0.0001; delta > 0 {
				c.moveDown(delta)
			}
		}
	}
	body()
	if c.scratch == 0 {
		c.renderedFootnote = append(c.renderedFootnote, fns...)
	}
}

var _ = asciidoc.NormalSubs

// inkAdmonitionIcon は注記ブロックのラベル領域にアイコンを描画します。
func (c *Converter) inkAdmonitionIcon(icon admonitionIcon, set, glyph string, img *loadedImage, labelWidth, labelHeight float64, align, valign string) {
	boxSize := icon.size * icon.scale
	if img != nil {
		w := boxSize
		h := w * img.h / img.w
		if h > labelHeight {
			w, h = w*labelHeight/h, labelHeight
		}
		x := c.left
		switch align {
		case "center":
			x += (labelWidth - w) / 2
		case "right":
			x += labelWidth - w
		}
		y := c.y - h
		switch valign {
		case "middle", "center":
			y = c.y - (labelHeight+h)/2
		case "bottom":
			y = c.y - labelHeight
		}
		g := pdf.NewG()
		img.drawTo(c, g, x, y, w, h)
		c.draw(g)
		return
	}
	if boxSize > labelHeight {
		boxSize = labelHeight
	}
	saved := c.saveFont()
	c.fontFamily, c.fontBold, c.fontItalic = set, false, false
	c.fontSize = boxSize
	iconSize := boxSize
	if f := c.currentFont(); f != nil {
		// グリフの描画高さがアイコンの大きさを超える場合は縮小する
		if rendered := ascender(f, boxSize) + descender(f, boxSize); rendered > boxSize {
			iconSize = boxSize / rendered * boxSize
		}
	}
	c.fontSize = iconSize
	top := c.y
	switch valign {
	case "middle", "center":
		if d := (labelHeight - boxSize) * 0.5; d > 0 {
			top -= d
		}
	case "bottom":
		if d := labelHeight - boxSize; d > 0 {
			top -= d
		}
	}
	color := c.fontColor
	if icon.hasClr {
		color = icon.color
	}
	c.drawTextBox([]frag{{text: glyph, color: color}}, c.left, top, labelWidth, 0, "top", textOpts{align: align})
	c.restoreFont(saved)
}
