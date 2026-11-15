package asciidoc

import (
	"fmt"
	"strconv"
	"strings"
)

type blockOpts struct {
	parseMetadata bool
	// text が true のとき、リスト項目のテキストのみを探す (ブロック構文を解釈しない)
	text bool
}

type delimitedBlock struct {
	context string
	masq    map[string]bool
}

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

var delimitedBlocks = map[string]delimitedBlock{
	"--":   {"open", set("comment", "example", "literal", "listing", "pass", "quote", "sidebar", "source", "verse", "admonition", "abstract", "partintro")},
	"----": {"listing", set("literal", "source")},
	"....": {"literal", set("listing", "source")},
	"====": {"example", set("admonition")},
	"****": {"sidebar", set()},
	"____": {"quote", set("verse")},
	"++++": {"pass", set("stem", "latexmath", "asciimath")},
	"|===": {"table", set()},
	",===": {"table", set()},
	":===": {"table", set()},
	"!===": {"table", set()},
	"////": {"comment", set()},
	"```":  {"fenced_code", set()},
}

var delimitedBlockTails = map[string]byte{"----": '-', "....": '.', "====": '=', "****": '*', "____": '_', "++++": '+', "|===": '=', ",===": '=', ":===": '=', "!===": '=', "////": '/'}

var admonitionStyles = set("NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION")
var paragraphStyles = set("comment", "example", "literal", "listing", "normal", "open", "pass", "quote", "sidebar", "source", "verse", "abstract", "partintro")
var verbatimStyles = set("literal", "listing", "source", "verse")

// matchDelimitedBlock は行が区切りブロックの開始行かどうかを判定します。
func matchDelimitedBlock(line string) (delimitedBlock, string, bool) {
	n := len(line)
	if n < 2 {
		return delimitedBlock{}, "", false
	}
	switch line[:2] {
	case "--", "..", "==", "**", "__", "++", "|=", ",=", ":=", "!=", "//", "``":
	default:
		return delimitedBlock{}, "", false
	}
	var tip string
	if n == 2 {
		tip = line
	} else {
		if n < 5 {
			tip = line
		} else {
			tip = line[:4]
		}
		if strings.HasPrefix(tip, "`") {
			if len(tip) == 4 {
				if tip == "````" {
					return delimitedBlock{}, "", false
				}
				if tip[:3] != "```" {
					return delimitedBlock{}, "", false
				}
				tip = "```"
				line = tip
				n = 3
			} else if tip != "```" {
				return delimitedBlock{}, "", false
			}
		} else if len(tip) == 3 {
			return delimitedBlock{}, "", false
		}
	}
	db, ok := delimitedBlocks[tip]
	if !ok {
		return delimitedBlock{}, "", false
	}
	if n == len(tip) || uniform(line[1:], delimitedBlockTails[tip]) {
		return db, line, true
	}
	return delimitedBlock{}, "", false
}

func isDelimitedBlock(line string) bool {
	_, _, ok := matchDelimitedBlock(line)
	return ok
}

func startOfBlock(line string) bool {
	return (strings.HasPrefix(line, "[") && blockAttributeLineRx.Test(line)) || isDelimitedBlock(line)
}

func startOfBlockOrList(line string) bool {
	return isDelimitedBlock(line) || (strings.HasPrefix(line, "[") && blockAttributeLineRx.Test(line)) || anyListRx.Test(line)
}

func (p *parser) readParagraphLines(reader *Reader, breakAtList, skipLineComments bool) []string {
	o := readOpts{breakOnBlankLines: true, breakOnListContinuation: true, preserveLastLine: true, skipLineComments: skipLineComments}
	if breakAtList {
		o.breakIf = startOfBlockOrList
	} else {
		o.breakIf = startOfBlock
	}
	return reader.readLinesUntil(o)
}

// rekey は位置属性を名前付き属性に割り当てます。
func rekey(attrs map[string]string, names ...string) {
	for i, name := range names {
		if name == "" {
			continue
		}
		if v, ok := attrs[strconv.Itoa(i+1)]; ok {
			attrs[name] = v
		}
	}
}

func newLeaf(parent *Block, context, contentModel string, lines []string) *Block {
	b := NewBlock(parent, context)
	b.ContentModel = contentModel
	b.Lines = lines
	return b
}

