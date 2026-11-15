package converter

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

func isNotitle(doc *asciidoc.Document) bool { return doc.HasAttr("notitle") }

// doctitleParts は文書タイトルを主題と副題に分けます。
func (c *Converter) doctitleParts(doc *asciidoc.Document) (combined, main, subtitle string) {
	if doc.HasHeader || doc.Attr("title") != "" {
		combined = doc.ResolvedDocTitle()
	} else if secs := doc.Sections(); len(secs) > 0 && secs[0].HasTitle() {
		combined = secs[0].Title()
	} else {
		combined = doc.Attr("untitled-label")
	}
	sep := doc.AttrOr("title-separator", ":") + " "
	main = combined
	if i := strings.LastIndex(combined, sep); i >= 0 {
		main, subtitle = combined[:i], combined[i+len(sep):]
	}
	return
}

func (c *Converter) resolveTocLevels(doc *asciidoc.Document) int {
	n, err := strconv.Atoi(doc.AttrOr("toclevels", "2"))
	if err != nil {
		n = 2
	}
	if n < 1 {
		n = 1
		if doc.Doctype() == "book" {
			for _, s := range doc.Sections() {
				if s.Sectname == "part" {
					n = 0
					break
				}
			}
		}
	}
	return n
}

func (c *Converter) convertDocument(doc *asciidoc.Document) {
	t := c.theme
	doctype := doc.Doctype()
	hasFrontCover := c.inkCoverPage(doc, "front")
	titleAsPage := doctype == "book" || doc.HasAttr("title-page")
	hasTitlePage := false
	if titleAsPage && doc.HasHeader && !isNotitle(doc) && t.Get("title_page") != false {
		hasTitlePage = true
		if c.pageCount() == 0 || !c.pageEmpty() {
			c.startNewPage()
		}
		c.inkTitlePage(doc)
		c.startNewPage()
	} else if c.pageCount() == 0 || !c.pageEmpty() {
		c.startNewPage()
	}
	// 記事の先頭の abstract セクションは目次に出さないよう open ブロックとして扱う
	if doctype == "article" {
		if secs := doc.Sections(); len(secs) > 0 && secs[0].Sectname == "abstract" && secs[0] == firstNonPreamble(doc) {
			secs[0].Context = "open"
			secs[0].Style = "abstract"
		}
	}
	bodyStartPage := c.pageNumber()
	if !titleAsPage && doc.HasHeader && !isNotitle(doc) {
		c.themeFont("heading", 1, func() {
			align := t.StrOr("heading_h1_text_align", "center")
			c.inkHeading(doc.Title(), headingOpts{align: align, level: 1})
		})
	}
	var frontMatter [2]int
	tocLevels := c.resolveTocLevels(doc)
	var tocPages []int
	c.indentSection(func() {
		placement := doc.Attr("toc-placement")
		tocAtTop := doc.HasAttr("toc") && placement != "macro" && placement != "preamble" && len(doc.Sections()) > 0
		if tocAtTop {
			if c.ppbook && !c.isRecto(c.pageNumber()) {
				c.startNewPage()
			}
			c.addDest("toc", 0, c.destY())
			c.tocExtent = c.allocateToc(doc, tocLevels, titleAsPage && t.Str("toc_break_after") != "auto")
		}
		minStartAt := 1
		if c.ppbook && !c.isRecto(c.pageNumber()) {
			minStartAt = 0
			c.startNewPage()
		}
		zeroOffset := 0
		if hasFrontCover {
			zeroOffset = 1
		}
		usesStartAtToc, usesStartAtAfterToc := false, false
		if titleAsPage {
			firstOffset := zeroOffset
			if hasTitlePage {
				firstOffset++
			}
			bodyStartPage = c.pageNumber()
			bodyOffset := bodyStartPage - 1
			resolve := func(key string) (string, int) {
				offset := bodyOffset
				val := t.Str(key)
				if n, ok := t.Get(key).(float64); ok {
					offset = bodyOffset + maxInt(int(n)-1, minStartAt-1)
					return "body", offset
				}
				switch val {
				case "cover":
					if key == "page_numbering_start_at" {
						if hasFrontCover {
							return "cover", 0
						}
						val = "title"
					}
				}
				switch val {
				case "title":
					if !hasTitlePage {
						val = "toc"
					}
				}
				switch val {
				case "toc":
					usesStartAtToc = true
					if !tocAtTop {
						val = "body"
					}
				case "after-toc":
					usesStartAtAfterToc = true
					val = "body"
				}
				return val, offset
			}
			runningStart, runningBodyOffset := resolve("running_content_start_at")
			numberingStart, numberingBodyOffset := resolve("page_numbering_start_at")
			pick := func(start string, bodyOff int) int {
				switch start {
				case "title":
					return zeroOffset
				case "toc":
					return firstOffset
				}
				return bodyOff
			}
			frontMatter = [2]int{pick(runningStart, runningBodyOffset), pick(numberingStart, numberingBodyOffset)}
			if numberingStart == "cover" {
				frontMatter[1] = numberingBodyOffset
			}
		} else {
			bodyOffset := bodyStartPage - 1
			frontMatter = [2]int{bodyOffset, bodyOffset}
			if n, ok := t.Get("running_content_start_at").(float64); ok {
				frontMatter[0] = bodyOffset + maxInt(int(n)-1, 0)
			}
			if n, ok := t.Get("page_numbering_start_at").(float64); ok {
				frontMatter[1] = bodyOffset + maxInt(int(n)-1, 0)
			} else if t.Str("page_numbering_start_at") == "cover" && hasFrontCover {
				frontMatter[1] = 0
			}
		}
		c.index.startPageNumber = frontMatter[1] + 1
		c.sectInfo[doc.Block] = &sectInfo{anchor: c.deriveAnchorFromID(doc.ID, "top"), pageStart: c.pageNumber()}

		c.traverse(doc.Block)
		c.outdentSection(true, func() { c.inkFootnotes(doc.Block) })

		if ext := c.tocExtent; ext != nil {
			if titleAsPage && !tocAtTop && (usesStartAtToc || usesStartAtAfterToc) {
				if usesStartAtToc {
					off := ext.fromPage
					if t.Str("running_content_start_at") == "toc" {
						frontMatter[0] = off
					}
					if t.Str("page_numbering_start_at") == "toc" {
						frontMatter[1] = off
					}
				}
				if usesStartAtAfterToc {
					off := ext.toPage + 1
					if c.ppbook && c.isRecto(off) {
						off++
					}
					if t.Str("running_content_start_at") == "after-toc" {
						frontMatter[0] = off
					}
					if t.Str("page_numbering_start_at") == "after-toc" {
						frontMatter[1] = off
					}
				}
			}
			tocPages = c.inkToc(doc, tocLevels, ext.fromPage, ext.fromY, frontMatter[1])
		}
		// 内容のない末尾ページを取り除く
		if c.pageCount() > 1 && c.pageEmpty() && c.page == c.pageCount()-1 {
			c.pdf.Pages = c.pdf.Pages[:c.pageCount()-1]
			c.inkCount = c.inkCount[:len(c.inkCount)-1]
			c.goToPage(c.pageCount() - 1)
		}
	})
	if c.pageCount() >= bodyStartPage {
		if !doc.HasAttr("noheader") && t.Float("header_height", 0) != 0 {
			c.inkRunningContent("header", doc, frontMatter, bodyStartPage)
		}
		if !doc.HasAttr("nofooter") && t.Float("footer_height", 0) != 0 {
			c.inkRunningContent("footer", doc, frontMatter, bodyStartPage)
		}
	}
	c.addOutline(doc, tocLevels, tocPages, frontMatter[1], hasFrontCover)
	if zoom := t.Str("page_initial_zoom"); zoom == "Fit" || zoom == "FitV" || zoom == "FitH" {
		c.pdf.OpenAction = zoom
	}
	c.pdf.DisplayDocTitle = true
	c.inkCoverPage(doc, "back")
	if top := c.sectInfo[doc.Block]; top != nil {
		if _, exists := c.pdf.GetDest(top.anchor); !exists {
			page := bodyStartPage - 1
			if page >= c.pageCount() {
				page = c.pageCount() - 1
			}
			c.pdf.SetDest(top.anchor, pdf.Dest{Page: page, X: 0, Y: c.pageH})
		}
	}
	if lang := doc.Attr("lang"); lang != "" && !doc.HasAttr("nolang") {
		c.pdf.Lang = lang
	}
}

