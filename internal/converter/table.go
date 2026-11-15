package converter

import (
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
)

type tcell struct {
	src              *asciidoc.Cell
	row, col         int
	colspan, rowspan int
	frags            []frag
	font             fontState
	pad              [4]float64
	leading          float64
	align, valign    string
	bg               clr
	head             bool
	foot             bool
	inner            *asciidoc.Document
	contentH         float64
}

func expandRect(v any, def any) [4]any {
	switch x := v.(type) {
	case nil:
		return [4]any{def, def, def, def}
	case []any:
		get := func(i int) any {
			if i < len(x) && x[i] != nil {
				return x[i]
			}
			return def
		}
		switch len(x) {
		case 0:
			return [4]any{def, def, def, def}
		case 1:
			return [4]any{get(0), get(0), get(0), get(0)}
		case 2:
			return [4]any{get(0), get(1), get(0), get(1)}
		case 3:
			return [4]any{get(0), get(1), get(2), get(1)}
		}
		return [4]any{get(0), get(1), get(2), get(3)}
	}
	return [4]any{v, v, v, v}
}

func expandGrid(v any, def any) [2]any {
	switch x := v.(type) {
	case nil:
		return [2]any{def, def}
	case []any:
		get := func(i int) any {
			if i < len(x) && x[i] != nil {
				return x[i]
			}
			return def
		}
		if len(x) == 1 {
			return [2]any{get(0), get(0)}
		}
		return [2]any{get(0), get(1)}
	}
	return [2]any{v, v}
}

func (c *Converter) convertTable(node *Block) {
	t := c.theme
	table := node.Table
	if table == nil {
		return
	}
	captionEnd := t.StrOr("table_caption_end", "top")
	if !c.atPageTop() && node.Option("unbreakable") && c.scratch == 0 {
		// 分割禁止の表は 1 ページに収まるなら次ページへ送る
		_, fromY, pages, toY := c.dryRun(true, func() { c.inkTable(node, captionEnd) })
		if pages == 0 && fromY-toY > c.cursor() {
			c.advancePage()
		}
	}
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	c.inkTable(node, captionEnd)
	c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
}

