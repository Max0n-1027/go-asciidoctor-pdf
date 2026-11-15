package converter_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/converter"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
)

// convertWithFixtures は testdata/pdf を基準ディレクトリとして変換します。
func convertWithFixtures(t *testing.T, src string, attrs map[string]string) (*pdf.Document, *logCollector) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "pdf"))
	if err != nil {
		t.Fatal(err)
	}
	log := &logCollector{t: t}
	conv := converter.New(log.Log)
	doc := asciidoc.Parse(src, asciidoc.Options{Attributes: attrs, BaseDir: dir, Logger: log, Inline: conv})
	out := conv.Convert(doc)
	var buf bytes.Buffer
	if _, err := out.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := pdf.OpenReader(buf.Bytes()); err != nil {
		t.Fatalf("output cannot be read back: %v", err)
	}
	if path := os.Getenv("PDF_TEST_DIR"); path != "" {
		os.WriteFile(filepath.Join(path, strings.ReplaceAll(t.Name(), "/", "_")+".pdf"), buf.Bytes(), 0o644)
	}
	return out, log
}

const (
	red   = "1.0 0.0 0.0 scn"
	green = "0.0 1.0 0.0 scn"
	blue  = "0.0 0.0 1.0 scn"
)

// importedColors は各ページが取り込んだページなら、その最初の塗り色を返します (通常のページは "-")。
func importedColors(d *pdf.Document) []string {
	var out []string
	for _, p := range d.Pages {
		if p.Import == nil {
			out = append(out, "-")
			continue
		}
		color := "?"
		for _, line := range strings.Split(string(p.Import.Content()), "\n") {
			if strings.HasSuffix(line, " scn") {
				color = line
				break
			}
		}
		out = append(out, color)
	}
	return out
}

func expectColors(t *testing.T, d *pdf.Document, want ...string) {
	t.Helper()
	got := importedColors(d)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("pages = %q, want %q", got, want)
	}
}

func isLetter(p *pdf.Page) bool {
	return p.Import != nil && p.Import.Width() == 612 && p.Import.Height() == 792
}

func TestInsertPDFPageAtBlockMacro(t *testing.T) {
	d, _ := convertWithFixtures(t, "before\n\nimage::blue-letter.pdf[]\n\nafter\n", nil)
	if len(d.Pages) != 3 {
		t.Fatalf("pages = %d, want 3", len(d.Pages))
	}
	if d.Pages[0].Imported || !isLetter(d.Pages[1]) || d.Pages[2].Imported {
		t.Error("expected only the second page to be the imported Letter page")
	}
	// 取り込んだページにはランニングコンテンツ (フッター) を描かない
	if len(d.Pages[1].Chunks) != 0 {
		t.Error("imported page should not have running content")
	}
	if len(d.Pages[2].Chunks) == 0 {
		t.Error("page after imported page is empty")
	}
}

func TestReplaceEmptyPageWithPDFPage(t *testing.T) {
	d, log := convertWithFixtures(t, ":page-background-image: image:bg.png[]\n\nbefore\n\n<<<\n\nimage::blue-letter.pdf[]\n\n<<<\n\nafter\n", nil)
	if len(d.Pages) != 3 || !isLetter(d.Pages[1]) {
		t.Fatalf("pages = %d, imported = %v", len(d.Pages), importedColors(d))
	}
	if len(log.messages) > 0 {
		t.Errorf("unexpected messages: %v", log.messages)
	}
}

func TestInternalReferencesWithPDFOnFirstPage(t *testing.T) {
	d, _ := convertWithFixtures(t, "image::blue-letter.pdf[]\n\nsee <<Section>>\n\n<<<\n\n== Section\n\ngo to <<__anchor-top,top>>\n\ngo to <<last>>\n\n<<<\n\n[#last]\nlast\n", map[string]string{"pdf-page-size": "Letter"})
	for name, want := range map[string]int{"__anchor-top": 0, "_section": 2, "last": 3} {
		dest, ok := d.GetDest(name)
		if !ok || dest.Page != want {
			t.Errorf("dest %s = %+v (%v), want page index %d", name, dest, ok, want)
		}
	}
}

