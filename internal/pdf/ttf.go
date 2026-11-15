// Package pdf は依存ライブラリなしで PDF を書き出すための低レベルライターです。
// Prawn (Ruby) が担っている役割のうち、Asciidoctor PDF 互換の出力に必要な部分を実装します。
package pdf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Font は解析済みの TrueType フォントを表します。
type Font struct {
	PSName             string
	UnitsPerEm         int
	Ascent             int // hhea.ascent
	Descent            int // hhea.descent (負値)
	LineGap            int // hhea.lineGap
	CapHeight          int
	XHeight            int
	BBox               [4]int
	ItalicAngle        float64
	Fixed              bool
	UnderlinePosition  int
	UnderlineThickness int

	data        []byte
	tables      map[string]tableRec
	numGlyphs   int
	numHMetrics int
	locaLong    bool
	cmap        map[rune]uint16
	advances    []uint16
	kern        map[uint32]int16

	// PDF への埋め込み状態
	used    map[uint16]rune
	resName string
	objID   int
}

type tableRec struct{ off, length uint32 }

var be = binary.BigEndian

// ParseFont は TrueType (glyf アウトライン) フォントを解析します。
func ParseFont(data []byte) (*Font, error) {
	if len(data) < 12 {
		return nil, errors.New("font data too short")
	}
	offset := uint32(0)
	tag := string(data[0:4])
	if tag == "ttcf" {
		// TrueType Collection: 最初のフォントを使う
		if len(data) < 16 {
			return nil, errors.New("invalid ttc")
		}
		offset = be.Uint32(data[12:16])
	}
	if int(offset)+12 > len(data) {
		return nil, errors.New("invalid font offset")
	}
	sfnt := be.Uint32(data[offset : offset+4])
	if sfnt == 0x4F54544F {
		return nil, errors.New("OpenType/CFF fonts are not supported (TrueType outlines required)")
	}
	if sfnt != 0x00010000 && sfnt != 0x74727565 {
		return nil, fmt.Errorf("unsupported font format %08x", sfnt)
	}
	n := int(be.Uint16(data[offset+4 : offset+6]))
	f := &Font{data: data, tables: map[string]tableRec{}, used: map[uint16]rune{}}
	for i := 0; i < n; i++ {
		p := int(offset) + 12 + 16*i
		if p+16 > len(data) {
			return nil, errors.New("truncated table directory")
		}
		rec := tableRec{be.Uint32(data[p+8 : p+12]), be.Uint32(data[p+12 : p+16])}
		if int(rec.off)+int(rec.length) > len(data) {
			return nil, errors.New("table out of bounds")
		}
		f.tables[string(data[p:p+4])] = rec
	}
	for _, req := range []string{"head", "hhea", "maxp", "hmtx", "cmap", "glyf", "loca"} {
		if _, ok := f.tables[req]; !ok {
			return nil, fmt.Errorf("font is missing required table %q", req)
		}
	}
	head := f.table("head")
	f.UnitsPerEm = int(be.Uint16(head[18:20]))
	for i := 0; i < 4; i++ {
		f.BBox[i] = int(int16(be.Uint16(head[36+2*i : 38+2*i])))
	}
	f.locaLong = be.Uint16(head[50:52]) != 0
	hhea := f.table("hhea")
	f.Ascent = int(int16(be.Uint16(hhea[4:6])))
	f.Descent = int(int16(be.Uint16(hhea[6:8])))
	f.LineGap = int(int16(be.Uint16(hhea[8:10])))
	f.numHMetrics = int(be.Uint16(hhea[34:36]))
	f.numGlyphs = int(be.Uint16(f.table("maxp")[4:6]))
	hmtx := f.table("hmtx")
	f.advances = make([]uint16, f.numGlyphs)
	var last uint16
	for i := 0; i < f.numGlyphs; i++ {
		if i < f.numHMetrics && 4*i+2 <= len(hmtx) {
			last = be.Uint16(hmtx[4*i : 4*i+2])
		}
		f.advances[i] = last
	}
	f.CapHeight = f.Ascent
	if os2 := f.table("OS/2"); len(os2) >= 78 {
		// ttfunk (Prawn) と同じく、OS/2 の typo メトリクスが 0 でなければ hhea より優先する
		if v := int(int16(be.Uint16(os2[68:70]))); v != 0 {
			f.Ascent = v
		}
		if v := int(int16(be.Uint16(os2[70:72]))); v != 0 {
			f.Descent = v
		}
		if v := int(int16(be.Uint16(os2[72:74]))); v != 0 {
			f.LineGap = v
		}
		ver := be.Uint16(os2[0:2])
		if ver >= 2 && len(os2) >= 90 {
			f.XHeight = int(int16(be.Uint16(os2[86:88])))
			f.CapHeight = int(int16(be.Uint16(os2[88:90])))
		}
	}
	if post := f.table("post"); len(post) >= 16 {
		f.ItalicAngle = float64(int32(be.Uint32(post[4:8]))) / 65536.0
		f.UnderlinePosition = int(int16(be.Uint16(post[8:10])))
		f.UnderlineThickness = int(int16(be.Uint16(post[10:12])))
		f.Fixed = be.Uint32(post[12:16]) != 0
	}
	f.parseName()
	if err := f.parseCmap(); err != nil {
		return nil, err
	}
	f.parseKern()
	return f, nil
}

