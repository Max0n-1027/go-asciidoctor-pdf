package converter

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

const (
	dummyText = "\x00"
	zwsp      = "​"
	nbsp      = " "
	shy       = '­'
)

// clr はテーマで指定される色です。
type clr struct {
	hex         string
	cmyk        []float64
	set         bool
	transparent bool
}

func toClr(v any) clr {
	switch x := v.(type) {
	case string:
		if x == "transparent" {
			return clr{transparent: true}
		}
		if x == "" {
			return clr{}
		}
		return clr{hex: x, set: true}
	case []float64:
		return clr{cmyk: x, set: true}
	case float64:
		return clr{hex: fmt.Sprintf("%06d", int(x)), set: true}
	}
	return clr{}
}

func hexClr(s string) clr { return clr{hex: s, set: s != ""} }

func (c clr) pdf() pdf.Color {
	if c.cmyk != nil && len(c.cmyk) == 4 {
		return pdf.CMYKColor(c.cmyk[0], c.cmyk[1], c.cmyk[2], c.cmyk[3])
	}
	return pdf.ParseColor(c.hex)
}

func (c clr) String() string {
	if c.cmyk != nil {
		parts := make([]string, len(c.cmyk))
		for i, v := range c.cmyk {
			parts[i] = theme.FormatNumber(v)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return c.hex
}

// frag は書式付きテキストの断片です (Prawn の formatted text fragment に相当)。
type frag struct {
	text   string
	family string
	bold   bool
	italic bool
	// noBold / noItalic は normal スタイルによる打ち消し
	underline, strike, sup, sub bool
	size                        float64
	sizeRel                     string
	color                       clr
	link, anchor, name          string
	destType                    string
	bg, borderColor             clr
	borderWidth                 float64
	borderRadius                float64
	borderOffset                float64
	hasBorderOffset             bool
	width                       string
	align                       string
	singleToken, wj             bool
	preserveSpace               bool
	transform                   string
	decoColor                   clr
	decoWidth                   float64
	image                       *inlineImage
	// inlineBlock はソースハイライトの行背景用
	callout bool
}

// props はタグやロールに対応するテーマ設定です。
type props struct {
	hasColor, hasBg, hasBorderColor, hasDecoColor bool
	color, bg, borderColor, decoColor             clr
	font                                          string
	size                                          any
	hasStyles                                     bool
	styles                                        []string
	borderWidth, borderOffset, borderRadius       *float64
	decoWidth                                     *float64
	transform                                     string
}

func toStyles(fontStyle, decoration string) ([]string, bool) {
	var styles []string
	ok := false
	switch fontStyle {
	case "bold":
		styles, ok = []string{"bold"}, true
	case "italic":
		styles, ok = []string{"italic"}, true
	case "bold_italic":
		styles, ok = []string{"bold", "italic"}, true
	case "normal_italic":
		styles, ok = []string{"normal", "italic"}, true
	case "normal":
		// font_style: normal は to_styles では nil になる
	}
	switch decoration {
	case "underline":
		styles, ok = append(styles, "underline"), true
	case "line-through":
		styles, ok = append(styles, "strikethrough"), true
	}
	return styles, ok
}

func (f *frag) apply(p props) {
	if p.hasColor {
		f.color = p.color
	}
	if p.font != "" {
		f.family = p.font
	}
	switch v := p.size.(type) {
	case float64:
		f.size, f.sizeRel = v, ""
	case string:
		if v != "" {
			f.sizeRel, f.size = v, 0
		}
	}
	if p.hasStyles {
		for _, s := range p.styles {
			switch s {
			case "normal":
				f.bold, f.italic = false, false
			case "bold":
				f.bold = true
			case "italic":
				f.italic = true
			case "underline":
				f.underline = true
			case "strikethrough":
				f.strike = true
			}
		}
	}
	if p.hasBg {
		f.bg = p.bg
	}
	if p.borderWidth != nil {
		f.borderWidth = *p.borderWidth
	}
	if p.hasBorderColor {
		f.borderColor = p.borderColor
	}
	if p.borderOffset != nil {
		f.borderOffset, f.hasBorderOffset = *p.borderOffset, true
		f.align = "center"
	}
	if p.borderRadius != nil {
		f.borderRadius = *p.borderRadius
	}
	if p.hasDecoColor {
		f.decoColor = p.decoColor
	}
	if p.decoWidth != nil {
		f.decoWidth = *p.decoWidth
	}
	if p.transform != "" {
		f.transform = p.transform
	}
}

// buildThemeSettings はタグ・ロールごとの書式設定をテーマから構築します。
func (c *Converter) buildThemeSettings() {
	t := c.theme
	settings := map[string]props{}
	num := func(key string) *float64 {
		if v, ok := t.Num(key); ok {
			return &v
		}
		return nil
	}
	boxed := func(prefix string, fontFallback string) props {
		p := props{}
		if v := t.Get(prefix + "_font_color"); v != nil {
			p.color, p.hasColor = toClr(v), true
		}
		p.font = t.Str(prefix + "_font_family")
		if p.font == "" {
			p.font = fontFallback
		}
		p.size = t.Get(prefix + "_font_size")
		p.styles, p.hasStyles = toStyles(t.Str(prefix+"_font_style"), "")
		bg := t.Get(prefix + "_background_color")
		bw := num(prefix + "_border_width")
		if bg != nil {
			p.bg, p.hasBg = toClr(bg), true
		}
		if bw != nil {
			p.borderWidth = bw
			bc := t.Get(prefix + "_border_color")
			if bc == nil {
				bc = t.Get("base_border_color")
			}
			p.borderColor, p.hasBorderColor = toClr(bc), true
		}
		if bg != nil || bw != nil {
			p.borderOffset = num(prefix + "_border_offset")
			p.borderRadius = num(prefix + "_border_radius")
		}
		return p
	}
	settings["button"] = boxed("button", "")
	settings["code"] = boxed("codespan", "")
	settings["kbd"] = boxed("kbd", t.Str("codespan_font_family"))
	link := props{}
	if v := t.Get("link_font_color"); v != nil {
		link.color, link.hasColor = toClr(v), true
	}
	link.font = t.Str("link_font_family")
	link.size = t.Get("link_font_size")
	link.styles, link.hasStyles = toStyles(t.Str("link_font_style"), t.Str("link_text_decoration"))
	if v := t.Get("link_text_decoration_color"); v != nil {
		link.decoColor, link.hasDecoColor = toClr(v), true
	}
	link.decoWidth = num("link_text_decoration_width")
	if v := t.Get("link_background_color"); v != nil {
		link.bg, link.hasBg = toClr(v), true
		link.borderOffset = num("link_border_offset")
	}
	settings["link"] = link
	mark := props{}
	if v := t.Get("mark_font_color"); v != nil {
		mark.color, mark.hasColor = toClr(v), true
	}
	mark.styles, mark.hasStyles = toStyles(t.Str("mark_font_style"), "")
	if v := t.Get("mark_background_color"); v != nil {
		mark.bg, mark.hasBg = toClr(v), true
		mark.borderOffset = num("mark_border_offset")
	}
	settings["mark"] = mark
	menu := props{}
	if v := t.Get("menu_font_color"); v != nil {
		menu.color, menu.hasColor = toClr(v), true
	}
	menu.font = t.Str("menu_font_family")
	menu.size = t.Get("menu_font_size")
	menu.styles, menu.hasStyles = toStyles(t.Str("menu_font_style"), "")
	settings["menu"] = menu

	// ロール
	roles := map[string]props{}
	styled := map[string]bool{}
	for _, key := range t.Keys() {
		if !strings.HasPrefix(key, "role_") {
			continue
		}
		rest := key[5:]
		role, prop, ok := strings.Cut(rest, "_")
		if !ok {
			continue
		}
		p := roles[role]
		val := t.Get(key)
		switch prop {
		case "background_color":
			p.bg, p.hasBg = toClr(val), true
		case "border_color":
			p.borderColor, p.hasBorderColor = toClr(val), true
		case "border_offset":
			p.borderOffset = num(key)
		case "border_radius":
			p.borderRadius = num(key)
		case "border_width":
			p.borderWidth = num(key)
			if t.Get("role_"+role+"_border_color") == nil {
				p.borderColor, p.hasBorderColor = toClr(t.Get("base_border_color")), true
			}
		case "font_color":
			p.color, p.hasColor = toClr(val), true
		case "font_family":
			p.font = t.Str(key)
		case "font_size":
			p.size = val
		case "text_decoration_color":
			p.decoColor, p.hasDecoColor = toClr(val), true
		case "text_decoration_width":
			p.decoWidth = num(key)
		case "text_transform":
			p.transform = t.Str(key)
		case "font_style", "text_decoration":
			styled[role] = true
		default:
			continue
		}
		roles[role] = p
	}
	for role := range styled {
		p := roles[role]
		p.styles, p.hasStyles = toStyles(t.Str("role_"+role+"_font_style"), t.Str("role_"+role+"_text_decoration"))
		roles[role] = p
	}
	if _, ok := roles["line-through"]; !ok {
		roles["line-through"] = props{styles: []string{"strikethrough"}, hasStyles: true}
	}
	if _, ok := roles["underline"]; !ok {
		roles["underline"] = props{styles: []string{"underline"}, hasStyles: true}
	}
	base := t.Float("base_font_size", 12)
	if _, ok := roles["big"]; !ok {
		if v, ok := t.Num("base_font_size_large"); ok {
			roles["big"] = props{size: fmt.Sprintf("%sem", theme.FormatNumber(math.Round(v/base*1e5)/1e5))}
		} else {
			roles["big"] = props{size: "1.1667em"}
		}
	}
	if _, ok := roles["small"]; !ok {
		if v, ok := t.Num("base_font_size_small"); ok {
			roles["small"] = props{size: fmt.Sprintf("%sem", theme.FormatNumber(math.Round(v/base*1e5)/1e5))}
		} else {
			roles["small"] = props{size: "0.8333em"}
		}
	}
	c.tagProps = settings
	c.roleProps = roles
}

// ---------------------------------------------------------------------------
// 擬似 HTML のパーサー

var charEntities = map[string]string{"amp": "&", "apos": "'", "gt": ">", "lt": "<", "nbsp": nbsp, "quot": "\""}

var markupTags = map[string]bool{"strong": true, "em": true, "code": true, "a": true, "font": true, "span": true, "sub": true, "sup": true, "del": true, "button": true, "kbd": true, "mark": true, "menu": true}

type markupParser struct {
	c             *Converter
	s             string
	pos           int
	out           []frag
	normalize     bool
	prevText      bool
	prevEndsSpace bool
}

// parseText は擬似 HTML を書式付き断片の列に変換します。
func (c *Converter) parseText(s string, normalize bool, inherited *frag) []frag {
	base := frag{}
	if inherited != nil {
		base = *inherited
	}
	if !strings.ContainsAny(s, "<&") {
		if normalize {
			s = squeezeWhitespace(s)
		}
		base.text = s
		return []frag{base}
	}
	if normalize {
		s = strings.NewReplacer("\t", " ", "\n", " ").Replace(s)
	}
	p := &markupParser{c: c, s: s, normalize: normalize}
	p.parseNodes(base, "")
	return p.out
}

func squeezeWhitespace(s string) string {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if !prev {
				b.WriteByte(' ')
			}
			prev = true
		} else {
			b.WriteRune(r)
			prev = false
		}
	}
	return b.String()
}