// nextBlock は次のブロックを解析します。
func (p *parser) nextBlock(reader *Reader, parent *Block, blockMeta *meta, o blockOpts) *Block {
	doc := p.doc
	skipped := reader.skipBlankLines()
	if skipped < 0 {
		return nil
	}
	textOnly := o.text
	if textOnly && skipped > 0 {
		textOnly = false
	}
	if o.parseMetadata {
		for p.parseBlockMetadataLine(reader, blockMeta, textOnly) {
			reader.shift()
			if reader.skipBlankLines() < 0 {
				return nil
			}
		}
	}
	attrs := blockMeta.attrs
	file, lineno := reader.File(), reader.Lineno()
	thisLine, _ := reader.read()
	docAttrs := doc.Attributes
	style := attrs["1"]
	var block *Block
	blockContext, terminator := "", ""
	delimited := false
	inList := parent.Context == "list_item"

	if db, term, ok := matchDelimitedBlock(thisLine); ok {
		delimited = true
		blockContext = db.context
		terminator = term
		if style != "" {
			if style != blockContext {
				switch {
				case db.masq[style]:
					blockContext = style
				case db.masq["admonition"] && admonitionStyles[style]:
					blockContext = "admonition"
				default:
					style = blockContext
				}
			}
		} else {
			style = blockContext
			attrs["style"] = style
		}
	}

	if !delimited {
		block, blockContext, style = p.parseUndelimited(reader, parent, blockMeta, thisLine, style, textOnly, skipped, inList)
		if block == nil && blockContext == "" {
			return nil
		}
	}

	if block == nil {
		switch blockContext {
		case "listing", "source":
			language, hasLang := "", false
			if blockContext == "source" {
				hasLang = false
			} else if _, ok := attrs["1"]; !ok {
				if v, ok := attrs["2"]; ok {
					language, hasLang = v, true
				} else if v, ok := docAttrs["source-language"]; ok {
					language, hasLang = v, true
				}
			}
			if blockContext == "source" || hasLang {
				if hasLang {
					attrs["style"] = "source"
					attrs["language"] = language
					rekey(attrs, "", "", "linenums")
				} else {
					rekey(attrs, "", "language", "linenums")
					if _, ok := attrs["language"]; !ok {
						if v, ok := docAttrs["source-language"]; ok {
							attrs["language"] = v
						}
					}
				}
				if _, ok := attrs["linenums"]; !ok {
					_, a := attrs["linenums-option"]
					_, b := docAttrs["source-linenums-option"]
					if a || b {
						attrs["linenums"] = ""
					}
				}
				if _, ok := attrs["indent"]; !ok {
					if v, ok := docAttrs["source-indent"]; ok {
						attrs["indent"] = v
					}
				}
			}
			block = p.buildBlock("listing", "verbatim", terminator, delimited, parent, reader, attrs)
		case "fenced_code":
			attrs["style"] = "source"
			language := ""
			if len(thisLine) > 3 {
				language = thisLine[3:]
				if comma := strings.Index(language, ","); comma >= 0 {
					if comma > 0 {
						rest := language[comma:]
						language = strings.TrimSpace(language[:comma])
						if len(rest) > 1 {
							attrs["linenums"] = ""
						}
					} else {
						if len(thisLine) > 4 {
							attrs["linenums"] = ""
						}
						language = ""
					}
				} else {
					language = strings.TrimLeft(language, " \t")
				}
			}
			if language == "" {
				if v, ok := docAttrs["source-language"]; ok {
					attrs["language"] = v
				}
			} else {
				attrs["language"] = language
			}
			if _, ok := attrs["linenums"]; !ok {
				_, a := attrs["linenums-option"]
				_, b := docAttrs["source-linenums-option"]
				if a || b {
					attrs["linenums"] = ""
				}
			}
			if _, ok := attrs["indent"]; !ok {
				if v, ok := docAttrs["source-indent"]; ok {
					attrs["indent"] = v
				}
			}
			block = p.buildBlock("listing", "verbatim", "```", true, parent, reader, attrs)
		case "table":
			tableLines := reader.readLinesUntil(readOpts{terminator: terminator, skipLineComments: true, context: "table"})
			if !strings.HasPrefix(terminator, "|") && !strings.HasPrefix(terminator, "!") {
				if _, ok := attrs["format"]; !ok {
					if strings.HasPrefix(terminator, ",") {
						attrs["format"] = "csv"
					} else {
						attrs["format"] = "dsv"
					}
				}
			}
			sub := newReader(doc, tableLines, file, reader.cur.dir)
			sub.process = false
			sub.cur.lineno = lineno + 1
			block = p.parseTable(sub, parent, attrs)
		case "sidebar":
			block = p.buildBlock("sidebar", "compound", terminator, delimited, parent, reader, attrs)
		case "admonition":
			name := strings.ToLower(style)
			attrs["name"] = name
			if v, ok := attrs["caption"]; ok {
				attrs["textlabel"] = v
				delete(attrs, "caption")
			} else if v, ok := docAttrs[name+"-caption"]; ok {
				attrs["textlabel"] = v
			}
			block = p.buildBlock("admonition", "compound", terminator, delimited, parent, reader, attrs)
		case "open", "abstract", "partintro":
			block = p.buildBlock("open", "compound", terminator, delimited, parent, reader, attrs)
		case "literal":
			block = p.buildBlock("literal", "verbatim", terminator, delimited, parent, reader, attrs)
		case "example":
			if _, ok := attrs["collapsible-option"]; ok {
				attrs["caption"] = ""
			}
			block = p.buildBlock("example", "compound", terminator, delimited, parent, reader, attrs)
		case "quote", "verse":
			rekey(attrs, "", "attribution", "citetitle")
			model := "compound"
			if blockContext == "verse" {
				model = "verbatim"
			}
			block = p.buildBlock(blockContext, model, terminator, delimited, parent, reader, attrs)
		case "stem", "latexmath", "asciimath":
			if blockContext == "stem" {
				v := attrs["2"]
				if v == "" {
					v = docAttrs["stem"]
				}
				if v == "latexmath" || v == "latex" || v == "tex" {
					attrs["style"] = "latexmath"
				} else {
					attrs["style"] = "asciimath"
				}
			}
			block = p.buildBlock("stem", "raw", terminator, delimited, parent, reader, attrs)
		case "pass":
			block = p.buildBlock("pass", "raw", terminator, delimited, parent, reader, attrs)
		case "comment":
			p.buildBlock("comment", "skip", terminator, delimited, parent, reader, attrs)
			blockMeta.clear()
			return nil
		default:
			doc.Error(fmt.Sprintf("%s: unsupported block type %s", doc.location(file, lineno), blockContext))
			blockMeta.clear()
			return nil
		}
	}

	block.File, block.Lineno = file, lineno
	blockTitle, hasTitle := attrs["title"]
	if hasTitle && blockTitle != "" {
		block.SetTitle(blockTitle)
		delete(attrs, "title")
		if captionAttributeNames[block.captionContext()] != "" && block.Context != "image" {
			caption, hasCaption := attrs["caption"]
			delete(attrs, "caption")
			block.assignCaption(caption, hasCaption)
		}
	}
	if v, ok := attrs["style"]; ok {
		block.Style = v
	}
	if block.ID == "" {
		block.ID = attrs["id"]
	}
	if block.ID != "" {
		if block.RawTitle != "" && strings.Contains(block.RawTitle, "{") {
			block.Title()
		}
		if !doc.Register(block.ID, &Ref{Block: block}) {
			doc.Warn(fmt.Sprintf("%s: id assigned to block already in use: %s", doc.location(file, lineno), block.ID))
		}
	}
	if len(attrs) > 0 {
		p.updateAttributes(block, attrs)
	}
	block.AttrEntries = append(block.AttrEntries, blockMeta.entries...)
	blockMeta.entries = nil
	p.commitSubs(block)
	if hasSub(block.Subs, "callouts") {
		if !p.catalogCallouts(block.Source()) {
			block.Subs = removeSub(block.Subs, "callouts")
		}
	}
	return block
}

