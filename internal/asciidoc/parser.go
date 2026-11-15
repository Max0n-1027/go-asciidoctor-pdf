package asciidoc

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// meta はブロックに先行するメタデータ (属性リスト、タイトル、属性定義) を保持します。
type meta struct {
	attrs   map[string]string
	entries []AttrEntry
}

func newMeta() *meta { return &meta{attrs: map[string]string{}} }

func (m *meta) clear() {
	m.attrs = map[string]string{}
	m.entries = nil
}

func (m *meta) style() string { return m.attrs["1"] }

// Parse は AsciiDoc のソースを解析して文書を返します。
func Parse(input string, opts Options) *Document {
	doc := newDocument(opts)
	doc.BaseDir = opts.BaseDir
	dir := opts.BaseDir
	file := ""
	if opts.InputFile != "" {
		abs, _ := filepath.Abs(opts.InputFile)
		if doc.BaseDir == "" {
			doc.BaseDir = filepath.Dir(abs)
		}
		dir = filepath.Dir(abs)
		file = filepath.Base(abs)
	}
	if dir == "" {
		dir = "."
	}
	if doc.BaseDir == "" {
		doc.BaseDir, _ = filepath.Abs(dir)
	}
	doc.initAttributes(opts)
	reader := newReader(doc, prepareLines(input), file, dir)
	p := &parser{doc: doc}
	blockMeta := p.parseDocumentHeader(reader)
	for reader.hasMore() {
		var section *Block
		section, blockMeta = p.nextSection(reader, doc.Block, blockMeta)
		if section != nil {
			doc.assignNumeral(section)
			doc.Blocks = append(doc.Blocks, section)
		}
	}
	p.finalize()
	doc.RestoreAttributes()
	return doc
}

// parseNested は表セル内の AsciiDoc を入れ子文書として解析します。
func parseNested(parent *Document, lines []string, file string, lineno int) *Document {
	doc := &Document{
		Attributes: map[string]string{},
		Refs:       parent.Refs,
		Inline:     parent.Inline,
		Logger:     parent.Logger,
		Safe:       parent.Safe,
		BaseDir:    parent.BaseDir,
		ParentDoc:  parent,
		locked:     parent.locked,
		callouts:   parent.callouts,
		nextIDs:    parent.nextIDs,
		Compat:     parent.Compat,
	}
	for k, v := range parent.Attributes {
		doc.Attributes[k] = v
	}
	delete(doc.Attributes, "doctitle")
	delete(doc.Attributes, "compat-mode")
	delete(doc.Attributes, "toc")
	delete(doc.Attributes, "toc-placement")
	delete(doc.Attributes, "toc-position")
	delete(doc.Attributes, "notitle")
	doc.Attributes["doctype"] = "article"
	doc.Block = NewBlock(nil, "document")
	doc.Block.Doc = doc
	doc.ContentModel = "compound"
	doc.Attrs["\x00nested"] = ""
	reader := newReader(doc, lines, file, parent.BaseDir)
	reader.cur.lineno = lineno
	p := &parser{doc: doc}
	blockMeta := p.parseDocumentHeader(reader)
	for reader.hasMore() {
		var section *Block
		section, blockMeta = p.nextSection(reader, doc.Block, blockMeta)
		if section != nil {
			doc.assignNumeral(section)
			doc.Blocks = append(doc.Blocks, section)
		}
	}
	p.finalize()
	return doc
}

// IsNested は入れ子文書かどうかを返します。
func (d *Document) IsNested() bool { return d.ParentDoc != nil }

type parser struct {
	doc *Document
}

func (p *parser) finalize() {}

// ---------------------------------------------------------------------------
// 文書ヘッダー