func isLastChild(b *Block) bool {
	blocks := b.Doc.Blocks
	return len(blocks) > 0 && blocks[len(blocks)-1] == b
}

func firstNonPreamble(doc *asciidoc.Document) *Block {
	for _, b := range doc.Blocks {
		if b.Context != "preamble" {
			return b
		}
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (c *Converter) destY() float64 {
	if c.atPageTop() {
		return c.pageH
	}
	return c.y
}

// ---------------------------------------------------------------------------
// 表紙・タイトルページ

func (c *Converter) inkCoverPage(doc *asciidoc.Document, face string) bool {
	img, _ := c.resolveBackgroundImage(doc, face+"-cover-image", "cover_"+face+"_image", true)
	if img == nil {
		return false
	}
	toEnd := func() {
		if face == "back" && c.pageCount() > 0 {
			c.goToPage(c.pageCount() - 1)
		}
	}
	if img.symbolic {
		if img.path == "~" {
			return false
		}
		// 空の値は白紙の表紙を意味する
		toEnd()
		c.startNewPageDiscretely()
		c.inked()
		return true
	}
	toEnd()
	if img.format == "pdf" {
		imported, err := c.importPage(img.path, importOpts{page: img.page, advance: face != "back"})
		if err != nil {
			c.logf("WARN", "could not embed %s cover image: %s; %v", face, img.path, err)
			return false
		}
		return imported
	}
	li := c.loadImage(img.path, img.path)
	if li == nil {
		c.logf("WARN", "could not embed %s cover image: %s", face, img.path)
		return false
	}
	c.startNewPageDiscretely()
	x, y, w, h := c.placeImage(li, img, c.pageW, c.pageH)
	g := pdf.NewG()
	li.drawTo(c, g, x, y, w, h)
	c.draw(g)
	c.inked()
	c.noRunning["header"][c.pageNumber()] = true
	c.noRunning["footer"][c.pageNumber()] = true
	return true
}

var imageAttrValueRx = regexp.MustCompile(`^image:{1,2}(.*?)\[(.*?)\]$`)

// resolveTop は "55%" や "2in" のような位置指定をページ上端からの y 座標に解決します。
func (c *Converter) resolveTop(v any) float64 {
	switch x := v.(type) {
	case float64:
		return c.pageH - x
	case string:
		if strings.HasSuffix(x, "vh") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(x, "vh"), 64)
			return c.pageH - c.pageH*n/100
		}
		if strings.HasSuffix(x, "%") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(x, "%"), 64)
			return c.top - (c.top-c.bottom)*n/100
		}
		return c.top - theme.StrToPt(x)
	}
	return c.y
}

