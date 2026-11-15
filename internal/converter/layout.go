package converter

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

type inlineImage struct {
	path, format, width, fit, alt string
	img                           *pdf.Image
	w, h                          float64
}

// piece は 1 つのフォントで描画できる断片の一部です。
type piece struct {
	f     *frag
	font  *pdf.Font
	size  float64
	asc   float64
	desc  float64
	lineH float64
	// yOff は上付き・下付きの垂直オフセット
	yOff float64
}

type tokKind int

const (
	tokWord tokKind = iota
	tokSpace
	tokNewline
	tokZWSP
	tokMarker
	tokImage
)

type token struct {
	p      *piece
	kind   tokKind
	glyphs []pdf.Glyph
	width  float64
	spaces int
	// endsBreak は末尾がハイフンなど改行可能な文字で終わるか
	endsBreak bool
	shy       bool
	fragIdx   int
	// nrunes は元のテキストで消費した文字数
	nrunes int
	padL   float64
	padR   float64
}

type textLine struct {
	toks    []token
	width   float64
	asc     float64
	desc    float64
	lineH   float64
	spaces  int
	paraEnd bool
	// first は段落の最初の行かどうか
	first bool
	// end は次の行の最初のトークンの位置
	end int
}

type textOpts struct {
	align               string
	leading             float64
	indentFirst         float64
	normalizeLineHeight bool
	charSpacing         float64
	forceJustify        bool
	singleLine          bool
	bottomGutter        float64
	// skipFirstGap は最初のページで行頭の余白 (initial gap) を入れない
	skipFirstGap bool
	noKerning    bool
}

// resolveSize はフォントサイズの指定 (数値・em・rem・%) を pt に解決します。
func (c *Converter) resolveSize(spec any, cur float64) float64 {
	switch v := spec.(type) {
	case float64:
		if v == 1 {
			return c.rootFontSize
		}
		if v < 1 {
			return cur * v
		}
		return v
	case string:
		n := theme.StrToPt(strings.TrimRight(v, "rem%"))
		if f, err := strconv.ParseFloat(strings.TrimRight(v, "rem%"), 64); err == nil {
			n = f
		}
		switch {
		case strings.HasSuffix(v, "rem"):
			return c.rootFontSize * n
		case strings.HasSuffix(v, "em"):
			return cur * n
		case strings.HasSuffix(v, "%"):
			return cur * n / 100
		}
		if n > 0 {
			return n
		}
	}
	return cur
}

const subSupRelativeSize = 0.583

func isIgnorable(r rune) bool {
	// WORD JOINER / BOM / ZWNJ / ZWJ
	return r == 0x2060 || r == 0xFEFF || r == 0x200C || r == 0x200D
}

