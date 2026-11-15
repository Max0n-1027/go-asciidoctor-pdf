package pdf

import (
	"bytes"
	"compress/flate"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 既存の PDF を読み込むための型です。
// Name / String / Array / Dict / Ref / *Stream のほか、nil・bool・int64・float64 を値として扱います。
type (
	// Name は PDF の名前オブジェクトです (先頭の / は含みません)。
	Name string
	// String は PDF の文字列オブジェクトです (バイト列)。
	String []byte
	// Array は PDF の配列です。
	Array []any
	// Dict は PDF の辞書です。
	Dict map[Name]any
	// Ref は間接参照です。
	Ref struct{ Num, Gen int }
)

// Stream はストリームオブジェクトです。Data はフィルターを適用したままの生データです (暗号化は解除済み)。
type Stream struct {
	Dict Dict
	Data []byte
}

// keyword は値ではない字句 (obj / endobj / stream / ] / >> など) です。
type keyword string

// ErrEncrypted はパスワードなしでは読めない暗号化 PDF のエラーです。
var ErrEncrypted = errors.New("the PDF is encrypted and requires a password")

type lexer struct {
	b   []byte
	pos int
}

func isSpace(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (l *lexer) skip() {
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		switch {
		case isSpace(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.b) && l.b[l.pos] != '\n' && l.b[l.pos] != '\r' {
				l.pos++
			}
		default:
			return
		}
	}
}

// regular は区切り文字・空白以外が続く字句を読み取ります。
func (l *lexer) regular() string {
	start := l.pos
	for l.pos < len(l.b) && !isSpace(l.b[l.pos]) && !isDelim(l.b[l.pos]) {
		l.pos++
	}
	return string(l.b[start:l.pos])
}

func decodeName(s string) string {
	if !strings.Contains(s, "#") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isInteger(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// object は次の値を読み取ります。
func (l *lexer) object(depth int) (any, error) {
	if depth > 200 {
		return nil, errors.New("objects are nested too deeply")
	}
	l.skip()
	if l.pos >= len(l.b) {
		return nil, io.ErrUnexpectedEOF
	}
	c := l.b[l.pos]
	switch c {
	case '/':
		l.pos++
		return Name(decodeName(l.regular())), nil
	case '(':
		l.pos++
		return l.literal()
	case '<':
		if l.pos+1 < len(l.b) && l.b[l.pos+1] == '<' {
			l.pos += 2
			return l.dict(depth)
		}
		l.pos++
		return l.hexString()
	case '[':
		l.pos++
		arr := Array{}
		for {
			v, err := l.object(depth + 1)
			if err != nil {
				return arr, err
			}
			if kw, ok := v.(keyword); ok {
				if kw == "]" {
					return arr, nil
				}
				return arr, fmt.Errorf("unexpected %q in array", string(kw))
			}
			arr = append(arr, v)
		}
	case '>':
		if l.pos+1 < len(l.b) && l.b[l.pos+1] == '>' {
			l.pos += 2
			return keyword(">>"), nil
		}
		l.pos++
		return keyword(">"), nil
	case ']', ')', '{', '}':
		l.pos++
		return keyword(string(c)), nil
	}
	tok := l.regular()
	if tok == "" {
		l.pos++
		return keyword(string(c)), nil
	}
	switch tok {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9') {
		if isInteger(tok) {
			n, err := strconv.ParseInt(tok, 10, 64)
			if err != nil {
				f, _ := strconv.ParseFloat(tok, 64)
				return f, nil
			}
			// "n g R" 形式の間接参照かどうかを確認する
			save := l.pos
			l.skip()
			gen := l.regular()
			if isInteger(gen) {
				l.skip()
				if l.pos < len(l.b) && l.b[l.pos] == 'R' && (l.pos+1 == len(l.b) || isSpace(l.b[l.pos+1]) || isDelim(l.b[l.pos+1])) {
					l.pos++
					g, _ := strconv.Atoi(gen)
					return Ref{Num: int(n), Gen: g}, nil
				}
			}
			l.pos = save
			return n, nil
		}
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return f, nil
		}
		// "--5" や "4.-2" のような不正な数値は 0 として扱う (pdf.js と同じ)
		if strings.Trim(tok, "+-.0123456789") == "" {
			return int64(0), nil
		}
	}
	return keyword(tok), nil
}

func (l *lexer) dict(depth int) (Dict, error) {
	d := Dict{}
	for {
		k, err := l.object(depth + 1)
		if err != nil {
			return d, err
		}
		key, ok := k.(Name)
		if !ok {
			if kw, isKw := k.(keyword); isKw && kw == ">>" {
				return d, nil
			}
			// キーではない値は読み飛ばす
			continue
		}
		v, err := l.object(depth + 1)
		if err != nil {
			return d, err
		}
		if kw, isKw := v.(keyword); isKw {
			if kw == ">>" {
				d[key] = nil
				return d, nil
			}
			continue
		}
		d[key] = v
	}
}

func (l *lexer) literal() (String, error) {
	var out []byte
	depth := 1
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		l.pos++
		switch c {
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return out, nil
			}
			out = append(out, c)
		case '\\':
			if l.pos >= len(l.b) {
				return out, nil
			}
			e := l.b[l.pos]
			l.pos++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if l.pos < len(l.b) && l.b[l.pos] == '\n' {
					l.pos++
				}
			case '\n':
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for i := 0; i < 2 && l.pos < len(l.b) && l.b[l.pos] >= '0' && l.b[l.pos] <= '7'; i++ {
						v = v*8 + int(l.b[l.pos]-'0')
						l.pos++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		case '\r':
			out = append(out, '\n')
			if l.pos < len(l.b) && l.b[l.pos] == '\n' {
				l.pos++
			}
		default:
			out = append(out, c)
		}
	}
	return out, io.ErrUnexpectedEOF
}

func (l *lexer) hexString() (String, error) {
	var digits []byte
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		l.pos++
		if c == '>' {
			if len(digits)%2 == 1 {
				digits = append(digits, '0')
			}
			out := make([]byte, len(digits)/2)
			_, err := hex.Decode(out, digits)
			return out, err
		}
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			digits = append(digits, c)
		}
	}
	return nil, io.ErrUnexpectedEOF
}