func (c *Converter) inkTitlePage(doc *asciidoc.Document) {
	t := c.theme
	align := t.StrOr("title_page_text_align", c.baseTextAlign)
	zero := fptr(0)
	if t.Str("title_page_logo_display") != "none" {
		logo, fromTheme := doc.Attr("title-logo-image"), false
		if logo == "" {
			logo, fromTheme = t.Str("title_page_logo_image"), true
		}
		if logo != "" {
			attrs := map[string]string{}
			target := logo
			if m := imageAttrValueRx.FindStringSubmatch(logo); m != nil {
				target = m[1]
				doc.ParseAttributes(m[2], []string{"alt", "width", "height"}, nil, attrs)
			}
			if fromTheme {
				target = c.themeAsset(doc.SubAttributes(target))
			}
			if imageFormat(target, attrs) == "pdf" {
				c.logf("ERROR", "PDF format not supported for title page logo image: %s", target)
				logo = ""
			}
		}
		if logo != "" {
			attrs := map[string]string{}
			target := logo
			if m := imageAttrValueRx.FindStringSubmatch(logo); m != nil {
				target = m[1]
				doc.ParseAttributes(m[2], []string{"alt", "width", "height"}, nil, attrs)
			}
			if fromTheme {
				target = c.themeAsset(doc.SubAttributes(target))
			}
			node := asciidoc.NewBlock(doc.Block, "image")
			for k, v := range attrs {
				node.Attrs[k] = v
			}
			node.Attrs["target"] = target
			if a := attrs["align"]; a == "" {
				node.Attrs["align"] = t.StrOr("title_page_logo_align", align)
			}
			initialY := c.y
			top := attrs["top"]
			if top == "" {
				top = t.Str("title_page_logo_top")
			}
			if top != "" {
				c.y = c.resolveTop(top)
			}
			c.indent(t.Float("title_page_logo_margin_left", 0), t.Float("title_page_logo_margin_right", 0), func() {
				c.convertImage(node, imageOpts{relativeToImagesdir: !fromTheme, pinned: true})
			})
			c.y = initialY
		}
	}
	c.themeFont("title_page", -1, func() {
		if v := t.Get("title_page_title_top"); v != nil {
			c.y = c.resolveTop(v)
		}
		_, main, subtitle := c.doctitleParts(doc)
		part := func(name, text string, o prose) {
			c.moveDown(t.Float("title_page_"+name+"_margin_top", 0))
			c.indent(t.Float("title_page_"+name+"_margin_left", 0), t.Float("title_page_"+name+"_margin_right", 0), func() {
				c.themeFont("title_page_"+name, -1, func() {
					o.align, o.margin = align, zero
					o.inheritStyles = true
					c.inkProse(text, o)
				})
			})
			c.moveDown(t.Float("title_page_"+name+"_margin_bottom", 0))
		}
		if t.Str("title_page_title_display") != "none" {
			part("title", main, prose{})
		}
		if t.Str("title_page_subtitle_display") != "none" && subtitle != "" {
			part("subtitle", subtitle, prose{})
		}
		if t.Str("title_page_authors_display") != "none" && doc.HasAttr("authors") {
			var names []string
			count, _ := strconv.Atoi(doc.AttrOr("authorcount", "1"))
			if count < 1 {
				count = 1
			}
			generic := t.Str("title_page_authors_content")
			for i := 1; i <= count; i++ {
				suffix := ""
				if count > 1 || i > 1 {
					suffix = "_" + strconv.Itoa(i)
				}
				get := func(key string) (string, bool) {
					if v, ok := doc.Attributes[key+suffix]; ok {
						return v, true
					}
					if i == 1 {
						v, ok := doc.Attributes[key]
						return v, ok
					}
					return "", false
				}
				name, _ := get("author")
				email, hasEmail := get("email")
				content, key := generic, "name_only"
				if hasEmail {
					key = "with_url"
					if !strings.Contains(email, "://") {
						key = "with_email"
					}
				}
				if v := t.Str("title_page_authors_content_" + key); v != "" {
					content = v
				}
				if content == "" {
					names = append(names, name)
					continue
				}
				// 著者ごとの属性を一時的に設定して内容を展開する
				saved := map[string]*string{}
				set := func(k, v string, ok bool) {
					if old, had := doc.Attributes[k]; had {
						o := old
						saved[k] = &o
					} else {
						saved[k] = nil
					}
					if ok {
						doc.Attributes[k] = v
					} else {
						delete(doc.Attributes, k)
					}
				}
				set("author", name, true)
				for _, k := range []string{"firstname", "middlename", "lastname", "authorinitials"} {
					v, ok := get(k)
					set(k, v, ok)
				}
				if hasEmail {
					url := email
					if !strings.Contains(email, "://") {
						url = "mailto:" + email
					}
					set("email", email, true)
					set("url", url, true)
				} else {
					set("email", "", false)
					set("url", "", false)
				}
				names = append(names, c.applySubsDiscretely(doc, content, true))
				for k, v := range saved {
					if v == nil {
						delete(doc.Attributes, k)
					} else {
						doc.Attributes[k] = *v
					}
				}
			}
			part("authors", strings.Join(names, t.Str("title_page_authors_delimiter")), prose{})
		}
		if t.Str("title_page_revision_display") != "none" {
			var rev string
			if content := t.Str("title_page_revision_content"); content != "" {
				rev = c.applySubsDiscretely(doc, content, true)
			} else {
				delims := []string{", ", ": "}
				for i, d := range t.List("title_page_revision_delimiter") {
					if s, ok := d.(string); ok && i < 2 {
						delims[i] = s
					}
				}
				if doc.HasAttr("revnumber") {
					rev = strings.TrimSpace(doc.Attr("version-label") + " " + doc.Attr("revnumber"))
				}
				if doc.HasAttr("revdate") {
					if rev != "" {
						rev += delims[0]
					}
					rev += doc.Attr("revdate")
				}
				if doc.HasAttr("revremark") {
					if rev != "" {
						rev += delims[1]
					}
					rev += doc.Attr("revremark")
				}
			}
			if rev != "" {
				part("revision", rev, prose{noNormalize: true})
			}
		}
	})
}

var simpleAttrRefRx = regexp.MustCompile(`\{[\p{L}\p{N}_][\p{L}\p{N}_-]*\}`)

