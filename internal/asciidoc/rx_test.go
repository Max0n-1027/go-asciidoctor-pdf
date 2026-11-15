package asciidoc

import "testing"

func TestRxCompile(t *testing.T) {
	if m := atxSectionTitleRx.Match("== Title here =="); m == nil || m.S(2) != "Title here" {
		t.Fatalf("atx: %v", m)
	}
	if m := attributeEntryRx.Match(":name: value"); m == nil || m.S(1) != "name" || m.S(2) != "value" {
		t.Fatal("attr entry")
	}
	if !anyListRx.Test("* item") || !anyListRx.Test("term:: def") || anyListRx.Test("plain text") {
		t.Fatal("anyList")
	}
	if got := passSlotRx.Gsub("a\u00961\u0097b", func(m *M) string { return "[" + m.S(1) + "]" }); got != "a[1]b" {
		t.Fatalf("pass slot: %q", got)
	}
	if m := inlineLinkRx.Match("see https://example.org[Example] now"); m == nil || m.S(4) != "example.org" || m.S(5) != "Example" {
		t.Fatalf("link rx")
	}
}