func (p *parser) parseDocumentHeader(reader *Reader) *meta {
	doc := p.doc
	blockMeta := newMeta()
	if reader.skipBlankLines() >= 0 {
		p.parseBlockMetadataLines(reader, blockMeta, false)
	}
	attrs := doc.Attributes
	leveloffset := 0
	if v, ok := attrs["leveloffset"]; ok {
		leveloffset, _ = strconv.Atoi(v)
	}
	implicitDoctitle := p.isNextLineDoctitle(reader, blockMeta, leveloffset)
	if implicitDoctitle && blockMeta.attrs["title"] != "" {
		attrs["authorcount"] = "0"
		return p.finalizeHeader(blockMeta, false)
	}
	doctitleAttrVal, hasDoctitleAttr := "", false
	if v, ok := attrs["doctitle"]; ok && v != "" {
		doc.DocTitle = v
		doctitleAttrVal, hasDoctitleAttr = v, true
	}
	if implicitDoctitle {
		doc.Lineno = reader.Lineno()
		id, _, title, _, atx := p.parseSectionTitle(reader, "")
		doc.ID = id
		titleSetByAttr := hasDoctitleAttr
		if !hasDoctitleAttr {
			doc.DocTitle = title
			doctitleAttrVal = subSpecialChars(title)
			if strings.Contains(doctitleAttrVal, "{") {
				doctitleAttrVal = doc.subAttributesOpts(doctitleAttrVal, "skip")
			}
			attrs["doctitle"] = doctitleAttrVal
		}
		if !atx && !doc.locked["compat-mode"] {
			attrs["compat-mode"] = ""
			doc.Compat = true
		}
		if sep, ok := blockMeta.attrs["separator"]; ok && !doc.locked["title-separator"] {
			attrs["title-separator"] = sep
		}
		if v, ok := blockMeta.attrs["id"]; ok {
			doc.ID = v
		}
		if v, ok := blockMeta.attrs["role"]; ok {
			attrs["role"] = v
		}
		if v, ok := blockMeta.attrs["reftext"]; ok {
			attrs["reftext"] = v
		}
		blockMeta.clear()
		p.parseHeaderMetadata(reader)
		if v, ok := attrs["doctitle"]; ok && v != "" && v != doctitleAttrVal && !titleSetByAttr {
			// ヘッダー内の属性定義で doctitle が上書きされた
			doc.DocTitle = v
		} else if !ok || v == "" {
			attrs["doctitle"] = doctitleAttrVal
		}
		if doc.ID != "" {
			doc.Register(doc.ID, &Ref{Block: doc.Block})
		}
		doc.HasHeader = true
	} else if author, ok := attrs["author"]; ok {
		md := processAuthors(author, true, false)
		if _, has := attrs["authorinitials"]; has {
			delete(md, "authorinitials")
		}
		for k, v := range md {
			attrs[k] = v
		}
	} else if authors, ok := attrs["authors"]; ok {
		for k, v := range processAuthors(authors, true, true) {
			attrs[k] = v
		}
	}
	return p.finalizeHeader(blockMeta, true)
}

func (p *parser) finalizeHeader(blockMeta *meta, headerValid bool) *meta {
	doc := p.doc
	attrs := doc.Attributes
	// ヘッダーで定義した属性は再生対象から外す
	blockMeta.entries = nil
	if !headerValid {
		blockMeta.attrs["invalid-header"] = ""
	}
	if _, ok := attrs["doctitle"]; !ok && doc.DocTitle != "" {
		attrs["doctitle"] = doc.DocTitle
	}
	if doc.ParentDoc == nil {
		// toc 属性の正規化
		tocVal, hasToc := attrs["toc"]
		if _, ok := attrs["toc2"]; ok {
			delete(attrs, "toc2")
			tocVal, hasToc = "left", true
		}
		if hasToc {
			placement := attrs["toc-placement"]
			if _, ok := attrs["toc-placement"]; !ok {
				placement = "macro"
			}
			posVal := attrs["toc-position"]
			if placement != "" && placement != "auto" {
				posVal = placement
			}
			position := posVal
			if position == "" {
				position = tocVal
				if position == "" {
					position = "left"
				}
			}
			attrs["toc"] = ""
			attrs["toc-placement"] = "auto"
			switch position {
			case "left", "<", "&lt;":
				attrs["toc-position"] = "left"
			case "right", ">", "&gt;":
				attrs["toc-position"] = "right"
			case "top", "^":
				attrs["toc-position"] = "top"
			case "bottom", "v":
				attrs["toc-position"] = "bottom"
			case "preamble", "macro":
				attrs["toc-position"] = "content"
				attrs["toc-placement"] = position
			default:
				delete(attrs, "toc-position")
			}
		}
		if icons, ok := attrs["icons"]; ok {
			if _, has := attrs["icontype"]; !has && icons != "" && icons != "font" {
				attrs["icons"] = ""
				if icons != "image" {
					attrs["icontype"] = icons
				}
			}
		}
		// doctype 属性の整合
		doctype := doc.Doctype()
		for k := range attrs {
			if strings.HasPrefix(k, "doctype-") && k != "doctype-"+doctype {
				delete(attrs, k)
			}
		}
		attrs["doctype-"+doctype] = ""
		// "flexible" 属性 (sectnums) のロックを解除する
		if _, ok := attrs["sectnums"]; ok {
			delete(doc.locked, "sectnums")
		}
	}
	doc.Compat = doc.HasAttr("compat-mode")
	if doc.Compat {
		if v, ok := attrs["language"]; ok {
			attrs["source-language"] = v
		}
	}
	doc.saveHeaderAttributes()
	return blockMeta
}