// tokenize は断片をフォント (フォールバック込み) ごとの piece に分割し、トークン化します。
func (c *Converter) tokenize(frags []frag, o textOpts) []token {
	var toks []token
	for fi := range frags {
		f := &frags[fi]
		family := f.family
		if family == "" {
			family = c.fontFamily
		}
		// 断片にスタイル指定があればそれを使い、無ければ現在のフォントのスタイルを使う (Prawn と同じ)
		style := styleName(c.fontBold, c.fontItalic)
		if f.bold || f.italic {
			style = styleName(f.bold, f.italic)
		}
		size := c.fontSize
		if f.sizeRel != "" {
			size = c.resolveSize(f.sizeRel, c.fontSize)
		} else if f.size > 0 {
			size = f.size
		}
		if f.sub || f.sup {
			size *= subSupRelativeSize
		}
		primary := c.fonts.get(family, style)
		if primary == nil {
			primary = c.fonts.get(c.fontFamily, style)
		}
		if primary == nil {
			primary = c.fonts.get(c.theme.Str("base_font_family"), "normal")
		}
		if primary == nil {
			continue
		}
		mk := func(font *pdf.Font) *piece {
			p := &piece{f: f, font: font, size: size, asc: ascender(font, size), desc: descender(font, size), lineH: fontHeight(font, size)}
			if f.sub {
				p.yOff = -p.desc
			} else if f.sup {
				p.yOff = 0.85 * p.asc
			}
			return p
		}
		start := len(toks)
		if f.image != nil {
			p := mk(primary)
			c.prepareInlineImage(f, p)
			toks = append(toks, token{p: p, kind: tokImage, width: f.image.w, fragIdx: fi, nrunes: len([]rune(f.text))})
		} else if f.text == dummyText {
			toks = append(toks, token{p: mk(primary), kind: tokMarker, fragIdx: fi, nrunes: 1})
		} else {
			// フォールバックフォントを考慮してフォントごとの連続区間に分ける
			var cur *piece
			var buf []rune
			flush := func() {
				if cur != nil && len(buf) > 0 {
					toks = c.tokenizePiece(toks, cur, buf, fi, o)
				}
				buf = buf[:0]
			}
			for _, r := range f.text {
				font := primary
				if r != '\n' && r != ' ' && r != '\t' && r != '​' && r != shy && !isIgnorable(r) && !primary.HasGlyph(r) {
					found := false
					for _, fb := range c.fallbackFonts {
						if ff := c.fonts.get(fb, style); ff != nil && ff.HasGlyph(r) {
							font, found = ff, true
							break
						}
					}
					if !found {
						c.warnMissingGlyph(r, family)
					}
				}
				if cur == nil || cur.font != font {
					flush()
					cur = mk(font)
				}
				buf = append(buf, r)
			}
			flush()
		}
		// 枠・背景のオフセット分を断片の両端に加える
		if f.hasBorderOffset && len(toks) > start {
			toks[start].padL = f.borderOffset
			toks[start].width += f.borderOffset
			toks[len(toks)-1].padR = f.borderOffset
			toks[len(toks)-1].width += f.borderOffset
		}
		if f.width != "" && len(toks) == start+1 {
			var w float64
			if strings.HasSuffix(f.width, "em") {
				n, _ := strconv.ParseFloat(strings.TrimSuffix(f.width, "em"), 64)
				w = n * c.fontSize
			} else {
				w = theme.StrToPt(f.width)
			}
			if extra := w - toks[start].width; extra > 0 {
				switch f.align {
				case "right":
					toks[start].padL += extra
				case "center":
					toks[start].padL += extra / 2
					toks[start].padR += extra / 2
				default:
					toks[start].padR += extra
				}
				toks[start].width = w
			}
		}
	}
	return toks
}

func (c *Converter) warnMissingGlyph(r rune, family string) {
	if c.scratch > 0 || c.missingChars[r] || unicode.IsControl(r) {
		return
	}
	c.missingChars[r] = true
	fonts := append([]string{family}, c.fallbackFonts...)
	c.logf("WARN", "Could not locate the character `%c' (\\u%04x) in the following fonts: %s", r, r, strings.Join(fonts, ", "))
}

func (c *Converter) glyphsFor(p *piece, runes []rune, o textOpts) ([]pdf.Glyph, float64) {
	scale := p.size / float64(p.font.UnitsPerEm)
	glyphs := make([]pdf.Glyph, 0, len(runes))
	width := 0.0
	var prev uint16
	hasPrev := false
	for _, r := range runes {
		if isIgnorable(r) {
			continue
		}
		if r == '\t' {
			r = ' '
		}
		gid, ok := p.font.GlyphID(r)
		if !ok {
			if r == ' ' || r == ' ' || r == ' ' || r == '　' {
				gid, _ = p.font.GlyphID(' ')
			}
		}
		if c.scratch == 0 {
			p.font.MarkUsed(gid, r)
		}
		if hasPrev && c.kerning && !o.noKerning {
			if k := p.font.Kern(prev, gid); k != 0 {
				adj := float64(k) * scale
				glyphs[len(glyphs)-1].Adjust += adj
				width += adj
			}
		}
		adv := float64(p.font.Width1000(gid)) / 1000 * p.size
		glyphs = append(glyphs, pdf.Glyph{ID: gid, Adjust: o.charSpacing})
		width += adv + o.charSpacing
		prev, hasPrev = gid, true
	}
	return glyphs, width
}

func isBreakRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '​' || r == shy || r == '-'
}