func (f *Font) table(tag string) []byte {
	rec, ok := f.tables[tag]
	if !ok {
		return nil
	}
	return f.data[rec.off : rec.off+rec.length]
}

func (f *Font) parseName() {
	t := f.table("name")
	if len(t) < 6 {
		f.PSName = "Font"
		return
	}
	count := int(be.Uint16(t[2:4]))
	strOff := int(be.Uint16(t[4:6]))
	for i := 0; i < count; i++ {
		p := 6 + 12*i
		if p+12 > len(t) {
			break
		}
		platform := be.Uint16(t[p : p+2])
		nameID := be.Uint16(t[p+6 : p+8])
		length := int(be.Uint16(t[p+8 : p+10]))
		off := strOff + int(be.Uint16(t[p+10:p+12]))
		if nameID != 6 || off+length > len(t) {
			continue
		}
		raw := t[off : off+length]
		var s []byte
		if platform == 3 || platform == 0 {
			for j := 0; j+1 < len(raw); j += 2 {
				s = append(s, raw[j+1])
			}
		} else {
			s = raw
		}
		clean := make([]byte, 0, len(s))
		for _, c := range s {
			if c > 32 && c < 127 && c != '/' && c != '(' && c != ')' && c != '[' && c != ']' && c != '<' && c != '>' && c != '%' {
				clean = append(clean, c)
			}
		}
		if len(clean) > 0 {
			f.PSName = string(clean)
			return
		}
	}
	f.PSName = "Font"
}

func (f *Font) parseCmap() error {
	t := f.table("cmap")
	n := int(be.Uint16(t[2:4]))
	best, bestScore := -1, -1
	for i := 0; i < n; i++ {
		p := 4 + 8*i
		if p+8 > len(t) {
			break
		}
		platform, enc := be.Uint16(t[p:p+2]), be.Uint16(t[p+2:p+4])
		off := int(be.Uint32(t[p+4 : p+8]))
		if off+2 > len(t) {
			continue
		}
		format := be.Uint16(t[off : off+2])
		score := -1
		switch {
		case platform == 3 && enc == 10 && format == 12:
			score = 5
		case platform == 0 && format == 12:
			score = 4
		case platform == 3 && enc == 1 && format == 4:
			score = 3
		case platform == 0 && format == 4:
			score = 2
		case platform == 3 && enc == 0 && format == 4:
			score = 1
		}
		if score > bestScore {
			best, bestScore = off, score
		}
	}
	if best < 0 {
		return errors.New("font has no supported unicode cmap")
	}
	f.cmap = map[rune]uint16{}
	st := t[best:]
	switch be.Uint16(st[0:2]) {
	case 4:
		segX2 := int(be.Uint16(st[6:8]))
		endOff := 14
		startOff := endOff + segX2 + 2
		deltaOff := startOff + segX2
		rangeOff := deltaOff + segX2
		for i := 0; i < segX2/2; i++ {
			end := int(be.Uint16(st[endOff+2*i:]))
			start := int(be.Uint16(st[startOff+2*i:]))
			delta := int(be.Uint16(st[deltaOff+2*i:]))
			ro := int(be.Uint16(st[rangeOff+2*i:]))
			for c := start; c <= end && c < 0xFFFF; c++ {
				var gid int
				if ro == 0 {
					gid = (c + delta) & 0xFFFF
				} else {
					p := rangeOff + 2*i + ro + 2*(c-start)
					if p+2 > len(st) {
						continue
					}
					gid = int(be.Uint16(st[p:]))
					if gid != 0 {
						gid = (gid + delta) & 0xFFFF
					}
				}
				if gid != 0 && gid < f.numGlyphs {
					f.cmap[rune(c)] = uint16(gid)
				}
			}
		}
	case 12:
		ng := int(be.Uint32(st[12:16]))
		for i := 0; i < ng; i++ {
			p := 16 + 12*i
			if p+12 > len(st) {
				break
			}
			start, end, gid := be.Uint32(st[p:]), be.Uint32(st[p+4:]), be.Uint32(st[p+8:])
			for c := start; c <= end; c++ {
				g := gid + (c - start)
				if g != 0 && int(g) < f.numGlyphs {
					f.cmap[rune(c)] = uint16(g)
				}
			}
		}
	}
	return nil
}