// applySubsDiscretely はテーマ内の文字列に置換を適用します。
func (c *Converter) applySubsDiscretely(doc *asciidoc.Document, value string, dropUnresolved bool) string {
	missing, had := doc.Attributes["attribute-missing"]
	doc.Attributes["attribute-missing"] = "skip"
	value = doc.ApplySubs(value, asciidoc.NormalSubs, doc.Block)
	if had {
		doc.Attributes["attribute-missing"] = missing
	} else {
		delete(doc.Attributes, "attribute-missing")
	}
	if dropUnresolved && strings.Contains(value, "{") {
		var kept []string
		for _, line := range strings.Split(value, "\n") {
			if !simpleAttrRefRx.MatchString(line) {
				kept = append(kept, line)
			}
		}
		value = strings.Join(kept, "\n")
	}
	return value
}

// ---------------------------------------------------------------------------
// 目次

func (c *Converter) allocateToc(doc *asciidoc.Document, levels int, breakAfter bool) *extent {
	startPage := c.page
	startY := c.y
	toPage := startPage
	_, _, pages, toY := c.dryRun(false, func() {
		pg := c.inkToc(doc, levels, c.page, c.y, 0)
		if len(pg) > 0 {
			toPage = startPage + len(pg) - 1
		}
		if !breakAfter {
			c.themeMargin("block", "bottom", true)
		}
	})
	ext := &extent{curPage: startPage, curY: startY, fromPage: startPage, fromY: startY, toPage: startPage + pages, toY: toY}
	if toPage > ext.toPage {
		ext.toPage = toPage
		ext.toY = c.bottom
	}
	if breakAfter {
		for p := ext.fromPage; p <= ext.toPage; p++ {
			c.startNewPage()
		}
	} else {
		for p := ext.fromPage; p < ext.toPage; p++ {
			c.startNewPage()
		}
		c.y = ext.toY
	}
	return ext
}

type dotLeader struct {
	color       clr
	bold        bool
	italic      bool
	size        float64
	levels      map[int]bool
	hasLevels   bool
	text        string
	width       float64
	spacerSize  float64
	spacerWidth float64
}

// inkToc は目次を描画し、目次が占めるページ (0 始まり) の一覧を返します。
func (c *Converter) inkToc(doc *asciidoc.Document, levels, tocPage int, startY float64, frontMatterPages int) []int {
	t := c.theme
	if c.scratch == 0 && c.page != tocPage {
		c.goToPage(tocPage)
	}
	startPage := c.page
	c.y = startY
	if title := doc.Attr("toc-title"); title != "" {
		c.themeFontCascade([]fontCat{{"heading", 2}, {"toc_title", -1}}, func() {
			align := c.baseTextAlign
			for _, k := range []string{"toc_title_text_align", "heading_h2_text_align", "heading_text_align"} {
				if v := t.Str(k); v != "" {
					align = v
					break
				}
			}
			c.inkHeading(title, headingOpts{align: align, level: 2, outdent: true})
		})
	}
	if levels >= 0 {
		var dl dotLeader
		c.themeFont("toc", -1, func() {
			style := t.StrOr("toc_dot_leader_font_style", "normal")
			c.setFontStyle(style)
			if v := t.Get("toc_dot_leader_font_size"); v != nil {
				c.fontSize = c.resolveSize(v, c.fontSize)
			}
			dl.bold, dl.italic = c.fontBold, c.fontItalic
			dl.size = c.fontSize
			dl.color = c.fontColor
			if v := t.Get("toc_dot_leader_font_color"); v != nil {
				dl.color = toClr(v)
			}
			if lv := t.Str("toc_dot_leader_levels"); lv == "none" {
				dl.hasLevels, dl.levels = true, map[int]bool{}
			} else if lv != "" && lv != "all" {
				dl.hasLevels, dl.levels = true, map[int]bool{}
				for _, f := range strings.Fields(lv) {
					if n, err := strconv.Atoi(f); err == nil {
						dl.levels[n] = true
					}
				}
			}
			dl.text = ". "
			if t.Has("toc_dot_leader_content") {
				dl.text = t.Str("toc_dot_leader_content")
			}
			if dl.text != "" {
				dl.width = c.renderedWidthOfString(dl.text)
			}
			dl.spacerSize = c.fontSize * 0.25
			saved := c.fontSize
			c.fontSize = dl.spacerSize
			dl.spacerWidth = c.renderedWidthOfString(nbsp)
			c.fontSize = saved
		})
		c.themeMargin("toc", "top", true)
		c.inkTocLevel(doc.Sections(), levels, &dl, frontMatterPages)
	}
	var pages []int
	for p := tocPage; p <= tocPage+(c.page-startPage); p++ {
		pages = append(pages, p)
	}
	if c.scratch == 0 {
		c.goToPage(c.pageCount() - 1)
	}
	return pages
}

var dropAnchorRx = regexp.MustCompile(`<(?:a\b[^>]*|/a)>`)

