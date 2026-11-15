package converter_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/converter"
)

type logCollector struct {
	t        *testing.T
	messages []string
}

func (l *logCollector) Log(severity, msg string) {
	l.messages = append(l.messages, severity+": "+msg)
}

func convertFile(t *testing.T, path string, attrs map[string]string) ([]byte, *logCollector) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := &logCollector{t: t}
	conv := converter.New(log.Log)
	doc := asciidoc.Parse(string(src), asciidoc.Options{Attributes: attrs, InputFile: path, Logger: log, Inline: conv})
	var buf bytes.Buffer
	if _, err := conv.Convert(doc).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), log
}

func convertString(t *testing.T, src string, attrs map[string]string) ([]byte, *logCollector) {
	t.Helper()
	log := &logCollector{t: t}
	conv := converter.New(log.Log)
	doc := asciidoc.Parse(src, asciidoc.Options{Attributes: attrs, Logger: log, Inline: conv, BaseDir: t.TempDir()})
	var buf bytes.Buffer
	if _, err := conv.Convert(doc).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), log
}

func pageCount(pdf []byte) int {
	return bytes.Count(pdf, []byte("/Type /Page /Parent"))
}

func examples(name string) string {
	return filepath.Join("..", "..", "testdata", "examples", name)
}

func TestBasicExample(t *testing.T) {
	pdf, log := convertFile(t, examples("basic-example.adoc"), nil)
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatal("output is not a PDF document")
	}
	if n := pageCount(pdf); n != 1 {
		t.Errorf("page count = %d, want 1", n)
	}
	if len(log.messages) > 0 {
		t.Errorf("unexpected log messages: %v", log.messages)
	}
	for _, want := range []string{"/Title (Document Title)", "/Author (Doc Writer)", "/Outlines", "/PageLabels", "/Dests"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("PDF is missing %q", want)
		}
	}
}

// 本家 Asciidoctor PDF が出力する chronicles-example.pdf は 17 ページ。
func TestChroniclesExample(t *testing.T) {
	pdf, log := convertFile(t, examples("chronicles-example.adoc"), nil)
	if n := pageCount(pdf); n != 17 {
		t.Errorf("page count = %d, want 17", n)
	}
	if len(log.messages) > 0 {
		t.Errorf("unexpected log messages: %v", log.messages)
	}
}

func TestEdgeCases(t *testing.T) {
	pdf, _ := convertFile(t, examples("edge-cases.adoc"), nil)
	if pageCount(pdf) == 0 {
		t.Error("no pages were produced")
	}
}

func TestBuiltinThemes(t *testing.T) {
	src := "= Title\nAuthor Name\n:doctype: book\n:toc:\n\n== Chapter\n\nText with `code`, *bold* and a link to https://example.org.\n\n|===\n|A |B\n\n|1 |2\n|===\n"
	for _, theme := range []string{"default", "default-sans", "default-for-print", "default-with-font-fallbacks", "base"} {
		pdf, log := convertString(t, src, map[string]string{"pdf-theme": theme})
		if n := pageCount(pdf); n != 3 {
			t.Errorf("theme %s: page count = %d, want 3", theme, n)
		}
		for _, m := range log.messages {
			if strings.HasPrefix(m, "ERROR") {
				t.Errorf("theme %s: %s", theme, m)
			}
		}
	}
}

func TestKeepTogetherAndPageBreaks(t *testing.T) {
	var b strings.Builder
	b.WriteString("= Doc\n\n")
	for i := 0; i < 60; i++ {
		b.WriteString("Paragraph of filler text to push the following block toward the bottom of the page.\n\n")
	}
	b.WriteString("[%unbreakable]\n====\n")
	for i := 0; i < 12; i++ {
		b.WriteString("Line inside an unbreakable example block.\n\n")
	}
	b.WriteString("====\n\n<<<\n\nLast page.\n")
	pdf, _ := convertString(t, b.String(), nil)
	if n := pageCount(pdf); n < 3 {
		t.Errorf("page count = %d, want at least 3", n)
	}
}
