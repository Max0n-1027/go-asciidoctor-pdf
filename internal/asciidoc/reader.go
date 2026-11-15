package asciidoc

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type callouts struct {
	lists     [][]calloutEntry
	listIndex int
	coIndex   int
}

type calloutEntry struct {
	ordinal int
	id      string
}

func newCallouts() *callouts {
	c := &callouts{}
	c.nextList()
	return c
}

func (c *callouts) current() *[]calloutEntry { return &c.lists[c.listIndex-1] }

func (c *callouts) register(ordinal int) string {
	id := fmt.Sprintf("CO%d-%d", c.listIndex, c.coIndex)
	list := c.current()
	*list = append(*list, calloutEntry{ordinal, id})
	c.coIndex++
	return id
}

func (c *callouts) readNextID() string {
	id := ""
	list := *c.current()
	if c.coIndex <= len(list) {
		id = list[c.coIndex-1].id
	}
	c.coIndex++
	return id
}

func (c *callouts) calloutIDs(ordinal int) string {
	var ids []string
	for _, e := range *c.current() {
		if e.ordinal == ordinal {
			ids = append(ids, e.id)
		}
	}
	return strings.Join(ids, " ")
}

func (c *callouts) nextList() {
	c.listIndex++
	if len(c.lists) < c.listIndex {
		c.lists = append(c.lists, nil)
	}
	c.coIndex = 1
}

func (c *callouts) rewind() {
	c.listIndex = 1
	c.coIndex = 1
}

type frame struct {
	lines  []string
	pos    int
	file   string // 表示用パス
	dir    string
	lineno int // 次に読む行の番号
}

type conditional struct {
	target   string
	skip     bool
	skipping bool
}

// Reader は前処理 (include / 条件付きディレクティブ) を行いながら行を供給します。
type Reader struct {
	doc          *Document
	cur          *frame
	stack        []*frame
	conditionals []conditional
	skipping     bool
	process      bool
	// lookAhead は cur.pos の位置から何行分が前処理済みかを表します
	lookAhead int
	maxDepth  int
	includes  map[string]bool
}

