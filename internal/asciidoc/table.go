package asciidoc

import (
	"math"
	"strconv"
	"strings"
)

// Table は表の構造です。
type Table struct {
	Columns []*Column
	Head    [][]*Cell
	Body    [][]*Cell
	Foot    [][]*Cell
}

// Column は表の列です。
type Column struct {
	Index int
	// Width は cols 指定の比率 (自動幅は -1)
	Width     int
	PcWidth   float64
	AutoWidth bool
	Style     string
	HAlign    string
	VAlign    string
}

// Cell は表のセルです。
type Cell struct {
	Column  *Column
	RawText string
	Colspan int
	Rowspan int
	// Style は "" "strong" "emphasis" "monospaced" "header" "literal" "asciidoc"
	Style  string
	HAlign string
	VAlign string
	// Inner は AsciiDoc 形式セルの入れ子文書
	Inner *Document
	Table *Block

	spec      map[string]string
	hasSpec   bool
	file      string
	lineno    int
	textCache *string
}

// Text は置換適用済みのセルテキストを返します。
func (c *Cell) Text() string {
	if c.textCache != nil {
		return *c.textCache
	}
	subs := NormalSubs
	if c.Style == "literal" {
		subs = BasicSubs
	}
	t := c.Table.Doc.ApplySubs(c.RawText, subs, c.Table)
	c.textCache = &t
	return t
}

var blankLineRx = rx(`\n{2,}`)

// Paragraphs はセルの内容を段落ごとに分けて返します (asciidoc スタイル以外)。
func (c *Cell) Paragraphs() []string {
	text := c.Text()
	if text == "" {
		return nil
	}
	if !strings.Contains(c.RawText, "\n\n") {
		return []string{text}
	}
	var paras []string
	rest := text
	for {
		m := blankLineRx.Match(rest)
		if m == nil {
			paras = append(paras, rest)
			break
		}
		paras = append(paras, m.Pre())
		rest = m.Post()
	}
	return paras
}

var tableCellHorzAlignments = map[string]string{"<": "left", ">": "right", "^": "center"}
var tableCellVertAlignments = map[string]string{"<": "top", ">": "bottom", "^": "middle"}
var tableCellStyles = map[string]string{"d": "none", "s": "strong", "e": "emphasis", "m": "monospaced", "h": "header", "l": "literal", "a": "asciidoc"}

func truncate(v float64, precision int) float64 {
	f := math.Pow(10, float64(precision))
	return math.Trunc(v*f) / f
}

func parseColspecs(records string) []*Column {
	records = strings.ReplaceAll(records, " ", "")
	if n, err := strconv.Atoi(records); err == nil && strconv.Itoa(n) == records {
		cols := make([]*Column, 0, n)
		for i := 0; i < n; i++ {
			cols = append(cols, &Column{Width: 1})
		}
		return cols
	}
	sep := ";"
	if strings.Contains(records, ",") {
		sep = ","
	}
	var cols []*Column
	for _, record := range strings.Split(records, sep) {
		if record == "" {
			cols = append(cols, &Column{Width: 1})
			continue
		}
		m := columnSpecRx.Match(record)
		if m == nil {
			continue
		}
		col := Column{Width: 1}
		if m.Has(2) {
			h, v, _ := strings.Cut(m.S(2), ".")
			col.HAlign = tableCellHorzAlignments[h]
			col.VAlign = tableCellVertAlignments[v]
		}
		if m.Has(3) {
			if m.S(3) == "~" {
				col.Width = -1
			} else {
				col.Width, _ = strconv.Atoi(strings.TrimSuffix(m.S(3), "%"))
			}
		}
		if m.Has(4) {
			if s, ok := tableCellStyles[m.S(4)]; ok {
				col.Style = s
			}
		}
		repeat := 1
		if m.Has(1) {
			repeat, _ = strconv.Atoi(m.S(1))
		}
		for i := 0; i < repeat; i++ {
			c := col
			cols = append(cols, &c)
		}
	}
	return cols
}