func TestImportSpecificPages(t *testing.T) {
	d, _ := convertWithFixtures(t, "image::red-green-blue.pdf[]", nil)
	expectColors(t, d, red)
	d, _ = convertWithFixtures(t, "image::red-green-blue.pdf[page=2]", nil)
	expectColors(t, d, green)
	d, _ = convertWithFixtures(t, "image::red-green-blue.pdf[page=1]\nimage::red-green-blue.pdf[page=2]\n", nil)
	expectColors(t, d, red, green)
	for _, attr := range []string{`pages="3,1,2"`, "pages=3;1..2"} {
		d, _ = convertWithFixtures(t, "image::red-green-blue.pdf["+attr+"]\n", nil)
		expectColors(t, d, blue, red, green)
	}
	d, _ = convertWithFixtures(t, "image::red-green-blue.pdf[pages=1..10]\n", nil)
	expectColors(t, d, red, green, blue)
}

func TestImportPDFWithoutPages(t *testing.T) {
	d, _ := convertWithFixtures(t, "== Before\n\nimage::no-pages.pdf[]\n\n== After\n", nil)
	if len(d.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(d.Pages))
	}
	labels := map[string]int{}
	for _, item := range d.Outline {
		labels[item.Title] = item.Page
	}
	if labels["Before"] != 0 || labels["After"] != 1 {
		t.Errorf("outline = %v", labels)
	}
}

func TestDestinationForImportedPage(t *testing.T) {
	for _, attr := range []string{"page=1", "pages=1..3"} {
		d, _ := convertWithFixtures(t, "go to <<red>>\n\n.Red Page\n[#red]\nimage::red-green-blue.pdf["+attr+"]\n", nil)
		dest, ok := d.GetDest("red")
		if !ok || dest.Page != 1 || dest.Y != 841.89 {
			t.Errorf("%s: dest = %+v (%v)", attr, dest, ok)
		}
		if len(d.Pages[0].Annots) != 1 || d.Pages[0].Annots[0].Dest != "red" {
			t.Errorf("%s: annotations = %+v", attr, d.Pages[0].Annots)
		}
	}
}

func TestHiddenSectionOnImportedPage(t *testing.T) {
	d, _ := convertWithFixtures(t, "= Document Title\n:notitle:\n:doctype: book\n\ngo to <<blue>>\n\n[#blue%notitle]\n== Blue Page\n\nimage::blue-letter.pdf[]\n", nil)
	dest, ok := d.GetDest("blue")
	if !ok || dest.Page != 1 || dest.Y != 792 {
		t.Errorf("dest = %+v (%v)", dest, ok)
	}
	if len(d.Pages) != 2 || !isLetter(d.Pages[1]) {
		t.Errorf("pages = %v", importedColors(d))
	}
	d, _ = convertWithFixtures(t, "= Document Title\n:doctype: book\n:toc:\n\n[#blue%notitle]\n== Blue Page\n\nimage::blue-letter.pdf[]\n", nil)
	if dest, ok := d.GetDest("blue"); !ok || dest.Page != 2 {
		t.Errorf("toc case: dest = %+v (%v)", dest, ok)
	}
	if n := len(d.Pages[1].Annots); n != 2 {
		t.Errorf("toc annotations = %d, want 2", n)
	}
}

