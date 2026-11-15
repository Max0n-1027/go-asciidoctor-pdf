package converter

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/svg"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/theme"
)

type loadedImage struct {
	data   []byte
	svg    *svg.Image
	img    *pdf.Image
	w, h   float64 // ピクセル寸法
	format string
	path   string
}

func (li *loadedImage) drawTo(c *Converter, g *pdf.G, x, y, w, h float64) {
	if c.scratch > 0 {
		return
	}
	if li.svg != nil {
		li.svg.Render(g, c.pdf, x, y, w, h)
		for _, w := range li.svg.Warnings {
			if !c.svgWarned[li.path+w] {
				c.svgWarned[li.path+w] = true
				c.logf("WARN", "problem encountered in image: %s; %s", li.path, w)
			}
		}
		return
	}
	if li.img == nil {
		img, err := c.pdf.AddImage(li.data)
		if err != nil {
			return
		}
		li.img = img
	}
	g.Image(li.img, x, y, w, h)
}

type imageOpts struct {
	relativeToImagesdir bool
	pinned              bool
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "ftp://")
}

// resolveImagePath は画像のパスを解決します (imagesdir と文書の基準ディレクトリを考慮します)。
func (c *Converter) resolveImagePath(node *Block, target string, relativeToImagesdir bool) string {
	if strings.HasPrefix(target, "data:") || isURL(target) {
		return target
	}
	doc := c.doc
	if node != nil && node.Doc != nil {
		doc = c.rootDoc(node.Doc)
	}
	imagesdir := ""
	if relativeToImagesdir && doc != nil {
		imagesdir = doc.Attr("imagesdir")
		if node != nil {
			if v, ok := node.Attrs["imagesdir"]; ok {
				imagesdir = v
			}
		}
		if imagesdir == "." || imagesdir == "./" {
			imagesdir = ""
		}
	}
	if isURL(imagesdir) {
		return strings.TrimSuffix(imagesdir, "/") + "/" + target
	}
	if filepath.IsAbs(target) {
		return target
	}
	base := ""
	if doc != nil {
		base = doc.BaseDir
	}
	p := target
	if imagesdir != "" {
		p = filepath.Join(imagesdir, target)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// loadImage は画像を読み込みます。読み込めない、または未対応の形式なら nil を返します。
func (c *Converter) loadImage(path, target string) *loadedImage {
	if li, ok := c.images[path]; ok {
		return li
	}
	var data []byte
	var err error
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.SplitN(path, "?", 2)[0]), "."))
	switch {
	case strings.HasPrefix(path, "data:"):
		meta, payload, ok := strings.Cut(path[5:], ",")
		if !ok || !strings.HasSuffix(meta, ";base64") {
			c.logf("WARN", "unsupported data URI for image: %s", target)
			c.images[path] = nil
			return nil
		}
		data, err = base64.StdEncoding.DecodeString(payload)
		format = strings.TrimPrefix(strings.TrimSuffix(meta, ";base64"), "image/")
	case isURL(path):
		if c.doc == nil || !c.doc.HasAttr("allow-uri-read") {
			c.logf("WARN", "cannot embed remote image: %s (allow-uri-read attribute not enabled)", path)
			c.images[path] = nil
			return nil
		}
		client := &http.Client{Timeout: 30 * time.Second}
		var resp *http.Response
		if resp, err = client.Get(path); err == nil {
			data, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
	default:
		data, err = os.ReadFile(path)
	}
	if err != nil {
		c.logf("WARN", "image to embed not found or not readable: %s", path)
		c.images[path] = nil
		return nil
	}
	if format == "svg" || strings.HasPrefix(strings.TrimSpace(string(data[:minInt(len(data), 256)])), "<") {
		parsed, err := svg.Parse(data)
		if err != nil {
			c.logf("WARN", "could not embed image: %s; %v", path, err)
			c.images[path] = nil
			return nil
		}
		if !isURL(path) && !strings.HasPrefix(path, "data:") {
			// SVG から参照される画像は SVG と同じディレクトリを基準に読み込む
			dir := filepath.Dir(path)
			parsed.Loader = func(href string) ([]byte, error) {
				if isURL(href) || filepath.IsAbs(href) {
					return nil, os.ErrPermission
				}
				p := filepath.Join(dir, filepath.FromSlash(href))
				if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") {
					return nil, os.ErrPermission
				}
				return os.ReadFile(p)
			}
		}
		li := &loadedImage{data: data, svg: parsed, w: parsed.W, h: parsed.H, format: "svg", path: path}
		c.images[path] = li
		return li
	}
	if format == "pdf" {
		c.logf("WARN", "could not embed image: %s; PDF images are not supported yet", path)
		c.images[path] = nil
		return nil
	}
	w, h, err := pdf.ImageSize(data)
	if err != nil || w == 0 || h == 0 {
		c.logf("WARN", "could not embed image: %s; %v", path, err)
		c.images[path] = nil
		return nil
	}
	if format == "jpeg" {
		format = "jpg"
	}
	li := &loadedImage{data: data, w: float64(w), h: float64(h), format: format}
	c.images[path] = li
	return li
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// explicitImageWidth は属性やテーマから画像の幅 (pt) を解決します。
func (c *Converter) explicitImageWidth(attrs map[string]string, boundsWidth float64, li *loadedImage, useFallback bool) (float64, bool) {
	pct := func(v string) float64 {
		n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		return n / 100 * boundsWidth
	}
	if v, ok := attrs["pdfwidth"]; ok {
		switch {
		case strings.HasSuffix(v, "%"):
			return pct(v), true
		case strings.HasSuffix(v, "vw"):
			n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "vw"), 64)
			return c.pageW * n / 100, true
		case strings.HasSuffix(v, "iw"):
			n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "iw"), 64)
			return li.w * 0.75 * n / 100, true
		}
		return theme.StrToPt(v), true
	}
	if v, ok := attrs["scaledwidth"]; ok {
		if strings.HasSuffix(v, "%") {
			return pct(v), true
		}
		return theme.StrToPt(v), true
	}
	if useFallback {
		switch v := c.theme.Get("image_width").(type) {
		case float64:
			return v, true
		case string:
			switch {
			case strings.HasSuffix(v, "%"):
				return pct(v), true
			case strings.HasSuffix(v, "vw"):
				n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "vw"), 64)
				return c.pageW * n / 100, true
			case v != "":
				return theme.StrToPt(v), true
			}
		}
	}
	if v, ok := attrs["width"]; ok {
		if strings.HasSuffix(v, "%") {
			return pct(v), true
		}
		if isDigits(v) {
			n, _ := strconv.ParseFloat(v, 64)
			return n * 0.75, true
		}
	}
	return 0, false
}

