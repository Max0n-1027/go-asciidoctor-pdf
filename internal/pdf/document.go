package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Document は 1 つの PDF 文書を表します。
type Document struct {
	Pages      []*Page
	Info       []InfoEntry
	Outline    []*OutlineItem
	PageLabels []PageLabel
	Compress   bool
	Version    string // 例: "1.4"
	PageMode   string // 例: "UseOutlines"
	// OpenAction: "FitH" / "Fit" / "FitV" (空なら出力しない)
	OpenAction        string
	DisplayDocTitle   bool
	Lang              string
	PageLayout        string
	NonFullScreenMode string

	fonts   []*Font
	images  []*Image
	dests   map[string]Dest
	gstates map[string]string // key → resource name
	gsOrder []string

	imports     []*ImportedPage
	importCache map[importKey]*ImportedPage
}

// InfoEntry は文書情報辞書の 1 項目です。
type InfoEntry struct {
	Key   string
	Value string
	Raw   bool // true のとき Value を PDF 構文としてそのまま書く
}

// Dest は文書内の移動先です。
type Dest struct {
	Page int // 0 始まり
	X, Y float64
	Fit  bool
}

// OutlineItem はしおり (アウトライン) の 1 項目です。
type OutlineItem struct {
	Title    string
	Page     int // 0 始まり
	Y        float64
	Closed   bool
	Children []*OutlineItem
}

// PageLabel はページラベルの範囲定義です。
type PageLabel struct {
	Start  int    // 0 始まりのページ番号
	Style  string // "D" (10 進), "r" (ローマ数字小文字), "" (ラベルなし)
	First  int    // ラベルの開始番号
	Prefix string
}

// Page は 1 ページ分の内容です。
type Page struct {
	W, H   float64
	Chunks [][]byte
	Annots []Annot
	// TrimBox / BleedBox など追加のボックス
	Boxes map[string][4]float64
	// Import は取り込んだ PDF のページです。設定されている場合、その内容を最初に描画し、ページの寸法も元のページに合わせます。
	Import *ImportedPage
	// Imported は取り込んだページかどうかです (ランニングコンテンツを描画しない判定に使います)。
	Imported bool
}

// Annot はリンク注釈です。
type Annot struct {
	Rect [4]float64
	URI  string
	Dest string
}

// New は空の文書を生成します。
func New() *Document {
	return &Document{Version: "1.4", dests: map[string]Dest{}, gstates: map[string]string{}}
}

// AddPage はページを末尾に追加します。
func (d *Document) AddPage(w, h float64) *Page {
	p := &Page{W: w, H: h}
	d.Pages = append(d.Pages, p)
	return p
}

// InsertPage はページを位置 at (0 始まり) に挿入し、以降のページを指す移動先をずらします。
func (d *Document) InsertPage(at int, p *Page) {
	if at >= len(d.Pages) {
		d.Pages = append(d.Pages, p)
		return
	}
	d.Pages = append(d.Pages, nil)
	copy(d.Pages[at+1:], d.Pages[at:])
	d.Pages[at] = p
	for name, dst := range d.dests {
		if dst.Page >= at {
			dst.Page++
			d.dests[name] = dst
		}
	}
}

// Add は内容ストリームの断片を追加します。
func (p *Page) Add(chunk []byte) {
	if len(chunk) > 0 {
		p.Chunks = append(p.Chunks, chunk)
	}
}

// InsertAt は指定位置に断片を挿入します (背景を内容の下に描くために使います)。
func (p *Page) InsertAt(idx int, chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if idx >= len(p.Chunks) {
		p.Chunks = append(p.Chunks, chunk)
		return
	}
	if idx < 0 {
		idx = 0
	}
	p.Chunks = append(p.Chunks, nil)
	copy(p.Chunks[idx+1:], p.Chunks[idx:])
	p.Chunks[idx] = chunk
}

// RegisterFont はフォントを文書に登録します。
func (d *Document) RegisterFont(f *Font) {
	if f.resName != "" {
		return
	}
	d.fonts = append(d.fonts, f)
	f.resName = fmt.Sprintf("F%d", len(d.fonts))
}