func removeSub(subs []string, name string) []string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		if s != name {
			out = append(out, s)
		}
	}
	return out
}

func (p *parser) commitSubs(b *Block) {
	var defaults []string
	switch b.ContentModel {
	case "simple":
		defaults = NormalSubs
	case "verbatim":
		if b.Context == "verse" {
			defaults = NormalSubs
		} else {
			defaults = VerbatimSubs
		}
	case "raw":
		if b.Context == "stem" {
			defaults = BasicSubs
		} else {
			defaults = NoSubs
		}
	default:
		return
	}
	if custom, ok := b.Attrs["subs"]; ok {
		kind := "block"
		b.Subs = ResolveSubs(custom, kind, defaults)
		if b.Subs == nil {
			b.Subs = []string{}
		}
	} else {
		b.Subs = append([]string{}, defaults...)
	}
}

func (p *parser) catalogCallouts(text string) bool {
	if !strings.Contains(text, "<") {
		return false
	}
	found := false
	autonum := 0
	for _, m := range calloutScanRx.Scan(text) {
		if !strings.HasPrefix(m.All(), "\\") {
			num := m.S(2)
			if num == "." {
				autonum++
				num = strconv.Itoa(autonum)
			}
			n, _ := strconv.Atoi(num)
			p.doc.callouts.register(n)
		}
		found = true
	}
	return found
}