func (c *Converter) convertImage(node *Block, o imageOpts) {
	t := c.theme
	target := node.Attr("target")
	if imageFormat(target, node.Attrs) == "pdf" {
		c.convertPDFImage(node, target, o)
		return
	}
	alignment := node.Attr("align")
	floatTo := node.Attr("float")
	if floatTo == "left" || floatTo == "right" {
		alignment = floatTo
	} else {
		floatTo = ""
	}
	if alignment != "left" && alignment != "center" && alignment != "right" {
		alignment = resolveTextAlignFromRole(node.Roles())
		if alignment == "" || alignment == "justify" {
			alignment = t.StrOr("image_align", "left")
		}
	}
	path := c.resolveImagePath(node, target, o.relativeToImagesdir)
	li := c.loadImage(path, target)
	if li == nil {
		c.onImageError(node, target, alignment, o)
		return
	}
	if node.ID != "" {
		c.addDestForBlock(node, "")
	}
	availW := c.width
	width, explicit := c.explicitImageWidth(node.Attrs, availW, li, true)
	if !explicit {
		width = minF(availW, li.w*0.75)
	}
	if width > availW && !strings.HasSuffix(node.Attr("pdfwidth"), "vw") {
		width = availW
	}
	height := width * li.h / li.w
	title := ""
	if node.HasTitle() {
		title = node.CaptionedTitle()
	}
	captionEnd := t.StrOr("image_caption_end", "bottom")
	var captionMax any
	if v := t.Get("image_caption_max_width"); v != nil {
		captionMax = v
	}
	if s, _ := captionMax.(string); floatTo != "" && !strings.HasPrefix(s, "fit-content") {
		captionMax = "fit-content"
	}
	captionH := 0.0
	if title != "" {
		_, fromY, pages, toY := c.dryRun(true, func() {
			c.concealTop = captionEnd == "bottom"
			c.inkCaption(title, captionOpts{category: "image", bottom: captionEnd == "bottom", blockAlign: alignment, blockWidth: width, maxWidth: captionMax})
		})
		if pages == 0 {
			captionH = fromY - toY
		}
	}
	availH := c.cursor()
	if height+captionH > availH+0.0001 {
		if !c.atPageTop() && !o.pinned {
			c.advancePage()
			availH = c.cursor()
		}
		if height+captionH > availH+0.0001 {
			height = availH - captionH
			if height < 1 {
				height = 1
			}
			width = height * li.w / li.h
		}
	}
	if captionEnd == "top" && title != "" {
		c.inkCaption(title, captionOpts{category: "image", blockAlign: alignment, blockWidth: width, maxWidth: captionMax})
	}
	x := c.left
	switch alignment {
	case "center":
		x += (availW - width) / 2
	case "right":
		x += availW - width
	}
	top := c.y
	g := pdf.NewG()
	li.drawTo(c, g, x, top-height, width, height)
	if c.scratch > 0 {
		c.inked()
	} else {
		c.draw(g)
	}
	if bw := t.Float("image_border_width", 0); bw > 0 && !node.HasRole("noborder") {
		if bc := c.resolveThemeColor("image_border_color", clr{}, clr{}); bc.set {
			bx, bwid := x, width
			if t.Str("image_border_fit") == "auto" {
				bx, bwid = c.left, availW
			}
			c.fillAndStroke(bx, top, bwid, height, clr{}, bc, bw, t.Str("image_border_style"), t.Float("image_border_radius", 0))
		}
	}
	if link := node.Attr("link"); link != "" && c.scratch == 0 {
		a := pdf.Annot{Rect: [4]float64{x, top - height, x + width, top}}
		if strings.HasPrefix(link, "#") {
			a.Dest = link[1:]
		} else {
			if link == "self" {
				link = target
			}
			a.URI = link
		}
		c.pdf.Pages[c.page].Annots = append(c.pdf.Pages[c.page].Annots, a)
	}
	c.moveDown(height)
	if captionEnd == "bottom" && title != "" {
		c.inkCaption(title, captionOpts{category: "image", bottom: true, blockAlign: alignment, blockWidth: width, maxWidth: captionMax})
	}
	if next := nextEnclosedBlock(node, false); !o.pinned && next != nil {
		if floatTo != "" && next.Context == "paragraph" && width < c.width {
			c.initFloatBox(width, height+captionH, floatTo)
		} else {
			c.themeMargin("block", "bottom", true)
		}
	}
}

