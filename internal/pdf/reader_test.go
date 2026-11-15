package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openFixture(t *testing.T, name string) *Reader {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "pdf", name))
	if err != nil {
		t.Fatal(err)
	}
	r, err := OpenReader(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

// firstColor はページの内容から最初の塗り色の命令を取り出します。
func firstColor(t *testing.T, r *Reader, index int) string {
	t.Helper()
	d := New()
	ip, err := d.ImportPage(r, index)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(ip.content), "\n") {
		if strings.HasSuffix(line, " scn") || strings.HasSuffix(line, " rg") {
			return line
		}
	}
	return ""
}

func TestReaderFixtures(t *testing.T) {
	cases := []struct {
		name  string
		pages int
	}{
		{"blue-letter.pdf", 1},
		{"red-green-blue.pdf", 3},
		{"no-pages.pdf", 0},
		{"tux-bg.pdf", 1},
		{"objstm.pdf", 3},
		{"encrypted-rc4.pdf", 3},
		{"broken-xref.pdf", 3},
	}
	for _, tc := range cases {
		r := openFixture(t, tc.name)
		if n := r.NumPages(); n != tc.pages {
			t.Errorf("%s: pages = %d, want %d", tc.name, n, tc.pages)
		}
	}
	// 同じ内容を別の形式で保存した PDF から同じ色が読めること
	for _, name := range []string{"red-green-blue.pdf", "objstm.pdf", "encrypted-rc4.pdf", "broken-xref.pdf"} {
		r := openFixture(t, name)
		var colors []string
		for i := 0; i < 3; i++ {
			colors = append(colors, firstColor(t, r, i))
		}
		got := strings.Join(colors, "|")
		if !strings.Contains(colors[0], "1") || colors[0] == colors[1] || colors[1] == colors[2] {
			t.Errorf("%s: unexpected colors %s", name, got)
		}
		t.Logf("%s: %s", name, got)
	}
	r := openFixture(t, "blue-letter.pdf")
	d := New()
	ip, err := d.ImportPage(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ip.Width() != 612 || ip.Height() != 792 {
		t.Errorf("blue-letter size = %vx%v", ip.Width(), ip.Height())
	}
}

func TestImportWritesValidPDF(t *testing.T) {
	d := New()
	for _, name := range []string{"red-green-blue.pdf", "encrypted-rc4.pdf", "objstm.pdf", "tux-bg.pdf"} {
		r := openFixture(t, name)
		for i := 0; i < r.NumPages(); i++ {
			ip, err := d.ImportPage(r, i)
			if err != nil {
				t.Fatal(err)
			}
			p := d.AddPage(ip.Width(), ip.Height())
			p.Import, p.Imported = ip, true
		}
	}
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	// 書き出した PDF を自分自身で読み直せること
	r, err := OpenReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if n := r.NumPages(); n != 10 {
		t.Errorf("pages = %d, want 10", n)
	}
	if out := os.Getenv("PDF_TEST_OUT"); out != "" {
		os.WriteFile(out, buf.Bytes(), 0o644)
	}
}

// TestAES256 は AES-256 (R6) で暗号化した文書を空のパスワードで復号できることを確認します。
func TestAES256(t *testing.T) {
	fileKey := bytes.Repeat([]byte{0x42}, 32)
	valSalt, keySalt := []byte("vsalt123"), []byte("ksalt456")
	u := append(append(hash2B(nil, valSalt, nil), valSalt...), keySalt...)
	ik := hash2B(nil, keySalt, nil)
	block, _ := aes.NewCipher(ik)
	ue := make([]byte, 32)
	cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(ue, fileKey)
	// 内容ストリームを AES-256-CBC で暗号化する
	content := []byte("0 0 1 rg 0 0 10 10 re f")
	pad := 16 - len(content)%16
	plain := append(append([]byte{}, content...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := []byte("0123456789abcdef")
	fb, _ := aes.NewCipher(fileKey)
	enc := make([]byte, len(plain))
	cipher.NewCBCEncrypter(fb, iv).CryptBlocks(enc, plain)
	stream := append(append([]byte{}, iv...), enc...)
	hexs := func(b []byte) string { return fmt.Sprintf("<%X>", b) }
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		fmt.Sprintf("<< /Filter /Standard /V 5 /R 6 /Length 256 /P -4 /U %s /UE %s /O %s /OE %s /Perms %s /CF << /StdCF << /CFM /AESV3 /Length 32 >> >> /StmF /StdCF /StrF /StdCF >>",
			hexs(u), hexs(ue), hexs(make([]byte, 48)), hexs(make([]byte, 32)), hexs(make([]byte, 16))),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Encrypt 5 0 R /ID [<00> <00>] >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	r, err := OpenReader(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	ip, err := New().ImportPage(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(ip.content) != string(content) {
		t.Errorf("decrypted content = %q", ip.content)
	}
}

func TestPredictor(t *testing.T) {
	r := &Reader{cache: map[int]any{}}
	// PNG Up フィルター: 2 行目は 1 行目との差分
	data := []byte{2, 1, 2, 3, 2, 1, 1, 1}
	out, err := r.unpredict(data, Dict{"Predictor": int64(12), "Columns": int64(3)})
	if err != nil || !bytes.Equal(out, []byte{1, 2, 3, 2, 3, 4}) {
		t.Errorf("unpredict = %v, %v", out, err)
	}
}
