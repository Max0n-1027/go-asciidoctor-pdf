// Package asciidoc は AsciiDoc 文書のパーサーです。
// Asciidoctor (Ruby) のパーサーおよび置換処理 (Substitutors) と同じ規則に従うことを目指しています。
package asciidoc

import (
	"strings"

	"github.com/dlclark/regexp2"
)

// Rx は Ruby の正規表現と同じ意味論 (^ と $ は常に行頭・行末) で動く正規表現です。
type Rx struct {
	re *regexp2.Regexp
}

// M は 1 回の一致結果です。
type M struct {
	m   *regexp2.Match
	src []rune
}

// rx は正規表現をコンパイルします。flags に "m" を含めると Ruby の /m (ドットが改行に一致) になります。
func rx(pattern string, flags ...string) *Rx {
	opts := regexp2.RegexOptions(regexp2.Multiline)
	for _, f := range flags {
		if strings.Contains(f, "m") {
			opts |= regexp2.Singleline
		}
		if strings.Contains(f, "i") {
			opts |= regexp2.IgnoreCase
		}
	}
	return &Rx{re: regexp2.MustCompile(pattern, opts)}
}

// Match は最初の一致を返します (一致しなければ nil)。
func (r *Rx) Match(s string) *M {
	src := []rune(s)
	m, err := r.re.FindRunesMatch(src)
	if err != nil || m == nil {
		return nil
	}
	return &M{m: m, src: src}
}

// Test は一致するかどうかを返します。
func (r *Rx) Test(s string) bool {
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}

// Gsub はすべての一致を fn の戻り値で置き換えます。
func (r *Rx) Gsub(s string, fn func(m *M) string) string {
	src := []rune(s)
	m, err := r.re.FindRunesMatch(src)
	if err != nil || m == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for m != nil {
		b.WriteString(string(src[last:m.Index]))
		b.WriteString(fn(&M{m: m, src: src}))
		last = m.Index + m.Length
		m, err = r.re.FindNextMatch(m)
		if err != nil {
			break
		}
	}
	b.WriteString(string(src[last:]))
	return b.String()
}

// Sub は最初の一致だけを置き換えます。
func (r *Rx) Sub(s string, fn func(m *M) string) string {
	src := []rune(s)
	m, err := r.re.FindRunesMatch(src)
	if err != nil || m == nil {
		return s
	}
	return string(src[:m.Index]) + fn(&M{m: m, src: src}) + string(src[m.Index+m.Length:])
}

// Scan はすべての一致を返します。
func (r *Rx) Scan(s string) []*M {
	src := []rune(s)
	var out []*M
	m, err := r.re.FindRunesMatch(src)
	for err == nil && m != nil {
		out = append(out, &M{m: m, src: src})
		m, err = r.re.FindNextMatch(m)
	}
	return out
}

// S は i 番目のグループの文字列を返します (不参加なら空文字列)。
func (m *M) S(i int) string {
	g := m.m.GroupByNumber(i)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.String()
}

// Has は i 番目のグループが一致に参加したかどうかを返します。
func (m *M) Has(i int) bool {
	g := m.m.GroupByNumber(i)
	return g != nil && len(g.Captures) > 0
}

// All は一致全体を返します。
func (m *M) All() string { return m.m.String() }

// Pre は一致より前の文字列を返します。
func (m *M) Pre() string { return string(m.src[:m.m.Index]) }

// Post は一致より後の文字列を返します。
func (m *M) Post() string { return string(m.src[m.m.Index+m.m.Length:]) }

// Asciidoctor の rx.rb に対応する文字クラス
const (
	ccAlpha = `\p{L}\p{Nl}`
	cgAlpha = `[\p{L}\p{Nl}]`
	ccAlnum = `\p{L}\p{Nl}\p{Nd}`
	cgAlnum = `[\p{L}\p{Nl}\p{Nd}]`
	ccWord  = `\p{L}\p{Mn}\p{Mc}\p{Nd}\p{Nl}\p{Pc}`
	cgWord  = `[\p{L}\p{Mn}\p{Mc}\p{Nd}\p{Nl}\p{Pc}]`
	cgBlank = `[ \t]`
)