// ---------------------------------------------------------------------------
// Reader

type xrefEntry struct {
	typ      byte // 0: 空き, 1: ファイル内, 2: オブジェクトストリーム内
	off      int
	stm, idx int
	gen      int
}

// Reader は既存の PDF ファイルを読み込みます。
type Reader struct {
	data    []byte
	version string
	xref    map[int]xrefEntry
	trailer Dict
	cache   map[int]any
	busy    map[int]bool
	stms    map[int]map[int]any
	offsets map[int]int
	crypt   *decrypter
	pages   []Dict
	walked  bool
}

var headerVersionRx = regexp.MustCompile(`^%PDF-(\d\.\d)`)

// OpenReader は PDF のデータを解析します。
func OpenReader(data []byte) (*Reader, error) {
	limit := len(data)
	if limit > 1024 {
		limit = 1024
	}
	start := bytes.Index(data[:limit], []byte("%PDF-"))
	if start < 0 {
		return nil, errors.New("not a PDF file")
	}
	r := &Reader{data: data[start:], xref: map[int]xrefEntry{}, cache: map[int]any{}, busy: map[int]bool{}, stms: map[int]map[int]any{}}
	r.version = "1.4"
	if m := headerVersionRx.FindSubmatch(r.data); m != nil {
		r.version = string(m[1])
	}
	if err := r.loadXrefChain(); err != nil || r.trailer["Root"] == nil {
		r.reconstruct()
	}
	if _, ok := r.resolve(r.trailer["Root"]).(Dict); !ok {
		r.reconstruct()
		if _, ok := r.resolve(r.trailer["Root"]).(Dict); !ok {
			return nil, errors.New("the PDF has no document catalog")
		}
	}
	if enc := r.trailer["Encrypt"]; enc != nil {
		encNum := -1
		if ref, ok := enc.(Ref); ok {
			encNum = ref.Num
		}
		encDict, _ := r.resolve(enc).(Dict)
		if encDict == nil {
			return nil, errors.New("the PDF has an invalid encryption dictionary")
		}
		dc, err := newDecrypter(r, encDict, encNum)
		if err != nil {
			return nil, err
		}
		// 復号前に読み込んだオブジェクトは破棄する
		r.crypt = dc
		r.cache = map[int]any{}
		r.stms = map[int]map[int]any{}
	}
	// カタログのバージョン指定はヘッダーより優先される
	if root, ok := r.resolve(r.trailer["Root"]).(Dict); ok {
		if v, ok := root["Version"].(Name); ok && string(v) > r.version {
			r.version = string(v)
		}
	}
	return r, nil
}