func (p *parser) isNextLineDoctitle(reader *Reader, blockMeta *meta, leveloffset int) bool {
	level, ok := p.isNextLineSection(reader, blockMeta)
	if !ok {
		return false
	}
	return level+leveloffset == 0 || (leveloffset != 0 && level+leveloffset == 0)
}

var setextSectionLevels = map[byte]int{'=': 0, '-': 1, '~': 2, '^': 3, '+': 4}

func uniform(s string, ch byte) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != ch {
			return false
		}
	}
	return true
}

func atxSectionLevel(line string) (int, bool) {
	if line == "" || (line[0] != '=' && line[0] != '#') {
		return 0, false
	}
	if m := atxSectionTitleRx.Match(line); m != nil {
		return len(m.S(1)) - 1, true
	}
	return 0, false
}

func setextSectionLevel(line1, line2 string) (int, bool) {
	if line2 == "" {
		return 0, false
	}
	level, ok := setextSectionLevels[line2[0]]
	if !ok || !uniform(line2, line2[0]) || !setextSectionTitleRx.Test(line1) {
		return 0, false
	}
	diff := len([]rune(line1)) - len(line2)
	if diff < -1 || diff > 1 {
		return 0, false
	}
	return level, true
}

func (p *parser) isNextLineSection(reader *Reader, blockMeta *meta) (int, bool) {
	style := blockMeta.style()
	if style == "discrete" || style == "float" {
		return 0, false
	}
	line1, ok := reader.peek()
	if !ok {
		return 0, false
	}
	if level, ok := atxSectionLevel(line1); ok {
		return level, true
	}
	// setext 形式 (2 行) の見出し
	if line1 == "" {
		return 0, false
	}
	reader.shift()
	line2, ok2 := reader.peek()
	reader.unshift(line1)
	if !ok2 {
		return 0, false
	}
	return setextSectionLevel(line1, line2)
}

func (p *parser) parseSectionTitle(reader *Reader, sectID string) (id, reftext, title string, level int, atx bool) {
	doc := p.doc
	line1, _ := reader.read()
	id = sectID
	extractAnchor := func() {
		if id == "" && strings.HasSuffix(title, "]]") {
			if m := inlineSectionAnchorRx.Match(title); m != nil && !m.Has(1) {
				title = strings.TrimSuffix(title, m.All())
				id, reftext = m.S(2), m.S(3)
			}
		}
	}
	if m := atxSectionTitleRx.Match(line1); m != nil && (line1[0] == '=' || line1[0] == '#') {
		level, title, atx = len(m.S(1))-1, m.S(2), true
		extractAnchor()
	} else {
		line2, _ := reader.peek()
		level, _ = setextSectionLevel(line1, line2)
		title = line1
		extractAnchor()
		reader.shift()
	}
	if v, ok := doc.Attributes["leveloffset"]; ok {
		off, _ := strconv.Atoi(v)
		level += off
		if level < 0 {
			level = 0
		}
	}
	return
}