// tokenizePiece は Prawn の LineWrap#tokenize と同じ規則で文字列を分割します。
func (c *Converter) tokenizePiece(toks []token, p *piece, runes []rune, fi int, o textOpts) []token {
	add := func(kind tokKind, text []rune, endsBreak, isShy bool) {
		t := token{p: p, kind: kind, fragIdx: fi, endsBreak: endsBreak, shy: isShy, nrunes: len(text)}
		if isShy || kind == tokNewline || kind == tokZWSP {
			t.nrunes++
		}
		if kind != tokNewline && kind != tokZWSP {
			t.glyphs, t.width = c.glyphsFor(p, text, o)
		}
		if kind == tokSpace {
			for _, r := range text {
				if r == ' ' || r == '\t' {
					t.spaces++
				}
			}
		}
		toks = append(toks, t)
	}
	if p.f.singleToken {
		// 改行を除き、全体を 1 トークンとして扱う
		start := 0
		for i, r := range runes {
			if r == '\n' {
				if i > start {
					add(tokWord, runes[start:i], false, false)
				}
				add(tokNewline, nil, false, false)
				start = i + 1
			}
		}
		if start < len(runes) {
			add(tokWord, runes[start:], false, false)
		}
		return toks
	}
	n := len(runes)
	i := 0
	for i < n {
		r := runes[i]
		switch {
		case r == '\n':
			add(tokNewline, nil, false, false)
			i++
		case r == '​':
			add(tokZWSP, nil, true, false)
			i++
		case r == ' ' || r == '\t':
			j := i
			for j < n && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			add(tokSpace, runes[i:j], true, false)
			i = j
		case r == shy:
			add(tokWord, nil, true, true)
			i++
		case r == '-':
			j := i
			for j < n && runes[j] == '-' {
				j++
			}
			for j < n && !isBreakRune(runes[j]) && runes[j] != '\n' {
				j++
			}
			add(tokWord, runes[i:j], false, false)
			i = j
		default:
			j := i
			for j < n && !isBreakRune(runes[j]) && runes[j] != '\n' {
				j++
			}
			switch {
			case j < n && runes[j] == shy:
				add(tokWord, runes[i:j], true, true)
				j++
			case j < n && runes[j] == '-':
				for j < n && runes[j] == '-' {
					j++
				}
				add(tokWord, runes[i:j], true, false)
			default:
				add(tokWord, runes[i:j], false, false)
			}
			i = j
		}
	}
	return toks
}

func glued(a, b *token) bool {
	if b.kind == tokSpace || b.kind == tokNewline || b.kind == tokZWSP {
		return false
	}
	if a.kind == tokSpace || a.kind == tokNewline || a.kind == tokZWSP || a.endsBreak {
		return false
	}
	// 断片をまたぐ単語、および word joiner 指定の断片は分割しない
	return a.p != b.p || b.p.f.wj
}