func assignColumnWidths(cols []*Column, explicit bool) {
	if len(cols) == 0 {
		return
	}
	const precision = 4
	total, last := 0.0, 0.0
	if explicit {
		widthBase := 0
		var auto []*Column
		for _, c := range cols {
			if c.Width < 0 {
				auto = append(auto, c)
			} else {
				widthBase += c.Width
			}
		}
		base := float64(widthBase)
		autoWidth := 0.0
		if len(auto) > 0 {
			if widthBase <= 100 {
				autoWidth = truncate((100.0-base)/float64(len(auto)), precision)
				base = 100
			}
			for _, c := range auto {
				c.AutoWidth = true
			}
		}
		if base <= 0 {
			explicit = false
		} else {
			for _, c := range cols {
				w := float64(c.Width)
				if c.AutoWidth {
					w = autoWidth
				}
				c.PcWidth = truncate(w*100.0/base, precision)
				last = c.PcWidth
				total += c.PcWidth
			}
		}
	}
	if !explicit {
		pc := truncate(100.0/float64(len(cols)), precision)
		for _, c := range cols {
			c.PcWidth = pc
			total += pc
		}
		last = pc
	}
	if total != 100 {
		f := math.Pow(10, precision)
		cols[len(cols)-1].PcWidth = math.Round((100-total+last)*f) / f
	}
}

type tableCtx struct {
	p             *parser
	reader        *Reader
	block         *Block
	table         *Table
	format        string
	delimiter     string
	colcount      int
	buffer        string
	cellspecs     []map[string]string
	cellOpen      bool
	activeRowspan []int
	columnVisits  int
	currentRow    []*Cell
	linenum       int
	cellFile      string
	cellLine      int
}