func (p *parser) parseHeaderMetadata(reader *Reader) {
	doc := p.doc
	attrs := doc.Attributes
	p.processAttributeEntries(reader, nil)
	implicit := map[string]string{}
	hasImplicit := false
	var implicitAuthor, implicitInitials, implicitAuthors string
	if reader.hasMore() && !reader.nextLineEmpty() {
		line, _ := reader.read()
		implicit = processAuthors(line, false, true)
		count := implicit["authorcount"]
		delete(implicit, "authorcount")
		attrs["authorcount"] = count
		if count != "0" {
			hasImplicit = true
			for k, v := range implicit {
				if _, exists := attrs[k]; !exists {
					attrs[k] = doc.ApplySubs(v, HeaderSubs, doc.Block)
				}
			}
			implicitAuthor = attrs["author"]
			implicitInitials = attrs["authorinitials"]
			implicitAuthors = attrs["authors"]
		}
		p.processAttributeEntries(reader, nil)
		if reader.hasMore() && !reader.nextLineEmpty() {
			revLine, _ := reader.read()
			if m := revisionInfoLineRx.Match(revLine); m != nil {
				rev := map[string]string{}
				if m.Has(1) {
					rev["revnumber"] = strings.TrimRight(m.S(1), " \t")
				}
				if comp := strings.TrimSpace(m.S(2)); comp != "" {
					if !m.Has(1) && strings.HasPrefix(comp, "v") {
						rev["revnumber"] = comp[1:]
					} else {
						rev["revdate"] = comp
					}
				}
				if m.Has(3) {
					rev["revremark"] = strings.TrimRight(m.S(3), " \t")
				}
				for k, v := range rev {
					if _, exists := attrs[k]; !exists {
						attrs[k] = doc.ApplySubs(v, HeaderSubs, doc.Block)
					}
				}
			} else {
				reader.unshift(revLine)
			}
		}
		p.processAttributeEntries(reader, nil)
		reader.skipBlankLines()
	}
	// author / authors 属性による上書き
	var md map[string]string
	if author, ok := attrs["author"]; ok && author != implicitAuthor {
		md = processAuthors(author, true, false)
		if attrs["authorinitials"] != implicitInitials {
			delete(md, "authorinitials")
		}
	} else if authors, ok := attrs["authors"]; ok && authors != implicitAuthors {
		md = processAuthors(authors, true, true)
	} else {
		var list []string
		explicit := false
		for idx := 1; ; idx++ {
			key := "author_" + strconv.Itoa(idx)
			v, ok := attrs[key]
			if !ok {
				break
			}
			if v == implicit[key] {
				parts := []string{}
				for _, part := range []string{"firstname", "middlename", "lastname"} {
					if pv, ok := implicit[part+"_"+strconv.Itoa(idx)]; ok {
						parts = append(parts, strings.ReplaceAll(pv, " ", "_"))
					}
				}
				list = append(list, strings.Join(parts, " "))
			} else {
				list = append(list, v)
				explicit = true
			}
		}
		if explicit {
			md = processAuthorList(list, true)
		}
	}
	if md != nil && md["authorcount"] != "0" {
		for k, v := range md {
			attrs[k] = v
		}
		if _, ok := attrs["email"]; !ok {
			if v, ok := attrs["email_1"]; ok {
				attrs["email"] = v
			}
		}
	} else if !hasImplicit {
		attrs["authorcount"] = "0"
	}
}

var authorKeys = []string{"author", "authorinitials", "firstname", "middlename", "lastname", "email"}

func processAuthors(line string, namesOnly, multiple bool) map[string]string {
	var entries []string
	if multiple && strings.Contains(line, ";") {
		rest := line
		for {
			m := authorDelimiterRx.Match(rest)
			if m == nil {
				entries = append(entries, rest)
				break
			}
			entries = append(entries, m.Pre())
			rest = m.Post()
		}
	} else {
		entries = []string{line}
	}
	return processAuthorList(entries, namesOnly)
}

func firstChar(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}

func squeezeSpaces(s string) string {
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

func processAuthorList(entries []string, namesOnly bool) map[string]string {
	md := map[string]string{}
	idx := 0
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		idx++
		key := func(name string) string {
			if idx == 1 {
				return name
			}
			return name + "_" + strconv.Itoa(idx)
		}
		var segments []string
		hasSegments := false
		if namesOnly {
			if strings.Contains(entry, "<") {
				md[key("author")] = strings.ReplaceAll(entry, "_", " ")
				entry = xmlSanitizeRx.Gsub(entry, func(*M) string { return "" })
			}
			fields := strings.Fields(entry)
			if len(fields) > 3 {
				fields = []string{fields[0], fields[1], strings.Join(fields[2:], " ")}
			}
			if len(fields) > 0 {
				segments, hasSegments = fields, true
				for len(segments) < 4 {
					segments = append(segments, "")
				}
			}
		} else if m := authorInfoLineRx.Match(entry); m != nil {
			segments, hasSegments = []string{m.S(1), m.S(2), m.S(3), m.S(4)}, true
		}
		if hasSegments {
			us := func(s string) string { return strings.ReplaceAll(s, "_", " ") }
			fname := us(segments[0])
			author := fname
			md[key("firstname")] = fname
			md[key("authorinitials")] = firstChar(fname)
			if segments[1] != "" {
				if segments[2] != "" {
					mname, lname := us(segments[1]), us(segments[2])
					md[key("middlename")] = mname
					md[key("lastname")] = lname
					author = fname + " " + mname + " " + lname
					md[key("authorinitials")] = firstChar(fname) + firstChar(mname) + firstChar(lname)
				} else {
					lname := us(segments[1])
					md[key("lastname")] = lname
					author = fname + " " + lname
					md[key("authorinitials")] = firstChar(fname) + firstChar(lname)
				}
			}
			if _, ok := md[key("author")]; !ok {
				md[key("author")] = author
			}
			if !namesOnly && segments[3] != "" {
				md[key("email")] = segments[3]
			}
		} else {
			fname := strings.TrimSpace(squeezeSpaces(entry))
			md[key("author")] = fname
			md[key("firstname")] = fname
			md[key("authorinitials")] = firstChar(fname)
		}
		if idx == 1 {
			md["authors"] = md[key("author")]
		} else {
			if idx == 2 {
				for _, k := range authorKeys {
					if v, ok := md[k]; ok {
						md[k+"_1"] = v
					}
				}
			}
			md["authors"] = md["authors"] + ", " + md[key("author")]
		}
	}
	md["authorcount"] = strconv.Itoa(idx)
	return md
}