// parseKern は kern テーブル (format 0) を読み込みます。Prawn (ttfunk) と同じ範囲です。
func (f *Font) parseKern() {
	t := f.table("kern")
	if len(t) < 4 || be.Uint16(t[0:2]) != 0 {
		return
	}
	n := int(be.Uint16(t[2:4]))
	p := 4
	for i := 0; i < n && p+6 <= len(t); i++ {
		length := int(be.Uint16(t[p+2 : p+4]))
		coverage := be.Uint16(t[p+4 : p+6])
		format := coverage >> 8
		horizontal := coverage&1 != 0
		minimum := coverage&2 != 0
		cross := coverage&4 != 0
		_, _, _ = horizontal, minimum, cross
		if format == 0 && p+14 <= len(t) {
			pairs := int(be.Uint16(t[p+6 : p+8]))
			if f.kern == nil {
				f.kern = make(map[uint32]int16, pairs)
			}
			q := p + 14
			for j := 0; j < pairs && q+6 <= len(t); j++ {
				l, r := be.Uint16(t[q:]), be.Uint16(t[q+2:])
				f.kern[uint32(l)<<16|uint32(r)] = int16(be.Uint16(t[q+4:]))
				q += 6
			}
			// Prawn は最初の format 0 サブテーブルしか参照しない
			return
		}
		if length == 0 {
			break
		}
		p += length
	}
}

// GlyphID は文字に対応するグリフ ID を返します。
func (f *Font) GlyphID(r rune) (uint16, bool) {
	g, ok := f.cmap[r]
	return g, ok
}

// HasGlyph はフォントが文字のグリフを持つかどうかを返します。
func (f *Font) HasGlyph(r rune) bool {
	_, ok := f.cmap[r]
	return ok
}

// Advance はグリフの送り幅 (フォント単位) を返します。
func (f *Font) Advance(gid uint16) int {
	if int(gid) < len(f.advances) {
		return int(f.advances[gid])
	}
	return 0
}

// Width1000 はグリフの送り幅を 1/1000 em 単位の整数 (切り捨て) で返します。
// Prawn と同じ丸め方で、レイアウトと PDF の幅テーブルの両方に使います。
func (f *Font) Width1000(gid uint16) int {
	return int(float64(f.Advance(gid)) * 1000 / float64(f.UnitsPerEm))
}

// Scaled1000 はフォント単位の値を 1/1000 em 単位の整数 (0 方向へ切り捨て) に変換します。
func (f *Font) Scaled1000(v int) int {
	return int(float64(v) * 1000 / float64(f.UnitsPerEm))
}

// Kern は 2 つのグリフ間のカーニング量 (フォント単位) を返します。
func (f *Font) Kern(l, r uint16) int {
	if f.kern == nil {
		return 0
	}
	return int(f.kern[uint32(l)<<16|uint32(r)])
}

// MarkUsed は埋め込み対象としてグリフを記録します。
func (f *Font) MarkUsed(gid uint16, r rune) {
	if _, ok := f.used[gid]; !ok {
		f.used[gid] = r
	}
}

