package converter

import (
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

// imageFormat は画像の形式を返します (format 属性、なければ拡張子)。
func imageFormat(target string, attrs map[string]string) string {
	if f := attrs["format"]; f != "" {
		return strings.ToLower(f)
	}
	if strings.HasPrefix(target, "data:") {
		meta, _, _ := strings.Cut(target[5:], ",")
		meta, _, _ = strings.Cut(meta, ";")
		return strings.TrimPrefix(strings.TrimSuffix(meta, "+xml"), "image/")
	}
	if i := strings.IndexAny(target, "?#"); i >= 0 && isURL(target) {
		target = target[:i]
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(target), "."))
	if ext == "jpeg" {
		ext = "jpg"
	}
	return ext
}

// readResource はローカルファイルまたは URL (allow-uri-read が有効な場合) の内容を読み込みます。
func (c *Converter) readResource(p string) ([]byte, error) {
	if isURL(p) {
		if c.doc == nil || !c.doc.HasAttr("allow-uri-read") {
			return nil, os.ErrPermission
		}
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(p)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, &os.PathError{Op: "get", Path: p, Err: os.ErrNotExist}
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(p)
}

func (c *Converter) readable(p string) bool {
	if isURL(p) {
		return c.doc != nil && c.doc.HasAttr("allow-uri-read")
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

type pdfSource struct {
	r   *pdf.Reader
	err error
}

// openPDF は取り込み元の PDF を開きます (ファイルごとに一度だけ解析します)。
func (c *Converter) openPDF(p string) (*pdf.Reader, error) {
	if c.pdfSources == nil {
		c.pdfSources = map[string]*pdfSource{}
	}
	if src, ok := c.pdfSources[p]; ok {
		return src.r, src.err
	}
	src := &pdfSource{}
	data, err := c.readResource(p)
	if err == nil {
		src.r, err = pdf.OpenReader(data)
	}
	src.err = err
	c.pdfSources[p] = src
	return src.r, err
}

type importOpts struct {
	page             int // 1 始まり
	replace          bool
	advance          bool
	advanceIfMissing bool
	// background が true の場合、取り込んだページを「空」のまま扱う (ページ背景用)
	background bool
	onImported func()
}

// importPage は PDF のページを現在のページの後ろに取り込みます (replace なら現在のページと置き換えます)。
// 指定のページが存在しない場合は false を返します (Asciidoctor PDF の import_page 相当)。
func (c *Converter) importPage(p string, o importOpts) (bool, error) {
	r, err := c.openPDF(p)
	if err != nil {
		return false, err
	}
	if o.page < 1 || o.page > r.NumPages() {
		if o.advanceIfMissing && !o.replace {
			c.advancePage()
		}
		return false, nil
	}
	var ip *pdf.ImportedPage
	if c.scratch == 0 {
		if ip, err = c.pdf.ImportPage(r, o.page-1); err != nil {
			return false, err
		}
	}
	page := &pdf.Page{W: c.pageW, H: c.pageH, Import: ip, Imported: true}
	if ip != nil {
		page.W, page.H = ip.Width(), ip.Height()
	}
	ink := 1
	if o.background {
		ink = 0
	}
	if o.replace && c.pageCount() > 0 {
		c.pdf.Pages[c.page] = page
		c.inkCount[c.page] = ink
	} else {
		at := 0
		if c.pageCount() > 0 {
			at = c.page + 1
		}
		c.insertPage(at, page, ink)
		c.goToPage(at)
	}
	if o.onImported != nil {
		o.onImported()
	}
	if o.advance {
		c.advancePage()
	}
	return true, nil
}

// insertPage はページを位置 at (0 始まり) に挿入し、ページ番号を記録している情報をずらします。
func (c *Converter) insertPage(at int, p *pdf.Page, ink int) {
	count := c.pageCount()
	c.pdf.InsertPage(at, p)
	c.inkCount = append(c.inkCount, 0)
	copy(c.inkCount[at+1:], c.inkCount[at:])
	c.inkCount[at] = ink
	if at >= count {
		return
	}
	for _, si := range c.sectInfo {
		if si.pageStart > at {
			si.pageStart++
		}
		if si.pageEnd > at {
			si.pageEnd++
		}
		if si.destPage >= at {
			si.destPage++
		}
	}
	for _, m := range c.noRunning {
		shifted := map[int]bool{}
		for pg, v := range m {
			if pg > at {
				pg++
			}
			shifted[pg] = v
		}
		for k := range m {
			delete(m, k)
		}
		for k, v := range shifted {
			m[k] = v
		}
	}
	if e := c.tocExtent; e != nil {
		if e.fromPage >= at {
			e.fromPage++
		}
		if e.toPage >= at {
			e.toPage++
		}
	}
}

// pageTop は現在のページの上端の y 座標 (移動先に使う) を返します。
func (c *Converter) pageTop() float64 {
	if ip := c.pdf.Pages[c.page].Import; ip != nil {
		return ip.MediaBox[3]
	}
	return c.pageH
}

// resolvePagenums は pages 属性 (例: "3,1..2" や "3;1..2") をページ番号の一覧にします。
func resolvePagenums(val string) []int {
	sep := ";"
	if strings.Contains(val, ",") {
		sep = ","
	}
	var out []int
	for _, entry := range strings.Split(val, sep) {
		entry = strings.TrimSpace(entry)
		if from, to, ok := strings.Cut(entry, ".."); ok {
			a, b := max(rubyToI(from), 1), max(rubyToI(to), 1)
			for n := a; n <= b; n++ {
				out = append(out, n)
			}
		} else {
			out = append(out, rubyToI(entry))
		}
	}
	return out
}

// rubyToI は Ruby の String#to_i と同じく先頭の整数部分だけを読み取ります (読めなければ 0)。
func rubyToI(s string) int {
	s = strings.TrimSpace(s)
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

// convertPDFImage は PDF を対象とする画像ブロックのページを取り込みます。
func (c *Converter) convertPDFImage(node *Block, target string, o imageOpts) {
	p := c.resolveImagePath(node, target, o.relativeToImagesdir)
	if !c.readable(p) {
		c.logf("WARN", "pdf to insert not found or not readable: %s", p)
		c.onImageError(node, target, "left", o)
		return
	}
	replace := c.pageEmpty()
	var parentSect *Block
	if replace && node.Parent != nil {
		if si := c.sectInfo[node.Parent]; si != nil && si.pageStart == c.pageNumber() && si.anchor != "" {
			parentSect = node.Parent
		}
	}
	onImported := func() {
		top := c.pageTop()
		if node.ID != "" {
			c.addDest(node.ID, 0, top)
		}
		if parentSect != nil {
			si := c.sectInfo[parentSect]
			c.addDest(si.anchor, 0, top)
			if c.scratch == 0 {
				si.destPage, si.destY = c.page, top
			}
		}
	}
	pages := []int{max(rubyToI(node.AttrOr("page", "1")), 1)}
	if v := node.Attr("pages"); v != "" {
		pages = resolvePagenums(v)
	}
	for idx, pg := range pages {
		opts := importOpts{page: pg, replace: replace || idx > 0, advance: true, advanceIfMissing: true}
		if idx == 0 {
			opts.onImported = onImported
		}
		if _, err := c.importPage(p, opts); err != nil {
			c.logf("WARN", "could not embed image: %s; %v", p, err)
			c.onImageError(node, target, "left", o)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// 表紙・ページ背景の画像

// bgImage は表紙やページ背景に使う画像です。
type bgImage struct {
	path     string
	format   string
	page     int
	attrs    map[string]string
	hasAttrs bool
	// symbolic は特別な値 ("" は白紙ページ、"~" は何もしない) が指定された場合に true
	symbolic bool
}

// resolveBackgroundImage は文書属性 (なければテーマのキー) から画像を解決します。
// 指定がなければ (nil, false)、"none" などで明示的に無効化されていれば (nil, true) を返します。
func (c *Converter) resolveBackgroundImage(doc *asciidoc.Document, attrKey, themeKey string, symbolic bool) (*bgImage, bool) {
	value, ok := doc.Attributes[attrKey]
	fromTheme := false
	if !ok {
		v := c.theme.Get(themeKey)
		if v == nil {
			return nil, false
		}
		value, fromTheme = c.theme.Str(themeKey), true
	}
	if symbolic && (value == "" || value == "~") {
		return &bgImage{path: value, symbolic: true}, true
	}
	if value == "none" || value == "" {
		return nil, true
	}
	img := &bgImage{attrs: map[string]string{}}
	relativeToImagesdir := false
	if strings.Contains(value, ":") {
		if m := imageAttrValueRx.FindStringSubmatch(value); m != nil {
			doc.ParseAttributes(m[2], []string{"alt", "width"}, nil, img.attrs)
			value, img.hasAttrs, relativeToImagesdir = m[1], true, true
		}
	}
	layout := "portrait"
	if c.pageW > c.pageH {
		layout = "landscape"
	}
	if fromTheme {
		value = c.subThemeAttributes(doc, value, layout)
	} else {
		value = strings.ReplaceAll(value, "{page-layout}", layout)
	}
	img.format = imageFormat(value, img.attrs)
	switch {
	case fromTheme && !filepath.IsAbs(value) && !isURL(value):
		img.path = c.themeAsset(value)
	default:
		img.path = c.resolveImagePath(doc.Block, value, relativeToImagesdir)
	}
	if !c.readable(img.path) {
		c.logf("WARN", "%s not found or readable: %s", strings.NewReplacer("-", " ", "_", " ").Replace(attrKey), img.path)
		return nil, true
	}
	if img.format == "pdf" {
		img.page = max(rubyToI(img.attrs["page"]), 1)
	}
	return img, true
}

// subThemeAttributes はテーマ内のパスの属性参照を展開します ({imagesdir} はテーマのディレクトリを指します)。
func (c *Converter) subThemeAttributes(doc *asciidoc.Document, value, layout string) string {
	if !strings.Contains(value, "{") {
		return value
	}
	saved := map[string]*string{}
	set := func(k, v string) {
		if old, had := doc.Attributes[k]; had {
			o := old
			saved[k] = &o
		} else {
			saved[k] = nil
		}
		doc.Attributes[k] = v
	}
	if c.themesDir != "" && c.themesDir != theme.BundledDir {
		set("imagesdir", c.themesDir)
	}
	set("page-layout", layout)
	value = doc.SubAttributes(value)
	for k, v := range saved {
		if v == nil {
			delete(doc.Attributes, k)
		} else {
			doc.Attributes[k] = *v
		}
	}
	return value
}

// placeImage は画像をコンテナ (ページ全体) に配置する位置と寸法を、属性 (fit / position / pdfwidth など) から求めます。
// (Asciidoctor PDF の resolve_image_options を background: true で呼んだ場合と同じ規則です)
func (c *Converter) placeImage(li *loadedImage, img *bgImage, cw, ch float64) (x, y, w, h float64) {
	iw, ih := li.w, li.h
	contain := func() (float64, float64) {
		s := minF(cw/iw, ch/ih)
		return iw * s, ih * s
	}
	w, h = iw, ih
	hpos, vpos := "center", "center"
	if img.hasAttrs {
		if p, ok := resolveBackgroundPosition(img.attrs["position"]); ok {
			hpos, vpos = p[0], p[1]
		}
		fit := img.attrs["fit"]
		if fit == "" {
			fit = "contain"
		}
		if li.format == "svg" && fit == "fill" {
			fit = "contain"
		}
		switch fit {
		case "none":
			if ew, ok := c.explicitImageWidth(img.attrs, cw, li, false); ok {
				w, h = ew, ih*ew/iw
			}
		case "scale-down":
			if ew, ok := c.explicitImageWidth(img.attrs, cw, li, false); ok {
				if ew > cw || ew*(ih/iw) > ch {
					w, h = contain()
				} else {
					w, h = ew, ih*ew/iw
				}
			} else if iw > cw || ih > ch {
				w, h = contain()
			}
		case "cover":
			if cw*(ih/iw) < ch {
				w, h = iw*ch/ih, ch
			} else {
				w, h = cw, ih*cw/iw
			}
		case "fill":
			w, h = cw, ch
		default:
			w, h = contain()
		}
	} else {
		w, h = contain()
	}
	switch hpos {
	case "left":
		x = 0
	case "right":
		x = cw - w
	default:
		x = (cw - w) / 2
	}
	switch vpos {
	case "top":
		y = ch - h
	case "bottom":
		y = 0
	default:
		y = (ch - h) / 2
	}
	return
}

// resolveBackgroundPosition は "left top" や "center" のような位置指定を (水平, 垂直) に変換します。
func resolveBackgroundPosition(value string) ([2]string, bool) {
	if value == "" {
		return [2]string{}, false
	}
	if strings.Contains(value, " ") {
		var h, v string
		center := false
		parts := strings.SplitN(value, " ", 2)
		for _, kw := range parts {
			switch kw {
			case "left", "right":
				h = kw
			case "top", "bottom":
				v = kw
			case "center":
				center = true
			}
		}
		switch {
		case center:
			if h == "" {
				h = "center"
			}
			if v == "" {
				v = "center"
			}
			return [2]string{h, v}, true
		case h != "" && v != "":
			return [2]string{h, v}, true
		}
		return [2]string{}, false
	}
	switch value {
	case "left", "right", "center":
		return [2]string{value, "center"}, true
	case "top", "bottom":
		return [2]string{"center", value}, true
	}
	return [2]string{}, false
}

// startNewPageDiscretely はページ背景などを適用せずに新しいページを追加します。
func (c *Converter) startNewPageDiscretely() {
	c.pdf.AddPage(c.pageW, c.pageH)
	c.inkCount = append(c.inkCount, 0)
	c.goToPage(len(c.pdf.Pages) - 1)
}

type pageBackground struct {
	resolved     bool
	recto, verso *bgImage
}

// pageBackgroundImage はページの左右 (recto / verso) に応じた背景画像を返します。
func (c *Converter) pageBackgroundImage(recto bool) *bgImage {
	bg := &c.pageBg
	if !bg.resolved {
		bg.resolved = true
		base, _ := c.resolveBackgroundImage(c.doc, "page-background-image", "page_background_image", false)
		bg.recto, bg.verso = base, base
		if img, set := c.resolveBackgroundImage(c.doc, "page-background-image-verso", "page_background_image_verso", false); set {
			bg.verso = img
		}
		if img, set := c.resolveBackgroundImage(c.doc, "page-background-image-recto", "page_background_image_recto", false); set {
			bg.recto = img
		}
	}
	if recto {
		return bg.recto
	}
	return bg.verso
}

// applyPageBackground は新しく作ったページに背景画像を描画します。
// 背景が PDF の場合は、そのページを取り込んだページに置き換えます (背景色とランニングコンテンツは適用されません)。
func (c *Converter) applyPageBackground() {
	if c.scratch > 0 || c.doc == nil {
		return
	}
	recto := c.isRecto(c.pageNumber())
	img := c.pageBackgroundImage(recto)
	if img == nil {
		return
	}
	fail := func(err any) {
		c.logf("WARN", "could not embed page background image: %s; %v", img.path, err)
		if c.pageBg.recto == c.pageBg.verso {
			c.pageBg.recto, c.pageBg.verso = nil, nil
		} else if recto {
			c.pageBg.recto = nil
		} else {
			c.pageBg.verso = nil
		}
	}
	if img.format == "pdf" {
		r, err := c.openPDF(img.path)
		if err != nil {
			fail(err)
			return
		}
		if img.page > r.NumPages() {
			return
		}
		ip, err := c.pdf.ImportPage(r, img.page-1)
		if err != nil {
			fail(err)
			return
		}
		page := c.pdf.Pages[c.page]
		page.Chunks, page.Import, page.Imported = nil, ip, true
		return
	}
	li := c.loadImage(img.path, img.path)
	if li == nil {
		fail("image could not be loaded")
		return
	}
	x, y, w, h := c.placeImage(li, img, c.pageW, c.pageH)
	g := pdf.NewG()
	li.drawTo(c, g, x, y, w, h)
	c.pdf.Pages[c.page].Add(g.Bytes())
}