func (c *Converter) inkTable(node *Block, captionEnd string) {
	t := c.theme
	table := node.Table
	numCols := len(table.Columns)
	if numCols == 0 {
		return
	}
	page := c.pageBgColor
	tblBg := c.resolveThemeColor("table_background_color", clr{}, clr{})
	headBg := c.resolveThemeColor("table_head_background_color", tblBg, clr{})
	footBg := c.resolveThemeColor("table_foot_background_color", tblBg, clr{})
	bodyBg := c.resolveThemeColor("table_body_background_color", tblBg, clr{})
	stripeBg := c.resolveThemeColor("table_body_stripe_background_color", tblBg, clr{})
	_ = page
	bodyPad := c.themePadding("table_cell_padding")
	var rows [][]*tcell
	numHead := len(table.Head)
	place := func(src []*asciidoc.Cell, r int, occupied map[[2]int]bool) []*tcell {
		var out []*tcell
		col := 0
		for _, cell := range src {
			for occupied[[2]int{r, col}] {
				col++
			}
			tc := &tcell{src: cell, row: r, col: col, colspan: maxInt(cell.Colspan, 1), rowspan: maxInt(cell.Rowspan, 1)}
			if tc.col+tc.colspan > numCols {
				tc.colspan = maxInt(numCols-tc.col, 1)
			}
			for dr := 0; dr < tc.rowspan; dr++ {
				for dc := 0; dc < tc.colspan; dc++ {
					occupied[[2]int{r + dr, col + dc}] = true
				}
			}
			col += tc.colspan
			out = append(out, tc)
		}
		return out
	}
	occupied := map[[2]int]bool{}
	cellText := func(cell *asciidoc.Cell, transform string) []frag {
		text := strings.TrimSpace(cell.Text())
		if transform != "" {
			text = transformText(text, transform)
		}
		if c.cjkLineBreaks {
			text = insertCJKBreaks(text)
		}
		if strings.Contains(text, "\n\n") {
			var paras []string
			for _, p := range strings.Split(text, "\n\n") {
				if p = strings.TrimSpace(squeezeWhitespace(p)); p != "" {
					paras = append(paras, p)
				}
			}
			return c.parseText(strings.Join(paras, "\n\n"), false, nil)
		}
		return c.parseText(text, true, nil)
	}
	halign := func(cell *asciidoc.Cell) string {
		if cell.HAlign != "" {
			return cell.HAlign
		}
		return "left"
	}
	valign := func(cell *asciidoc.Cell) string {
		switch cell.VAlign {
		case "middle":
			return "center"
		case "bottom":
			return "bottom"
		}
		return "top"
	}
	c.themeFont("table", -1, func() {
		if numHead > 0 {
			c.themeFont("table_head", -1, func() {
				lh := c.baseLineHeight
				for _, k := range []string{"table_head_line_height", "table_cell_line_height"} {
					if v, ok := t.Num(k); ok {
						lh = v
						break
					}
				}
				lm := c.calcLineMetrics(lh)
				pad := bodyPad
				if t.Get("table_head_cell_padding") != nil {
					pad = c.themePadding("table_head_cell_padding")
				}
				pad[0] += lm.padTop
				pad[2] += lm.padBottom
				transform := t.Str("table_head_text_transform")
				if transform == "" {
					transform = c.textTransform
				}
				for r, row := range table.Head {
					cells := place(row, r, occupied)
					for _, tc := range cells {
						tc.head = true
						tc.frags = cellText(tc.src, transform)
						tc.font = c.saveFont()
						tc.pad, tc.leading = pad, lm.leading
						tc.align, tc.valign = halign(tc.src), valign(tc.src)
						tc.bg = headBg
					}
					rows = append(rows, cells)
				}
			})
		}
		bodyLH := t.Float("table_cell_line_height", c.baseLineHeight)
		bodyLM := c.calcLineMetrics(bodyLH)
		all := append(append([][]*asciidoc.Cell{}, table.Body...), table.Foot...)
		stripes := node.Attr("stripes")
		if stripes == "" {
			stripes = node.Doc.Attr("table-stripes")
		}
		for i, row := range all {
			r := numHead + i
			isFoot := i >= len(table.Body)
			rowBg := bodyBg
			switch stripes {
			case "all":
				rowBg = stripeBg
			case "even":
				if i%2 == 1 {
					rowBg = stripeBg
				}
			case "odd":
				if i%2 == 0 {
					rowBg = stripeBg
				}
			}
			if isFoot {
				rowBg = footBg
			}
			cells := place(row, r, occupied)
			for _, tc := range cells {
				cell := tc.src
				tc.foot = isFoot
				tc.align, tc.valign = halign(cell), valign(cell)
				tc.bg = rowBg
				saved := c.saveFont()
				lm := bodyLM
				transform := ""
				if isFoot {
					if v := t.Get("table_foot_font_color"); v != nil {
						c.fontColor = toClr(v)
					}
					if v := t.Get("table_foot_font_size"); v != nil {
						c.fontSize = c.resolveSize(v, c.fontSize)
					}
					if v := t.Str("table_foot_font_family"); v != "" {
						c.fontFamily = v
					}
					if v := t.Str("table_foot_font_style"); v != "" {
						c.setFontStyle(v)
					}
				}
				setup := func() {
					tc.font = c.saveFont()
					tc.pad = bodyPad
					tc.pad[0] += lm.padTop
					tc.pad[2] += lm.padBottom
					tc.leading = lm.leading
				}
				switch cell.Style {
				case "emphasis":
					c.fontBold, c.fontItalic = false, true
					setup()
				case "strong":
					c.fontBold, c.fontItalic = true, false
					setup()
				case "header":
					c.themeFontCascade([]fontCat{{"table_head", -1}, {"table_header_cell", -1}}, func() {
						transform = c.textTransform
						lm = c.calcLineMetrics(c.baseLineHeight)
						setup()
					})
					tc.bg = c.resolveThemeColor("table_header_cell_background_color", headBg, clr{})
				case "monospaced":
					c.fontBold, c.fontItalic = false, false
					lh := c.baseLineHeight
					c.themeFont("codespan", -1, func() {
						lm = c.calcLineMetrics(lh)
						setup()
					})
				case "literal":
					c.fontBold, c.fontItalic = false, false
					size := c.fontSize
					c.themeFont("code", -1, func() {
						c.fontSize *= size / c.rootFontSize
						lm = c.calcLineMetrics(c.baseLineHeight)
						setup()
					})
					tc.frags = c.parseText(guardIndentation(cell.Text()), false, nil)
				case "asciidoc":
					tc.inner = cell.Inner
					tc.font = c.saveFont()
					tc.pad = bodyPad
				default:
					setup()
				}
				if tc.frags == nil && tc.inner == nil {
					tc.frags = cellText(cell, transform)
				}
				if v, ok := node.Doc.Attributes["cellbgcolor"]; ok {
					if v == "transparent" {
						tc.bg = bodyBg
					} else if hex, ok := parseHexColor(v); ok {
						tc.bg = hexClr(hex)
					}
				}
				c.restoreFont(saved)
			}
			rows = append(rows, cells)
		}
	})
	if len(rows) == 0 {
		c.logf("WARN", "no rows found in table")
		return
	}
	numRows := len(rows)
	// 罫線の設定
	borderColor := expandRect(t.Get("table_border_color"), "transparent")
	borderStyle := expandRect(t.Get("table_border_style"), "solid")
	borderWidth := expandRect(t.Get("table_border_width"), 0.0)
	gridColorSrc := t.Get("table_grid_color")
	if gridColorSrc == nil {
		gridColorSrc = []any{borderColor[0], borderColor[3]}
	}
	gridStyleSrc := t.Get("table_grid_style")
	if gridStyleSrc == nil {
		gridStyleSrc = []any{borderStyle[0], borderStyle[3]}
	}
	gridWidthSrc := t.Get("table_grid_width")
	if gridWidthSrc == nil {
		gridWidthSrc = []any{borderWidth[0], borderWidth[3]}
	}
	gridColor := expandGrid(gridColorSrc, "transparent")
	gridStyle := expandGrid(gridStyleSrc, "solid")
	gridWidth := expandGrid(gridWidthSrc, 0.0)
	gw := [2]float64{toF(gridWidth[0]), toF(gridWidth[1])}
	bwid := [4]float64{toF(borderWidth[0]), toF(borderWidth[1]), toF(borderWidth[2]), toF(borderWidth[3])}
	headBottomColor := toClr(gridColor[0])
	if v := t.Get("table_head_border_bottom_color"); v != nil {
		headBottomColor = toClr(v)
	}
	headBottomStyle := t.StrOr("table_head_border_bottom_style", asStr(gridStyle[0]))
	headBottomWidth := t.Float("table_head_border_bottom_width", gw[0]*2.5)
	attrOr := func(name, docAttr, def string) string {
		if v, ok := node.Attrs[name]; ok {
			return v
		}
		if v, ok := node.Doc.Attributes[docAttr]; ok {
			return v
		}
		return def
	}
	grid := attrOr("grid", "table-grid", "all")
	switch grid {
	case "all":
	case "cols":
		gw[0] = 0
	case "rows":
		gw[1] = 0
	default:
		gw[0], gw[1] = 0, 0
	}
	frame := attrOr("frame", "table-frame", "all")
	switch frame {
	case "all":
	case "topbot", "ends":
		bwid[1], bwid[3] = 0, 0
	case "sides":
		bwid[0], bwid[2] = 0, 0
	default:
		bwid = [4]float64{}
	}
	alignment := t.StrOr("table_align", "left")
	if a := node.Attr("align"); a == "left" || a == "center" || a == "right" {
		alignment = a
	} else {
		for _, r := range node.Roles() {
			if r == "left" || r == "center" || r == "right" {
				alignment = r
			}
		}
	}
	// 列幅
	pcwidth := 100.0
	if v, ok := node.Attrs["tablepcwidth"]; ok {
		pcwidth = toF(v)
	}
	tableWidth := c.width * pcwidth / 100
	colWidths := make([]float64, numCols)
	measure := func(tc *tcell, width float64) float64 {
		if tc.inner != nil {
			return c.asciidocCellHeight(tc, width)
		}
		saved := c.saveFont()
		c.restoreFont(tc.font)
		h := c.heightOfFragments(tc.frags, width-tc.pad[1]-tc.pad[3], textOpts{align: tc.align, leading: tc.leading})
		c.restoreFont(saved)
		return h
	}
	if node.Option("autowidth") {
		natural := make([]float64, numCols)
		minimum := make([]float64, numCols)
		for _, row := range rows {
			for _, tc := range row {
				if tc.colspan != 1 {
					continue
				}
				var w, mw float64
				if tc.inner != nil {
					w, mw = c.width, 30
				} else {
					saved := c.saveFont()
					c.restoreFont(tc.font)
					w = c.widthOfFragments(tc.frags, textOpts{}) + tc.pad[1] + tc.pad[3]
					c.scratch++
					for _, tok := range c.tokenize(tc.frags, textOpts{}) {
						if tok.width > mw {
							mw = tok.width
						}
					}
					c.scratch--
					mw += tc.pad[1] + tc.pad[3]
					c.restoreFont(saved)
				}
				if w > natural[tc.col] {
					natural[tc.col] = w
				}
				if mw > minimum[tc.col] {
					minimum[tc.col] = mw
				}
			}
		}
		total, minTotal := 0.0, 0.0
		for i := range natural {
			if natural[i] == 0 {
				natural[i] = bodyPad[1] + bodyPad[3]
			}
			total += natural[i]
			minTotal += minimum[i]
		}
		avail := c.width
		target := total
		if _, ok := node.Attrs["width"]; ok {
			target = tableWidth
		} else if node.HasRole("stretch") {
			target = c.width
		}
		if target > avail {
			target = avail
		}
		switch {
		case total <= target && total > 0:
			for i := range colWidths {
				colWidths[i] = natural[i] * target / total
			}
		case minTotal < target && total > minTotal:
			f := (target - minTotal) / (total - minTotal)
			for i := range colWidths {
				colWidths[i] = minimum[i] + (natural[i]-minimum[i])*f
			}
		default:
			for i := range colWidths {
				colWidths[i] = target / float64(numCols)
			}
		}
		tableWidth = 0
		for _, w := range colWidths {
			tableWidth += w
		}
	} else {
		for i, col := range table.Columns {
			colWidths[i] = col.PcWidth * tableWidth / 100
		}
	}
	colX := make([]float64, numCols+1)
	for i, w := range colWidths {
		colX[i+1] = colX[i] + w
	}
	// 行の高さ
	rowH := make([]float64, numRows)
	for _, row := range rows {
		for _, tc := range row {
			w := colX[minInt(tc.col+tc.colspan, numCols)] - colX[tc.col]
			tc.contentH = measure(tc, w)
			if tc.rowspan == 1 {
				if h := tc.contentH + tc.pad[0] + tc.pad[2]; h > rowH[tc.row] {
					rowH[tc.row] = h
				}
			}
		}
	}
	for _, row := range rows {
		for _, tc := range row {
			if tc.rowspan > 1 {
				last := minInt(tc.row+tc.rowspan, numRows) - 1
				sum := 0.0
				for r := tc.row; r <= last; r++ {
					sum += rowH[r]
				}
				if need := tc.contentH + tc.pad[0] + tc.pad[2]; need > sum {
					rowH[last] += need - sum
				}
			}
		}
	}
	title := ""
	if node.HasTitle() {
		title = node.CaptionedTitle()
	}
	var maxWidth any = "fit-content"
	if v := t.Get("table_caption_max_width"); v != nil {
		maxWidth = v
	}
	if captionEnd == "top" && title != "" {
		c.inkCaption(title, captionOpts{category: "table", blockAlign: alignment, blockWidth: tableWidth, maxWidth: maxWidth})
	}
	offset := 0.0
	if tableWidth < c.width {
		switch alignment {
		case "center":
			offset = (c.width - tableWidth) / 2
		case "right":
			offset = c.width - tableWidth
		}
	}
	left := c.left + offset
	drawRow := func(r int, firstOnPage bool, lastRow bool) {
		top := c.y
		bg := pdf.NewG()
		for _, tc := range rows[r] {
			x := left + colX[tc.col]
			w := colX[minInt(tc.col+tc.colspan, numCols)] - colX[tc.col]
			h := 0.0
			for rr := tc.row; rr < tc.row+tc.rowspan && rr < numRows; rr++ {
				h += rowH[rr]
			}
			if tc.bg.set {
				bg.Save().FillColor(tc.bg.pdf()).Rect(x, top-h, w, h).Fill().Restore()
			}
		}
		c.draw(bg)
		for _, tc := range rows[r] {
			x := left + colX[tc.col]
			w := colX[minInt(tc.col+tc.colspan, numCols)] - colX[tc.col]
			h := 0.0
			for rr := tc.row; rr < tc.row+tc.rowspan && rr < numRows; rr++ {
				h += rowH[rr]
			}
			// prawn-table はセルの内容領域を幅・高さとも 1pt (FPTolerance) 広く取る
			const fpTolerance = 1.0
			innerH := h - tc.pad[0] - tc.pad[2] + fpTolerance
			voff := 0.0
			switch tc.valign {
			case "center":
				voff = (innerH - tc.contentH) / 2
			case "bottom":
				voff = innerH - tc.contentH
			}
			if voff < 0 {
				voff = 0
			}
			saved := c.saveFont()
			c.restoreFont(tc.font)
			if tc.inner != nil {
				c.drawAsciidocCell(tc, x, top-voff, w)
			} else {
				c.drawTextBox(tc.frags, x+tc.pad[3], top-tc.pad[0]-voff, w-tc.pad[1]-tc.pad[3]+fpTolerance, 0, "top", textOpts{align: tc.align, leading: tc.leading})
			}
			c.restoreFont(saved)
			// 罫線
			g := pdf.NewG()
			line := func(x1, y1, x2, y2, lw float64, col clr, style string) {
				if lw <= 0 || !col.set {
					return
				}
				g.Save().StrokeColor(col.pdf()).LineWidth(lw)
				applyLineStyle(g, style, lw)
				g.MoveTo(x1, y1).LineTo(x2, y2).Stroke().Restore()
			}
			tw, tcol, tstyle := gw[0], toClr(gridColor[0]), asStr(gridStyle[0])
			bw2, bcol, bstyle := gw[0], toClr(gridColor[0]), asStr(gridStyle[0])
			lw, lcol, lstyle := gw[1], toClr(gridColor[1]), asStr(gridStyle[1])
			rw, rcol, rstyle := gw[1], toClr(gridColor[1]), asStr(gridStyle[1])
			lastSpanRow := tc.row + tc.rowspan - 1
			if numHead > 0 {
				if lastSpanRow == numHead-1 {
					bw2, bcol, bstyle = headBottomWidth, headBottomColor, headBottomStyle
				}
				if tc.row == numHead && numRows > numHead && !(grid == "none" && frame == "none") {
					tw, tcol, tstyle = headBottomWidth, headBottomColor, headBottomStyle
				}
			}
			if !(grid == "none" && frame == "none") {
				if tc.row == 0 || firstOnPage && tc.row >= numHead && numHead == 0 {
					tw, tcol, tstyle = bwid[0], toClr(borderColor[0]), asStr(borderStyle[0])
				}
				if tc.col+tc.colspan >= numCols {
					rw, rcol, rstyle = bwid[1], toClr(borderColor[1]), asStr(borderStyle[1])
				}
				if lastSpanRow >= numRows-1 {
					bw2, bcol, bstyle = bwid[2], toClr(borderColor[2]), asStr(borderStyle[2])
				}
				if tc.col == 0 {
					lw, lcol, lstyle = bwid[3], toClr(borderColor[3]), asStr(borderStyle[3])
				}
			}
			line(x-lw/2, top, x+w+rw/2, top, tw, tcol, tstyle)
			line(x-lw/2, top-h, x+w+rw/2, top-h, bw2, bcol, bstyle)
			line(x, top+tw/2, x, top-h-bw2/2, lw, lcol, lstyle)
			line(x+w, top+tw/2, x+w, top-h-bw2/2, rw, rcol, rstyle)
			c.draw(g)
		}
		c.y = top - rowH[r]
		_ = lastRow
	}
	headH := 0.0
	for r := 0; r < numHead; r++ {
		headH += rowH[r]
	}
	firstOnPage := true
	for r := 0; r < numRows; r++ {
		// rowspan でつながった行はまとめて同じページに置く
		need := rowH[r]
		for _, tc := range rows[r] {
			if tc.rowspan > 1 {
				sum := 0.0
				for rr := tc.row; rr < tc.row+tc.rowspan && rr < numRows; rr++ {
					sum += rowH[rr]
				}
				if sum > need {
					need = sum
				}
			}
		}
		if r == numHead-1 && numRows > numHead {
			// ヘッダーだけがページ末に取り残されないようにする
			need += rowH[numHead]
		}
		if c.cursor() < need-0.0001 && !c.atPageTop() {
			c.advancePage()
			firstOnPage = true
			if r >= numHead && numHead > 0 && !node.Option("noheader") {
				for hr := 0; hr < numHead; hr++ {
					drawRow(hr, hr == 0, false)
				}
				firstOnPage = false
			}
		}
		drawRow(r, firstOnPage, r == numRows-1)
		firstOnPage = false
	}
	if captionEnd == "bottom" && title != "" {
		c.inkCaption(title, captionOpts{category: "table", bottom: true, blockAlign: alignment, blockWidth: tableWidth, maxWidth: maxWidth})
	}
}

func asStr(v any) string {
	s, _ := v.(string)
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// asciidocCellHeight は AsciiDoc 形式のセルの内容の高さを測ります。
func (c *Converter) asciidocCellHeight(tc *tcell, width float64) float64 {
	_, fromY, pages, toY := c.dryRun(true, func() {
		c.left, c.width = c.left+tc.pad[3], width-tc.pad[1]-tc.pad[3]
		c.traverse(tc.inner.Block)
	})
	if pages > 0 {
		return fromY - c.bottom
	}
	return fromY - toY
}

func (c *Converter) drawAsciidocCell(tc *tcell, x, top, width float64) {
	savedLeft, savedWidth, savedY, savedPage := c.left, c.width, c.y, c.page
	savedGutters := c.bottomGutters
	c.bottomGutters = []map[*Block]float64{{}}
	c.left, c.width = x+tc.pad[3], width-tc.pad[1]-tc.pad[3]
	c.y = top - tc.pad[0]
	c.traverse(tc.inner.Block)
	c.bottomGutters = savedGutters
	if c.page != savedPage {
		c.goToPage(savedPage)
	}
	c.left, c.width, c.y = savedLeft, savedWidth, savedY
}