var (
	authorInfoLineRx   = rx(`^(` + cgWord + `[` + ccWord + `\-'.]*)(?: +(` + cgWord + `[` + ccWord + `\-'.]*))?(?: +(` + cgWord + `[` + ccWord + `\-'.]*))?(?: +<([^>]+)>)?$`)
	authorDelimiterRx  = rx(`;(?: |$)`)
	revisionInfoLineRx = rx(`^(?:[^\d{]*(.*?),)? *(?!:)(.*?)(?: *(?!^),?: *(.*))?$`)

	conditionalDirectiveRx = rx(`^(\\)?(ifdef|ifndef|ifeval|endif)::(\S*?(?:([,+])\S*?)?)\[(.+)?\]$`)
	evalExpressionRx       = rx(`^(.+?) *([=!><]=|[><]) *(.+)$`)
	includeDirectiveRx     = rx(`^(\\)?include::([^\s\[](?:[^\[]*[^\s\[])?)\[(.+)?\]$`)
	tagDirectiveRx         = rx(`\b(?:tag|(e)nd)::(\S+?)\[\](?=$|[ \r])`, "m")

	attributeEntryRx          = rx(`^:(!?` + cgWord + `[^:]*):(?:[ \t]+(.*))?$`)
	invalidAttributeNameRx    = rx(`[^` + ccWord + `-]`)
	attributeEntryPassMacroRx = rx(`^pass:([a-z]+(?:,[a-z-]+)*)?\[(.*)\]$`, "m")
	attributeReferenceRx      = rx(`(\\)?\{(` + cgWord + `[` + ccWord + `-]*|(set|counter2?):.+?)(\\)?\}`)

	blockAnchorRx         = rx(`^\[\[(?:|([` + ccAlpha + `_:][` + ccWord + `\-:.]*)(?:, *(.+))?)\]\]$`)
	blockAttributeListRx  = rx(`^\[(|[` + ccWord + `.#%{,"'].*)\]$`)
	blockAttributeLineRx  = rx(`^\[(?:|[` + ccWord + `.#%{,"'].*|\[(?:|[` + ccAlpha + `_:][` + ccWord + `\-:.]*(?:, *.+)?)\])\]$`)
	blockTitleRx          = rx(`^\.(\.?[^ \t.].*)$`)
	admonitionParagraphRx = rx(`^(NOTE|TIP|IMPORTANT|WARNING|CAUTION):[ \t]+`)
	literalParagraphRx    = rx(`^([ \t]+.*)$`)

	atxSectionTitleRx       = rx(`^(=={0,5}|#{1,6})[ \t]+(.+?)(?:[ \t]+\1)?$`)
	setextSectionTitleRx    = rx(`^((?!\.).*?` + cgAlnum + `.*)$`)
	inlineSectionAnchorRx   = rx(` (\\)?\[\[([` + ccAlpha + `_:][` + ccWord + `\-:.]*)(?:, *(.+))?\]\]$`)
	invalidSectionIDCharsRx = rx(`<[^>]+>|&(?:[a-z][a-z]+\d{0,2}|#\d\d\d{0,4}|#x[\da-f][\da-f][\da-f]{0,3});|[^ ` + ccWord + `\-.]+?`)
	sectionLevelStyleRx     = rx(`^sect\d$`)

	anyListRx         = rx(`^(?:[ \t]*(?:-|\*\**|\.\.*|•|\d+\.|[a-zA-Z]\.|[IVXivx]+\))[ \t]|(?!//[^/])[ \t]*[^ \t].*?(?::::{0,2}|;;)(?:$|[ \t])|<(?:\d+|\.)>[ \t])`)
	unorderedListRx   = rx(`^[ \t]*(-|\*\**|•)[ \t]+(.*)$`)
	orderedListRx     = rx(`^[ \t]*(\.\.*|\d+\.|[a-zA-Z]\.|[IVXivx]+\))[ \t]+(.*)$`)
	descriptionListRx = rx(`^(?!//[^/])[ \t]*([^ \t].*?)(:::{0,2}|;;)(?:$|[ \t]+(.*)$)`)
	calloutListRx     = rx(`^<(\d+|\.)>[ \t]+(.*)$`)
	calloutExtractRx  = rx(`((?://|#|--|;;) ?)?(\\)?<!?(|--)(\d+|\.)\3>(?=(?: ?\\?<!?\3(?:\d+|\.)\3>)*$)`)
	calloutScanRx     = rx(`\\?<!?(|--)(\d+|\.)\1>(?=(?: ?\\?<!?\1(?:\d+|\.)\1>)*$)`)
	calloutSourceRx   = rx(`((?://|#|--|;;) ?)?(\\)?&lt;!?(|--)(\d+|\.)\3&gt;(?=(?: ?\\?&lt;!?\3(?:\d+|\.)\3&gt;)*$)`)

	descriptionListSiblingRx = map[string]*Rx{
		"::":   rx(`^(?!//[^/])[ \t]*([^ \t].*?[^:]|[^ \t:])(::)(?:$|[ \t]+(.*)$)`),
		":::":  rx(`^(?!//[^/])[ \t]*([^ \t].*?[^:]|[^ \t:])(:::)(?:$|[ \t]+(.*)$)`),
		"::::": rx(`^(?!//[^/])[ \t]*([^ \t].*?[^:]|[^ \t:])(::::)(?:$|[ \t]+(.*)$)`),
		";;":   rx(`^(?!//[^/])[ \t]*([^ \t].*?)(;;)(?:$|[ \t]+(.*)$)`),
	}

	columnSpecRx    = rx(`^(?:(\d+)\*)?([<^>](?:\.[<^>]?)?|(?:[<^>]?\.)?[<^>])?(\d+%?|~)?([a-z])?$`)
	cellSpecStartRx = rx(`^[ \t]*(?:(\d+(?:\.\d*)?|(?:\d*\.)?\d+)([*+]))?([<^>](?:\.[<^>]?)?|(?:[<^>]?\.)?[<^>])?([a-z])?$`)
	cellSpecEndRx   = rx(`[ \t]+(?:(\d+(?:\.\d*)?|(?:\d*\.)?\d+)([*+]))?([<^>](?:\.[<^>]?)?|(?:[<^>]?\.)?[<^>])?([a-z])?$`)

	customBlockMacroRx = rx(`^(` + cgWord + `[` + ccWord + `-]*)::(|\S|\S.*?\S)\[(.+)?\]$`)
	blockMediaMacroRx  = rx(`^(image|video|audio)::(\S|\S.*?\S)\[(.+)?\]$`)
	blockTocMacroRx    = rx(`^toc::\[(.+)?\]$`)

	inlineAnchorRx         = rx(`(\\)?(?:\[\[([` + ccAlpha + `_:][` + ccWord + `\-:.]*)(?:, *(.+?))?\]\]|anchor:([` + ccAlpha + `_:][` + ccWord + `\-:.]*)\[(?:\]|(.*?[^\\])\]))`)
	inlineAnchorScanRx     = rx(`(?:^|[^\\\[])\[\[([` + ccAlpha + `_:][` + ccWord + `\-:.]*)(?:, *(.+?))?\]\]|(?:^|[^\\])anchor:([` + ccAlpha + `_:][` + ccWord + `\-:.]*)\[(?:\]|(.*?[^\\])\])`)
	leadingInlineAnchorRx  = rx(`^\[\[([` + ccAlpha + `_:][` + ccWord + `\-:.]*)(?:, *(.+?))?\]\]`)
	inlineBiblioAnchorRx   = rx(`^\[\[\[([` + ccAlpha + `_:][` + ccWord + `\-:.]*)(?:, *(.+?))?\]\]\]`)
	inlineEmailRx          = rx(`([\\>:/])?` + cgWord + `(?:&amp;|[` + ccWord + `\-.%+])*@` + cgAlnum + `[` + ccAlnum + `_\-.]*\.[a-zA-Z]{2,5}\b`)
	inlineFootnoteMacroRx  = rx(`\\?footnote(?:(ref):|:([`+ccWord+`-]+)?)\[(?:|(.*?[^\\]))\](?!</a>)`, "m")
	inlineImageMacroRx     = rx(`\\?i(?:mage|con):([^:\s\[](?:[^\n\[]*[^\s\[])?)\[(|.*?[^\\])\]`, "m")
	inlineIndextermMacroRx = rx(`\\?(?:(indexterm2?):\[(.*?[^\\])\]|\(\((.+?)\)\)(?!\)))`, "m")
	inlineKbdBtnMacroRx    = rx(`(\\)?(kbd|btn):\[(.*?[^\\])\]`, "m")
	inlineLinkRx           = rx(`(^|link:|`+cgBlank+`|\\?&lt;()|[>\(\)\[\];"'])(\\?(?:https?|file|ftp|irc)://)(?:([^\s\[\]]+)\[(|.*?[^\\])\]|\2([^\s]+?)&gt;|([^\s\[\]<]*([^\s,.?!\[\]<\)])))`, "m")
	inlineLinkMacroRx      = rx(`\\?(?:link|(mailto)):(|[^:\s\[][^\s\[]*)\[(|.*?[^\\])\]`, "m")
	inlineStemMacroRx      = rx(`\\?(stem|(?:latex|ascii)math):([a-z]+(?:,[a-z-]+)*)?\[(.*?[^\\])\]`, "m")
	inlineMenuMacroRx      = rx(`\\?menu:(`+cgWord+`|[`+ccWord+`&][^\n\[]*[^\s\[])\[ *(?:|(.*?[^\\]))\]`, "m")
	inlineMenuRx           = rx(`\\?"([` + ccWord + `&][^"]*?[ \n]+&gt;[ \n]+[^"]*)"`)
	inlinePassRx           = rx(`((?:^|[^`+ccWord+`;:\\])(?=(\[)|\+)|\\(?=\[)|(?=\\\+))(?:\2(x-|[^\]]+ x-)\]|(?:\[([^\]]+)\])?(?=(\\)?\+))(\5?(\+|`+"`"+`)(\S|\S.*?\S)\7)(?!`+cgWord+`)`, "m")
	inlinePassMacroRx      = rx(`(?:(?:(\\?)\[([^\]]+)\])?(\\{0,2})(\+\+\+?|\$\$)(.*?)\4|(\\?)pass:([a-z]+(?:,[a-z-]+)*)?\[(|.*?[^\\])\])`, "m")
	inlineXrefMacroRx      = rx(`\\?(?:&lt;&lt;([`+ccWord+`#/.:{].*?)&gt;&gt;|xref:([`+ccWord+`#/.:{].*?)\[(?:\]|(.*?[^\\])\]))`, "m")
	hardLineBreakRx        = rx(`^(.*) \+$`)
	extLayoutBreakRx       = rx(`^(?:'{3,}|<{3,}|([-*_])( *)\1\2\1)$`)
	escapedSpaceRx         = rx(`\\([ \t\n])`)
	spaceDelimiterRx       = rx(`([^\\])[ \t\n]+`)
	uriSniffRx             = rx(`^` + cgAlpha + `[` + ccAlnum + `.+-]+:/{0,2}`)
	xmlSanitizeRx          = rx(`<[^>]+>`)
	passSlotRx             = rx(`\u0096(\d+)\u0097`)
	manpageTitleVolnumRx   = rx(`^(.+?) *\( *(.+?) *\)$`)
	inlineBreakNLRx        = rx(`\n`)
)