// Version は PDF のバージョン (例: "1.7") を返します。
func (r *Reader) Version() string { return r.version }

func (r *Reader) loadXrefChain() error {
	sx := bytes.LastIndex(r.data, []byte("startxref"))
	if sx < 0 {
		return errors.New("startxref not found")
	}
	l := &lexer{b: r.data, pos: sx + 9}
	v, err := l.object(0)
	off, ok := v.(int64)
	if err != nil || !ok {
		return errors.New("invalid startxref")
	}
	seen := map[int]bool{}
	queue := []int{int(off)}
	first := true
	for len(queue) > 0 {
		o := queue[0]
		queue = queue[1:]
		if o <= 0 || o >= len(r.data) || seen[o] {
			continue
		}
		seen[o] = true
		trailer, err := r.loadXrefSection(o)
		if err != nil {
			return err
		}
		if first {
			r.trailer = trailer
			first = false
		} else {
			for k, v := range trailer {
				if _, has := r.trailer[k]; !has && k != "Prev" && k != "XRefStm" {
					r.trailer[k] = v
				}
			}
		}
		if xs, ok := trailer["XRefStm"].(int64); ok {
			queue = append([]int{int(xs)}, queue...)
		}
		if p, ok := trailer["Prev"].(int64); ok {
			queue = append(queue, int(p))
		}
	}
	if len(r.xref) == 0 || r.trailer == nil {
		return errors.New("empty cross-reference table")
	}
	return nil
}

func (r *Reader) loadXrefSection(off int) (Dict, error) {
	l := &lexer{b: r.data, pos: off}
	l.skip()
	if bytes.HasPrefix(r.data[l.pos:], []byte("xref")) {
		l.pos += 4
		for {
			l.skip()
			tok := l.regular()
			if tok == "trailer" {
				break
			}
			start, err1 := strconv.Atoi(tok)
			l.skip()
			count, err2 := strconv.Atoi(l.regular())
			if err1 != nil || err2 != nil || count < 0 {
				return nil, errors.New("invalid cross-reference table")
			}
			for i := 0; i < count; i++ {
				l.skip()
				o, _ := strconv.Atoi(l.regular())
				l.skip()
				g, _ := strconv.Atoi(l.regular())
				l.skip()
				t := l.regular()
				num := start + i
				if _, exists := r.xref[num]; exists {
					continue
				}
				if t == "n" && o > 0 {
					r.xref[num] = xrefEntry{typ: 1, off: o, gen: g}
				} else {
					r.xref[num] = xrefEntry{typ: 0}
				}
			}
		}
		v, err := l.object(0)
		d, ok := v.(Dict)
		if err != nil || !ok {
			return nil, errors.New("invalid trailer")
		}
		return d, nil
	}
	// 相互参照ストリーム (PDF 1.5 以降)
	obj, err := r.parseIndirectAt(off, -1)
	if err != nil {
		return nil, err
	}
	s, ok := obj.(*Stream)
	if !ok || s.Dict["Type"] != Name("XRef") {
		return nil, errors.New("invalid cross-reference stream")
	}
	data, err := r.Decode(s)
	if err != nil {
		return nil, err
	}
	w := r.ints(s.Dict["W"])
	if len(w) < 3 {
		return nil, errors.New("invalid cross-reference stream widths")
	}
	index := r.ints(s.Dict["Index"])
	if len(index) < 2 {
		index = []int{0, r.intOf(s.Dict["Size"], 0)}
	}
	rowLen := w[0] + w[1] + w[2]
	p := 0
	field := func(width int) int {
		v := 0
		for k := 0; k < width; k++ {
			v = v<<8 | int(data[p])
			p++
		}
		return v
	}
	for i := 0; i+1 < len(index); i += 2 {
		start, n := index[i], index[i+1]
		for j := 0; j < n && p+rowLen <= len(data); j++ {
			t := 1
			if w[0] > 0 {
				t = field(w[0])
			}
			a, b := field(w[1]), field(w[2])
			num := start + j
			if _, exists := r.xref[num]; exists {
				continue
			}
			switch t {
			case 1:
				r.xref[num] = xrefEntry{typ: 1, off: a, gen: b}
			case 2:
				r.xref[num] = xrefEntry{typ: 2, stm: a, idx: b}
			default:
				r.xref[num] = xrefEntry{typ: 0}
			}
		}
	}
	return s.Dict, nil
}

