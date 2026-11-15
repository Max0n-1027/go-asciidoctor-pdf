package pdf

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ImportedPage は他の PDF から取り込んだページです。
// 元のページの内容を Form XObject として包み、出力側のページに描画します (prawn-templates の import_page 相当)。
type ImportedPage struct {
	// MediaBox は元のページの MediaBox です ([左下 x, 左下 y, 右上 x, 右上 y])。
	MediaBox [4]float64
	// Boxes は CropBox / BleedBox / TrimBox / ArtBox です。
	Boxes  map[string][4]float64
	Rotate int

	src       *Reader
	content   []byte
	resources any
	group     any
	annots    []any
	resName   string
}

// Content は元のページの内容ストリーム (復号済み) を返します。
func (p *ImportedPage) Content() []byte { return p.content }

// Width と Height は MediaBox の寸法を返します。
func (p *ImportedPage) Width() float64  { return p.MediaBox[2] - p.MediaBox[0] }
func (p *ImportedPage) Height() float64 { return p.MediaBox[3] - p.MediaBox[1] }

type importKey struct {
	r     *Reader
	index int
}

// ImportPage は r の index 番目 (0 始まり) のページを取り込みます。同じページは一度だけ取り込みます。
func (d *Document) ImportPage(r *Reader, index int) (*ImportedPage, error) {
	if d.importCache == nil {
		d.importCache = map[importKey]*ImportedPage{}
	}
	key := importKey{r, index}
	if ip, ok := d.importCache[key]; ok {
		return ip, nil
	}
	pages := r.pageDicts()
	if index < 0 || index >= len(pages) {
		return nil, fmt.Errorf("page %d not found", index+1)
	}
	pd := pages[index]
	ip := &ImportedPage{src: r, Boxes: map[string][4]float64{}, resources: pd["Resources"], group: pd["Group"]}
	mb, ok := r.rect(pd["MediaBox"])
	if !ok {
		mb = [4]float64{0, 0, 612, 792}
	}
	ip.MediaBox = mb
	for _, k := range []string{"CropBox", "BleedBox", "TrimBox", "ArtBox"} {
		if b, ok := r.rect(pd[Name(k)]); ok {
			ip.Boxes[k] = b
		}
	}
	ip.Rotate = ((r.intOf(pd["Rotate"], 0)%360)/90*90 + 360) % 360
	switch c := r.resolve(pd["Contents"]).(type) {
	case *Stream:
		data, err := r.Decode(c)
		if err != nil {
			return nil, err
		}
		ip.content = data
	case Array:
		for _, e := range c {
			s, ok := r.resolve(e).(*Stream)
			if !ok {
				continue
			}
			data, err := r.Decode(s)
			if err != nil {
				return nil, err
			}
			ip.content = append(append(ip.content, data...), '\n')
		}
	case nil:
	default:
		return nil, errors.New("invalid page contents")
	}
	if annots, ok := r.resolve(pd["Annots"]).(Array); ok {
		ip.annots = annots
	}
	d.imports = append(d.imports, ip)
	ip.resName = "Imp" + strconv.Itoa(len(d.imports))
	d.importCache[key] = ip
	if r.version > d.Version && r.version <= "2.0" {
		d.Version = r.version
	}
	return ip, nil
}

// copier は取り込み元の PDF のオブジェクトを出力側にコピーします。
type copier struct {
	w     *writer
	r     *Reader
	ids   map[int]int
	queue []int
}

func newCopier(w *writer, r *Reader) *copier {
	return &copier{w: w, r: r, ids: map[int]int{}}
}

// pageLike はページツリーやカタログなど、コピーすると元の文書全体を引き込んでしまうオブジェクトかどうかを返します。
func (cp *copier) pageLike(v any) bool {
	d := cp.r.dict(v)
	if d == nil {
		return false
	}
	switch d["Type"] {
	case Name("Page"), Name("Pages"), Name("Catalog"), Name("Outlines"):
		return true
	}
	return false
}

func (cp *copier) ref(ref Ref) int {
	if id, ok := cp.ids[ref.Num]; ok {
		return id
	}
	obj := cp.r.get(ref.Num)
	if obj == nil || cp.pageLike(obj) {
		cp.ids[ref.Num] = 0
		return 0
	}
	id := cp.w.alloc()
	cp.ids[ref.Num] = id
	cp.queue = append(cp.queue, ref.Num)
	return id
}