func TestPDFCoverPages(t *testing.T) {
	d, _ := convertWithFixtures(t, "= Document Title\n:front-cover-image: blue-letter.pdf\n\ncontent\n", nil)
	if len(d.Pages) != 2 || !isLetter(d.Pages[0]) || d.Pages[1].Imported || d.Pages[1].W != 595.28 {
		t.Errorf("front cover: %v", importedColors(d))
	}
	d, _ = convertWithFixtures(t, ":front-cover-image: image:red-green-blue.pdf[page=3]\n\ncontent\n", nil)
	expectColors(t, d, blue, "-")
	d, _ = convertWithFixtures(t, ":front-cover-image: image:red-green-blue.pdf[page=10]\n\none\n\n<<<\n\ntwo\n", nil)
	expectColors(t, d, "-", "-")
	if len(d.Outline) == 0 || d.Outline[0].Title != "Untitled" || d.Outline[0].Page != 0 {
		t.Errorf("outline = %+v", d.Outline)
	}
	d, _ = convertWithFixtures(t, ":back-cover-image: image:red-green-blue.pdf[page=3]\n\ncontent\n", nil)
	expectColors(t, d, "-", blue)
	d, _ = convertWithFixtures(t, ":back-cover-image: image:red-green-blue.pdf[page=10]\n\ncontent\n", nil)
	expectColors(t, d, "-")
	d, _ = convertWithFixtures(t, ":front-cover-image: image:no-pages.pdf[]\n\none\n\n<<<\n\ntwo\n", nil)
	expectColors(t, d, "-", "-")
	d, _ = convertWithFixtures(t, ":back-cover-image: image:no-pages.pdf[]\n\ncontent\n", nil)
	expectColors(t, d, "-")
}

func TestPDFCoverFromTheme(t *testing.T) {
	dir := t.TempDir()
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "pdf", "red-green-blue.pdf"))
	theme := "extends: default\ncover:\n  front:\n    image: image:" + fixture + "[page=3]\n"
	if err := os.WriteFile(filepath.Join(dir, "cover-theme.yml"), []byte(theme), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := convertWithFixtures(t, "content\n", map[string]string{"pdf-theme": "cover", "pdf-themesdir": dir})
	expectColors(t, d, blue, "-")
}

func TestPDFPageBackground(t *testing.T) {
	d, _ := convertWithFixtures(t, ":page-background-image-recto: image:tux-bg.pdf[]\n\nTux has left his mark on this page.\n\n<<<\n\nBut not on this page.\n", nil)
	if len(d.Pages) != 2 || !d.Pages[0].Imported || d.Pages[1].Imported {
		t.Fatalf("pages = %d, imported = %v", len(d.Pages), importedColors(d))
	}
	if len(d.Pages[0].Chunks) == 0 {
		t.Error("text is missing on the page with the PDF background")
	}
	_, log := convertWithFixtures(t, "= Document Title\n:page-background-image: image:no-such-file.pdf[]\n\ncontent\n\n<<<\n\nmore content\n\n<<<\n\neven more content\n", nil)
	if len(log.messages) != 1 || !strings.Contains(log.messages[0], "page background image not found or readable") {
		t.Errorf("messages = %v", log.messages)
	}
}

func TestEncryptedAndObjectStreamPDFs(t *testing.T) {
	for _, name := range []string{"encrypted-rc4.pdf", "objstm.pdf", "broken-xref.pdf"} {
		d, log := convertWithFixtures(t, "image::"+name+"[pages=1..3]\n", nil)
		if got := importedColors(d); len(got) != 3 || got[0] == "-" || got[1] == "-" || got[2] == "-" {
			t.Errorf("%s: pages = %v", name, got)
		}
		if len(log.messages) > 0 {
			t.Errorf("%s: messages = %v", name, log.messages)
		}
	}
}

func TestMissingPDF(t *testing.T) {
	d, log := convertWithFixtures(t, "image::no-such-file.pdf[Alt Text]\n", nil)
	if len(d.Pages) != 1 || d.Pages[0].Imported {
		t.Errorf("pages = %v", importedColors(d))
	}
	if len(log.messages) != 1 || !strings.Contains(log.messages[0], "pdf to insert not found or not readable") {
		t.Errorf("messages = %v", log.messages)
	}
}