func (p *parser) parseTable(reader *Reader, parent *Block, attrs map[string]string) *Block {
	doc := p.doc
	block := NewBlock(parent, "table")
	block.ContentModel = "empty"
	table := &Table{}
	block.Table = table
	// 幅の解決
	pcwidth := 100
	if v, ok := attrs["width"]; ok {
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), "%"))
		if n > 100 || n < 1 {
			if !(n == 0 && (v == "0" || v == "0%")) {
				n = 100
			}
		}
		pcwidth = n
	}
	attrs["tablepcwidth"] = strconv.Itoa(pcwidth)
	if _, ok := attrs["rotate-option"]; ok {
		attrs["orientation"] = "landscape"
	}
	explicitColspecs := false
	if v, ok := attrs["cols"]; ok {
		table.Columns = parseColspecs(v)
		for i, c := range table.Columns {
			c.Index = i
		}
		if len(table.Columns) > 0 {
			attrs["colcount"] = strconv.Itoa(len(table.Columns))
			assignColumnWidths(table.Columns, true)
		}
		explicitColspecs = true
	}
	skipped := reader.skipBlankLines()
	if skipped < 0 {
		skipped = 0
	}
	hasHeader, implicitHeader := false, false
	if _, ok := attrs["header-option"]; ok {
		hasHeader = true
	} else if _, no := attrs["noheader-option"]; skipped == 0 && !no {
		implicitHeader = true
	}
	ctx := &tableCtx{p: p, reader: reader, block: block, table: table, colcount: -1, activeRowspan: []int{0}, linenum: -1}
	if len(table.Columns) > 0 {
		ctx.colcount = len(table.Columns)
	}
	xsv := "psv"
	ctx.format = "psv"
	if f, ok := attrs["format"]; ok {
		switch f {
		case "tsv":
			ctx.format, xsv = "csv", "tsv"
		case "csv", "dsv":
			ctx.format, xsv = f, f
		case "psv":
		default:
			doc.Error("illegal table format: " + f)
		}
	}
	if xsv == "psv" && doc.IsNested() {
		xsv = "!sv"
	}
	delimiters := map[string]string{"psv": "|", "csv": ",", "dsv": ":", "tsv": "\t", "!sv": "!"}
	ctx.delimiter = delimiters[xsv]
	if sep, ok := attrs["separator"]; ok && sep != "" {
		if sep == `\t` {
			ctx.delimiter = "\t"
		} else {
			ctx.delimiter = sep
		}
	}
	loopIdx := -1
	implicitHeaderBoundary := -1
	ctx.cellFile, ctx.cellLine = reader.File(), reader.Lineno()
	for {
		line, ok := reader.read()
		if !ok {
			break
		}
		loopIdx++
		beyondFirst := loopIdx > 0
		lineNil := false
		if beyondFirst && line == "" {
			lineNil = true
			if implicitHeaderBoundary >= 0 {
				implicitHeaderBoundary++
			}
		} else if ctx.format == "psv" {
			if strings.HasPrefix(line, ctx.delimiter) {
				line = line[len(ctx.delimiter):]
				ctx.closeOpenCell(map[string]string{})
				implicitHeaderBoundary = -1
			} else {
				spec, rest, found := parseCellspecStart(line, ctx.delimiter)
				if found {
					line = rest
					ctx.closeOpenCell(spec)
					implicitHeaderBoundary = -1
				} else if implicitHeaderBoundary >= 0 && implicitHeaderBoundary == loopIdx {
					implicitHeader = false
					implicitHeaderBoundary = -1
				}
			}
		}
		if !beyondFirst {
			if implicitHeader {
				if next, ok := reader.peek(); ok && next == "" {
					implicitHeaderBoundary = 1
				} else {
					implicitHeader = false
				}
			}
		}
	inner:
		for {
			idx := -1
			if !lineNil {
				idx = strings.Index(line, ctx.delimiter)
			}
			if idx < 0 {
				// 区切り文字がもう無いので、行全体をバッファに取り込む
				if !lineNil {
					ctx.buffer += line
				}
				ctx.buffer += "\n"
				switch ctx.format {
				case "csv":
					if ctx.bufferHasUnclosedQuotes("") {
						if implicitHeaderBoundary >= 0 && loopIdx == 0 {
							implicitHeader = false
							implicitHeaderBoundary = -1
						}
						ctx.cellOpen = true
					} else {
						ctx.closeCell(true)
					}
				case "dsv":
					ctx.closeCell(true)
				default:
					ctx.cellOpen = true
				}
				break inner
			}
			pre, post := line[:idx], line[idx+len(ctx.delimiter):]
			switch ctx.format {
			case "csv":
				if ctx.bufferHasUnclosedQuotes(pre) {
					ctx.buffer += pre + ctx.delimiter
					if post == "" {
						break inner
					}
					line = post
					continue inner
				}
				ctx.buffer += pre
			default:
				if strings.HasSuffix(pre, "\\") {
					ctx.buffer += pre[:len(pre)-1] + ctx.delimiter
					if post == "" {
						ctx.buffer += "\n"
						ctx.cellOpen = true
						break inner
					}
					line = post
					continue inner
				}
				if ctx.format == "psv" {
					spec, cellText := parseCellspecEnd(pre)
					ctx.cellspecs = append(ctx.cellspecs, spec)
					ctx.buffer += cellText
				} else {
					ctx.buffer += pre
				}
			}
			line = post
			if post == "" {
				lineNil = true
			}
			ctx.closeCell(false)
		}
		if ctx.cellOpen {
			if !reader.hasMore() {
				ctx.closeCell(true)
			}
		} else if reader.skipBlankLines() < 0 {
			break
		}
	}
	if _, ok := attrs["colcount"]; !ok {
		attrs["colcount"] = strconv.Itoa(len(table.Columns))
	}
	if len(table.Columns) > 0 && !explicitColspecs {
		assignColumnWidths(table.Columns, false)
	}
	if implicitHeader {
		hasHeader = true
		attrs["header-option"] = ""
	}
	// ヘッダー・フッターの分割
	body := table.Body
	attrs["rowcount"] = strconv.Itoa(len(body))
	if len(body) > 0 && hasHeader {
		table.Head = [][]*Cell{body[0]}
		body = body[1:]
	}
	if _, ok := attrs["footer-option"]; ok && len(body) > 0 {
		table.Foot = [][]*Cell{body[len(body)-1]}
		body = body[:len(body)-1]
	}
	table.Body = body
	for _, row := range table.Head {
		for _, c := range row {
			ctx.finalizeCell(c, true)
		}
	}
	for _, rows := range [][][]*Cell{table.Body, table.Foot} {
		for _, row := range rows {
			for _, c := range row {
				ctx.finalizeCell(c, false)
			}
		}
	}
	return block
}

// parseCellspecStart は行頭のセル指定 (例: "2+^.>s|") を解析します。
func parseCellspecStart(line, delimiter string) (map[string]string, string, bool) {
	idx := strings.Index(line, delimiter)
	if idx < 0 {
		return nil, line, false
	}
	specPart, rest := line[:idx], line[idx+len(delimiter):]
	m := cellSpecStartRx.Match(specPart)
	if m == nil {
		return nil, line, false
	}
	if m.All() == "" {
		return map[string]string{}, rest, true
	}
	return buildCellspec(m), rest, true
}