// wrapLines はトークン列を行に分割します。widthAt は行ごとの利用可能幅を返します。
func (c *Converter) wrapLines(toks []token, o textOpts, widthAt func(first bool) float64) []textLine {
	var lines []textLine
	cur := textLine{first: true}
	acc := 0.0
	division := false
	hasContent := false
	var lastPiece *piece
	i := 0
	finish := func(paraEnd bool, metricsFrom *piece) {
		cur.end = i
		// 行末の空白は幅に含めない
		for len(cur.toks) > 0 {
			last := cur.toks[len(cur.toks)-1]
			if last.kind == tokSpace && !last.p.f.preserveSpace {
				acc -= last.width
				cur.toks = cur.toks[:len(cur.toks)-1]
			} else {
				break
			}
		}
		// 行末のソフトハイフンは表示する
		if n := len(cur.toks); n > 0 && cur.toks[n-1].shy && !paraEnd {
			t := &cur.toks[n-1]
			if gid, ok := t.p.font.GlyphID('-'); ok {
				if c.scratch == 0 {
					t.p.font.MarkUsed(gid, '-')
				}
				w := float64(t.p.font.Width1000(gid)) / 1000 * t.p.size
				t.glyphs = append(append([]pdf.Glyph{}, t.glyphs...), pdf.Glyph{ID: gid})
				t.width += w
				acc += w
			}
		}
		cur.width = acc
		cur.paraEnd = paraEnd
		for _, t := range cur.toks {
			cur.spaces += t.spaces
			if t.p.asc > cur.asc {
				cur.asc = t.p.asc
			}
			if t.p.desc > cur.desc {
				cur.desc = t.p.desc
			}
			if t.p.lineH > cur.lineH {
				cur.lineH = t.p.lineH
			}
		}
		if len(cur.toks) == 0 || o.normalizeLineHeight {
			mp := metricsFrom
			if mp == nil || o.normalizeLineHeight {
				if f := c.currentFont(); f != nil {
					mp = &piece{asc: ascender(f, c.fontSize), desc: descender(f, c.fontSize), lineH: fontHeight(f, c.fontSize)}
				}
			}
			if mp != nil {
				if mp.asc > cur.asc {
					cur.asc = mp.asc
				}
				if mp.desc > cur.desc {
					cur.desc = mp.desc
				}
				if mp.lineH > cur.lineH {
					cur.lineH = mp.lineH
				}
			}
		}
		lines = append(lines, cur)
		cur = textLine{first: paraEnd}
		acc = 0
		division = false
		hasContent = false
	}
	const eps = 0.0001
	for i < len(toks) {
		t := toks[i]
		W := widthAt(cur.first)
		switch t.kind {
		case tokNewline:
			mp := t.p
			if lastPiece != nil && hasContent {
				mp = nil
			}
			finish(true, mp)
			i++
			continue
		case tokSpace:
			// 行の幅がまだ 0 の間 (不可視のマーカーだけの場合を含む) は行頭の空白を捨てる
			if acc == 0 && !t.p.f.preserveSpace {
				i++
				continue
			}
			if acc+t.width <= W+eps || t.p.f.preserveSpace {
				cur.toks = append(cur.toks, t)
				acc += t.width
				division = true
				i++
			} else {
				finish(false, nil)
				i++
			}
			continue
		case tokZWSP:
			if hasContent {
				division = true
			}
			i++
			continue
		}
		// 分割できないトークンのまとまり
		j := i + 1
		for j < len(toks) && glued(&toks[j-1], &toks[j]) {
			j++
		}
		cw := 0.0
		for k := i; k < j; k++ {
			cw += toks[k].width
		}
		if acc+cw <= W+eps {
			for k := i; k < j; k++ {
				cur.toks = append(cur.toks, toks[k])
				if toks[k].endsBreak {
					division = true
				}
				lastPiece = toks[k].p
			}
			acc += cw
			hasContent = true
			i = j
			continue
		}
		if division || hasContent {
			finish(false, nil)
			continue
		}
		// 1 語だけで行幅を超える場合は文字単位で折り返す
		k := i
		for k < j {
			tk := toks[k]
			if acc+tk.width <= W+eps {
				cur.toks = append(cur.toks, tk)
				acc += tk.width
				hasContent = true
				k++
				continue
			}
			w := tk.padL
			n := 0
			for n < len(tk.glyphs) {
				gw := float64(tk.p.font.Width1000(tk.glyphs[n].ID))/1000*tk.p.size + tk.glyphs[n].Adjust
				if acc+w+gw > W+eps && (n > 0 || hasContent) {
					break
				}
				w += gw
				n++
			}
			if n >= len(tk.glyphs) {
				// 分割できない (または 1 文字も入らない) 場合はそのまま配置する
				if len(tk.glyphs) > 0 && hasContent && n == 0 {
					break
				}
				cur.toks = append(cur.toks, tk)
				acc += tk.width
				hasContent = true
				k++
				if len(tk.glyphs) == 0 {
					continue
				}
				break
			}
			if n > 0 {
				head, tail := tk, tk
				head.glyphs, head.width, head.padR, head.shy, head.endsBreak = tk.glyphs[:n], w, 0, false, false
				tail.glyphs, tail.width, tail.padL = tk.glyphs[n:], tk.width-w, 0
				head.nrunes, tail.nrunes = n, tk.nrunes-n
				cur.toks = append(cur.toks, head)
				acc += w
				toks[k] = tail
				hasContent = true
			}
			break
		}
		i = k
		if i < len(toks) {
			finish(false, nil)
		}
	}
	if len(cur.toks) > 0 || len(lines) == 0 {
		var mp *piece
		if len(cur.toks) == 0 && len(toks) > 0 {
			mp = toks[len(toks)-1].p
		}
		finish(true, mp)
	}
	if o.singleLine && len(lines) > 1 {
		lines = lines[:1]
	}
	return lines
}

// linesHeight は行の集合の高さ (Prawn の Text::Box#height) を返します。
func linesHeight(lines []textLine, leading float64) float64 {
	if len(lines) == 0 {
		return 0
	}
	h := lines[0].asc
	for _, ln := range lines[1:] {
		h += ln.lineH + leading
	}
	return h + lines[len(lines)-1].desc
}