// parseUndelimited は区切りブロック以外 (マクロ、リスト、段落など) を解析します。
// ブロックが確定した場合は block を、後続の buildBlock に委ねる場合は blockContext を返します。
func (p *parser) parseUndelimited(reader *Reader, parent *Block, blockMeta *meta, thisLine, style string, textOnly bool, skipped int, inList bool) (*Block, string, string) {
	doc := p.doc
	attrs := blockMeta.attrs
	docAttrs := doc.Attributes

	if style != "" && verbatimStyles[style] {
		reader.unshift(thisLine)
		return nil, style, style
	}
	indented := false
	var ch0 byte
	if thisLine != "" {
		ch0 = thisLine[0]
	}
	if textOnly {
		indented = ch0 == ' ' || ch0 == '\t'
	} else {
		switch {
		case ch0 == ' ':
			indented = true
			trimmed := strings.TrimLeft(thisLine, " ")
			if trimmed != "" && strings.ContainsRune("-*_", rune(trimmed[0])) && markdownThematicBreakRx.Test(thisLine) {
				b := NewBlock(parent, "thematic_break")
				b.ContentModel = "empty"
				return b, "thematic_break", style
			}
		case ch0 == '\t':
			indented = true
		default:
			if (ch0 == '\'' || ch0 == '<' || ch0 == '-' || ch0 == '*' || ch0 == '_') && extLayoutBreakRx.Test(thisLine) {
				ctx := "thematic_break"
				if ch0 == '<' {
					ctx = "page_break"
				}
				b := NewBlock(parent, ctx)
				b.ContentModel = "empty"
				return b, ctx, style
			}
			if strings.HasSuffix(thisLine, "]") && strings.Contains(thisLine, "::") {
				if ch0 == 'i' || strings.HasPrefix(thisLine, "video:") || strings.HasPrefix(thisLine, "audio:") {
					if m := blockMediaMacroRx.Match(thisLine); m != nil {
						ctx, target := m.S(1), m.S(2)
						b := NewBlock(parent, ctx)
						b.ContentModel = "empty"
						if m.Has(3) {
							var posattrs []string
							switch ctx {
							case "video":
								posattrs = []string{"poster", "width", "height"}
							case "image":
								posattrs = []string{"alt", "width", "height"}
							}
							attrlist := m.S(3)
							if strings.Contains(attrlist, "{") {
								attrlist = doc.SubAttributes(attrlist)
							}
							// 位置属性は属性リストの解析結果で上書きする
							for _, k := range []string{"1", "2", "3"} {
								delete(attrs, k)
							}
							doc.ParseAttributes(attrlist, posattrs, b, attrs)
						}
						delete(attrs, "style")
						if strings.Contains(target, "{") {
							expanded := doc.SubAttributes(target)
							if expanded == "" && doc.AttrOr("attribute-missing", "skip") == "drop-line" {
								blockMeta.clear()
								return nil, "", style
							}
							target = expanded
						}
						if ctx == "image" {
							if v, ok := docAttrs["imagesdir"]; ok {
								attrs["imagesdir"] = v
							}
							if _, ok := attrs["alt"]; !ok {
								if style != "" {
									attrs["alt"] = style
								} else {
									alt := strings.NewReplacer("_", " ", "-", " ").Replace(basenameNoExt(target))
									attrs["alt"] = alt
									attrs["default-alt"] = alt
								}
							}
							if sw, ok := attrs["scaledwidth"]; ok {
								if sw == "" {
									delete(attrs, "scaledwidth")
								} else if c := sw[len(sw)-1]; c >= '0' && c <= '9' {
									attrs["scaledwidth"] = sw + "%"
								}
							}
							if title, ok := attrs["title"]; ok && title != "" {
								b.SetTitle(title)
								delete(attrs, "title")
								caption, hasCaption := attrs["caption"]
								delete(attrs, "caption")
								b.assignCaption(caption, hasCaption)
							}
						}
						attrs["target"] = target
						return b, ctx, style
					}
				}
				if ch0 == 't' && strings.HasPrefix(thisLine, "toc:") {
					if m := blockTocMacroRx.Match(thisLine); m != nil {
						b := NewBlock(parent, "toc")
						b.ContentModel = "empty"
						if m.Has(1) {
							doc.ParseAttributes(m.S(1), nil, b, attrs)
						}
						return b, "toc", style
					}
				}
			}
		}
	}

	if !indented && ch0 == '<' {
		if m := calloutListRx.Match(thisLine); m != nil {
			reader.unshift(thisLine)
			b := p.parseCalloutList(reader, m, parent)
			attrs["style"] = "arabic"
			return b, "colist", style
		}
	}
	if unorderedListRx.Test(thisLine) {
		reader.unshift(thisLine)
		if style == "" && parent.Context == "section" && parent.Sectname == "bibliography" {
			style = "bibliography"
			attrs["style"] = style
		}
		b := p.parseList(reader, "ulist", parent, style)
		return b, "ulist", style
	}
	if orderedListRx.Test(thisLine) {
		reader.unshift(thisLine)
		b := p.parseList(reader, "olist", parent, style)
		if b.Style != "" {
			attrs["style"] = b.Style
		}
		return b, "olist", style
	}
	if strings.Contains(thisLine, "::") || strings.Contains(thisLine, ";;") {
		if m := descriptionListRx.Match(thisLine); m != nil {
			reader.unshift(thisLine)
			b := p.parseDescriptionList(reader, m, parent)
			return b, "dlist", style
		}
	}
	if style == "float" || style == "discrete" {
		isTitle := false
		if _, ok := atxSectionLevel(thisLine); ok && !indented {
			isTitle = true
		} else if next, ok := reader.peek(); ok {
			_, isTitle = setextSectionLevel(thisLine, next)
		}
		if isTitle {
			reader.unshift(thisLine)
			id, reftext, title, level, _ := p.parseSectionTitle(reader, attrs["id"])
			if reftext != "" {
				attrs["reftext"] = reftext
			}
			b := NewBlock(parent, "floating_title")
			b.ContentModel = "empty"
			b.SetTitle(title)
			delete(attrs, "title")
			b.ID = id
			if b.ID == "" && doc.HasAttr("sectids") {
				b.ID = generateSectionID(b.Title(), doc)
			}
			b.Level = level
			return b, "floating_title", style
		}
	}
	if style != "" && style != "normal" {
		switch {
		case paragraphStyles[style]:
			reader.unshift(thisLine)
			return nil, style, style
		case admonitionStyles[style]:
			reader.unshift(thisLine)
			return nil, "admonition", style
		default:
			style = ""
		}
	}

	reader.unshift(thisLine)
	var block *Block
	if indented && style == "" {
		lines := p.readParagraphLines(reader, inList && skipped == 0, textOnly)
		lines = adjustIndentation(lines, 0, 0)
		block = newLeaf(parent, "literal", "verbatim", lines)
		if inList {
			attrs["listparagraph-option"] = ""
		}
		return block, "literal", style
	}
	lines := p.readParagraphLines(reader, inList && skipped == 0, true)
	if len(lines) == 0 {
		// 行コメントのみだった場合など
		reader.advance()
		blockMeta.clear()
		return nil, "", style
	}
	switch {
	case textOnly:
		if indented && style == "normal" {
			lines = adjustIndentation(lines, 0, 0)
		}
		block = newLeaf(parent, "paragraph", "simple", lines)
	case (ch0 == 'N' || ch0 == 'T' || ch0 == 'I' || ch0 == 'W' || ch0 == 'C') && strings.Contains(thisLine, ":") && admonitionParagraphRx.Test(lines[0]):
		m := admonitionParagraphRx.Match(lines[0])
		lines[0] = m.Post()
		name := strings.ToLower(m.S(1))
		attrs["style"] = m.S(1)
		attrs["name"] = name
		if v, ok := attrs["caption"]; ok {
			attrs["textlabel"] = v
			delete(attrs, "caption")
		} else if v, ok := docAttrs[name+"-caption"]; ok {
			attrs["textlabel"] = v
		}
		block = newLeaf(parent, "admonition", "simple", lines)
	case ch0 == '>' && strings.HasPrefix(thisLine, "> "):
		for i, line := range lines {
			if line == ">" {
				lines[i] = ""
			} else if strings.HasPrefix(line, "> ") {
				lines[i] = line[2:]
			}
		}
		credit, hasCredit := "", false
		if last := lines[len(lines)-1]; strings.HasPrefix(last, "-- ") {
			credit, hasCredit = last[3:], true
			lines = lines[:len(lines)-1]
			for len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
		}
		attrs["style"] = "quote"
		sub := newReader(doc, lines, reader.File(), reader.cur.dir)
		sub.process = false
		block = NewBlock(parent, "quote")
		block.ContentModel = "compound"
		p.parseBlocks(sub, block)
		if hasCredit {
			attribution, citetitle, found := strings.Cut(doc.ApplySubs(credit, NormalSubs, block), ", ")
			if attribution != "" {
				attrs["attribution"] = attribution
			}
			if found {
				attrs["citetitle"] = citetitle
			}
		}
	case ch0 == '"' && len(lines) > 1 && strings.HasPrefix(lines[len(lines)-1], "-- ") && strings.HasSuffix(lines[len(lines)-2], "\""):
		lines[0] = lines[0][1:]
		credit := lines[len(lines)-1][3:]
		lines = lines[:len(lines)-1]
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		last := lines[len(lines)-1]
		lines[len(lines)-1] = last[:len(last)-1]
		attrs["style"] = "quote"
		block = newLeaf(parent, "quote", "simple", lines)
		attribution, citetitle, found := strings.Cut(doc.ApplySubs(credit, NormalSubs, block), ", ")
		if attribution != "" {
			attrs["attribution"] = attribution
		}
		if found {
			attrs["citetitle"] = citetitle
		}
	default:
		if indented && style == "normal" {
			lines = adjustIndentation(lines, 0, 0)
		}
		block = newLeaf(parent, "paragraph", "simple", lines)
	}
	p.catalogInlineAnchors(strings.Join(lines, "\n"), block)
	return block, block.Context, style
}

