package asciidoc

import (
	"strconv"
	"strings"
	"unicode"
)

// ParseAttributes は属性リスト (例: `source,ruby,role="x"`) を解析します。
// 位置属性は "1" "2" … のキーで格納され、posattrs で指定した名前にも割り当てられます。
func (d *Document) ParseAttributes(attrlist string, posattrs []string, block *Block, into map[string]string) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	if attrlist == "" {
		return into
	}
	p := &attrListParser{src: []rune(attrlist), doc: d, block: block, attrs: into}
	index := 0
	for p.parseAttribute(index, posattrs) {
		if p.eos() {
			break
		}
		p.skipDelimiter()
		index++
	}
	return into
}

type attrListParser struct {
	src   []rune
	pos   int
	doc   *Document
	block *Block
	attrs map[string]string
}

func (p *attrListParser) eos() bool { return p.pos >= len(p.src) }

func (p *attrListParser) peek() rune {
	if p.eos() {
		return 0
	}
	return p.src[p.pos]
}

func (p *attrListParser) skipBlank() int {
	start := p.pos
	for !p.eos() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	return p.pos - start
}

func (p *attrListParser) skipDelimiter() {
	// /[ \t]*,[ \t]*/ 相当
	save := p.pos
	p.skipBlank()
	if !p.eos() && p.src[p.pos] == ',' {
		p.pos++
		p.skipBlank()
		return
	}
	p.pos = save
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r)
}

func (p *attrListParser) scanName() (string, bool) {
	if p.eos() || !isWordRune(p.src[p.pos]) {
		return "", false
	}
	start := p.pos
	p.pos++
	for !p.eos() && (isWordRune(p.src[p.pos]) || p.src[p.pos] == '-' || p.src[p.pos] == '.') {
		p.pos++
	}
	return string(p.src[start:p.pos]), true
}

// scanToDelimiter は /.*?(?=[ \t]*(,|$))/ 相当の読み取りを行います。
func (p *attrListParser) scanToDelimiter() string {
	start := p.pos
	end := len(p.src)
	for i := p.pos; i < len(p.src); i++ {
		if p.src[i] == ',' {
			end = i
			break
		}
	}
	// 末尾の空白は含めない
	trimmed := end
	for trimmed > start && (p.src[trimmed-1] == ' ' || p.src[trimmed-1] == '\t') {
		trimmed--
	}
	p.pos = trimmed
	return string(p.src[start:trimmed])
}

// scanToQuote は /.*?[^\\](?=quote)/m 相当の読み取りを行います。
func (p *attrListParser) scanToQuote(quote rune) (string, bool) {
	for i := p.pos + 1; i < len(p.src); i++ {
		if p.src[i] == quote && p.src[i-1] != '\\' {
			s := string(p.src[p.pos:i])
			p.pos = i
			return s, true
		}
	}
	return "", false
}

func (p *attrListParser) parseAttributeValue(quote rune) string {
	if p.peek() == quote {
		p.pos++
		return ""
	}
	if value, ok := p.scanToQuote(quote); ok {
		p.pos++
		esc := "\\" + string(quote)
		if strings.Contains(value, esc) {
			value = strings.ReplaceAll(value, esc, string(quote))
		}
		return value
	}
	return string(quote) + p.scanToDelimiter()
}

func (p *attrListParser) parseAttribute(index int, posattrs []string) bool {
	cont := true
	singleQuoted := false
	var name, value string
	hasName, hasValue := false, false
	p.skipBlank()
	switch p.peek() {
	case '"':
		p.pos++
		name, hasName = p.parseAttributeValue('"'), true
	case '\'':
		p.pos++
		name, hasName = p.parseAttributeValue('\''), true
		if !strings.HasPrefix(name, "'") {
			singleQuoted = true
		}
	default:
		skipped := 0
		name, hasName = p.scanName()
		if hasName {
			skipped = p.skipBlank()
		}
		if p.eos() {
			if !hasName && !strings.HasSuffix(strings.TrimRight(string(p.src), " \t"), ",") {
				return false
			}
			cont = false
		} else {
			c := p.src[p.pos]
			p.pos++
			switch {
			case c == ',':
				p.pos--
			case hasName:
				if c == '=' {
					p.skipBlank()
					hasValue = true
					if p.eos() {
						value = ""
					} else {
						c = p.src[p.pos]
						p.pos++
						switch c {
						case '"':
							value = p.parseAttributeValue('"')
						case '\'':
							value = p.parseAttributeValue('\'')
							if !strings.HasPrefix(value, "'") {
								singleQuoted = true
							}
						case ',':
							value = ""
							p.pos--
						default:
							value = string(c) + p.scanToDelimiter()
							if value == "None" {
								return true
							}
						}
					}
				} else {
					name = name + strings.Repeat(" ", skipped) + string(c) + p.scanToDelimiter()
				}
			default:
				name, hasName = string(c)+p.scanToDelimiter(), true
			}
		}
	}
	if hasValue {
		switch name {
		case "options", "opts":
			if strings.Contains(value, ",") {
				value = strings.ReplaceAll(value, " ", "")
				for _, opt := range strings.Split(value, ",") {
					if opt != "" {
						p.attrs[opt+"-option"] = ""
					}
				}
			} else if value != "" {
				p.attrs[value+"-option"] = ""
			}
		default:
			if singleQuoted && p.block != nil && name != "title" && name != "reftext" {
				value = p.doc.ApplySubs(value, NormalSubs, p.block)
			}
			p.attrs[name] = value
		}
	} else if hasName {
		if singleQuoted && p.block != nil {
			name = p.doc.ApplySubs(name, NormalSubs, p.block)
		}
		if index < len(posattrs) && posattrs[index] != "" {
			p.attrs[posattrs[index]] = name
		}
		p.attrs[strconv.Itoa(index+1)] = name
	}
	return cont
}

// parseStyleAttribute は第 1 位置属性の省略記法 (style#id.role%option) を展開します。
func parseStyleAttribute(attrs map[string]string) string {
	raw, ok := attrs["1"]
	if !ok {
		return ""
	}
	if strings.ContainsAny(raw, " ") || !strings.ContainsAny(raw, ".#%") {
		attrs["style"] = raw
		return raw
	}
	var style, id string
	var roles, opts []string
	hasID := false
	kind := byte(0)
	var accum strings.Builder
	flush := func() {
		v := accum.String()
		accum.Reset()
		switch kind {
		case 0:
			if v != "" {
				style = v
			}
		case '.':
			if v != "" {
				roles = append(roles, v)
			}
		case '#':
			if v != "" {
				id, hasID = v, true
			}
		case '%':
			if v != "" {
				opts = append(opts, v)
			}
		}
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '.' || c == '#' || c == '%' {
			flush()
			kind = c
		} else {
			accum.WriteByte(c)
		}
	}
	flush()
	if style != "" {
		attrs["style"] = style
	}
	if hasID {
		attrs["id"] = id
	}
	if len(roles) > 0 {
		if existing := attrs["role"]; existing != "" {
			attrs["role"] = existing + " " + strings.Join(roles, " ")
		} else {
			attrs["role"] = strings.Join(roles, " ")
		}
	}
	for _, o := range opts {
		attrs[o+"-option"] = ""
	}
	return style
}