// drawLine は 1 行を (x, baseline) から幅 width の範囲に描画します。
func (c *Converter) drawLine(ln *textLine, x, baseline, width float64, o textOpts) {
	if len(ln.toks) == 0 {
		return
	}
	wordSpacing := 0.0
	switch o.align {
	case "center":
		x += (width - ln.width) / 2
	case "right":
		x += width - ln.width
	case "justify":
		if ln.spaces > 0 && (!ln.paraEnd || o.forceJustify) {
			wordSpacing = (width - ln.width) / float64(ln.spaces)
		}
	}
	lastTok := ln.toks[len(ln.toks)-1]
	c.lastLine = &linePos{page: c.page, right: x + ln.width, top: baseline + lastTok.p.asc}
	if c.scratch > 0 {
		c.inked()
		return
	}
	page := c.pdf.Pages[c.page]
	bgG := pdf.NewG()
	textG := pdf.NewG()
	overG := pdf.NewG()
	// 同じ断片に属する連続トークンをまとめて処理する
	i := 0
	cx := x
	for i < len(ln.toks) {
		j := i
		f := ln.toks[i].p.f
		startX := cx
		for j < len(ln.toks) && ln.toks[j].p.f == f {
			t := &ln.toks[j]
			tx := cx + t.padL
			tw := t.width + float64(t.spaces)*wordSpacing
			switch t.kind {
			case tokImage:
				c.drawInlineImage(textG, f, tx, baseline, t.p)
			case tokMarker:
				c.addInlineDest(f, tx, baseline+t.p.asc)
			default:
				if len(t.glyphs) > 0 {
					run := pdf.TextRun{Font: t.p.font, Size: t.p.size, Color: c.fragColor(f).pdf()}
					appendTok := func(tk *token) {
						for _, g := range tk.glyphs {
							if wordSpacing != 0 && tk.spaces > 0 {
								g.Adjust += wordSpacing
							}
							run.Glyphs = append(run.Glyphs, g)
						}
					}
					appendTok(t)
					// 直後のトークンが同じ piece なら 1 つの TJ にまとめる
					k := j + 1
					for k < len(ln.toks) && ln.toks[k].p == t.p && len(ln.toks[k].glyphs) > 0 && ln.toks[k].padL == 0 && ln.toks[k-1].padR == 0 {
						nt := &ln.toks[k]
						appendTok(nt)
						tw += nt.width + float64(nt.spaces)*wordSpacing
						k++
					}
					textG.Text(tx, baseline+t.p.yOff, run)
					cx += tw
					j = k
					continue
				}
			}
			cx += tw
			j++
		}
		endX := cx
		p := ln.toks[i].p
		if f.text != dummyText && endX > startX {
			top := baseline + p.yOff + p.asc
			bottom := baseline + p.yOff - p.desc
			if f.bg.set || (f.borderColor.set && f.borderWidth > 0) {
				off := 0.0
				if f.hasBorderOffset {
					off = f.borderOffset
				}
				bx, by, bw, bh := startX, bottom-off, endX-startX, (top-bottom)+2*off
				if f.bg.set {
					bgG.Save().FillColor(f.bg.pdf()).RoundedRect(bx, by, bw, bh, f.borderRadius).Fill().Restore()
				}
				if f.borderColor.set && f.borderWidth > 0 {
					bgG.Save().StrokeColor(f.borderColor.pdf()).LineWidth(f.borderWidth).RoundedRect(bx, by, bw, bh, f.borderRadius).Stroke().Restore()
				}
			}
			if f.underline || f.strike {
				lw := f.decoWidth
				if lw == 0 {
					lw = c.textDecorationWidth
				}
				if lw == 0 {
					lw = 1
				}
				col := c.fragColor(f)
				if f.decoColor.set {
					col = f.decoColor
				}
				overG.Save().StrokeColor(col.pdf()).LineWidth(lw)
				if f.underline {
					y := baseline + p.yOff - 1.25
					overG.MoveTo(startX, y).LineTo(endX, y).Stroke()
				}
				if f.strike {
					y := baseline + p.yOff + p.asc*0.3
					overG.MoveTo(startX, y).LineTo(endX, y).Stroke()
				}
				overG.Restore()
			}
			if f.link != "" {
				page.Annots = append(page.Annots, pdf.Annot{Rect: [4]float64{startX, bottom, endX, top}, URI: f.link})
			} else if f.anchor != "" {
				page.Annots = append(page.Annots, pdf.Annot{Rect: [4]float64{startX, bottom, endX, top}, Dest: f.anchor})
			}
		}
		i = j
	}
	page.Add(bgG.Bytes())
	page.Add(textG.Bytes())
	page.Add(overG.Bytes())
	c.inked()
}