var markdownThematicBreakRx = rx(`^ {0,3}([-*_])( *)\1\2\1$`)

func (p *parser) catalogInlineAnchors(text string, block *Block) {
	if !strings.Contains(text, "[[") && !strings.Contains(text, "or:") {
		return
	}
	for _, m := range inlineAnchorScanRx.Scan(text) {
		id, reftext := m.S(1), m.S(2)
		if !m.Has(1) {
			id, reftext = m.S(3), m.S(4)
			if strings.Contains(reftext, "]") {
				reftext = strings.ReplaceAll(reftext, "\\]", "]")
			}
			if strings.Contains(reftext, "{") {
				reftext = p.doc.SubAttributes(reftext)
			}
		}
		if id == "" {
			continue
		}
		p.registerInlineAnchor(id, reftext, block, "ref")
	}
}

func (p *parser) registerInlineAnchor(id, reftext string, block *Block, typ string) {
	if reftext != "" {
		reftext = p.doc.ApplySubs(reftext, ReftextSubs, block)
	}
	ref := &Ref{Inline: &Inline{Context: "anchor", Type: typ, ID: id, Text: reftext, Parent: block, Doc: p.doc}, Reftext: reftext}
	if !p.doc.Register(id, ref) {
		p.doc.Warn(fmt.Sprintf("%s: id assigned to anchor already in use: %s", p.doc.location(block.File, block.Lineno), id))
	}
}

func (p *parser) buildBlock(context, contentModel, terminator string, delimited bool, parent *Block, reader *Reader, attrs map[string]string) *Block {
	doc := p.doc
	skipProcessing := contentModel == "skip"
	parseAs := contentModel
	if contentModel == "skip" || contentModel == "raw" {
		parseAs = "simple"
	}
	var lines []string
	var blockReader *Reader
	switch {
	case !delimited:
		if parseAs == "verbatim" {
			lines = reader.readLinesUntil(readOpts{breakOnBlankLines: true, breakOnListContinuation: true})
		} else {
			if contentModel == "compound" {
				contentModel = "simple"
			}
			o := readOpts{breakOnBlankLines: true, breakOnListContinuation: true, preserveLastLine: true, skipLineComments: true, skipProcessing: skipProcessing, breakIf: startOfBlock}
			lines = reader.readLinesUntil(o)
		}
	case parseAs != "compound":
		lines = reader.readLinesUntil(readOpts{terminator: terminator, skipProcessing: skipProcessing, context: context})
	default:
		file, lineno, dir := reader.File(), reader.Lineno(), reader.cur.dir
		blockLines := reader.readLinesUntil(readOpts{terminator: terminator, skipProcessing: skipProcessing, context: context})
		blockReader = newReader(doc, blockLines, file, dir)
		blockReader.process = false
		blockReader.cur.lineno = lineno
	}
	switch contentModel {
	case "verbatim":
		tabSize := doc.tabSize()
		if v, ok := attrs["tabsize"]; ok {
			tabSize, _ = strconv.Atoi(v)
		}
		if v, ok := attrs["indent"]; ok {
			n, _ := strconv.Atoi(v)
			lines = adjustIndentation(lines, n, tabSize)
		} else if tabSize > 0 {
			lines = adjustIndentation(lines, -1, tabSize)
		}
	case "skip":
		return nil
	}
	block := NewBlock(parent, context)
	block.ContentModel = contentModel
	block.Lines = lines
	if contentModel == "compound" && blockReader != nil {
		p.parseBlocks(blockReader, block)
	}
	return block
}