func (c *Converter) inkTocLevel(entries []*Block, levels int, dl *dotLeader, frontMatterPages int) {
	t := c.theme
	hanging := t.Float("toc_hanging_indent", 0)
	var tocFamily string
	c.themeFont("toc", -1, func() { tocFamily = c.fontFamily })
	for _, entry := range entries {
		entryLevels := levels
		if v, ok := entry.Attrs["toclevels"]; ok {
			entryLevels, _ = strconv.Atoi(v)
		}
		entryLevel := entry.Level + 1
		if entryLevels < entryLevel-1 {
			continue
		}
		if entry.Option("notitle") && len(entry.Blocks) == 0 && isLastChild(entry) {
			continue
		}
		title := c.numberedTitle(entry, false)
		if title == "" {
			continue
		}
		c.themeFont("toc", entryLevel, func() {
			if strings.Contains(title, "<a") {
				title = dropAnchorRx.ReplaceAllString(title, "")
			}
			if c.textTransform != "" {
				title = transformText(title, c.textTransform)
			}
			placeholder := c.renderedWidthOfString(strings.Repeat("0", c.tocMaxDigits))
			zero := fptr(0)
			if c.scratch > 0 {
				c.indent(0, placeholder, func() {
					c.inkProse(title, prose{anchorSelf: true, noNormalize: true, hangingIndent: hanging, normalizeLineHeight: true, margin: zero})
				})
				return
			}
			si := c.sectInfo[entry]
			anchor, label := entry.ID, "?"
			if si != nil {
				if si.anchor != "" {
					anchor = si.anchor
				}
				if si.pageStart > 0 {
					virtual := si.pageStart - frontMatterPages
					if virtual < 1 {
						label = strings.ToLower(toRoman(si.pageStart))
					} else {
						label = strconv.Itoa(virtual)
					}
				}
			}
			startPage, startY := c.page, c.y
			inherited := &frag{anchor: anchor, color: c.fontColor}
			if d := t.Str("toc_h" + strconv.Itoa(entryLevel) + "_text_decoration"); d != "" || t.Str("toc_text_decoration") != "" {
				if d == "" {
					d = t.Str("toc_text_decoration")
				}
				inherited.underline, inherited.strike = d == "underline", d == "line-through"
			}
			frags := c.parseText(title, false, inherited)
			lm := c.calcLineMetrics(c.baseLineHeight)
			c.lastLine = nil
			c.indent(0, placeholder, func() {
				c.typesetFragments(frags, lm, prose{hangingIndent: hanging, normalizeLineHeight: true})
			})
			last := c.lastLine
			if last == nil {
				return
			}
			startDots := last.right - c.left
			lastTop := last.top + lm.padTop
			if last.page > startPage || (startY-lastTop) > lm.height {
				startY = lastTop
			}
			endPage, endY := c.page, c.y
			c.y = startY
			labelFrag := frag{text: label, color: c.fontColor, anchor: anchor, bold: c.fontBold, italic: c.fontItalic, family: c.fontFamily, size: c.fontSize}
			right := prose{align: "right"}
			if dl.width > 0 && (!dl.hasLevels || dl.levels[entryLevel-1]) {
				labelWidth := c.renderedWidthOfString(label)
				saved := c.saveFont()
				c.fontFamily, c.fontSize, c.fontBold, c.fontItalic = tocFamily, dl.size, dl.bold, dl.italic
				numDots := int((c.width - startDots - dl.spacerWidth - labelWidth) / dl.width)
				if numDots < 0 {
					numDots = 0
				}
				c.typesetFragments([]frag{
					{text: strings.Repeat(dl.text, numDots), color: dl.color},
					{text: nbsp, size: dl.spacerSize},
					labelFrag,
				}, lm, right)
				c.restoreFont(saved)
			} else {
				c.typesetFragments([]frag{labelFrag}, lm, right)
			}
			if c.page != endPage {
				c.goToPage(endPage)
			}
			c.y = endY
		})
		if entryLevels >= entryLevel {
			c.indent(t.Float("toc_indent", 0), 0, func() {
				c.inkTocLevel(entry.Sections(), entryLevels, dl, frontMatterPages)
			})
		}
	}
}