func prepareLines(data string) []string {
	data = strings.TrimPrefix(data, "\xEF\xBB\xBF")
	data = strings.ReplaceAll(data, "\r\n", "\n")
	lines := strings.Split(data, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	// 末尾の空行を取り除く
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func newReader(doc *Document, lines []string, file, dir string) *Reader {
	return &Reader{doc: doc, cur: &frame{lines: lines, file: file, dir: dir, lineno: 1}, process: true, maxDepth: 64, includes: map[string]bool{}}
}

// Lineno は次に読む行の番号を返します。
func (r *Reader) Lineno() int { return r.cur.lineno }

// File は現在のファイル名を返します。
func (r *Reader) File() string { return r.cur.file }

func (r *Reader) lineInfo() string {
	f := r.cur.file
	if f == "" {
		f = "<stdin>"
	}
	return fmt.Sprintf("%s: line %d", f, r.cur.lineno)
}

func (r *Reader) prevLineInfo() string {
	f := r.cur.file
	if f == "" {
		f = "<stdin>"
	}
	return fmt.Sprintf("%s: line %d", f, r.cur.lineno-1)
}

func (r *Reader) warn(msg string)  { r.doc.Warn(r.lineInfo() + ": " + msg) }
func (r *Reader) error(msg string) { r.doc.Error(r.lineInfo() + ": " + msg) }

// peek は次の行を返します (消費しません)。
func (r *Reader) peek() (string, bool) {
	for {
		if r.cur.pos >= len(r.cur.lines) {
			if len(r.stack) == 0 {
				return "", false
			}
			r.cur = r.stack[len(r.stack)-1]
			r.stack = r.stack[:len(r.stack)-1]
			r.lookAhead = 0
			continue
		}
		if r.lookAhead > 0 || !r.process {
			return r.cur.lines[r.cur.pos], true
		}
		line := r.cur.lines[r.cur.pos]
		if r.processLine(line) {
			r.lookAhead = 1
			return r.cur.lines[r.cur.pos], true
		}
	}
}

// processLine は行を前処理します。現在位置の行をそのまま返せる状態なら true、
// 行が消費された (もしくは再度前処理が必要な) 場合は false を返します。
func (r *Reader) processLine(line string) bool {
	if strings.HasSuffix(line, "]") && strings.Contains(line, "::") {
		if strings.Contains(line, "if") {
			if m := conditionalDirectiveRx.Match(line); m != nil {
				if m.Has(1) {
					if r.skipping {
						r.shift()
						return false
					}
					// エスケープされたディレクティブ
					r.cur.lines[r.cur.pos] = line[1:]
					return true
				}
				return r.preprocessConditional(m.S(2), m.S(3), m.S(4), m.S(5), m.Has(5))
			}
		}
		if r.skipping {
			r.shift()
			return false
		}
		if strings.HasPrefix(line, "inc") || strings.HasPrefix(line, "\\inc") {
			if m := includeDirectiveRx.Match(line); m != nil {
				if m.Has(1) {
					r.cur.lines[r.cur.pos] = line[1:]
					return true
				}
				return r.preprocessInclude(m.S(2), m.S(3))
			}
		}
		return true
	}
	if r.skipping {
		r.shift()
		return false
	}
	return true
}

// shift は現在行を消費します。
func (r *Reader) shift() {
	r.cur.pos++
	r.cur.lineno++
	if r.lookAhead > 0 {
		r.lookAhead--
	}
}

// read は次の行を読み取ります。
func (r *Reader) read() (string, bool) {
	line, ok := r.peek()
	if !ok {
		return "", false
	}
	r.shift()
	return line, true
}

func (r *Reader) advance() bool {
	_, ok := r.read()
	return ok
}

// unshift は行を先頭に戻します (前処理済みとして扱います)。
func (r *Reader) unshift(line string) {
	f := r.cur
	if f.pos > 0 {
		f.pos--
		f.lines[f.pos] = line
	} else {
		f.lines = append([]string{line}, f.lines...)
	}
	f.lineno--
	r.lookAhead++
}

func (r *Reader) unshiftAll(lines []string) {
	for i := len(lines) - 1; i >= 0; i-- {
		r.unshift(lines[i])
	}
}

// replaceNext は次の行を置き換えます。
func (r *Reader) replaceNext(line string) {
	r.cur.lines[r.cur.pos] = line
}

func (r *Reader) hasMore() bool {
	_, ok := r.peek()
	return ok
}

// nextLineEmpty は次の行が空 (または入力終端) かを返します。
func (r *Reader) nextLineEmpty() bool {
	line, ok := r.peek()
	return !ok || line == ""
}

// skipBlankLines は空行を読み飛ばし、読み飛ばした行数を返します。入力終端なら -1 を返します。
func (r *Reader) skipBlankLines() int {
	n := 0
	for {
		line, ok := r.peek()
		if !ok {
			if n == 0 {
				return -1
			}
			return n
		}
		if line != "" {
			return n
		}
		r.shift()
		n++
	}
}

// skipCommentLines は行コメントとコメントブロックを読み飛ばします。
func (r *Reader) skipCommentLines() {
	for {
		line, ok := r.peek()
		if !ok || line == "" || !strings.HasPrefix(line, "//") {
			return
		}
		if strings.HasPrefix(line, "///") {
			if len(line) > 3 && line == strings.Repeat("/", len(line)) {
				r.shift()
				r.readLinesUntil(readOpts{terminator: line, skipProcessing: true, skipFirstLine: false})
			} else {
				return
			}
		} else {
			r.shift()
		}
	}
}

type readOpts struct {
	terminator              string
	breakOnBlankLines       bool
	breakOnListContinuation bool
	skipLineComments        bool
	skipProcessing          bool
	preserveLastLine        bool
	readLastLine            bool
	skipFirstLine           bool
	// breakIf が true を返した行で読み取りを止めます
	breakIf func(line string) bool
	// context は未終了ブロックの警告に使う名前
	context string
}

// readLinesUntil は条件を満たすまで行を読み取ります。
func (r *Reader) readLinesUntil(o readOpts) []string {
	var result []string
	restore := r.process
	if o.skipProcessing {
		r.process = false
	}
	startLine := r.cur.lineno
	startFile := r.cur.file
	terminated := false
	lineRead := false
	if o.skipFirstLine {
		r.shift()
	}
	for {
		line, ok := r.read()
		if !ok {
			break
		}
		stop, preserve := false, o.preserveLastLine
		if o.terminator != "" {
			stop = line == o.terminator
		} else {
			switch {
			case o.breakOnBlankLines && line == "":
				stop = true
			case o.breakOnListContinuation && lineRead && line == "+":
				stop, preserve = true, true
			case o.breakIf != nil && o.breakIf(line):
				stop = true
			}
		}
		if stop {
			terminated = true
			if o.readLastLine {
				result = append(result, line)
			}
			if preserve {
				r.unshift(line)
			}
			break
		}
		if o.skipLineComments && strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "///") {
			continue
		}
		result = append(result, line)
		lineRead = true
	}
	r.process = restore
	if o.terminator != "" && !terminated && o.context != "" {
		f := startFile
		if f == "" {
			f = "<stdin>"
		}
		r.doc.Warn(fmt.Sprintf("%s: line %d: unterminated %s block", f, startLine-1, o.context))
	}
	return result
}

// ---------------------------------------------------------------------------
// 条件付きディレクティブ

func (r *Reader) preprocessConditional(keyword, target, delimiter, text string, hasText bool) bool {
	noTarget := target == ""
	target = strings.ToLower(target)
	attrs := r.doc.Attributes
	has := func(name string) bool {
		_, ok := attrs[name]
		return ok
	}
	if keyword == "endif" {
		switch {
		case hasText:
			r.error("malformed preprocessor directive - text not permitted: endif::" + target + "[" + text + "]")
		case len(r.conditionals) == 0:
			r.error("unmatched preprocessor directive: endif::" + target + "[]")
		case noTarget || target == r.conditionals[len(r.conditionals)-1].target:
			r.conditionals = r.conditionals[:len(r.conditionals)-1]
			if len(r.conditionals) == 0 {
				r.skipping = false
			} else {
				r.skipping = r.conditionals[len(r.conditionals)-1].skipping
			}
		default:
			r.error("mismatched preprocessor directive: endif::" + target + "[], expected endif::" + r.conditionals[len(r.conditionals)-1].target + "[]")
		}
		r.shift()
		return false
	}
	skip := false
	if !r.skipping {
		switch keyword {
		case "ifdef":
			if noTarget {
				r.error("malformed preprocessor directive - missing target: ifdef::[" + text + "]")
				r.shift()
				return false
			}
			switch delimiter {
			case ",":
				skip = true
				for _, name := range strings.Split(target, ",") {
					if has(name) {
						skip = false
						break
					}
				}
			case "+":
				for _, name := range strings.Split(target, "+") {
					if !has(name) {
						skip = true
						break
					}
				}
			default:
				skip = !has(target)
			}
		case "ifndef":
			if noTarget {
				r.error("malformed preprocessor directive - missing target: ifndef::[" + text + "]")
				r.shift()
				return false
			}
			switch delimiter {
			case ",":
				for _, name := range strings.Split(target, ",") {
					if has(name) {
						skip = true
						break
					}
				}
			case "+":
				skip = true
				for _, name := range strings.Split(target, "+") {
					if !has(name) {
						skip = false
						break
					}
				}
			default:
				skip = has(target)
			}
		case "ifeval":
			if !noTarget || !hasText {
				r.error("malformed preprocessor directive - target not permitted or missing expression: ifeval::" + target + "[" + text + "]")
				r.shift()
				return false
			}
			m := evalExpressionRx.Match(strings.TrimSpace(text))
			if m == nil {
				r.error("malformed preprocessor directive - invalid expression: ifeval::[" + text + "]")
				r.shift()
				return false
			}
			skip = !r.evalExpr(m.S(1), m.S(2), m.S(3))
		}
	}
	if keyword == "ifeval" || !hasText {
		if skip {
			r.skipping = true
		}
		r.conditionals = append(r.conditionals, conditional{target: target, skip: skip, skipping: r.skipping})
		r.shift()
		return false
	}
	// 1 行形式の条件付き取り込み
	if r.skipping || skip {
		r.shift()
		return false
	}
	text = strings.TrimRight(text, " \t")
	r.replaceNext(text)
	// 置き換えた行が include ディレクティブなら再度前処理する
	return !strings.HasPrefix(text, "include::")
}

func (r *Reader) resolveExprVal(val string) any {
	quoted := false
	if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") && len(val) >= 2) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") && len(val) >= 2) {
		quoted = true
		val = val[1 : len(val)-1]
	}
	if strings.Contains(val, "{") {
		val = r.doc.subAttributesOpts(val, "")
	}
	if quoted {
		return val
	}
	switch {
	case val == "":
		return nil
	case val == "true":
		return true
	case val == "false":
		return false
	case strings.TrimSpace(val) == "":
		return " "
	case strings.Contains(val, "."):
		f, _ := strconv.ParseFloat(val, 64)
		return f
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0.0
	}
	return float64(n)
}