// ---------------------------------------------------------------------------
// 属性定義

func (p *parser) processAttributeEntries(reader *Reader, m *meta) {
	reader.skipCommentLines()
	for p.processAttributeEntry(reader, m, nil) {
		reader.shift()
		reader.skipCommentLines()
	}
}

func (p *parser) processAttributeEntry(reader *Reader, blockMeta *meta, match *M) bool {
	if match == nil {
		line, ok := reader.peek()
		if !ok || !strings.HasPrefix(line, ":") {
			return false
		}
		if match = attributeEntryRx.Match(line); match == nil {
			return false
		}
	}
	value := match.S(2)
	if strings.HasSuffix(value, " \\") || strings.HasSuffix(value, " +") {
		con := value[len(value)-2:]
		value = strings.TrimRight(value[:len(value)-2], " \t")
		for reader.advance() {
			next, ok := reader.peek()
			if !ok || next == "" {
				break
			}
			next = strings.TrimLeft(next, " \t")
			keepOpen := strings.HasSuffix(next, con)
			if keepOpen {
				next = strings.TrimRight(next[:len(next)-2], " \t")
			}
			sep := " "
			if strings.HasSuffix(value, " +") {
				sep = "\n"
			}
			value += sep + next
			if !keepOpen {
				break
			}
		}
	}
	p.storeAttribute(match.S(1), value, blockMeta)
	return true
}

func sanitizeAttributeName(name string) string {
	return strings.ToLower(invalidAttributeNameRx.Gsub(name, func(*M) string { return "" }))
}

func (p *parser) storeAttribute(name, value string, blockMeta *meta) {
	doc := p.doc
	negate := false
	if strings.HasSuffix(name, "!") {
		name, negate = name[:len(name)-1], true
	} else if strings.HasPrefix(name, "!") {
		name, negate = name[1:], true
	}
	name = sanitizeAttributeName(name)
	switch name {
	case "numbered":
		name = "sectnums"
	case "hardbreaks":
		name = "hardbreaks-option"
	case "showtitle":
		if negate {
			p.storeAttribute("notitle", "", blockMeta)
		} else {
			p.storeAttribute("notitle!", "", blockMeta)
		}
	}
	if negate {
		if doc.UnsetAttr(name) && blockMeta != nil {
			blockMeta.entries = append(blockMeta.entries, AttrEntry{Name: name, Negate: true})
		}
		return
	}
	if name == "leveloffset" {
		cur, _ := strconv.Atoi(doc.AttrOr("leveloffset", "0"))
		if strings.HasPrefix(value, "+") {
			n, _ := strconv.Atoi(value[1:])
			value = strconv.Itoa(cur + n)
		} else if strings.HasPrefix(value, "-") {
			n, _ := strconv.Atoi(value[1:])
			value = strconv.Itoa(cur - n)
		}
	}
	if doc.locked[name] {
		return
	}
	if value != "" {
		if m := attributeEntryPassMacroRx.Match(value); m != nil {
			value = m.S(2)
			if m.Has(1) {
				value = doc.ApplySubs(value, resolvePassSubs(m.S(1)), doc.Block)
			}
		} else {
			value = doc.ApplySubs(value, HeaderSubs, doc.Block)
		}
	}
	doc.SetAttr(name, value)
	if blockMeta != nil {
		blockMeta.entries = append(blockMeta.entries, AttrEntry{Name: name, Value: doc.Attributes[name]})
	}
}