func toRoman(n int) string {
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

// numberedTitle はセクション番号付きのタイトルを返します。
func (c *Converter) numberedTitle(sect *Block, formal bool) string {
	if cached, ok := c.numberedTitles[sect]; ok {
		if formal {
			return cached[1]
		}
		return cached[0]
	}
	doc := sect.Doc
	title := sect.Title()
	var plain, formalTitle string
	sectnumlevels, err := strconv.Atoi(doc.AttrOr("sectnumlevels", "3"))
	if err != nil {
		sectnumlevels = 3
	}
	switch {
	case sect.Numbered && sect.Caption == "" && sect.Level <= sectnumlevels:
		signify := func(attr, def string) string {
			sig, ok := doc.Attributes[attr]
			if !ok {
				if doc.AttrUnspecified(attr) {
					sig = def
				}
			}
			if sig == "" {
				return plain
			}
			return sig + " " + plain
		}
		if doc.Doctype() == "book" && sect.Level == 0 {
			colon := ":"
			plain = sect.Sectnum("", &colon) + " " + title
			formalTitle = signify("part-signifier", "Part")
		} else if doc.Doctype() == "book" && sect.Level == 1 {
			plain = sect.Sectnum(".", nil) + " " + title
			formalTitle = signify("chapter-signifier", "Chapter")
		} else {
			plain = sect.Sectnum(".", nil) + " " + title
			formalTitle = plain
		}
	case sect.Level == 0:
		plain, formalTitle = title, title
	default:
		plain = sect.CaptionedTitle()
		formalTitle = plain
	}
	c.numberedTitles[sect] = [2]string{plain, formalTitle}
	if formal {
		return formalTitle
	}
	return plain
}

// ---------------------------------------------------------------------------
// アウトライン (しおり) とページラベル

func (c *Converter) addOutline(doc *asciidoc.Document, tocLevels int, tocPages []int, frontMatterPages int, hasFrontCover bool) {
	numLevels, expandLevels := tocLevels, tocLevels
	if v, ok := doc.Attributes["outlinelevels"]; ok {
		if a, b, found := strings.Cut(v, ":"); found {
			if a != "" {
				numLevels, _ = strconv.Atoi(a)
			}
			expandLevels, _ = strconv.Atoi(b)
		} else if n, err := strconv.Atoi(v); err == nil {
			numLevels, expandLevels = n, n
		}
	}
	for n := 0; n < c.pageCount(); n++ {
		label := ""
		if n < frontMatterPages {
			label = strings.ToLower(toRoman(n + 1))
		} else {
			label = strconv.Itoa(n - frontMatterPages + 1)
		}
		c.pdf.PageLabels = append(c.pdf.PageLabels, pdf.PageLabel{Start: n, Prefix: label})
	}
	if doc.HasAttr("outline") {
		var items []*pdf.OutlineItem
		initial := 0
		if hasFrontCover {
			initial = 1
		}
		if title, ok := doc.Attributes["outline-title"]; ok && c.pageCount() > initial {
			if title == "" {
				title, _, _ = c.doctitleParts(doc)
			}
			if title = sanitize(title); title != "" {
				items = append(items, &pdf.OutlineItem{Title: title, Page: initial, Y: c.pageH})
			}
		}
		sections := doc.Sections()
		if tocTitle := doc.Attr("toc-title"); len(tocPages) > 0 && tocTitle != "" && len(sections) > 0 {
			items = append(items, &pdf.OutlineItem{Title: sanitize(tocTitle), Page: tocPages[0], Y: c.pageH})
		}
		items = append(items, c.outlineLevel(sections, numLevels, expandLevels)...)
		c.pdf.Outline = items
	}
	mode := doc.AttrOr("pdf-page-mode", c.theme.Str("page_mode"))
	switch mode {
	case "fullscreen":
		c.pdf.PageMode, c.pdf.NonFullScreenMode = "FullScreen", "UseOutlines"
	case "fullscreen none":
		c.pdf.PageMode, c.pdf.NonFullScreenMode = "FullScreen", "UseNone"
	case "fullscreen thumbs":
		c.pdf.PageMode, c.pdf.NonFullScreenMode = "FullScreen", "UseThumbs"
	case "none":
		c.pdf.PageMode = "UseNone"
	case "thumbs":
		c.pdf.PageMode = "UseThumbs"
	default:
		c.pdf.PageMode = "UseOutlines"
	}
}

func (c *Converter) outlineLevel(sections []*Block, numLevels, expandLevels int) []*pdf.OutlineItem {
	var items []*pdf.OutlineItem
	for _, sect := range sections {
		levels := numLevels
		if v, ok := sect.Attrs["outlinelevels"]; ok {
			levels, _ = strconv.Atoi(v)
		}
		if levels < sect.Level || (sect.Option("notitle") && len(sect.Blocks) == 0 && isLastChild(sect)) {
			continue
		}
		title := sanitize(c.numberedTitle(sect, true))
		si := c.sectInfo[sect]
		if title == "" || si == nil {
			continue
		}
		item := &pdf.OutlineItem{Title: title, Page: si.destPage, Y: si.destY}
		if subs := sect.Sections(); sect.Level < levels && len(subs) > 0 {
			item.Closed = expandLevels < 1
			item.Children = c.outlineLevel(subs, levels, expandLevels-1)
		}
		items = append(items, item)
	}
	return items
}

// ---------------------------------------------------------------------------
// ランニングコンテンツ (ヘッダー・フッター)

func findSections(b *Block, maxLevel int, out *[]*Block) {
	for _, child := range b.Blocks {
		if child.Context == "section" {
			if child.Level <= maxLevel {
				*out = append(*out, child)
			}
			findSections(child, maxLevel, out)
		} else if len(child.Blocks) > 0 && child.Context != "table" {
			findSections(child, maxLevel, out)
		}
	}
}

func (c *Converter) inkRunningContent(periphery string, doc *asciidoc.Document, skip [2]int, bodyStartPage int) {
	t := c.theme
	skipPages, skipPagenums := skip[0], skip[1]
	numPages := c.pageCount()
	if skipPages >= numPages {
		return
	}
	prevPage, prevY := c.page, c.y
	isBook := doc.Doctype() == "book"
	sectlevels := int(t.Float(periphery+"_sectlevels", 2))
	var sections []*Block
	findSections(doc.Block, sectlevels, &sections)
	titleStyle := t.Str(periphery + "_title_style")
	titleOf := func(v any) string {
		switch x := v.(type) {
		case string:
			return x
		case *Block:
			switch titleStyle {
			case "document":
				return x.Title()
			case "toc":
				return c.numberedTitle(x, false)
			}
			return c.numberedTitle(x, true)
		}
		return ""
	}
	numeralOf := func(v any) (string, bool) {
		if b, ok := v.(*Block); ok && b.Numeral != "" {
			return b.Numeral, true
		}
		return "", false
	}
	partStart, chapStart, sectStart, trailing := map[int]any{}, map[int]any{}, map[int]any{}, map[int]any{}
	for _, sect := range sections {
		si := c.sectInfo[sect]
		if si == nil {
			continue
		}
		pg := si.pageStart
		if isBook && (sect.Sectname == "part" || sect.Level == 1) {
			if sect.Sectname == "part" {
				if _, ok := partStart[pg]; !ok {
					partStart[pg] = sect
				}
			} else {
				if _, ok := chapStart[pg]; !ok {
					chapStart[pg] = sect
				}
				if sect.Sectname == "appendix" && len(partStart) > 0 {
					partStart[pg] = ""
				}
			}
		} else {
			trailing[pg] = sect
			if _, ok := sectStart[pg]; !ok {
				sectStart[pg] = sect
			}
		}
	}
	parts, chaps, sects := map[int]any{}, map[int]any{}, map[int]any{}
	var lastPart, lastChap, lastSect any
	pre := isBook
	threshold := 1
	tocFrom, tocTo := -1, -1
	if c.tocExtent != nil {
		tocFrom, tocTo = c.tocExtent.fromPage+1, c.tocExtent.toPage+1
	}
	tocTitle := doc.Attr("toc-title")
	for pg := 1; pg <= numPages; pg++ {
		part, hasPart := partStart[pg]
		if hasPart {
			lastPart, lastChap, lastSect, pre = part, nil, nil, false
		}
		chap, hasChap := chapStart[pg]
		if hasChap {
			lastChap, lastSect, pre = chap, nil, false
		}
		if sect, ok := sectStart[pg]; ok {
			lastSect = sect
		} else if hasPart || hasChap {
			threshold = pg
		} else if lastSect != nil {
			for prev := pg - 1; prev >= threshold; prev-- {
				if s, ok := trailing[prev]; ok {
					lastSect = s
					break
				}
			}
		}
		parts[pg] = lastPart
		switch {
		case tocFrom > 0 && pg >= tocFrom && pg <= tocTo:
			if isBook {
				chaps[pg], sects[pg] = tocTitle, nil
			} else {
				chaps[pg] = nil
				if s, ok := sectStart[pg]; ok {
					sects[pg] = s
				} else {
					sects[pg] = tocTitle
				}
			}
		case pre:
			if pg < bodyStartPage {
				chaps[pg] = doc.Title()
			} else if v, ok := doc.Attributes["preface-title"]; ok {
				chaps[pg] = v
			} else if doc.AttrUnspecified("preface-title") {
				chaps[pg] = "Preface"
			} else {
				chaps[pg] = ""
			}
			sects[pg] = lastSect
		default:
			chaps[pg], sects[pg] = lastChap, lastSect
		}
	}
	combined, main, subtitle := c.doctitleParts(doc)
	attrs := doc.Attributes
	attrs["doctitle"] = combined
	attrs["document-title"] = main
	attrs["document-subtitle"] = subtitle
	attrs["page-count"] = strconv.Itoa(numPages - skipPagenums)
	pagenums := doc.HasAttr("pagenums")
	layout := "portrait"
	if c.pageW > c.pageH {
		layout = "landscape"
	}
	attrs["page-layout"] = layout

	height := t.Float(periphery+"_height", 0)
	borderWidth := t.Float(periphery+"_border_width", 0)
	valign := "middle"
	if list := t.List(periphery + "_vertical_align"); len(list) > 0 {
		if s, ok := list[0].(string); ok {
			valign = s
		}
	}
	lineHeight := t.Float(periphery+"_line_height", c.baseLineHeight)
	bgColor := c.resolveThemeColor(periphery+"_background_color", clr{}, clr{})
	borderColor := c.resolveThemeColor(periphery+"_border_color", c.themeClr("base_border_color"), clr{})

	for pg := skipPages + 1; pg <= numPages; pg++ {
		if c.noRunning[periphery][pg] || c.pdf.Pages[pg-1].Imported {
			continue
		}
		c.goToPage(pg - 1)
		virtual := pg - skipPagenums
		label := strconv.Itoa(virtual)
		if virtual < 1 {
			label = strings.ToLower(toRoman(pg))
		}
		sidePg := virtual
		if c.folioPhysical {
			sidePg = pg
		}
		side, pageMargin := "recto", c.marginRecto
		if !c.isRecto(sidePg) {
			side, pageMargin = "verso", c.marginVerso
		}
		if c.media != "prepress" {
			pageMargin = c.marginRecto
		}
		inherit := func(key string, base [4]float64) [4]float64 {
			v := t.Get(periphery + "_" + side + "_" + key)
			if v == nil {
				v = t.Get(periphery + "_" + key)
			}
			if v == nil {
				return [4]float64{0, base[1], 0, base[3]}
			}
			out := expandPadding(v)
			if list, ok := v.([]any); ok {
				full := list
				switch len(list) {
				case 1:
					full = []any{list[0], list[0], list[0], list[0]}
				case 2:
					full = []any{list[0], list[1], list[0], list[1]}
				case 3:
					full = []any{list[0], list[1], list[2], list[1]}
				}
				for i := 1; i < 4 && i < len(full); i += 2 {
					if s, ok := full[i].(string); ok && s == "inherit" {
						out[i] = base[i]
					}
				}
			} else if s, ok := v.(string); ok && s == "inherit" {
				out[1], out[3] = base[1], base[3]
			}
			return out
		}
		trimMargin := inherit("margin", pageMargin)
		contentMargin := inherit("content_margin", [4]float64{0, pageMargin[1] - trimMargin[1], 0, pageMargin[3] - trimMargin[3]})
		padding := contentMargin
		pv := t.Get(periphery + "_" + side + "_padding")
		if pv == nil {
			pv = t.Get(periphery + "_padding")
		}
		if pv != nil {
			p := expandPadding(pv)
			for i := range padding {
				padding[i] = p[i] + contentMargin[i]
			}
		}
		trimTop := height + trimMargin[2]
		if periphery == "header" {
			trimTop = c.pageH - trimMargin[0]
		}
		trimLeft := trimMargin[3]
		trimWidth := c.pageW - trimLeft - trimMargin[1]
		contentLeft := trimLeft + padding[3]
		contentWidth := trimWidth - padding[1] - padding[3]
		contentHeight := height - padding[0] - padding[2] - borderWidth*0.5
		contentOffset := 0.0
		if periphery == "footer" {
			contentOffset = borderWidth * 0.5
		}
		g := pdf.NewG()
		if bgColor.set {
			g.Save().FillColor(bgColor.pdf()).Rect(trimLeft, trimTop-height, trimWidth, height).Fill().Restore()
		}
		if borderWidth > 0 && borderColor.set {
			by := trimTop
			if periphery == "header" {
				by = trimTop - height
			}
			g.Save().StrokeColor(borderColor.pdf()).LineWidth(borderWidth)
			applyLineStyle(g, t.Str(periphery+"_border_style"), borderWidth)
			g.MoveTo(trimLeft, by).LineTo(trimLeft+trimWidth, by).Stroke().Restore()
		}
		c.draw(g)
		// 列の定義
		type colspec struct {
			align    string
			width, x float64
		}
		cols := map[string]colspec{
			"left": {"left", contentWidth, 0}, "center": {"center", contentWidth, 0}, "right": {"right", contentWidth, 0},
		}
		custom := t.Str(periphery + "_" + side + "_columns")
		if custom == "" {
			custom = t.Str(periphery + "_columns")
		}
		if custom != "" {
			specs := strings.Fields(strings.ReplaceAll(custom, ",", " "))
			var named [3]string
			switch len(specs) {
			case 0:
				named = [3]string{"0", "100", "0"}
			case 1:
				named = [3]string{"0", specs[0], "0"}
			case 2:
				named = [3]string{specs[0], "0", specs[1]}
			default:
				named = [3]string{specs[0], specs[1], specs[2]}
			}
			total := 0.0
			var parsed [3]colspec
			for i, spec := range named {
				align := "left"
				if spec != "" && (spec[0] < '0' || spec[0] > '9') {
					align = map[byte]string{'<': "left", '=': "center", '^': "center", '>': "right"}[spec[0]]
					spec = spec[1:]
				}
				w, _ := strconv.ParseFloat(spec, 64)
				total += w
				parsed[i] = colspec{align: align, width: w}
			}
			if total > 0 {
				x := 0.0
				for i, name := range []string{"left", "center", "right"} {
					parsed[i].width = parsed[i].width / total * contentWidth
					parsed[i].x = x
					x += parsed[i].width
					cols[name] = parsed[i]
				}
			}
		}
		// ページごとの属性
		if pagenums {
			attrs["page-number"] = label
		} else {
			delete(attrs, "page-number")
		}
		attrs["part-title"] = titleOf(parts[pg])
		if n, ok := numeralOf(parts[pg]); ok {
			attrs["part-numeral"] = n
		} else {
			delete(attrs, "part-numeral")
		}
		attrs["chapter-title"] = titleOf(chaps[pg])
		if n, ok := numeralOf(chaps[pg]); ok {
			attrs["chapter-numeral"] = n
		} else {
			delete(attrs, "chapter-numeral")
		}
		sectTitle := titleOf(sects[pg])
		attrs["section-title"] = sectTitle
		if sectTitle != "" {
			attrs["section-or-chapter-title"] = sectTitle
		} else {
			attrs["section-or-chapter-title"] = attrs["chapter-title"]
		}
		c.themeFontCascade([]fontCat{{periphery, -1}, {periphery + "_" + side, -1}}, func() {
			lm := c.calcLineMetrics(lineHeight)
			proseHeight := contentHeight - lm.padTop - lm.padBottom
			for _, pos := range []string{"left", "center", "right"} {
				content := t.Str(periphery + "_" + side + "_" + pos + "_content")
				col := cols[pos]
				if content == "" || col.width <= 0 {
					continue
				}
				c.themeFont(periphery+"_"+side+"_"+pos, -1, func() {
					if content == "{page-number}" {
						if !pagenums {
							return
						}
						content = label
					} else if strings.HasPrefix(content, "image:") && imageAttrValueRx.MatchString(content) {
						c.inkRunningImage(doc, content, contentLeft+col.x, trimTop-padding[0]-contentOffset, col.width, contentHeight, col.align, valign)
						return
					} else {
						content = c.applySubsDiscretely(doc, content, true)
						if c.textTransform != "" {
							content = transformText(content, c.textTransform)
						}
					}
					if content == "" {
						return
					}
					top := trimTop - padding[0] - contentOffset
					frags := c.parseText(content, true, nil)
					c.drawTextBox(frags, contentLeft+col.x, top, col.width, proseHeight, valign, textOpts{align: col.align, leading: lm.leading})
				})
			}
		})
	}
	for _, k := range []string{"page-number", "part-title", "part-numeral", "chapter-title", "chapter-numeral", "section-title", "section-or-chapter-title"} {
		delete(attrs, k)
	}
	c.goToPage(prevPage)
	c.y = prevY
}

func applyLineStyle(g *pdf.G, style string, width float64) {
	switch style {
	case "dashed":
		g.Dash(width*4, width*4)
	case "dotted":
		g.Dash(width, width)
	}
}

func (c *Converter) inkRunningImage(doc *asciidoc.Document, content string, x, top, width, height float64, align, valign string) {
	m := imageAttrValueRx.FindStringSubmatch(content)
	attrs := doc.ParseAttributes(m[2], []string{"alt", "width"}, nil, nil)
	path := c.themeAsset(doc.SubAttributes(m[1]))
	li := c.loadImage(path, m[1])
	if li == nil {
		c.logf("WARN", "could not embed image in running content: %s", path)
		return
	}
	w, h := li.w*0.75, li.h*0.75
	if ew, ok := c.explicitImageWidth(attrs, width, li, false); ok && (attrs["pdfwidth"] != "" || attrs["scaledwidth"] != "") {
		w = ew
		h = li.h * w / li.w
	} else if spec := attrs["width"]; spec != "" {
		if strings.HasSuffix(spec, "%") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(spec, "%"), 64)
			w = width * n / 100
		} else {
			w = theme.StrToPt(spec)
			if !strings.ContainsAny(spec, "abcdefghijklmnopqrstuvwxyz") {
				w *= 0.75
			}
		}
		h = li.h * w / li.w
	} else if a := attrs["fit"]; a == "contain" || h > height || w > width {
		s := height / li.h
		if s2 := width / li.w; s2 < s {
			s = s2
		}
		w, h = li.w*s, li.h*s
	}
	if h > height {
		w, h = w*height/h, height
	}
	ix := x
	switch align {
	case "center":
		ix = x + (width-w)/2
	case "right":
		ix = x + width - w
	}
	iy := top - h
	switch valign {
	case "middle", "center":
		iy = top - (height+h)/2
	case "bottom":
		iy = top - height
	}
	g := pdf.NewG()
	li.drawTo(c, g, ix, iy, w, h)
	c.draw(g)
}