// parseBlocks はリーダーが尽きるまでブロックを解析して parent に追加します。
func (p *parser) parseBlocks(reader *Reader, parent *Block) {
	for {
		blockMeta := newMeta()
		block := p.nextBlock(reader, parent, blockMeta, blockOpts{parseMetadata: true})
		if block != nil {
			parent.Blocks = append(parent.Blocks, block)
		}
		if !reader.hasMore() {
			break
		}
	}
}

// ---------------------------------------------------------------------------
// リスト

var listRxMap = map[string]*Rx{"ulist": unorderedListRx, "olist": orderedListRx, "dlist": descriptionListRx, "colist": calloutListRx}

func (p *parser) parseList(reader *Reader, listType string, parent *Block, style string) *Block {
	list := NewBlock(parent, listType)
	list.ContentModel = "compound"
	rxList := listRxMap[listType]
	for {
		line, ok := reader.peek()
		if !ok {
			break
		}
		m := rxList.Match(line)
		if m == nil {
			break
		}
		if item := p.parseListItem(reader, list, m, m.S(1), nil, style); item != nil {
			list.Blocks = append(list.Blocks, item)
		}
		if reader.skipBlankLines() < 0 {
			break
		}
	}
	return list
}

func (p *parser) parseCalloutList(reader *Reader, match *M, parent *Block) *Block {
	doc := p.doc
	list := NewBlock(parent, "colist")
	list.ContentModel = "compound"
	nextIndex := 1
	autonum := 0
	for {
		if match == nil {
			line, ok := reader.peek()
			if !ok {
				break
			}
			if match = calloutListRx.Match(line); match == nil {
				break
			}
		}
		num := match.S(1)
		if num == "." {
			autonum++
			num = strconv.Itoa(autonum)
		}
		if num != strconv.Itoa(nextIndex) {
			doc.Warn(fmt.Sprintf("%s: callout list item index: expected %d, got %s", reader.lineInfo(), nextIndex, num))
		}
		if item := p.parseListItem(reader, list, match, "<1>", nil, ""); item != nil {
			list.Blocks = append(list.Blocks, item)
			if coids := doc.callouts.calloutIDs(len(list.Blocks)); coids == "" {
				doc.Warn(fmt.Sprintf("%s: no callout found for <%d>", reader.prevLineInfo(), len(list.Blocks)))
			} else {
				item.Attrs["coids"] = coids
			}
		}
		nextIndex++
		match = nil
		// 項目間の空行を読み飛ばす
		if reader.skipBlankLines() < 0 {
			break
		}
	}
	doc.callouts.nextList()
	return list
}

func (p *parser) parseDescriptionList(reader *Reader, match *M, parent *Block) *Block {
	list := NewBlock(parent, "dlist")
	list.ContentModel = "compound"
	sibling := descriptionListSiblingRx[match.S(2)]
	current := p.parseListItem(reader, list, match, "", sibling, "")
	list.Blocks = append(list.Blocks, current)
	for {
		line, ok := reader.peek()
		if !ok {
			break
		}
		m := sibling.Match(line)
		if m == nil {
			break
		}
		next := p.parseListItem(reader, list, m, "", sibling, "")
		if current.Desc != nil {
			list.Blocks = append(list.Blocks, next)
			current = next
		} else {
			current.Terms = append(current.Terms, next.Terms...)
			current.Desc = next.Desc
		}
	}
	return list
}

var orderedListStyles = []string{"arabic", "loweralpha", "lowerroman", "upperalpha", "upperroman"}
var orderedListMarkerRx = map[string]*Rx{
	"arabic": rx(`^\d+\.$`), "loweralpha": rx(`^[a-z]\.$`), "lowerroman": rx(`^[ivx]+\)$`),
	"upperalpha": rx(`^[A-Z]\.$`), "upperroman": rx(`^[IVX]+\)$`),
}

func romanToInt(s string) int {
	vals := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	s = strings.ToUpper(s)
	total := 0
	for i := 0; i < len(s); i++ {
		v := vals[s[i]]
		if i+1 < len(s) && vals[s[i+1]] > v {
			total -= v
		} else {
			total += v
		}
	}
	return total
}

// resolveOrderedListMarker はマーカーを正規化し、スタイルと序数を返します。
func resolveOrderedListMarker(marker string) (normalized, style string, ordinal int) {
	if strings.HasPrefix(marker, ".") {
		return marker, "", 0
	}
	for _, s := range orderedListStyles {
		if !orderedListMarkerRx[s].Test(marker) {
			continue
		}
		body := marker[:len(marker)-1]
		switch s {
		case "arabic":
			n, _ := strconv.Atoi(body)
			return "1.", s, n
		case "loweralpha":
			return "a.", s, int(body[0]-'a') + 1
		case "upperalpha":
			return "A.", s, int(body[0]-'A') + 1
		case "lowerroman":
			return "i)", s, romanToInt(body)
		case "upperroman":
			return "I)", s, romanToInt(body)
		}
	}
	return marker, "", 0
}

func (p *parser) isSiblingListItem(line, listType, siblingTrait string, siblingRx *Rx) bool {
	if siblingRx != nil {
		return siblingRx.Test(line)
	}
	m := listRxMap[listType].Match(line)
	if m == nil {
		return false
	}
	switch listType {
	case "ulist":
		return siblingTrait == m.S(1)
	case "olist":
		norm, _, _ := resolveOrderedListMarker(m.S(1))
		return siblingTrait == norm
	}
	return siblingTrait == "<1>"
}