func (c *Converter) fragColor(f *frag) clr {
	if f.color.set {
		return f.color
	}
	return c.fontColor
}

// fillText は断片を現在位置から流し込みます。ページをまたぐ場合は改ページします。
func (c *Converter) fillText(frags []frag, o textOpts, initialGap float64) {
	toks := c.tokenize(frags, o)
	width := c.width
	lines := c.wrapLines(toks, o, func(first bool) float64 {
		if first {
			return width - o.indentFirst
		}
		return width
	})
	i := 0
	stalled := false
	firstPage := true
	for i < len(lines) {
		if !(firstPage && o.skipFirstGap) {
			c.y -= initialGap
		}
		firstPage = false
		avail := c.y - c.bottom
		baseline := 0.0
		printed := 0
		for i < len(lines) {
			ln := &lines[i]
			var need float64
			if printed == 0 {
				need = ln.asc + ln.desc
			} else {
				need = baseline + ln.desc + ln.lineH + o.leading
			}
			limit := avail
			if i == len(lines)-1 && o.bottomGutter > 0 {
				limit -= o.bottomGutter
			}
			if need > limit+0.0001 {
				break
			}
			if printed == 0 {
				baseline = ln.asc
			} else {
				baseline += ln.lineH + o.leading
			}
			x, w := c.left, width
			if ln.first && o.indentFirst != 0 {
				x += o.indentFirst
				w -= o.indentFirst
			}
			c.drawLine(ln, x, c.y-baseline, w, o)
			printed++
			i++
		}
		if printed > 0 {
			c.y -= baseline + lines[i-1].desc
			stalled = false
		}
		if i < len(lines) {
			if printed == 0 {
				if stalled || c.atPageTopWithGap(initialGap) {
					c.logf("ERROR", "cannot fit formatted text on page: %s", fragsText(frags))
					break
				}
				stalled = true
			}
			c.advancePage()
		}
	}
}