// SetDest は名前付き移動先を登録します。
func (d *Document) SetDest(name string, dest Dest) { d.dests[name] = dest }

// GetDest は名前付き移動先を取得します。
func (d *Document) GetDest(name string) (Dest, bool) {
	v, ok := d.dests[name]
	return v, ok
}

// Opacity は塗り・線の不透明度を指定する ExtGState のリソース名を返します。
func (d *Document) Opacity(fill, stroke float64) string {
	key := fmt.Sprintf("%s/%s", num(fill), num(stroke))
	if name, ok := d.gstates[key]; ok {
		return name
	}
	name := fmt.Sprintf("GS%d", len(d.gstates)+1)
	d.gstates[key] = name
	d.gsOrder = append(d.gsOrder, key)
	return name
}

func num(v float64) string {
	if v != v || v > 1e15 || v < -1e15 {
		return "0"
	}
	s := strconv.FormatFloat(v, 'f', 5, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

// textString は PDF のテキスト文字列を生成します。
func textString(s string) string {
	ascii := true
	for _, r := range s {
		if r > 126 || r < 32 {
			ascii = false
			break
		}
	}
	if ascii {
		return literalString(s)
	}
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	b.WriteString(">")
	return b.String()
}

func literalString(s string) string {
	var b strings.Builder
	b.WriteByte('(')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(')')
	return b.String()
}

func deflate(data []byte) []byte {
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

type writer struct {
	objs [][]byte
}

func (w *writer) alloc() int {
	w.objs = append(w.objs, nil)
	return len(w.objs)
}

func (w *writer) set(id int, body string) { w.objs[id-1] = []byte(body) }

func (w *writer) add(body string) int {
	id := w.alloc()
	w.set(id, body)
	return id
}

func (w *writer) stream(id int, dict string, data []byte, compress bool) {
	if compress {
		data = deflate(data)
		dict += " /Filter /FlateDecode"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< %s /Length %d >>\nstream\n", strings.TrimSpace(dict), len(data))
	b.Write(data)
	b.WriteString("\nendstream")
	w.objs[id-1] = b.Bytes()
}

func (w *writer) addStream(dict string, data []byte, compress bool) int {
	id := w.alloc()
	w.stream(id, dict, data, compress)
	return id
}

// WriteTo は PDF をシリアライズして書き出します。
func (d *Document) WriteTo(out io.Writer) (int64, error) {
	w := &writer{}
	catalogID := w.alloc()
	pagesID := w.alloc()
	resID := w.alloc()

	// フォント
	var fontRes strings.Builder
	for _, f := range d.fonts {
		if len(f.used) == 0 {
			continue
		}
		f.objID = d.writeFont(w, f)
		fmt.Fprintf(&fontRes, "/%s %d 0 R ", f.resName, f.objID)
	}
	// 画像
	var xobjRes strings.Builder
	for _, img := range d.images {
		img.objID = d.writeImage(w, img)
		fmt.Fprintf(&xobjRes, "/%s %d 0 R ", img.resName, img.objID)
	}
	importRes, copiers := d.writeImports(w)
	xobjRes.WriteString(importRes)
	var gsRes strings.Builder
	for _, key := range d.gsOrder {
		parts := strings.SplitN(key, "/", 2)
		id := w.add(fmt.Sprintf("<< /Type /ExtGState /ca %s /CA %s >>", parts[0], parts[1]))
		fmt.Fprintf(&gsRes, "/%s %d 0 R ", d.gstates[key], id)
	}
	res := "<< /ProcSet [/PDF /Text /ImageB /ImageC /ImageI]"
	if fontRes.Len() > 0 {
		res += " /Font << " + fontRes.String() + ">>"
	}
	if xobjRes.Len() > 0 {
		res += " /XObject << " + xobjRes.String() + ">>"
	}
	if gsRes.Len() > 0 {
		res += " /ExtGState << " + gsRes.String() + ">>"
	}
	res += " >>"
	w.set(resID, res)

	// ページ
	pageIDs := make([]int, len(d.Pages))
	for i := range d.Pages {
		pageIDs[i] = w.alloc()
	}
	for i, p := range d.Pages {
		var content bytes.Buffer
		if p.Import != nil {
			fmt.Fprintf(&content, "q /%s Do Q\n", p.Import.resName)
		}
		for _, c := range p.Chunks {
			content.Write(c)
			if len(c) > 0 && c[len(c)-1] != '\n' {
				content.WriteByte('\n')
			}
		}
		contentID := w.addStream("", content.Bytes(), d.Compress)
		var b strings.Builder
		mb := [4]float64{0, 0, p.W, p.H}
		boxes := map[string][4]float64{}
		if p.Import != nil {
			mb = p.Import.MediaBox
			for k, v := range p.Import.Boxes {
				boxes[k] = v
			}
		}
		for k, v := range p.Boxes {
			boxes[k] = v
		}
		if _, ok := boxes["CropBox"]; !ok {
			boxes["CropBox"] = mb
		}
		fmt.Fprintf(&b, "<< /Type /Page /Parent %d 0 R /MediaBox [%s %s %s %s]", pagesID, num(mb[0]), num(mb[1]), num(mb[2]), num(mb[3]))
		keys := make([]string, 0, len(boxes))
		for k := range boxes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := boxes[k]
			fmt.Fprintf(&b, " /%s [%s %s %s %s]", k, num(v[0]), num(v[1]), num(v[2]), num(v[3]))
		}
		if p.Import != nil && p.Import.Rotate != 0 {
			fmt.Fprintf(&b, " /Rotate %d", p.Import.Rotate)
		}
		fmt.Fprintf(&b, " /Resources %d 0 R /Contents %d 0 R", resID, contentID)
		var importedAnnots []int
		if p.Import != nil {
			cp := copiers[p.Import.src]
			for _, a := range p.Import.annots {
				if id := cp.annotation(a); id > 0 {
					importedAnnots = append(importedAnnots, id)
				}
			}
		}
		if len(p.Annots) > 0 || len(importedAnnots) > 0 {
			b.WriteString(" /Annots [")
			for _, a := range p.Annots {
				var ab strings.Builder
				fmt.Fprintf(&ab, "<< /Type /Annot /Subtype /Link /Border [0 0 0] /Rect [%s %s %s %s]", num(a.Rect[0]), num(a.Rect[1]), num(a.Rect[2]), num(a.Rect[3]))
				if a.URI != "" {
					fmt.Fprintf(&ab, " /A << /Type /Action /S /URI /URI %s >>", literalString(asciiURI(a.URI)))
				} else {
					fmt.Fprintf(&ab, " /Dest %s", destName(a.Dest))
				}
				ab.WriteString(" >>")
				fmt.Fprintf(&b, "%d 0 R ", w.add(ab.String()))
			}
			for _, id := range importedAnnots {
				fmt.Fprintf(&b, "%d 0 R ", id)
			}
			b.WriteString("]")
		}
		b.WriteString(" >>")
		w.set(pageIDs[i], b.String())
	}
	for _, cp := range copiers {
		cp.flush()
	}
	var kids strings.Builder
	for _, id := range pageIDs {
		fmt.Fprintf(&kids, "%d 0 R ", id)
	}
	w.set(pagesID, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pageIDs), strings.TrimSpace(kids.String())))

	destArray := func(dst Dest) string {
		if dst.Page < 0 || dst.Page >= len(pageIDs) {
			return "null"
		}
		if dst.Fit {
			return fmt.Sprintf("[%d 0 R /Fit]", pageIDs[dst.Page])
		}
		return fmt.Sprintf("[%d 0 R /XYZ %s %s null]", pageIDs[dst.Page], num(dst.X), num(dst.Y))
	}

	// カタログ
	var cat strings.Builder
	fmt.Fprintf(&cat, "<< /Type /Catalog /Pages %d 0 R", pagesID)
	if len(d.dests) > 0 {
		names := make([]string, 0, len(d.dests))
		for name := range d.dests {
			names = append(names, name)
		}
		sort.Strings(names)
		var nb strings.Builder
		for _, name := range names {
			arr := destArray(d.dests[name])
			if arr == "null" {
				continue
			}
			fmt.Fprintf(&nb, "%s %s ", destName(name), arr)
		}
		if nb.Len() > 0 {
			destsID := w.add(fmt.Sprintf("<< /Limits [%s %s] /Names [%s] >>", destName(names[0]), destName(names[len(names)-1]), strings.TrimSpace(nb.String())))
			namesID := w.add(fmt.Sprintf("<< /Dests << /Kids [%d 0 R] >> >>", destsID))
			fmt.Fprintf(&cat, " /Names %d 0 R", namesID)
		}
	}
	if len(d.Outline) > 0 {
		rootID := w.alloc()
		first, last, count := d.writeOutlineLevel(w, d.Outline, rootID, destArray)
		w.set(rootID, fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>", first, last, count))
		fmt.Fprintf(&cat, " /Outlines %d 0 R", rootID)
	}
	if d.PageMode != "" {
		fmt.Fprintf(&cat, " /PageMode /%s", d.PageMode)
	}
	if d.PageLayout != "" {
		fmt.Fprintf(&cat, " /PageLayout /%s", d.PageLayout)
	}
	if len(d.PageLabels) > 0 {
		cat.WriteString(" /PageLabels << /Nums [")
		for _, pl := range d.PageLabels {
			fmt.Fprintf(&cat, "%d << ", pl.Start)
			if pl.Style != "" {
				fmt.Fprintf(&cat, "/S /%s /St %d ", pl.Style, pl.First)
			}
			if pl.Prefix != "" {
				fmt.Fprintf(&cat, "/P %s ", textString(pl.Prefix))
			}
			cat.WriteString(">> ")
		}
		cat.WriteString("] >>")
	}
	if d.OpenAction != "" && len(pageIDs) > 0 {
		p0 := d.Pages[0]
		switch d.OpenAction {
		case "Fit":
			fmt.Fprintf(&cat, " /OpenAction [%d 0 R /Fit]", pageIDs[0])
		case "FitV":
			fmt.Fprintf(&cat, " /OpenAction [%d 0 R /FitV 0]", pageIDs[0])
		case "FitH":
			fmt.Fprintf(&cat, " /OpenAction [%d 0 R /FitH %s]", pageIDs[0], num(p0.H))
		}
	}
	vp := ""
	if d.DisplayDocTitle {
		vp += " /DisplayDocTitle true"
	}
	if d.NonFullScreenMode != "" {
		vp += " /NonFullScreenPageMode /" + d.NonFullScreenMode
	}
	if vp != "" {
		fmt.Fprintf(&cat, " /ViewerPreferences <<%s >>", vp)
	}
	if d.Lang != "" {
		fmt.Fprintf(&cat, " /Lang %s", literalString(d.Lang))
	}
	cat.WriteString(" >>")
	w.set(catalogID, cat.String())

	// 文書情報
	var info strings.Builder
	info.WriteString("<<")
	for _, e := range d.Info {
		if e.Raw {
			fmt.Fprintf(&info, " /%s %s", e.Key, e.Value)
		} else {
			fmt.Fprintf(&info, " /%s %s", e.Key, textString(e.Value))
		}
	}
	info.WriteString(" >>")
	infoID := w.add(info.String())

	// 出力
	var buf bytes.Buffer
	version := d.Version
	if version == "" {
		version = "1.4"
	}
	fmt.Fprintf(&buf, "%%PDF-%s\n%%\xFF\xFF\xFF\xFF\n", version)
	offsets := make([]int, len(w.objs))
	for i, body := range w.objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", i+1)
		buf.Write(body)
		buf.WriteString("\nendobj\n")
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(w.objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(w.objs)+1, catalogID, infoID, xref)
	n, err := out.Write(buf.Bytes())
	return int64(n), err
}

func destName(name string) string {
	var b strings.Builder
	b.WriteString("<")
	for _, c := range []byte(name) {
		fmt.Fprintf(&b, "%02x", c)
	}
	b.WriteString(">")
	return b.String()
}

// asciiURI は URI 中の非 ASCII 文字をパーセントエンコードします。
func asciiURI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x21 || c > 0x7e {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (d *Document) writeOutlineLevel(w *writer, items []*OutlineItem, parent int, destArray func(Dest) string) (first, last, count int) {
	ids := make([]int, len(items))
	for i := range items {
		ids[i] = w.alloc()
	}
	for i, it := range items {
		var b strings.Builder
		fmt.Fprintf(&b, "<< /Title %s /Parent %d 0 R /Dest %s", textString(it.Title), parent, destArray(Dest{Page: it.Page, X: 0, Y: it.Y}))
		if i > 0 {
			fmt.Fprintf(&b, " /Prev %d 0 R", ids[i-1])
		}
		if i < len(items)-1 {
			fmt.Fprintf(&b, " /Next %d 0 R", ids[i+1])
		}
		count++
		if len(it.Children) > 0 {
			f, l, c := d.writeOutlineLevel(w, it.Children, ids[i], destArray)
			if it.Closed {
				fmt.Fprintf(&b, " /First %d 0 R /Last %d 0 R /Count %d", f, l, -c)
			} else {
				fmt.Fprintf(&b, " /First %d 0 R /Last %d 0 R /Count %d", f, l, c)
				count += c
			}
		}
		b.WriteString(" >>")
		w.set(ids[i], b.String())
	}
	return ids[0], ids[len(ids)-1], count
}

func (d *Document) writeFont(w *writer, f *Font) int {
	scale := 1000.0 / float64(f.UnitsPerEm)
	sc := func(v int) string { return num(float64(v) * scale) }
	program := f.subset()
	fileID := w.addStream(fmt.Sprintf("/Length1 %d", len(program)), program, true)

	gids := make([]int, 0, len(f.used))
	for g := range f.used {
		gids = append(gids, int(g))
	}
	sort.Ints(gids)
	// サブセットタグ (内容から決定的に生成)
	var h uint32 = 2166136261
	for _, g := range gids {
		h = (h ^ uint32(g)) * 16777619
	}
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = byte('A' + h%26)
		h = h/26 + uint32(i)*7919
	}
	baseName := string(tag) + "+" + f.PSName

	flags := 4
	if f.Fixed {
		flags |= 1
	}
	if f.ItalicAngle != 0 {
		flags |= 64
	}
	descID := w.add(fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags %d /FontBBox [%s %s %s %s] /ItalicAngle %s /Ascent %s /Descent %s /CapHeight %s /XHeight %s /StemV 80 /FontFile2 %d 0 R >>",
		baseName, flags, sc(f.BBox[0]), sc(f.BBox[1]), sc(f.BBox[2]), sc(f.BBox[3]), num(f.ItalicAngle), sc(f.Ascent), sc(f.Descent), sc(f.CapHeight), sc(f.XHeight), fileID))

	var wb strings.Builder
	wb.WriteString("[")
	for i := 0; i < len(gids); {
		j := i
		fmt.Fprintf(&wb, "%d [", gids[i])
		for j < len(gids) && (j == i || gids[j] == gids[j-1]+1) {
			wb.WriteString(strconv.Itoa(f.Width1000(uint16(gids[j]))))
			wb.WriteByte(' ')
			j++
		}
		wb.WriteString("] ")
		i = j
	}
	wb.WriteString("]")
	cidID := w.add(fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW %s /W %s /CIDToGIDMap /Identity >>",
		baseName, descID, strconv.Itoa(f.Width1000(0)), wb.String()))

	var cm strings.Builder
	cm.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for i := 0; i < len(gids); i += 100 {
		end := i + 100
		if end > len(gids) {
			end = len(gids)
		}
		fmt.Fprintf(&cm, "%d beginbfchar\n", end-i)
		for _, g := range gids[i:end] {
			fmt.Fprintf(&cm, "<%04X> <", g)
			for _, u := range utf16.Encode([]rune{f.used[uint16(g)]}) {
				fmt.Fprintf(&cm, "%04X", u)
			}
			cm.WriteString(">\n")
		}
		cm.WriteString("endbfchar\n")
	}
	cm.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	tuID := w.addStream("", []byte(cm.String()), true)

	return w.add(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", baseName, cidID, tuID))
}