func (r *Reader) evalExpr(lhs, op, rhs string) bool {
	l, rv := r.resolveExprVal(lhs), r.resolveExprVal(rhs)
	switch a := l.(type) {
	case float64:
		b, ok := rv.(float64)
		if !ok {
			return op == "!="
		}
		switch op {
		case "==":
			return a == b
		case "!=":
			return a != b
		case "<":
			return a < b
		case "<=":
			return a <= b
		case ">":
			return a > b
		case ">=":
			return a >= b
		}
	case string:
		b, ok := rv.(string)
		if !ok {
			return op == "!="
		}
		switch op {
		case "==":
			return a == b
		case "!=":
			return a != b
		case "<":
			return a < b
		case "<=":
			return a <= b
		case ">":
			return a > b
		case ">=":
			return a >= b
		}
	default:
		switch op {
		case "==":
			return l == rv
		case "!=":
			return l != rv
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// include ディレクティブ

func isURI(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "ftp://") || strings.HasPrefix(s, "file://")
}

// preprocessInclude は include ディレクティブを処理します。
// 現在位置の行をそのまま返せる状態なら true、行が消費されたなら false を返します。
func (r *Reader) preprocessInclude(target, attrlist string) bool {
	doc := r.doc
	expanded := target
	if strings.Contains(target, "{") {
		expanded = doc.subAttributesOpts(target, "drop-line")
		if expanded == "" {
			if doc.AttrOr("attribute-missing", "skip") == "skip" {
				r.replaceNext("Unresolved directive in " + r.cur.file + " - include::" + target + "[" + attrlist + "]")
				return true
			}
			r.shift()
			return false
		}
	}
	if doc.Safe >= SafeSecure {
		r.replaceNext("link:" + expanded + "[]")
		return true
	}
	if len(r.stack) >= r.maxDepth {
		r.error(fmt.Sprintf("maximum include depth of %d exceeded", r.maxDepth))
		return true
	}
	attrs := map[string]string{}
	if attrlist != "" {
		if strings.Contains(attrlist, "{") {
			attrlist = doc.SubAttributes(attrlist)
		}
		doc.ParseAttributes(attrlist, nil, nil, attrs)
	}
	_, optional := attrs["optional-option"]
	var data []byte
	var incFile, incDir string
	if isURI(expanded) || isURI(r.cur.dir) {
		if !doc.HasAttr("allow-uri-read") {
			r.replaceNext("link:" + expanded + "[" + attrlist + "]")
			return true
		}
		uri := expanded
		if !isURI(uri) {
			uri = strings.TrimSuffix(r.cur.dir, "/") + "/" + expanded
		}
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(uri)
		if err == nil && resp.StatusCode == 200 {
			data, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		} else if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		if err != nil {
			r.error("include uri not readable: " + uri)
			r.replaceNext("Unresolved directive in " + r.cur.file + " - include::" + target + "[" + attrlist + "]")
			return true
		}
		incFile = uri
		if i := strings.LastIndex(uri, "/"); i >= 0 {
			incDir = uri[:i]
		}
	} else {
		p := expanded
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.cur.dir, p)
		}
		p = filepath.Clean(p)
		if doc.Safe >= SafeSafe && doc.BaseDir != "" {
			if rel, err := filepath.Rel(doc.BaseDir, p); err != nil || strings.HasPrefix(rel, "..") {
				r.warn("include file is outside of jail; recovering automatically")
				p = filepath.Join(doc.BaseDir, filepath.Base(p))
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			if optional {
				doc.Info(r.lineInfo() + ": optional include dropped because include file not found: " + p)
				r.shift()
				return false
			}
			r.error("include file not found: " + p)
			r.replaceNext("Unresolved directive in " + r.cur.file + " - include::" + target + "[" + attrlist + "]")
			return true
		}
		data = b
		incDir = filepath.Dir(p)
		incFile = expanded
		if base := doc.BaseDir; base != "" {
			if rel, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(rel, "..") {
				incFile = filepath.ToSlash(rel)
			}
		}
		r.includes[strings.TrimSuffix(filepath.ToSlash(incFile), filepath.Ext(incFile))] = true
	}
	lines := prepareLines(string(data))
	startLine := 1
	if v, ok := attrs["lines"]; ok && v != "" {
		lines, startLine = selectLines(lines, v)
	} else if v, ok := attrs["tag"]; ok {
		if v != "" && v != "!" {
			tags := map[string]bool{}
			if strings.HasPrefix(v, "!") {
				tags[v[1:]] = false
			} else {
				tags[v] = true
			}
			lines = r.selectTags(lines, tags, incFile)
		}
	} else if v, ok := attrs["tags"]; ok && v != "" {
		tags := map[string]bool{}
		sep := ","
		if strings.Contains(v, ";") && !strings.Contains(v, ",") {
			sep = ";"
		}
		for _, t := range strings.Split(v, sep) {
			t = strings.TrimSpace(t)
			if t == "" || t == "!" {
				continue
			}
			if strings.HasPrefix(t, "!") {
				tags[t[1:]] = false
			} else {
				tags[t] = true
			}
		}
		if len(tags) > 0 {
			lines = r.selectTags(lines, tags, incFile)
		}
	}
	if v, ok := attrs["indent"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			lines = adjustIndentation(lines, n, doc.tabSize())
		}
	}
	r.shift()
	if v, ok := attrs["leveloffset"]; ok {
		// leveloffset を include の範囲だけに適用する
		prev, had := doc.Attributes["leveloffset"]
		restoreLine := ":leveloffset!:"
		if had {
			restoreLine = ":leveloffset: " + prev
		}
		lines = append([]string{":leveloffset: " + v, ""}, lines...)
		lines = append(lines, "", restoreLine)
		startLine -= 2
	}
	if len(lines) == 0 {
		return false
	}
	r.stack = append(r.stack, r.cur)
	r.cur = &frame{lines: lines, file: incFile, dir: incDir, lineno: startLine}
	r.lookAhead = 0
	return false
}