func fragsText(frags []frag) string {
	var b strings.Builder
	for _, f := range frags {
		b.WriteString(f.text)
	}
	s := b.String()
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

// heightOfFragments は断片を幅 width に流し込んだときの高さを返します。
func (c *Converter) heightOfFragments(frags []frag, width float64, o textOpts) float64 {
	c.scratch++
	toks := c.tokenize(frags, o)
	c.scratch--
	lines := c.wrapLines(toks, o, func(first bool) float64 {
		if first {
			return width - o.indentFirst
		}
		return width
	})
	return linesHeight(lines, o.leading)
}

// widthOfFragments は断片を折り返さずに並べたときの幅を返します。
func (c *Converter) widthOfFragments(frags []frag, o textOpts) float64 {
	c.scratch++
	toks := c.tokenize(frags, o)
	c.scratch--
	max, cur := 0.0, 0.0
	for _, t := range toks {
		if t.kind == tokNewline {
			if cur > max {
				max = cur
			}
			cur = 0
			continue
		}
		cur += t.width
	}
	if cur > max {
		max = cur
	}
	return max
}

// drawTextBox は固定領域内にテキストを描画します (改ページしません)。
// 戻り値は描画した高さです。
func (c *Converter) drawTextBox(frags []frag, x, top, width, height float64, valign string, o textOpts) float64 {
	toks := c.tokenize(frags, o)
	lines := c.wrapLines(toks, o, func(bool) float64 { return width })
	// 領域に収まる行だけを描画する
	fit := 0
	baseline := 0.0
	for i := range lines {
		ln := &lines[i]
		var need float64
		if i == 0 {
			need = ln.asc + ln.desc
		} else {
			need = baseline + ln.desc + ln.lineH + o.leading
		}
		if height > 0 && need > height+0.0001 {
			break
		}
		if i == 0 {
			baseline = ln.asc
		} else {
			baseline += ln.lineH + o.leading
		}
		fit++
	}
	if fit == 0 {
		return 0
	}
	lines = lines[:fit]
	h := linesHeight(lines, o.leading)
	offset := 0.0
	if height > 0 {
		switch valign {
		case "center", "middle":
			offset = (height - h) / 2
		case "bottom":
			offset = height - h
		}
	}
	baseline = 0
	for i := range lines {
		ln := &lines[i]
		if i == 0 {
			baseline = ln.asc
		} else {
			baseline += ln.lineH + o.leading
		}
		c.drawLine(ln, x, top-offset-baseline, width, o)
	}
	return h
}

// consumedRunes は先頭から n 行分で消費した元テキストの文字数を返します。
func consumedRunes(toks []token, lines []textLine, n int) int {
	if n <= 0 || n > len(lines) {
		return 0
	}
	total := 0
	for k := 0; k < lines[n-1].end && k < len(toks); k++ {
		total += toks[k].nrunes
	}
	// 行の区切りになった改行・空白も消費済みとして数える
	if end := lines[n-1].end; end < len(toks) && lines[n-1].paraEnd && toks[end].kind == tokNewline {
		total += toks[end].nrunes
	}
	return total
}

// splitFrags は断片を先頭 n 文字とそれ以降に分けます。
func splitFrags(frags []frag, n int) (head, tail []frag) {
	for i, f := range frags {
		r := []rune(f.text)
		if f.image != nil {
			r = r[:len(r):len(r)]
		}
		switch {
		case n <= 0:
			tail = append(tail, frags[i:]...)
			return
		case len(r) <= n:
			head = append(head, f)
			n -= len(r)
		default:
			h, t := f, f
			h.text, t.text = string(r[:n]), string(r[n:])
			head = append(head, h)
			tail = append(tail, t)
			n = 0
		}
	}
	return
}

// firstLineOpts は段落の 1 行目だけに適用する書式です。
type firstLineOpts struct {
	hasStyle     bool
	bold, italic bool
	color        clr
	transform    string
}

// fillTextFirstLine は 1 行目だけ別の書式で配置し、残りを通常の書式で流し込みます。
func (c *Converter) fillTextFirstLine(frags []frag, o textOpts, initialGap float64, fl firstLineOpts) {
	saved := c.saveFont()
	if fl.hasStyle {
		c.fontBold, c.fontItalic = fl.bold, fl.italic
	}
	if fl.color.set {
		c.fontColor = fl.color
	}
	width := c.width - o.indentFirst
	toks := c.tokenize(frags, o)
	lines := c.wrapLines(toks, o, func(bool) float64 { return width })
	if len(lines) == 0 {
		c.restoreFont(saved)
		return
	}
	n := consumedRunes(toks, lines, 1)
	head, rest := splitFrags(frags, n)
	first := lines[0]
	fo := o
	if fl.transform != "" && fl.transform != "none" {
		for i := range head {
			head[i].text = transformPlain(head[i].text, fl.transform)
		}
		ht := c.tokenize(head, o)
		hl := c.wrapLines(ht, o, func(bool) float64 { return 1e9 })
		if len(hl) > 0 {
			first = hl[0]
			// 変換後に幅を超える場合は収まるよう縮小する
			if first.width > width {
				size := c.fontSize
				c.fontSize = size * width / first.width
				ht = c.tokenize(head, o)
				if hl = c.wrapLines(ht, o, func(bool) float64 { return 1e9 }); len(hl) > 0 {
					first = hl[0]
				}
				c.fontSize = size
			}
		}
		if len(rest) > 0 && o.align == "justify" && !lines[0].paraEnd {
			fo.forceJustify = true
		}
	}
	if len(rest) > 0 && !lines[0].paraEnd {
		first.paraEnd = false
	}
	c.y -= initialGap
	if c.cursor() < first.asc+first.desc && !c.atPageTopWithGap(initialGap) {
		c.advancePage()
		c.y -= initialGap
	}
	c.drawLine(&first, c.left+o.indentFirst, c.y-first.asc, width, fo)
	c.y -= first.asc + first.desc
	c.restoreFont(saved)
	if len(rest) > 0 {
		c.y -= (first.lineH - first.asc - first.desc) + o.leading
		ro := o
		ro.indentFirst = 0
		ro.skipFirstGap = true
		c.fillText(rest, ro, initialGap)
	}
}