func (p *markupParser) emitText(text string, inh frag, charref bool) {
	if !charref {
		if p.prevEndsSpace && p.normalize && !inh.preserveSpace {
			text = strings.TrimLeft(text, " ")
		}
		if text == "" {
			return
		}
		if p.normalize && !inh.preserveSpace {
			for strings.Contains(text, "  ") {
				text = strings.ReplaceAll(text, "  ", " ")
			}
		}
	}
	if inh.transform != "" && inh.transform != "none" {
		text = transformPlain(text, inh.transform)
	}
	if p.prevText && len(p.out) > 0 {
		p.out[len(p.out)-1].text += text
	} else {
		f := inh
		f.text = text
		p.out = append(p.out, f)
	}
	p.prevText = true
	p.prevEndsSpace = !charref && strings.HasSuffix(text, " ")
}

func (p *markupParser) parseNodes(inh frag, closing string) {
	s := p.s
	for p.pos < len(s) {
		i := strings.IndexAny(s[p.pos:], "<&")
		if i < 0 {
			p.emitText(s[p.pos:], inh, false)
			p.pos = len(s)
			return
		}
		if i > 0 {
			p.emitText(s[p.pos:p.pos+i], inh, false)
			p.pos += i
		}
		if s[p.pos] == '&' {
			end := strings.IndexByte(s[p.pos:], ';')
			if end > 1 && end <= 10 {
				ref := s[p.pos+1 : p.pos+end]
				text, ok := "", false
				if strings.HasPrefix(ref, "#x") || strings.HasPrefix(ref, "#X") {
					if n, err := strconv.ParseInt(ref[2:], 16, 32); err == nil {
						text, ok = string(rune(n)), true
					}
				} else if strings.HasPrefix(ref, "#") {
					if n, err := strconv.Atoi(ref[1:]); err == nil {
						text, ok = string(rune(n)), true
					}
				} else if v, found := charEntities[ref]; found {
					text, ok = v, true
				}
				if ok {
					p.emitText(text, inh, true)
					p.pos += end + 1
					continue
				}
			}
			p.emitText("&", inh, true)
			p.pos++
			continue
		}
		// '<'
		rest := s[p.pos:]
		if strings.HasPrefix(rest, "</") {
			end := strings.IndexByte(rest, '>')
			if end > 0 && closing != "" && rest[2:end] == closing {
				p.pos += end + 1
				return
			}
			p.emitText("<", inh, true)
			p.pos++
			continue
		}
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			p.emitText("<", inh, true)
			p.pos++
			continue
		}
		tag := rest[1:end]
		name, attrText, _ := strings.Cut(tag, " ")
		name = strings.TrimSuffix(name, "/")
		switch {
		case name == "br":
			p.pos += end + 1
			p.emitBreak(inh)
		case name == "img":
			p.pos += end + 1
			attrs := parseTagAttrs(attrText)
			f := frag{text: "[" + strings.ReplaceAll(attrs["alt"], zwsp, "") + "]", link: inh.link, anchor: inh.anchor}
			f.image = &inlineImage{path: attrs["src"], format: attrs["format"], width: attrs["width"], fit: attrs["fit"], alt: attrs["alt"]}
			if inh.bg.set || (inh.borderColor.set && inh.borderWidth > 0) {
				f.bg, f.borderColor, f.borderWidth, f.borderRadius = inh.bg, inh.borderColor, inh.borderWidth, inh.borderRadius
				f.borderOffset, f.hasBorderOffset = inh.borderOffset, inh.hasBorderOffset
			}
			for _, class := range strings.Fields(attrs["class"]) {
				if rp, ok := p.c.roleProps[class]; ok {
					f.apply(rp)
				}
			}
			p.out = append(p.out, f)
			p.prevText, p.prevEndsSpace = false, false
		case markupTags[name]:
			p.pos += end + 1
			attrs := parseTagAttrs(attrText)
			child, visible := p.c.buildFragment(inh, name, attrs)
			if name == "a" && strings.HasPrefix(s[p.pos:], dummyText+"</a>") {
				child.text = dummyText
				if !visible {
					child = frag{text: dummyText, name: attrs["id"], destType: attrs["type"], wj: inh.wj}
				}
				p.out = append(p.out, child)
				p.pos += len(dummyText) + 4
				p.prevText = false
				continue
			}
			before := len(p.out)
			p.prevText = false
			p.parseNodes(child, name)
			if len(p.out) > before {
				p.prevEndsSpace = false
			}
			p.prevText = false
		default:
			p.emitText("<", inh, true)
			p.pos++
		}
	}
}

