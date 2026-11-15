package asciidoc

import (
	"fmt"
	"strings"
	"testing"
)

type testInline struct{}

func (testInline) ConvertInline(n *Inline) string {
	switch n.Context {
	case "quoted":
		tags := map[string]string{"strong": "b", "emphasis": "i", "monospaced": "code", "mark": "mark", "superscript": "sup", "subscript": "sub"}
		if t, ok := tags[n.Type]; ok {
			return "<" + t + ">" + n.Text + "</" + t + ">"
		}
		return n.Text
	case "anchor":
		switch n.Type {
		case "xref":
			return fmt.Sprintf("<xref %s>%s</xref>", n.Attrs["refid"], n.Text)
		case "link":
			return fmt.Sprintf("<a %s>%s</a>", n.Target, n.Text)
		}
		return "<anchor " + n.ID + ">"
	case "footnote":
		return "<fn " + n.Attrs["index"] + ">"
	case "break":
		return n.Text + "<br>"
	case "callout":
		return "(" + n.Text + ")"
	case "image":
		return "<img " + n.Target + ">"
	case "kbd":
		return "<kbd>" + strings.Join(n.List, "+") + "</kbd>"
	}
	return n.Text
}

type testLogger struct{ t *testing.T }

func (l testLogger) Log(sev, msg string) { l.t.Logf("%s: %s", sev, msg) }

func dump(b *Block, depth int, out *strings.Builder) {
	ind := strings.Repeat("  ", depth)
	fmt.Fprintf(out, "%s%s", ind, b.Context)
	if b.Style != "" {
		fmt.Fprintf(out, "[%s]", b.Style)
	}
	if b.ID != "" {
		fmt.Fprintf(out, "#%s", b.ID)
	}
	if b.Context == "section" {
		fmt.Fprintf(out, " L%d %q num=%q", b.Level, b.Title(), b.Numeral)
	} else if b.HasTitle() {
		fmt.Fprintf(out, " title=%q", b.CaptionedTitle())
	}
	if b.Context == "list_item" {
		if len(b.Terms) > 0 {
			for _, t := range b.Terms {
				fmt.Fprintf(out, " term=%q", t.Text())
			}
			out.WriteString("\n")
			if b.Desc != nil {
				dump(b.Desc, depth+1, out)
			}
			return
		}
		fmt.Fprintf(out, " %s %q", b.Marker, b.Text())
	}
	if len(b.Lines) > 0 {
		fmt.Fprintf(out, " %q", b.Content())
	}
	if b.Context == "image" {
		fmt.Fprintf(out, " target=%s", b.Attrs["target"])
	}
	out.WriteString("\n")
	if b.Table != nil {
		for _, sec := range []struct {
			n    string
			rows [][]*Cell
		}{{"head", b.Table.Head}, {"body", b.Table.Body}, {"foot", b.Table.Foot}} {
			for _, row := range sec.rows {
				fmt.Fprintf(out, "%s  %s:", ind, sec.n)
				for _, c := range row {
					fmt.Fprintf(out, " |%s(%dx%d %s)", c.Text(), c.Colspan, c.Rowspan, c.Style)
				}
				out.WriteString("\n")
			}
		}
	}
	for _, c := range b.Blocks {
		dump(c, depth+1, out)
	}
}

func parseDump(t *testing.T, src string, attrs map[string]string) (string, *Document) {
	doc := Parse(src, Options{Inline: testInline{}, Logger: testLogger{t}, Attributes: attrs})
	var out strings.Builder
	dump(doc.Block, 0, &out)
	return out.String(), doc
}

const sample = `= Document Title: Sub
Doc Writer <doc@example.com>; Second Author
v1.0, 2026-01-01: First
:sectnums:
:toc:
:experimental:

Preamble *bold* and _italic_ with ` + "`mono`" + ` and https://example.org[link].

== First Section

Paragraph with footnote:[A note] and <<second,Second>> ref.
Line two (C) -- ok...

NOTE: An admonition.

.A listing
[source,ruby]
----
puts "hi" # <1>
----
<1> Prints

* item one
** nested a
** nested b
* item two
+
continued paragraph

. first
. second

term:: definition
other::
  more text

[[second]]
=== Second

|===
|A |B

|1 |2
2+|span
|===

.Titled image
image::foo.png[Alt,100]

[quote,Someone,Somewhere]
____
Quoted text.
____

ifdef::backend-pdf[]
PDF only.
endif::[]
ifndef::backend-pdf[Not shown.]

[appendix]
== Appx

Text kbd:[Ctrl+C].
`

func TestParseSample(t *testing.T) {
	out, doc := parseDump(t, sample, nil)
	t.Log("\n" + out)
	for _, want := range []string{
		`section#_first_section L1 "First Section" num="1"`,
		`section#second L2 "Second" num="1"`,
		`section#_appx L1 "Appx" num="A"`,
		`admonition[NOTE] "An admonition."`,
		`listing[source] title="A listing"`,
		`* "item one"`,
		`** "nested a"`,
		`paragraph "continued paragraph"`,
		`term="term"`,
		`head: |A(1x1 ) |B(1x1 )`,
		`body: |span(2x1 )`,
		`image title="Figure 1. Titled image" target=foo.png`,
		`"PDF only."`,
		`<kbd>Ctrl+C</kbd>`,
		`<b>bold</b> and <i>italic</i> with <code>mono</code> and <a https://example.org>link</a>.`,
		`<fn 1>`,
		`<xref second>Second</xref>`,
		"&#169;&#8201;&#8212;&#8201;ok&#8230;&#8203;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "Not shown") {
		t.Error("ifndef content leaked")
	}
	if doc.Attr("author") != "Doc Writer" || doc.Attr("email") != "doc@example.com" || doc.Attr("author_2") != "Second Author" {
		t.Errorf("authors: %q %q %q", doc.Attr("author"), doc.Attr("email"), doc.Attr("author_2"))
	}
	if doc.Attr("revnumber") != "1.0" || doc.Attr("revdate") != "2026-01-01" || doc.Attr("revremark") != "First" {
		t.Errorf("rev: %q %q %q", doc.Attr("revnumber"), doc.Attr("revdate"), doc.Attr("revremark"))
	}
	if doc.DocTitle != "Document Title: Sub" || len(doc.Footnotes) != 1 {
		t.Errorf("title=%q footnotes=%d", doc.DocTitle, len(doc.Footnotes))
	}
}

func TestParseDocumentStartingWithSection(t *testing.T) {
	out, _ := parseDump(t, "== Before\n\nimage::foo.png[]\n\n== After\n\ntext\n", map[string]string{"sectnums": ""})
	for _, want := range []string{`section#_before L1 "Before" num="1"`, "image target=foo.png", `section#_after L1 "After" num="2"`, `paragraph "text"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