func (d *Document) tabSize() int {
	if v, ok := d.Attributes["tabsize"]; ok {
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func selectLines(lines []string, spec string) ([]string, int) {
	sep := ","
	if strings.Contains(spec, ";") {
		sep = ";"
	}
	include := make([]bool, len(lines)+1)
	for _, part := range strings.Split(spec, sep) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "..") {
			from, to, _ := strings.Cut(part, "..")
			f, _ := strconv.Atoi(from)
			t := len(lines)
			if to != "" && to != "-1" {
				t, _ = strconv.Atoi(to)
			}
			if f < 1 {
				f = 1
			}
			for i := f; i <= t && i <= len(lines); i++ {
				include[i] = true
			}
		} else if n, err := strconv.Atoi(part); err == nil && n >= 1 && n <= len(lines) {
			include[n] = true
		}
	}
	var out []string
	first := 0
	for i, l := range lines {
		if include[i+1] {
			if first == 0 {
				first = i + 1
			}
			out = append(out, l)
		}
	}
	if first == 0 {
		first = 1
	}
	return out, first
}

func (r *Reader) selectTags(lines []string, tags map[string]bool, file string) []string {
	// ワイルドカードの解釈は Asciidoctor の PreprocessorReader#read_lines_with_tags に準拠
	var selectDefault, baseSelect bool
	wildcard, hasWildcard := false, false
	if v, ok := tags["**"]; ok {
		delete(tags, "**")
		selectDefault, baseSelect = v, v
		if w, ok := tags["*"]; ok {
			delete(tags, "*")
			wildcard, hasWildcard = w, true
		} else if !v {
			allTrue := true
			for _, tv := range tags {
				if !tv {
					allTrue = false
				}
			}
			if allTrue && len(tags) > 0 {
				wildcard, hasWildcard = true, true
			}
		}
	} else if w, ok := tags["*"]; ok {
		delete(tags, "*")
		wildcard, hasWildcard = w, true
		selectDefault, baseSelect = !w, !w
	} else {
		anyFalse := false
		for _, tv := range tags {
			if !tv {
				anyFalse = true
			}
		}
		selectDefault, baseSelect = anyFalse, anyFalse
	}
	sel := selectDefault
	type stackEntry struct {
		tag string
		sel bool
	}
	var tagStack []stackEntry
	activeTag := ""
	found := map[string]bool{}
	var out []string
	for _, line := range lines {
		if strings.HasSuffix(line, "[]") || strings.Contains(line, "[] ") {
			if strings.Contains(line, "::") {
				if m := tagDirectiveRx.Match(line); m != nil {
					thisTag := m.S(2)
					if m.Has(1) {
						// end::tag[]
						if thisTag == activeTag {
							tagStack = tagStack[:len(tagStack)-1]
							if len(tagStack) == 0 {
								activeTag, sel = "", baseSelect
							} else {
								activeTag, sel = tagStack[len(tagStack)-1].tag, tagStack[len(tagStack)-1].sel
							}
						} else if _, known := tags[thisTag]; known {
							r.doc.Warn(fmt.Sprintf("%s: mismatched or unexpected end tag (expected '%s' but found '%s') in include file", file, activeTag, thisTag))
						}
						continue
					}
					if v, known := tags[thisTag]; known {
						found[thisTag] = true
						sel = v
						activeTag = thisTag
						tagStack = append(tagStack, stackEntry{thisTag, sel})
					} else if hasWildcard {
						if sel && !wildcard {
							sel = false
						} else if !sel || len(tagStack) == 0 || tagStack[len(tagStack)-1].sel {
							sel = wildcard
						}
						activeTag = thisTag
						tagStack = append(tagStack, stackEntry{thisTag, sel})
					}
					continue
				}
			}
		}
		if sel {
			out = append(out, line)
		}
	}
	for tag, want := range tags {
		if want && !found[tag] {
			r.doc.Warn(fmt.Sprintf("%s: tag '%s' not found in include file: %s", r.prevLineInfo(), tag, file))
		}
	}
	return out
}

// adjustIndentation はブロックのインデントを調整します (indent 属性)。
func adjustIndentation(lines []string, indentSize, tabSize int) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)
	if tabSize > 0 {
		for i, line := range out {
			if !strings.Contains(line, "\t") {
				continue
			}
			var b strings.Builder
			col := 0
			for _, ch := range line {
				if ch == '\t' {
					n := tabSize - col%tabSize
					b.WriteString(strings.Repeat(" ", n))
					col += n
				} else {
					b.WriteRune(ch)
					col++
				}
			}
			out[i] = b.String()
		}
	}
	if indentSize < 0 {
		return out
	}
	blockIndent := -1
	for _, line := range out {
		if line == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if n == 0 {
			blockIndent = 0
			break
		}
		if blockIndent < 0 || n < blockIndent {
			blockIndent = n
		}
	}
	if blockIndent < 0 {
		blockIndent = 0
	}
	pad := strings.Repeat(" ", indentSize)
	for i, line := range out {
		if line == "" {
			continue
		}
		if blockIndent > 0 && len(line) >= blockIndent {
			line = line[blockIndent:]
		}
		out[i] = pad + line
	}
	return out
}