// parseListItem はリスト項目を解析します。dlist の場合は Terms と Desc を持つ項目を返します。
func (p *parser) parseListItem(reader *Reader, list *Block, match *M, siblingTrait string, siblingRx *Rx, style string) *Block {
	doc := p.doc
	listType := list.Context
	dlist := listType == "dlist"
	var item, term *Block
	hasText := false
	file, lineno := reader.File(), reader.Lineno()
	if dlist {
		termText := match.S(1)
		term = NewBlock(list, "list_item")
		term.RawText, term.HasText = termText, true
		term.File, term.Lineno = file, lineno
		if strings.HasPrefix(termText, "[[") {
			if m := leadingInlineAnchorRx.Match(termText); m != nil {
				reftext := m.S(2)
				if !m.Has(2) {
					reftext = strings.TrimLeft(m.Post(), " \t")
				}
				p.registerInlineAnchor(m.S(1), reftext, term, "ref")
			}
		}
		item = NewBlock(list, "list_item")
		if match.Has(3) && match.S(3) != "" {
			item.RawText, item.HasText = match.S(3), true
			hasText = true
		}
	} else {
		hasText = true
		itemText := match.S(2)
		item = NewBlock(list, "list_item")
		item.RawText, item.HasText = itemText, true
		switch listType {
		case "ulist":
			item.Marker = siblingTrait
			if strings.HasPrefix(itemText, "[") {
				switch {
				case style == "bibliography":
					if m := inlineBiblioAnchorRx.Match(itemText); m != nil {
						p.registerInlineAnchor(m.S(1), m.S(2), item, "bibref")
					}
				case strings.HasPrefix(itemText, "[["):
					if m := leadingInlineAnchorRx.Match(itemText); m != nil {
						p.registerInlineAnchor(m.S(1), m.S(2), item, "ref")
					}
				case strings.HasPrefix(itemText, "[ ] ") || strings.HasPrefix(itemText, "[x] ") || strings.HasPrefix(itemText, "[*] "):
					list.Options["checklist"] = true
					list.Attrs["checklist-option"] = ""
					item.Attrs["checkbox"] = ""
					if !strings.HasPrefix(itemText, "[ ") {
						item.Attrs["checked"] = ""
					}
					item.RawText = itemText[4:]
				}
			}
		case "olist":
			norm, implicitStyle, ordinal := resolveOrderedListMarker(siblingTrait)
			index := len(list.Blocks)
			if index == 0 {
				if style == "" {
					if implicitStyle != "" {
						list.Style = implicitStyle
					} else if n := len(norm) - 1; n >= 0 && n < len(orderedListStyles) {
						list.Style = orderedListStyles[n]
					} else {
						list.Style = "arabic"
					}
				}
				if implicitStyle != "" && ordinal != 1 {
					list.Attrs["start"] = strconv.Itoa(ordinal)
				}
			} else if implicitStyle != "" {
				start := 1
				if v, ok := list.Attrs["start"]; ok {
					start, _ = strconv.Atoi(v)
				}
				if expected := start + index; ordinal != expected && implicitStyle == "arabic" {
					doc.Warn(fmt.Sprintf("%s: list item index: expected %d, got %d", reader.lineInfo(), expected, ordinal))
				}
			}
			siblingTrait = norm
			item.Marker = norm
			if strings.HasPrefix(itemText, "[[") {
				if m := leadingInlineAnchorRx.Match(itemText); m != nil {
					p.registerInlineAnchor(m.S(1), m.S(2), item, "ref")
				}
			}
		default:
			item.Marker = siblingTrait
			if strings.HasPrefix(itemText, "[[") {
				if m := leadingInlineAnchorRx.Match(itemText); m != nil {
					p.registerInlineAnchor(m.S(1), m.S(2), item, "ref")
				}
			}
		}
	}
	item.File, item.Lineno = file, lineno
	item.ContentModel = "compound"

	reader.shift()
	itemLines := p.readLinesForListItem(reader, listType, siblingTrait, siblingRx, hasText)
	sub := newReader(doc, itemLines, reader.File(), reader.cur.dir)
	sub.process = false
	sub.cur.lineno = lineno + 1
	if sub.hasMore() {
		// コメント行を読み飛ばした先を確認する
		var comments []string
		for {
			line, ok := sub.peek()
			if !ok || !strings.HasPrefix(line, "//") || strings.HasPrefix(line, "///") {
				break
			}
			comments = append(comments, line)
			sub.shift()
		}
		continuationConnectsFirstBlock, contentAdjacent := false, false
		if next, ok := sub.peek(); ok {
			sub.unshiftAll(comments)
			if next == "" {
				continuationConnectsFirstBlock = true
			} else {
				contentAdjacent = true
				if !dlist {
					hasText = false
				}
			}
		}
		opts := blockOpts{parseMetadata: true, text: !hasText}
		for {
			if block := p.nextBlock(sub, item, newMeta(), opts); block != nil {
				item.Blocks = append(item.Blocks, block)
			}
			opts.text = false
			if !sub.hasMore() {
				break
			}
		}
		// 最初の段落を項目テキストに取り込む (fold_first)
		if len(item.Blocks) > 0 {
			first := item.Blocks[0]
			if (first.Context == "paragraph" && !continuationConnectsFirstBlock) ||
				((contentAdjacent || !continuationConnectsFirstBlock) && first.Context == "literal" && first.Option("listparagraph")) {
				item.Blocks = item.Blocks[1:]
				lines := first.Lines
				if item.RawText != "" {
					lines = append([]string{item.RawText}, lines...)
				}
				item.RawText = strings.Join(lines, "\n")
				item.HasText = true
				item.textCache = nil
			}
		}
	}
	if dlist {
		result := NewBlock(list, "list_item")
		result.ContentModel = "compound"
		result.File, result.Lineno = file, lineno
		result.Terms = []*Block{term}
		if item.HasText || len(item.Blocks) > 0 {
			result.Desc = item
		}
		return result
	}
	return item
}