func (p *markupParser) emitBreak(inh frag) {
	if p.prevText && len(p.out) > 0 {
		p.out[len(p.out)-1].text += "\n"
	} else {
		f := inh
		f.text = "\n"
		p.out = append(p.out, f)
	}
	p.prevText, p.prevEndsSpace = true, true
}

func parseTagAttrs(s string) map[string]string {
	attrs := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ")
		eq := strings.Index(s, "=\"")
		if eq < 0 {
			break
		}
		name := s[:eq]
		s = s[eq+2:]
		end := strings.IndexByte(s, '"')
		if end < 0 {
			break
		}
		attrs[name] = s[:end]
		s = s[end+1:]
	}
	return attrs
}

func decodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			if end := strings.IndexByte(s[i:], ';'); end > 1 && end <= 10 {
				ref := s[i+1 : i+end]
				if strings.HasPrefix(ref, "#x") {
					if n, err := strconv.ParseInt(ref[2:], 16, 32); err == nil {
						b.WriteRune(rune(n))
						i += end
						continue
					}
				} else if strings.HasPrefix(ref, "#") {
					if n, err := strconv.Atoi(ref[1:]); err == nil {
						b.WriteRune(rune(n))
						i += end
						continue
					}
				} else if v, ok := charEntities[ref]; ok {
					b.WriteString(v)
					i += end
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseHexColor(v string) (string, bool) {
	if !strings.HasPrefix(v, "#") {
		return "", false
	}
	v = v[1:]
	if len(v) == 3 {
		v = string([]byte{v[0], v[0], v[1], v[1], v[2], v[2]})
	}
	if len(v) != 6 {
		return "", false
	}
	for _, ch := range v {
		if !unicode.Is(unicode.ASCII_Hex_Digit, ch) {
			return "", false
		}
	}
	return strings.ToUpper(v), true
}

// buildFragment はタグと属性から断片の書式を決定します (Transform#build_fragment の移植)。
func (c *Converter) buildFragment(inh frag, tag string, attrs map[string]string) (frag, bool) {
	f := inh
	f.text = ""
	visible := true
	switch tag {
	case "strong":
		f.bold = true
	case "em":
		f.italic = true
	case "button", "code", "kbd", "mark", "menu":
		f.apply(c.tagProps[tag])
	case "font":
		if v, ok := attrs["name"]; ok {
			f.family = v
		}
		if v, ok := attrs["size"]; ok {
			if strings.HasSuffix(v, "em") {
				if v != "1em" {
					f.sizeRel, f.size = v, 0
				}
			} else {
				f.size, _ = strconv.ParseFloat(v, 64)
				f.sizeRel = ""
			}
		}
		if v, ok := attrs["width"]; ok {
			f.width = v
			f.align = "center"
		}
		if v, ok := attrs["color"]; ok && v != "" {
			switch v[0] {
			case '#':
				if hex, ok := parseHexColor(v); ok {
					f.color = hexClr(hex)
				}
			case '[':
				parts := strings.Split(strings.Trim(v, "[]"), ",")
				cmyk := make([]float64, 4)
				for i := 0; i < len(parts) && i < 4; i++ {
					cmyk[i], _ = strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
				}
				f.color = clr{cmyk: cmyk, set: true}
			default:
				f.color = hexClr(v)
			}
		}
	case "a":
		if v, ok := attrs["anchor"]; ok {
			f.anchor = v
		} else if v, ok := attrs["href"]; ok {
			f.link = decodeEntities(v)
		} else if _, ok := attrs["id"]; ok {
			visible = false
		}
		if visible {
			f.apply(c.tagProps["link"])
		}
	case "sub":
		f.sub = true
	case "sup":
		f.sup = true
	case "del":
		f.strike = true
	default: // span
		if style, ok := attrs["style"]; ok {
			for _, decl := range strings.Split(strings.ReplaceAll(style, " ", ""), ";") {
				name, value, _ := strings.Cut(decl, ":")
				switch name {
				case "color":
					if hex, ok := parseHexColor(value); ok {
						f.color = hexClr(hex)
					}
				case "font-weight":
					if value == "bold" {
						f.bold = true
					}
				case "font-style":
					if value == "italic" {
						f.italic = true
					}
				case "align", "text-align":
					f.align = value
				case "width":
					f.width = value
				case "background-color":
					if hex, ok := parseHexColor(value); ok {
						f.bg = hexClr(hex)
					}
				}
			}
		}
	}
	if class, ok := attrs["class"]; ok {
		for _, name := range strings.Fields(class) {
			switch name {
			case "nobreak", "nowrap":
				f.singleToken = true
			case "wj":
				f.wj = true
			case "pre-wrap":
				f.preserveSpace = true
			}
			if rp, ok := c.roleProps[name]; ok {
				f.apply(rp)
			}
		}
	}
	return f, visible
}

// ---------------------------------------------------------------------------
// 文字変換 (text_transform)

var smallCaps = []rune("ᴀʙᴄᴅᴇғɢʜɪᴊᴋʟᴍɴoᴘǫʀsᴛᴜᴠᴡxʏᴢ")

func transformPlain(s, transform string) string {
	switch transform {
	case "uppercase":
		return strings.ToUpper(s)
	case "lowercase":
		return strings.ToLower(s)
	case "capitalize":
		var b strings.Builder
		start := true
		for _, r := range s {
			if unicode.IsSpace(r) {
				start = true
				b.WriteRune(r)
			} else if start {
				b.WriteRune(unicode.ToUpper(r))
				start = false
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
		}
		return b.String()
	case "smallcaps":
		var b strings.Builder
		for _, r := range s {
			if r >= 'a' && r <= 'z' {
				b.WriteRune(smallCaps[r-'a'])
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return s
}

// transformText は擬似 HTML のテキスト部分だけに文字変換を適用します。
func transformText(s, transform string) string {
	if transform == "" || transform == "none" {
		return s
	}
	if !strings.ContainsAny(s, "<&") {
		return transformPlain(s, transform)
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch s[i] {
		case '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				b.WriteString(transformPlain(s[i:], transform))
				return b.String()
			}
			b.WriteString(s[i : i+end+1])
			i += end + 1
		case '&':
			end := strings.IndexByte(s[i:], ';')
			if end > 0 && end <= 10 && transform != "lowercase" {
				b.WriteString(s[i : i+end+1])
				i += end + 1
			} else {
				b.WriteByte('&')
				i++
			}
		default:
			j := strings.IndexAny(s[i:], "<&")
			if j < 0 {
				j = len(s) - i
			}
			b.WriteString(transformPlain(s[i:i+j], transform))
			i += j
		}
	}
	return b.String()
}

// sanitize は擬似 HTML からタグを取り除き、文字参照を展開したプレーンテキストを返します。
func sanitize(s string) string {
	if strings.Contains(s, "<") {
		var b strings.Builder
		depth := false
		for i := 0; i < len(s); i++ {
			switch {
			case s[i] == '<' && strings.IndexByte(s[i:], '>') > 0:
				depth = true
			case s[i] == '>' && depth:
				depth = false
			case !depth:
				b.WriteByte(s[i])
			}
		}
		s = b.String()
	}
	s = decodeEntities(s)
	s = strings.ReplaceAll(s, dummyText, "")
	s = strings.ReplaceAll(s, zwsp, "")
	return strings.TrimSpace(squeezeWhitespace(s))
}