// flush はコピー待ちのオブジェクトをすべて書き出します。
func (cp *copier) flush() {
	for len(cp.queue) > 0 {
		num := cp.queue[0]
		cp.queue = cp.queue[1:]
		id := cp.ids[num]
		switch obj := cp.r.get(num).(type) {
		case *Stream:
			dict := Dict{}
			for k, v := range obj.Dict {
				if k != "Length" {
					dict[k] = v
				}
			}
			cp.w.objs[id-1] = rawStream(strings.TrimSuffix(strings.TrimPrefix(cp.ser(dict), "<<"), ">>"), obj.Data)
		default:
			cp.w.set(id, cp.ser(obj))
		}
	}
}

func encodeName(n Name) string {
	var b strings.Builder
	b.WriteByte('/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < 0x21 || c > 0x7e || c == '#' || isDelim(c) {
			fmt.Fprintf(&b, "#%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ser は値を PDF の構文で書き出します (間接参照は出力側の番号に置き換えます)。
func (cp *copier) ser(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case Name:
		return encodeName(x)
	case String:
		return "<" + fmt.Sprintf("%X", []byte(x)) + ">"
	case Ref:
		if id := cp.ref(x); id > 0 {
			return strconv.Itoa(id) + " 0 R"
		}
		return "null"
	case Array:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = cp.ser(e)
		}
		return "[" + strings.Join(parts, " ") + "]"
	case Dict:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("<<")
		for _, k := range keys {
			b.WriteString(encodeName(Name(k)))
			b.WriteByte(' ')
			b.WriteString(cp.ser(x[Name(k)]))
			b.WriteByte(' ')
		}
		b.WriteString(">>")
		return b.String()
	case *Stream:
		// ストリームは直接値にできないので間接オブジェクトとして書き出す
		id := cp.w.alloc()
		dict := Dict{}
		for k, v := range x.Dict {
			if k != "Length" {
				dict[k] = v
			}
		}
		cp.w.objs[id-1] = rawStream(strings.TrimSuffix(strings.TrimPrefix(cp.ser(dict), "<<"), ">>"), x.Data)
		return strconv.Itoa(id) + " 0 R"
	}
	return "null"
}

// annotation は取り込んだページの注釈を書き出し、その番号を返します (書き出さない場合は 0)。
// 元の文書のページを指すリンクやポップアップは取り込めないため除外します。
func (cp *copier) annotation(v any) int {
	d := cp.r.dict(v)
	if d == nil {
		return 0
	}
	sub, _ := cp.r.resolve(d["Subtype"]).(Name)
	if sub == "Popup" {
		return 0
	}
	if sub == "Link" {
		if _, ok := d["Dest"]; ok {
			return 0
		}
		if a := cp.r.dict(d["A"]); a != nil {
			if s, _ := cp.r.resolve(a["S"]).(Name); s == "GoTo" {
				return 0
			}
		}
	}
	out := Dict{}
	for k, val := range d {
		switch k {
		case "P", "Parent", "Popup", "IRT", "StructParent":
			continue
		}
		out[k] = val
	}
	return cp.w.add(cp.ser(out))
}

// writeImports は取り込んだページの Form XObject を書き出し、XObject リソースの項目を返します。
func (d *Document) writeImports(w *writer) (string, map[*Reader]*copier) {
	copiers := map[*Reader]*copier{}
	used := map[*ImportedPage]bool{}
	for _, p := range d.Pages {
		if p.Import != nil {
			used[p.Import] = true
		}
	}
	var res strings.Builder
	for _, ip := range d.imports {
		if !used[ip] {
			continue
		}
		cp := copiers[ip.src]
		if cp == nil {
			cp = newCopier(w, ip.src)
			copiers[ip.src] = cp
		}
		resources := "<< >>"
		if ip.resources != nil {
			resources = cp.ser(ip.resources)
		}
		mb := ip.MediaBox
		dict := fmt.Sprintf("/Type /XObject /Subtype /Form /FormType 1 /BBox [%s %s %s %s] /Resources %s", num(mb[0]), num(mb[1]), num(mb[2]), num(mb[3]), resources)
		if ip.group != nil {
			dict += " /Group " + cp.ser(ip.group)
		}
		id := w.addStream(dict, ip.content, true)
		fmt.Fprintf(&res, "/%s %d 0 R ", ip.resName, id)
	}
	return res.String(), copiers
}