func (f *Font) glyphRange(gid int) (int, int) {
	loca := f.table("loca")
	if f.locaLong {
		if 4*gid+8 > len(loca) {
			return 0, 0
		}
		return int(be.Uint32(loca[4*gid:])), int(be.Uint32(loca[4*gid+4:]))
	}
	if 2*gid+4 > len(loca) {
		return 0, 0
	}
	return 2 * int(be.Uint16(loca[2*gid:])), 2 * int(be.Uint16(loca[2*gid+2:]))
}

// subset は使用済みグリフだけを残したフォントプログラムを生成します。
// グリフ ID は元のまま保持し、未使用グリフのアウトラインを空にします。
func (f *Font) subset() []byte {
	glyf := f.table("glyf")
	keep := map[int]bool{0: true}
	var add func(g int)
	add = func(g int) {
		if g >= f.numGlyphs || keep[g] {
			return
		}
		keep[g] = true
		s, e := f.glyphRange(g)
		if e <= s || e > len(glyf) || e-s < 10 {
			return
		}
		d := glyf[s:e]
		if int16(be.Uint16(d[0:2])) >= 0 {
			return
		}
		// 複合グリフ: 構成要素も保持する
		p := 10
		for p+4 <= len(d) {
			flags := be.Uint16(d[p:])
			add(int(be.Uint16(d[p+2:])))
			p += 4
			if flags&1 != 0 {
				p += 4
			} else {
				p += 2
			}
			switch {
			case flags&8 != 0:
				p += 2
			case flags&0x40 != 0:
				p += 4
			case flags&0x80 != 0:
				p += 8
			}
			if flags&0x20 == 0 {
				break
			}
		}
	}
	keep[0] = false
	add(0)
	for g := range f.used {
		add(int(g))
	}
	newGlyf := make([]byte, 0, len(glyf)/4)
	newLoca := make([]byte, 4*(f.numGlyphs+1))
	for g := 0; g < f.numGlyphs; g++ {
		be.PutUint32(newLoca[4*g:], uint32(len(newGlyf)))
		if keep[g] {
			s, e := f.glyphRange(g)
			if e > s && e <= len(glyf) {
				newGlyf = append(newGlyf, glyf[s:e]...)
				for len(newGlyf)%4 != 0 {
					newGlyf = append(newGlyf, 0)
				}
			}
		}
	}
	be.PutUint32(newLoca[4*f.numGlyphs:], uint32(len(newGlyf)))
	head := append([]byte(nil), f.table("head")...)
	be.PutUint32(head[8:12], 0)  // checkSumAdjustment
	be.PutUint16(head[50:52], 1) // long loca
	out := map[string][]byte{"head": head, "glyf": newGlyf, "loca": newLoca}
	for _, tag := range []string{"hhea", "maxp", "hmtx", "cvt ", "fpgm", "prep"} {
		if t := f.table(tag); t != nil {
			out[tag] = t
		}
	}
	tags := make([]string, 0, len(out))
	for tag := range out {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	n := len(tags)
	searchRange, entrySel := 1, 0
	for searchRange*2 <= n {
		searchRange *= 2
		entrySel++
	}
	buf := make([]byte, 12+16*n)
	be.PutUint32(buf[0:], 0x00010000)
	be.PutUint16(buf[4:], uint16(n))
	be.PutUint16(buf[6:], uint16(searchRange*16))
	be.PutUint16(buf[8:], uint16(entrySel))
	be.PutUint16(buf[10:], uint16(n*16-searchRange*16))
	headOff := 0
	for i, tag := range tags {
		d := out[tag]
		off := len(buf)
		if tag == "head" {
			headOff = off
		}
		buf = append(buf, d...)
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		p := 12 + 16*i
		copy(buf[p:], tag)
		be.PutUint32(buf[p+4:], tableChecksum(d))
		be.PutUint32(buf[p+8:], uint32(off))
		be.PutUint32(buf[p+12:], uint32(len(d)))
	}
	be.PutUint32(buf[headOff+8:], 0xB1B0AFBA-tableChecksum(buf))
	return buf
}

func tableChecksum(d []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(d); i += 4 {
		sum += be.Uint32(d[i:])
	}
	if rem := len(d) % 4; rem != 0 {
		var last [4]byte
		copy(last[:], d[len(d)-rem:])
		sum += be.Uint32(last[:])
	}
	return sum
}