func (c *Converter) onImageError(node *Block, target, alignment string, o imageOpts) {
	tmpl := "%{link}[%{alt}]%{/link} | <em>%{target}</em>"
	if c.theme.Has("image_alt_content") {
		tmpl = c.theme.Str("image_alt_content")
	}
	if tmpl != "" {
		open, close := "", ""
		if link := node.Attr("link"); link != "" {
			open, close = `<a href="`+link+`">`, "</a>"
		}
		text := strings.NewReplacer("%{link}", open, "%{/link}", close, "%{alt}", node.Attr("alt"), "%{target}", target).Replace(tmpl)
		c.themeFont("image_alt", -1, func() {
			c.inkProse(text, prose{align: alignment, margin: fptr(0), noNormalize: true, singleLine: true})
		})
	}
	if node.HasTitle() {
		c.inkCaption(node.CaptionedTitle(), captionOpts{category: "image", bottom: true})
	}
	if !o.pinned {
		c.themeMargin("block", "bottom", nextEnclosedBlock(node, false) != nil)
	}
}

// prepareInlineImage はインライン画像の寸法を決めます。
func (c *Converter) prepareInlineImage(f *frag, p *piece) {
	im := f.image
	li := c.loadImage(im.path, im.path)
	if li == nil {
		return
	}
	w, _ := strconv.ParseFloat(im.width, 64)
	if w <= 0 {
		w = li.w * 0.75
	}
	h := w * li.h / li.w
	// 行の高さに収まるよう縮小する (fit=line) か、利用可能な幅に収める
	maxW := c.width
	if w > maxW {
		w, h = maxW, maxW*li.h/li.w
	}
	if im.fit == "line" {
		if lineH := p.asc + p.desc; h > lineH {
			w, h = w*lineH/h, lineH
		}
	}
	im.w, im.h = w, h
	// 画像が行より高い場合は行の高さを広げる
	if h > p.asc+p.desc {
		p.asc = h - p.desc
		p.lineH = h
	}
}

func (c *Converter) drawInlineImage(g *pdf.G, f *frag, x, baseline float64, p *piece) {
	im := f.image
	if im.w == 0 {
		return
	}
	li := c.loadImage(im.path, im.path)
	if li == nil {
		return
	}
	// 行の中央に揃える
	y := baseline - p.desc + ((p.asc+p.desc)-im.h)/2
	li.drawTo(c, g, x, y, im.w, im.h)
}