// parseCellspecEnd は区切り文字の直前にあるセル指定を解析します。
func parseCellspecEnd(line string) (map[string]string, string) {
	m := cellSpecEndRx.Match(line)
	if m == nil {
		return map[string]string{}, line
	}
	if strings.TrimLeft(m.All(), " \t") == "" {
		return map[string]string{}, strings.TrimRight(line, " \t")
	}
	return buildCellspec(m), m.Pre()
}

func buildCellspec(m *M) map[string]string {
	spec := map[string]string{}
	if m.Has(1) {
		colspec, rowspec, _ := strings.Cut(m.S(1), ".")
		c, r := 1, 1
		if colspec != "" {
			c, _ = strconv.Atoi(colspec)
		}
		if rowspec != "" {
			r, _ = strconv.Atoi(rowspec)
		}
		switch m.S(2) {
		case "+":
			if c != 1 {
				spec["colspan"] = strconv.Itoa(c)
			}
			if r != 1 {
				spec["rowspan"] = strconv.Itoa(r)
			}
		case "*":
			if c != 1 {
				spec["repeatcol"] = strconv.Itoa(c)
			}
		}
	}
	if m.Has(3) {
		colspec, rowspec, _ := strings.Cut(m.S(3), ".")
		if v, ok := tableCellHorzAlignments[colspec]; ok {
			spec["halign"] = v
		}
		if v, ok := tableCellVertAlignments[rowspec]; ok {
			spec["valign"] = v
		}
	}
	if m.Has(4) {
		if v, ok := tableCellStyles[m.S(4)]; ok {
			spec["style"] = v
		}
	}
	return spec
}

func (c *tableCtx) bufferHasUnclosedQuotes(appendStr string) bool {
	record := strings.TrimSpace(c.buffer + appendStr)
	const q = `"`
	if record == q {
		return true
	}
	if strings.HasPrefix(record, q) {
		qq := q + q
		trailing := strings.HasSuffix(record, q)
		if (trailing && strings.HasSuffix(record, qq)) || strings.HasPrefix(record, qq) {
			record = strings.ReplaceAll(record, qq, "")
			return strings.HasPrefix(record, q) && !strings.HasSuffix(record, q)
		}
		return !trailing
	}
	return false
}

func (c *tableCtx) closeOpenCell(next map[string]string) {
	c.cellspecs = append(c.cellspecs, next)
	if c.cellOpen {
		c.closeCell(true)
	}
	c.linenum++
}