var objHeaderRx = regexp.MustCompile(`(\d+)[\x00\t\n\f\r ]+(\d+)[\x00\t\n\f\r ]+obj\b`)

// scan はファイル全体から "n g obj" を探して各オブジェクトの位置を返します (相互参照表が壊れている場合の代替)。
func (r *Reader) scan() map[int]int {
	if r.offsets != nil {
		return r.offsets
	}
	r.offsets = map[int]int{}
	for _, m := range objHeaderRx.FindAllSubmatchIndex(r.data, -1) {
		if m[0] > 0 && r.data[m[0]-1] >= '0' && r.data[m[0]-1] <= '9' {
			continue
		}
		num, err := strconv.Atoi(string(r.data[m[2]:m[3]]))
		if err == nil {
			r.offsets[num] = m[0]
		}
	}
	return r.offsets
}

// reconstruct は相互参照表を使わずにオブジェクトとトレーラーを復元します。
func (r *Reader) reconstruct() {
	r.xref = map[int]xrefEntry{}
	r.cache = map[int]any{}
	r.stms = map[int]map[int]any{}
	offsets := r.scan()
	for num, off := range offsets {
		r.xref[num] = xrefEntry{typ: 1, off: off}
	}
	trailer := Dict{}
	for i := 0; ; {
		j := bytes.Index(r.data[i:], []byte("trailer"))
		if j < 0 {
			break
		}
		l := &lexer{b: r.data, pos: i + j + 7}
		if v, err := l.object(0); err == nil {
			if d, ok := v.(Dict); ok {
				for k, val := range d {
					trailer[k] = val
				}
			}
		}
		i += j + 7
	}
	nums := make([]int, 0, len(offsets))
	for num := range offsets {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	for _, num := range nums {
		s, ok := r.get(num).(*Stream)
		if !ok {
			continue
		}
		switch s.Dict["Type"] {
		case Name("XRef"):
			for k, v := range s.Dict {
				if _, has := trailer[k]; !has {
					trailer[k] = v
				}
			}
		case Name("ObjStm"):
			if objs := r.objStm(num); objs != nil {
				for n := range objs {
					if _, has := r.xref[n]; !has {
						r.xref[n] = xrefEntry{typ: 2, stm: num}
					}
				}
			}
		}
	}
	if _, ok := r.resolve(trailer["Root"]).(Dict); !ok {
		for _, num := range nums {
			if d, ok := r.get(num).(Dict); ok && d["Type"] == Name("Catalog") {
				trailer["Root"] = Ref{Num: num}
				break
			}
		}
	}
	r.trailer = trailer
	r.cache = map[int]any{}
}

// parseIndirectAt は位置 off にある間接オブジェクト "n g obj ... endobj" を解析します。
func (r *Reader) parseIndirectAt(off, want int) (any, error) {
	if off < 0 || off >= len(r.data) {
		return nil, errors.New("object offset out of range")
	}
	l := &lexer{b: r.data, pos: off}
	l.skip()
	num, err := strconv.Atoi(l.regular())
	if err != nil {
		return nil, errors.New("invalid object header")
	}
	l.skip()
	if _, err := strconv.Atoi(l.regular()); err != nil {
		return nil, errors.New("invalid object header")
	}
	l.skip()
	if l.regular() != "obj" || (want >= 0 && num != want) {
		return nil, errors.New("invalid object header")
	}
	v, err := l.object(0)
	if err != nil {
		return nil, err
	}
	d, ok := v.(Dict)
	if !ok {
		return v, nil
	}
	save := l.pos
	l.skip()
	if !bytes.HasPrefix(r.data[l.pos:], []byte("stream")) {
		l.pos = save
		return d, nil
	}
	p := l.pos + 6
	if p < len(r.data) && r.data[p] == '\r' {
		p++
	}
	if p < len(r.data) && r.data[p] == '\n' {
		p++
	}
	length := -1
	switch n := d["Length"].(type) {
	case int64:
		length = int(n)
	case Ref:
		if v, ok := r.get(n.Num).(int64); ok {
			length = int(v)
		}
	}
	var data []byte
	if length >= 0 && p+length <= len(r.data) && endstreamAt(r.data, p+length) {
		data = r.data[p : p+length]
	} else {
		e := bytes.Index(r.data[p:], []byte("endstream"))
		if e < 0 {
			e = len(r.data) - p
		}
		data = r.data[p : p+e]
		switch {
		case bytes.HasSuffix(data, []byte("\r\n")):
			data = data[:len(data)-2]
		case bytes.HasSuffix(data, []byte("\n")), bytes.HasSuffix(data, []byte("\r")):
			data = data[:len(data)-1]
		}
	}
	return &Stream{Dict: d, Data: data}, nil
}

func endstreamAt(b []byte, p int) bool {
	for p < len(b) && isSpace(b[p]) {
		p++
	}
	return bytes.HasPrefix(b[p:], []byte("endstream"))
}

// get は番号 num のオブジェクトを返します (存在しなければ nil)。
func (r *Reader) get(num int) any {
	if v, ok := r.cache[num]; ok {
		return v
	}
	if r.busy[num] {
		return nil
	}
	r.busy[num] = true
	defer delete(r.busy, num)
	var obj any
	e, ok := r.xref[num]
	switch {
	case ok && e.typ == 1:
		v, err := r.parseIndirectAt(e.off, num)
		if err != nil {
			if off, found := r.scan()[num]; found {
				v, err = r.parseIndirectAt(off, num)
			}
		}
		if err == nil {
			obj = r.decryptObject(v, num, e.gen)
		}
	case ok && e.typ == 2:
		if objs := r.objStm(e.stm); objs != nil {
			obj = objs[num]
		}
	case !ok:
		if off, found := r.scan()[num]; found {
			if v, err := r.parseIndirectAt(off, num); err == nil {
				obj = r.decryptObject(v, num, 0)
			}
		}
	}
	r.cache[num] = obj
	return obj
}

func (r *Reader) decryptObject(v any, num, gen int) any {
	if r.crypt == nil || num == r.crypt.encNum {
		return v
	}
	return r.crypt.object(v, num, gen)
}

// objStm はオブジェクトストリームに含まれるオブジェクトを返します。
func (r *Reader) objStm(num int) map[int]any {
	if objs, ok := r.stms[num]; ok {
		return objs
	}
	r.stms[num] = nil
	s, ok := r.get(num).(*Stream)
	if !ok {
		return nil
	}
	data, err := r.Decode(s)
	if err != nil {
		return nil
	}
	n := r.intOf(s.Dict["N"], 0)
	first := r.intOf(s.Dict["First"], 0)
	l := &lexer{b: data}
	objs := map[int]any{}
	type pair struct{ num, off int }
	var pairs []pair
	for i := 0; i < n; i++ {
		a, err1 := l.object(0)
		b, err2 := l.object(0)
		an, ok1 := a.(int64)
		bn, ok2 := b.(int64)
		if err1 != nil || err2 != nil || !ok1 || !ok2 {
			break
		}
		pairs = append(pairs, pair{int(an), int(bn)})
	}
	for _, p := range pairs {
		ol := &lexer{b: data, pos: first + p.off}
		if ol.pos < 0 || ol.pos >= len(data) {
			continue
		}
		if v, err := ol.object(0); err == nil {
			if _, isKw := v.(keyword); !isKw {
				objs[p.num] = v
			}
		}
	}
	r.stms[num] = objs
	return objs
}

// resolve は間接参照をたどって値を返します。
func (r *Reader) resolve(v any) any {
	for i := 0; i < 32; i++ {
		ref, ok := v.(Ref)
		if !ok {
			return v
		}
		v = r.get(ref.Num)
	}
	return nil
}

func (r *Reader) dict(v any) Dict {
	switch x := r.resolve(v).(type) {
	case Dict:
		return x
	case *Stream:
		return x.Dict
	}
	return nil
}

func (r *Reader) number(v any) (float64, bool) {
	switch x := r.resolve(v).(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func (r *Reader) intOf(v any, def int) int {
	if f, ok := r.number(v); ok {
		return int(f)
	}
	return def
}

func (r *Reader) ints(v any) []int {
	arr, ok := r.resolve(v).(Array)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(arr))
	for _, e := range arr {
		out = append(out, r.intOf(e, 0))
	}
	return out
}

// rect は矩形の配列を [左下 x, 左下 y, 右上 x, 右上 y] に正規化します。
func (r *Reader) rect(v any) ([4]float64, bool) {
	arr, ok := r.resolve(v).(Array)
	if !ok || len(arr) < 4 {
		return [4]float64{}, false
	}
	var out [4]float64
	for i := 0; i < 4; i++ {
		f, ok := r.number(arr[i])
		if !ok {
			return [4]float64{}, false
		}
		out[i] = f
	}
	x1, x2 := math.Min(out[0], out[2]), math.Max(out[0], out[2])
	y1, y2 := math.Min(out[1], out[3]), math.Max(out[1], out[3])
	if x2-x1 <= 0 || y2-y1 <= 0 {
		return [4]float64{}, false
	}
	return [4]float64{x1, y1, x2, y2}, true
}

// ---------------------------------------------------------------------------
// ページ

var inheritablePageKeys = []Name{"Resources", "MediaBox", "CropBox", "Rotate"}

// pageDicts はページ辞書の一覧を返します (継承される属性は各ページにコピー済み)。
func (r *Reader) pageDicts() []Dict {
	if r.walked {
		return r.pages
	}
	r.walked = true
	root := r.dict(r.trailer["Root"])
	if root == nil {
		return nil
	}
	visited := map[int]bool{}
	var walk func(node any, inherited Dict, depth int)
	walk = func(node any, inherited Dict, depth int) {
		if depth > 64 || len(r.pages) > 100000 {
			return
		}
		if ref, ok := node.(Ref); ok {
			if visited[ref.Num] {
				return
			}
			visited[ref.Num] = true
		}
		d := r.dict(node)
		if d == nil {
			return
		}
		inh := Dict{}
		for k, v := range inherited {
			inh[k] = v
		}
		for _, k := range inheritablePageKeys {
			if v, ok := d[k]; ok && v != nil {
				inh[k] = v
			}
		}
		kids, hasKids := r.resolve(d["Kids"]).(Array)
		if d["Type"] == Name("Pages") || (d["Type"] != Name("Page") && hasKids) {
			for _, kid := range kids {
				walk(kid, inh, depth+1)
			}
			return
		}
		page := Dict{}
		for k, v := range d {
			page[k] = v
		}
		for k, v := range inh {
			if _, has := page[k]; !has {
				page[k] = v
			}
		}
		r.pages = append(r.pages, page)
	}
	walk(root["Pages"], Dict{}, 0)
	return r.pages
}

// NumPages はページ数を返します。
func (r *Reader) NumPages() int { return len(r.pageDicts()) }

// ---------------------------------------------------------------------------
// ストリームの復号 (フィルター)

func (r *Reader) names(v any) []Name {
	switch x := r.resolve(v).(type) {
	case Name:
		return []Name{x}
	case Array:
		out := make([]Name, 0, len(x))
		for _, e := range x {
			if n, ok := r.resolve(e).(Name); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

// Decode はストリームのフィルターを適用してデータを復元します。
func (r *Reader) Decode(s *Stream) ([]byte, error) {
	data := s.Data
	filters := r.names(s.Dict["Filter"])
	var parms []Dict
	switch p := r.resolve(s.Dict["DecodeParms"]).(type) {
	case Dict:
		parms = []Dict{p}
	case Array:
		for _, e := range p {
			parms = append(parms, r.dict(e))
		}
	}
	for i, f := range filters {
		var p Dict
		if i < len(parms) {
			p = parms[i]
		}
		var err error
		switch f {
		case "FlateDecode", "Fl":
			if data, err = inflate(data); err == nil {
				data, err = r.unpredict(data, p)
			}
		case "LZWDecode", "LZW":
			rd := lzw.NewReader(bytes.NewReader(data), lzw.MSB, 8)
			data, err = io.ReadAll(rd)
			rd.Close()
			if err != nil && len(data) > 0 {
				err = nil
			}
			if err == nil {
				data, err = r.unpredict(data, p)
			}
		case "ASCIIHexDecode", "AHx":
			data, err = asciiHexDecode(data)
		case "ASCII85Decode", "A85":
			data, err = ascii85Decode(data)
		case "RunLengthDecode", "RL":
			data = runLengthDecode(data)
		case "Crypt":
		default:
			return nil, fmt.Errorf("unsupported stream filter %s", f)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
	}
	return data, nil
}

func inflate(data []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		// zlib ヘッダーのない生の deflate データ
		out, err2 := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
		if len(out) > 0 {
			return out, nil
		}
		if err2 != nil {
			return nil, err
		}
		return out, nil
	}
	out, err := io.ReadAll(zr)
	if err != nil && len(out) > 0 {
		// 末尾が壊れていても読めた分は使う
		return out, nil
	}
	return out, err
}

func (r *Reader) unpredict(data []byte, p Dict) ([]byte, error) {
	if p == nil {
		return data, nil
	}
	pred := r.intOf(p["Predictor"], 1)
	if pred < 2 {
		return data, nil
	}
	colors := r.intOf(p["Colors"], 1)
	bpc := r.intOf(p["BitsPerComponent"], 8)
	cols := r.intOf(p["Columns"], 1)
	bpp := colors * bpc / 8
	if bpp < 1 {
		bpp = 1
	}
	rowLen := (colors*bpc*cols + 7) / 8
	if rowLen <= 0 {
		return data, nil
	}
	if pred == 2 {
		if bpc != 8 {
			return data, nil
		}
		out := append([]byte(nil), data...)
		for row := 0; row < len(out); row += rowLen {
			end := row + rowLen
			if end > len(out) {
				end = len(out)
			}
			for i := row + bpp; i < end; i++ {
				out[i] += out[i-bpp]
			}
		}
		return out, nil
	}
	out := make([]byte, 0, len(data))
	prev := make([]byte, rowLen)
	for pos := 0; pos < len(data); pos += rowLen + 1 {
		ft := data[pos]
		row := make([]byte, rowLen)
		copy(row, data[min(pos+1, len(data)):min(pos+1+rowLen, len(data))])
		for i := 0; i < rowLen; i++ {
			var left, upLeft byte
			if i >= bpp {
				left, upLeft = row[i-bpp], prev[i-bpp]
			}
			up := prev[i]
			switch ft {
			case 1:
				row[i] += left
			case 2:
				row[i] += up
			case 3:
				row[i] += byte((int(left) + int(up)) / 2)
			case 4:
				row[i] += paeth(left, up, upLeft)
			}
		}
		out = append(out, row...)
		prev = row
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func asciiHexDecode(data []byte) ([]byte, error) {
	var digits []byte
	for _, c := range data {
		if c == '>' {
			break
		}
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			digits = append(digits, c)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	_, err := hex.Decode(out, digits)
	return out, err
}

func ascii85Decode(data []byte) ([]byte, error) {
	data = bytes.TrimSpace(data)
	data = bytes.TrimPrefix(data, []byte("<~"))
	if i := bytes.Index(data, []byte("~>")); i >= 0 {
		data = data[:i]
	}
	out := make([]byte, len(data)*4/5+8)
	n, _, err := ascii85.Decode(out, data, true)
	return out[:n], err
}

func runLengthDecode(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); {
		n := int(data[i])
		i++
		switch {
		case n < 128:
			end := min(i+n+1, len(data))
			out = append(out, data[i:end]...)
			i = end
		case n > 128:
			if i < len(data) {
				out = append(out, bytes.Repeat([]byte{data[i]}, 257-n)...)
			}
			i++
		default:
			return out
		}
	}
	return out
}