// readLinesForListItem はリスト項目に属する行を読み取ります (Asciidoctor の同名メソッドの移植)。
func (p *parser) readLinesForListItem(reader *Reader, listType, siblingTrait string, siblingRx *Rx, hasText bool) []string {
	var buffer []string
	const (
		inactive = iota
		active
		frozen
	)
	continuation := inactive
	withinNestedList := false
	detachedContinuation := -1
	dlist := listType == "dlist"
	nestable := []string{"ulist", "olist", "dlist"}
	isSibling := func(line string) bool { return p.isSiblingListItem(line, listType, siblingTrait, siblingRx) }
	// nestedListType は行が入れ子リストの項目ならその種類を返します
	nestedListType := func(line string, candidates []string) (string, *M) {
		for _, ctx := range candidates {
			if m := listRxMap[ctx].Match(line); m != nil {
				return ctx, m
			}
		}
		return "", nil
	}
	var thisLine string
	pending := false
	for reader.hasMore() {
		thisLine, _ = reader.read()
		pending = true
		if isSibling(thisLine) {
			break
		}
		prevLine, hasPrev := "", false
		if len(buffer) > 0 {
			prevLine, hasPrev = buffer[len(buffer)-1], true
		}
		if hasPrev && prevLine == "+" {
			if continuation == inactive {
				continuation = active
				hasText = true
				if !withinNestedList {
					buffer[len(buffer)-1] = ""
				}
			}
			if thisLine == "+" {
				if continuation != frozen {
					continuation = frozen
					buffer = append(buffer, thisLine)
				}
				pending = false
				continue
			}
		}
		if _, term, ok := matchDelimitedBlock(thisLine); ok {
			if continuation != active {
				break
			}
			buffer = append(buffer, thisLine)
			buffer = append(buffer, reader.readLinesUntil(readOpts{terminator: term, readLastLine: true})...)
			continuation = inactive
		} else if dlist && continuation != active && blockAttributeLineRx.Test(thisLine) {
			break
		} else if continuation == active && thisLine != "" {
			switch {
			case literalParagraphRx.Test(thisLine):
				reader.unshift(thisLine)
				o := readOpts{preserveLastLine: true, breakOnBlankLines: true, breakOnListContinuation: true}
				if dlist {
					o.breakIf = isSibling
				}
				buffer = append(buffer, reader.readLinesUntil(o)...)
				continuation = inactive
			case blockTitleRx.Test(thisLine) || blockAttributeLineRx.Test(thisLine) || attributeEntryRx.Test(thisLine):
				buffer = append(buffer, thisLine)
			default:
				candidates := nestable
				if withinNestedList {
					candidates = []string{"dlist"}
				}
				if ctx, m := nestedListType(thisLine, candidates); ctx != "" {
					withinNestedList = true
					if ctx == "dlist" && m.S(3) == "" {
						hasText = false
					}
				}
				buffer = append(buffer, thisLine)
				continuation = inactive
			}
		} else if hasPrev && prevLine == "" {
			if thisLine == "" {
				if reader.skipBlankLines() < 0 {
					pending = false
					break
				}
				var ok bool
				if thisLine, ok = reader.read(); !ok {
					pending = false
					break
				}
				if isSibling(thisLine) {
					break
				}
			}
			if thisLine == "+" {
				detachedContinuation = len(buffer)
				buffer = append(buffer, thisLine)
			} else if hasText {
				if isSibling(thisLine) {
					break
				} else if ctx, m := nestedListType(thisLine, nestable); ctx != "" {
					buffer = append(buffer, thisLine)
					withinNestedList = true
					if ctx == "dlist" && m.S(3) == "" {
						hasText = false
					}
				} else if literalParagraphRx.Test(thisLine) {
					reader.unshift(thisLine)
					o := readOpts{preserveLastLine: true, breakOnBlankLines: true, breakOnListContinuation: true}
					if dlist {
						o.breakIf = isSibling
					}
					buffer = append(buffer, reader.readLinesUntil(o)...)
				} else {
					break
				}
			} else {
				if !withinNestedList {
					buffer = buffer[:len(buffer)-1]
				}
				buffer = append(buffer, thisLine)
				hasText = true
			}
		} else {
			if thisLine != "" {
				hasText = true
			}
			candidates := nestable
			if withinNestedList {
				candidates = []string{"dlist"}
			}
			if ctx, m := nestedListType(thisLine, candidates); ctx != "" {
				withinNestedList = true
				if ctx == "dlist" && m.S(3) == "" {
					hasText = false
				}
			}
			buffer = append(buffer, thisLine)
		}
		pending = false
	}
	if pending {
		reader.unshift(thisLine)
	}
	if detachedContinuation >= 0 && detachedContinuation < len(buffer) {
		buffer[detachedContinuation] = ""
	}
	for len(buffer) > 0 {
		last := buffer[len(buffer)-1]
		if last == "" {
			buffer = buffer[:len(buffer)-1]
		} else {
			if last == "+" {
				buffer = buffer[:len(buffer)-1]
			}
			break
		}
	}
	return buffer
}