func (c *tableCtx) closeCell(eol bool) {
	doc := c.p.doc
	var cellText string
	var spec map[string]string
	hasSpec := false
	repeat := 1
	if c.format == "psv" {
		cellText = c.buffer
		c.buffer = ""
		if len(c.cellspecs) > 0 {
			spec = c.cellspecs[0]
			c.cellspecs = c.cellspecs[1:]
			if v, ok := spec["repeatcol"]; ok {
				repeat, _ = strconv.Atoi(v)
				delete(spec, "repeatcol")
			}
		} else {
			doc.Error(c.reader.prevLineInfo() + ": table missing leading separator; recovering automatically")
			spec = map[string]string{}
		}
		hasSpec = true
	} else {
		cellText = strings.TrimSpace(c.buffer)
		c.buffer = ""
		if c.format == "csv" && cellText != "" && strings.Contains(cellText, `"`) {
			squeeze := func(s string) string {
				for strings.Contains(s, `""`) {
					s = strings.ReplaceAll(s, `""`, `"`)
				}
				return s
			}
			if strings.HasPrefix(cellText, `"`) && strings.HasSuffix(cellText, `"`) && len(cellText) >= 2 {
				cellText = squeeze(strings.TrimSpace(cellText[1 : len(cellText)-1]))
			} else {
				cellText = squeeze(cellText)
			}
		}
	}
	for i := 1; i <= repeat; i++ {
		var column *Column
		if c.colcount == -1 {
			column = &Column{Index: len(c.table.Columns), Width: 1}
			c.table.Columns = append(c.table.Columns, column)
			if v, ok := spec["colspan"]; ok {
				extra, _ := strconv.Atoi(v)
				for j := 1; j < extra; j++ {
					c.table.Columns = append(c.table.Columns, &Column{Index: len(c.table.Columns), Width: 1})
				}
			}
		} else {
			if len(c.currentRow) >= len(c.table.Columns) {
				// rowspan による占有分を考慮して列を決める
				doc.Error(c.reader.prevLineInfo() + ": dropping cell because it exceeds specified number of columns")
				c.cellOpen = false
				return
			}
			column = c.table.Columns[len(c.currentRow)]
		}
		cell := &Cell{Column: column, RawText: cellText, Colspan: 1, Rowspan: 1, Table: c.block, hasSpec: hasSpec, file: c.cellFile, lineno: c.cellLine}
		if hasSpec {
			cell.spec = map[string]string{}
			for k, v := range spec {
				cell.spec[k] = v
			}
			if v, ok := spec["colspan"]; ok {
				cell.Colspan, _ = strconv.Atoi(v)
			}
			if v, ok := spec["rowspan"]; ok {
				cell.Rowspan, _ = strconv.Atoi(v)
			}
		}
		c.cellFile, c.cellLine = c.reader.File(), c.reader.Lineno()
		if cell.Rowspan > 1 {
			for k := 1; k < cell.Rowspan; k++ {
				for len(c.activeRowspan) <= k {
					c.activeRowspan = append(c.activeRowspan, 0)
				}
				c.activeRowspan[k] += cell.Colspan
			}
		}
		c.columnVisits += cell.Colspan
		c.currentRow = append(c.currentRow, cell)
		endOfRow := c.colcount == -1 || c.columnVisits+c.activeRowspan[0] == c.colcount
		if endOfRow && (c.colcount != -1 || c.linenum > 0 || (eol && i == repeat)) {
			c.closeRow()
		}
	}
	c.cellOpen = false
}

func (c *tableCtx) closeRow() {
	c.table.Body = append(c.table.Body, c.currentRow)
	if c.colcount == -1 {
		c.colcount = c.columnVisits
	}
	c.columnVisits = 0
	c.currentRow = nil
	if len(c.activeRowspan) > 1 {
		c.activeRowspan = c.activeRowspan[1:]
	} else {
		c.activeRowspan = []int{0}
	}
}

// finalizeCell はヘッダー行かどうかが確定した後でセルのスタイルと内容を確定します。
func (c *tableCtx) finalizeCell(cell *Cell, inHeader bool) {
	col := cell.Column
	style := ""
	if !inHeader {
		style = col.Style
	}
	cell.HAlign, cell.VAlign = col.HAlign, col.VAlign
	text := cell.RawText
	if cell.hasSpec {
		if v, ok := cell.spec["halign"]; ok {
			cell.HAlign = v
		}
		if v, ok := cell.spec["valign"]; ok {
			cell.VAlign = v
		}
		if v, ok := cell.spec["style"]; ok && !inHeader {
			style = v
		}
		switch style {
		case "asciidoc":
			text = strings.TrimRight(text, " \t\n")
			if strings.HasPrefix(text, "\n") {
				for strings.HasPrefix(text, "\n") {
					text = text[1:]
					cell.lineno++
				}
			} else {
				text = strings.TrimLeft(text, " \t")
			}
		case "literal":
			text = strings.TrimRight(text, " \t\n")
			for strings.HasPrefix(text, "\n") {
				text = text[1:]
			}
		default:
			text = strings.TrimSpace(text)
		}
	}
	if style == "none" {
		style = ""
	}
	cell.Style = style
	cell.RawText = text
	doc := c.p.doc
	if style == "asciidoc" {
		lines := strings.Split(text, "\n")
		if text == "" {
			lines = nil
		}
		// 最初の行に前処理ディレクティブ (include など) があれば処理する
		if len(lines) > 0 && strings.Contains(lines[0], "::") {
			pre := newReader(doc, []string{lines[0]}, cell.file, c.reader.cur.dir)
			var processed []string
			for {
				l, ok := pre.read()
				if !ok {
					break
				}
				processed = append(processed, l)
			}
			lines = append(processed, lines[1:]...)
		}
		cell.Inner = parseNested(doc, lines, cell.file, cell.lineno)
	} else if style != "literal" && !inHeader {
		c.p.catalogInlineAnchors(text, c.block)
	}
}