// ---------------------------------------------------------------------------
// ブロックのメタデータ

func (p *parser) parseBlockMetadataLines(reader *Reader, blockMeta *meta, textOnly bool) {
	for p.parseBlockMetadataLine(reader, blockMeta, textOnly) {
		reader.shift()
		if reader.skipBlankLines() < 0 {
			break
		}
	}
}

func (p *parser) parseBlockMetadataLine(reader *Reader, blockMeta *meta, textOnly bool) bool {
	line, ok := reader.peek()
	if !ok || line == "" {
		return false
	}
	doc := p.doc
	attrs := blockMeta.attrs
	ch := line[0]
	normal := !textOnly && (ch == '[' || ch == '.' || ch == '/' || ch == ':')
	if textOnly && ch != '[' && ch != '/' {
		return false
	}
	if !textOnly && !normal {
		return false
	}
	switch {
	case ch == '[':
		if strings.HasPrefix(line, "[[") {
			if strings.HasSuffix(line, "]]") {
				if m := blockAnchorRx.Match(line); m != nil {
					attrs["id"] = m.S(1)
					if m.Has(2) {
						reftext := m.S(2)
						if strings.Contains(reftext, "{") {
							reftext = doc.SubAttributes(reftext)
						}
						attrs["reftext"] = reftext
					}
					return true
				}
			}
		} else if strings.HasSuffix(line, "]") {
			if m := blockAttributeListRx.Match(line); m != nil {
				currentStyle, hadStyle := attrs["1"]
				delete(attrs, "1")
				attrlist := m.S(1)
				if strings.Contains(attrlist, "{") {
					attrlist = doc.SubAttributes(attrlist)
				}
				doc.ParseAttributes(attrlist, nil, doc.Block, attrs)
				if _, ok := attrs["1"]; ok {
					if style := parseStyleAttribute(attrs); style != "" {
						attrs["1"] = style
					} else if hadStyle {
						attrs["1"] = currentStyle
					} else {
						delete(attrs, "1")
					}
				} else if hadStyle {
					attrs["1"] = currentStyle
				}
				return true
			}
		}
	case normal && ch == '.':
		if m := blockTitleRx.Match(line); m != nil {
			attrs["title"] = m.S(1)
			return true
		}
	case ch == '/':
		if strings.HasPrefix(line, "//") {
			if line == "//" {
				return true
			}
			if normal && uniform(line, '/') {
				if len(line) != 3 {
					reader.readLinesUntil(readOpts{terminator: line, skipFirstLine: true, preserveLastLine: true, skipProcessing: true, context: "comment"})
					return true
				}
			} else if !strings.HasPrefix(line, "///") {
				return true
			}
		}
	case normal && ch == ':':
		if m := attributeEntryRx.Match(line); m != nil {
			p.processAttributeEntry(reader, blockMeta, m)
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// セクション

func (p *parser) nextSection(reader *Reader, parent *Block, blockMeta *meta) (*Block, *meta) {
	doc := p.doc
	var preamble, intro *Block
	part := false
	var section *Block
	currentLevel := 0
	expectedNext, expectedNextAlt := 1, -1
	noNested := false
	sectname := ""
	book := doc.Doctype() == "book"

	_, invalidHeader := blockMeta.attrs["invalid-header"]
	delete(blockMeta.attrs, "invalid-header")
	atStart := parent.Context == "document" && len(parent.Blocks) == 0
	isSection := false
	if atStart && !doc.HasHeader && !invalidHeader {
		_, isSection = p.isNextLineSection(reader, blockMeta)
	}
	if atStart && (doc.HasHeader || invalidHeader || !isSection) {
		if doc.HasHeader || (book && blockMeta.style() != "abstract") {
			preamble = NewBlock(parent, "preamble")
			preamble.ContentModel = "compound"
			intro = preamble
			if book {
				if v, ok := doc.Attributes["preface-title"]; ok {
					preamble.SetTitle(v)
				}
			}
			parent.Blocks = append(parent.Blocks, preamble)
		}
		section = parent
		if doc.IsNested() {
			expectedNext = -1
		} else if book {
			expectedNext, expectedNextAlt = 1, 0
		}
	} else {
		section = p.initializeSection(reader, parent, blockMeta)
		title, hasTitle := blockMeta.attrs["title"]
		blockMeta = newMeta()
		if hasTitle {
			blockMeta.attrs["title"] = title
		}
		currentLevel = section.Level
		expectedNext = currentLevel + 1
		if currentLevel == 0 {
			part = book
		} else if currentLevel == 1 && section.Special {
			sectname = section.Sectname
			if sectname != "appendix" && sectname != "preface" && sectname != "abstract" {
				noNested = true
			}
		}
	}
	reader.skipBlankLines()

	for reader.hasMore() {
		p.parseBlockMetadataLines(reader, blockMeta, false)
		if nextLevel, ok := p.isNextLineSection(reader, blockMeta); ok {
			if v, ok := doc.Attributes["leveloffset"]; ok {
				off, _ := strconv.Atoi(v)
				nextLevel += off
				if nextLevel < 0 {
					nextLevel = 0
				}
			}
			switch {
			case nextLevel > currentLevel:
				if noNested {
					doc.Error(reader.lineInfo() + ": " + sectname + " sections do not support nested sections")
				} else if !(nextLevel == expectedNext || (expectedNextAlt >= 0 && nextLevel == expectedNextAlt) || expectedNext < 0) {
					cond := fmt.Sprintf("expected level %d", expectedNext)
					if expectedNextAlt >= 0 {
						cond = fmt.Sprintf("expected levels %d or %d", expectedNextAlt, expectedNext)
					}
					doc.Warn(fmt.Sprintf("%s: section title out of sequence: %s, got level %d", reader.lineInfo(), cond, nextLevel))
				}
				var newSection *Block
				newSection, blockMeta = p.nextSection(reader, section, blockMeta)
				section.assignNumeral(newSection)
				section.Blocks = append(section.Blocks, newSection)
			case nextLevel == 0 && section == doc.Block:
				if !book {
					doc.Error(reader.lineInfo() + ": level 0 sections can only be used when doctype is book")
				}
				var newSection *Block
				newSection, blockMeta = p.nextSection(reader, section, blockMeta)
				section.assignNumeral(newSection)
				section.Blocks = append(section.Blocks, newSection)
			default:
				goto done
			}
		} else {
			target := section
			if intro != nil {
				target = intro
			}
			if newBlock := p.nextBlock(reader, target, blockMeta, blockOpts{parseMetadata: false}); newBlock != nil {
				if part && intro == nil {
					// 部 (part) の直下の内容は partintro として扱う
					if newBlock.Context == "open" && (newBlock.Style == "partintro" || newBlock.Style == "open" || newBlock.Style == "") && len(section.Blocks) == 0 {
						newBlock.Style = "partintro"
						section.Blocks = append(section.Blocks, newBlock)
						blockMeta.clear()
						if reader.skipBlankLines() < 0 {
							break
						}
						continue
					}
					intro = NewBlock(section, "open")
					intro.ContentModel = "compound"
					intro.Style = "partintro"
					section.Blocks = append(section.Blocks, intro)
					target = intro
					newBlock.Parent = intro
				} else if part && intro != nil {
					newBlock.Parent = intro
				}
				target.Blocks = append(target.Blocks, newBlock)
				blockMeta.clear()
			}
		}
		if reader.skipBlankLines() < 0 {
			break
		}
	}
done:
	if part {
		if len(section.Blocks) == 0 || section.Blocks[len(section.Blocks)-1].Context != "section" {
			doc.Error(reader.lineInfo() + ": invalid part, must have at least one section (e.g., chapter, appendix, etc.)")
		}
	} else if preamble != nil {
		if len(preamble.Blocks) > 0 {
			if !book && len(doc.Blocks) < 2 {
				// セクションのない文書では preamble を展開する
				doc.Blocks = doc.Blocks[1:]
				for _, child := range preamble.Blocks {
					child.Parent = doc.Block
					doc.Blocks = append(doc.Blocks, child)
				}
			}
		} else {
			doc.Blocks = doc.Blocks[1:]
		}
	}
	if section == parent {
		return nil, blockMeta
	}
	return section, blockMeta
}

func (p *parser) initializeSection(reader *Reader, parent *Block, blockMeta *meta) *Block {
	doc := p.doc
	attrs := blockMeta.attrs
	doctype := doc.Doctype()
	book := doctype == "book"
	file, lineno := reader.File(), reader.Lineno()
	sectStyle := attrs["1"]
	id, reftext, title, level, _ := p.parseSectionTitle(reader, attrs["id"])
	sectName := "section"
	special, numbered := false, false
	if sectStyle != "" {
		switch {
		case book && sectStyle == "abstract":
			sectName, level = "chapter", 1
		case strings.HasPrefix(sectStyle, "sect") && sectionLevelStyleRx.Test(sectStyle):
			sectName = "section"
		default:
			sectName, special = sectStyle, true
			if level == 0 {
				level = 1
			}
			numbered = sectName == "appendix"
		}
	} else if book {
		switch {
		case level == 0:
			sectName = "part"
		case level > 1:
			sectName = "section"
		default:
			sectName = "chapter"
		}
	} else if doctype == "manpage" && strings.EqualFold(title, "synopsis") {
		sectName, special = "synopsis", true
	}
	if reftext != "" {
		attrs["reftext"] = reftext
	}
	section := NewBlock(parent, "section")
	section.Level = level
	section.ContentModel = "compound"
	section.ID = id
	section.RawTitle = title
	section.Sectname = sectName
	section.File, section.Lineno = file, lineno
	sectnums, hasSectnums := doc.Attributes["sectnums"]
	if special {
		section.Special = true
		if numbered {
			section.Numbered = true
		} else if hasSectnums && sectnums == "all" {
			section.Numbered = true
			section.NumberedChapter = book && level == 1
		}
	} else if hasSectnums && level > 0 {
		section.Numbered = true
	} else if book && level == 0 && doc.HasAttr("partnums") {
		section.Numbered = true
	}
	if section.Special && !numbered && hasSectnums && sectnums != "all" && level > 1 {
		// special セクションの入れ子は親の番号付けに従う
		section.Numbered = parent.Numbered
	}
	generated := false
	if section.ID == "" && doc.HasAttr("sectids") {
		section.ID = generateSectionID(section.Title(), doc)
		generated = true
	}
	if section.ID != "" {
		if !generated && strings.Contains(title, "{") {
			section.Title()
		}
		if !doc.Register(section.ID, &Ref{Block: section}) {
			doc.Warn(fmt.Sprintf("%s: id assigned to section already in use: %s", doc.location(file, lineno), section.ID))
		}
	}
	p.updateAttributes(section, attrs)
	reader.skipBlankLines()
	return section
}

// updateAttributes はメタデータの属性をブロックに反映します。
func (p *parser) updateAttributes(b *Block, attrs map[string]string) {
	for k, v := range attrs {
		b.Attrs[k] = v
		if strings.HasSuffix(k, "-option") {
			b.Options[strings.TrimSuffix(k, "-option")] = true
		}
	}
	if v, ok := attrs["id"]; ok && b.ID == "" {
		b.ID = v
	}
}

// GenerateSectionID はタイトルからセクション ID を生成します。
func GenerateSectionID(title string, doc *Document) string { return generateSectionID(title, doc) }

func generateSectionID(title string, doc *Document) string {
	attrs := doc.Attributes
	pre := "_"
	if v, ok := attrs["idprefix"]; ok {
		pre = v
	}
	sep, sepSub := "_", " _.-"
	noSep := false
	if v, ok := attrs["idseparator"]; ok {
		if v == "" {
			noSep = true
			sep = ""
		} else {
			sep = firstChar(v)
			if sep == "-" || sep == "." {
				sepSub = " .-"
			} else {
				sepSub = " " + sep + ".-"
			}
		}
	}
	genID := pre + invalidSectionIDCharsRx.Gsub(strings.ToLower(title), func(*M) string { return "" })
	if noSep {
		genID = strings.ReplaceAll(genID, " ", "")
	} else {
		// 区切り文字に置き換え、連続する区切り文字をまとめる (String#tr_s 相当)
		var b strings.Builder
		prevSub := false
		for _, r := range genID {
			if strings.ContainsRune(sepSub, r) {
				if !prevSub {
					b.WriteString(sep)
				}
				prevSub = true
			} else {
				b.WriteRune(r)
				prevSub = false
			}
		}
		genID = b.String()
		genID = strings.TrimSuffix(genID, sep)
		if pre == "" && strings.HasPrefix(genID, sep) {
			genID = genID[len(sep):]
		}
	}
	if doc.Ref(genID) != nil {
		for cnt := 2; ; cnt++ {
			candidate := genID + sep + strconv.Itoa(cnt)
			if doc.Ref(candidate) == nil {
				return candidate
			}
		}
	}
	return genID
}
