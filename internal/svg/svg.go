// Package svg は SVG 画像を PDF の描画命令に変換する簡易レンダラーです。
// パス・基本図形・グループ・変形・塗りと線・埋め込み画像に対応しています。
// テキスト、グラデーション (単色で近似)、クリッピング、フィルターには未対応です。
package svg

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/pdf"
)

type node struct {
	name     string
	attrs    map[string]string
	children []*node
}

// Image は解析済みの SVG 画像です。
type Image struct {
	// W, H は固有の寸法 (px 相当のユーザー単位)
	W, H float64
	// Warnings は未対応の要素などの警告です。
	Warnings []string
	// Loader は外部画像 (href) を読み込む関数です。nil なら外部画像は描画しません。
	Loader func(href string) ([]byte, error)

	root   *node
	ids    map[string]*node
	vb     [4]float64
	images map[string]*pdf.Image
}

// Parse は SVG 文書を解析します。
func Parse(data []byte) (*Image, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var stack []*node
	var root *node
	ids := map[string]*node{}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			// style 属性の宣言は個別の属性より優先する
			for _, decl := range strings.Split(n.attrs["style"], ";") {
				if k, v, ok := strings.Cut(decl, ":"); ok {
					n.attrs[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			if id := n.attrs["id"]; id != "" {
				ids[id] = n
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if root == nil || root.name != "svg" {
		return nil, errors.New("not an SVG document")
	}
	img := &Image{root: root, ids: ids, images: map[string]*pdf.Image{}}
	hasVB := false
	if vb := fields(root.attrs["viewBox"]); len(vb) == 4 {
		img.vb = [4]float64{vb[0], vb[1], vb[2], vb[3]}
		hasVB = vb[2] > 0 && vb[3] > 0
	}
	w, wok := length(root.attrs["width"], img.vb[2])
	h, hok := length(root.attrs["height"], img.vb[3])
	switch {
	case wok && hok:
	case wok && hasVB:
		h = w * img.vb[3] / img.vb[2]
	case hok && hasVB:
		w = h * img.vb[2] / img.vb[3]
	case hasVB:
		w, h = img.vb[2], img.vb[3]
	default:
		w, h = 300, 150
	}
	if !hasVB {
		img.vb = [4]float64{0, 0, w, h}
	}
	if w <= 0 || h <= 0 {
		return nil, errors.New("SVG has no size")
	}
	img.W, img.H = w, h
	return img, nil
}

func fields(s string) []float64 {
	var out []float64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' || r == '\r' }) {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// length は長さの指定をユーザー単位 (px) に変換します。
func length(s string, ref float64) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	units := map[string]float64{"px": 1, "pt": 96.0 / 72, "pc": 16, "mm": 96 / 25.4, "cm": 96 / 2.54, "in": 96, "em": 16, "ex": 8}
	for suffix, factor := range units {
		if strings.HasSuffix(s, suffix) {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, suffix)), 64)
			return v * factor, err == nil
		}
	}
	if strings.HasSuffix(s, "%") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		return v / 100 * ref, err == nil && ref > 0
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[2]*n[1], m[1]*n[0] + m[3]*n[1],
		m[0]*n[2] + m[2]*n[3], m[1]*n[2] + m[3]*n[3],
		m[0]*n[4] + m[2]*n[5] + m[4], m[1]*n[4] + m[3]*n[5] + m[5],
	}
}

func parseTransform(s string) matrix {
	m := identity
	for s != "" {
		open := strings.IndexByte(s, '(')
		close := strings.IndexByte(s, ')')
		if open < 0 || close < open {
			break
		}
		name := strings.Trim(s[:open], " ,\t\n")
		args := fields(s[open+1 : close])
		s = s[close+1:]
		arg := func(i int, def float64) float64 {
			if i < len(args) {
				return args[i]
			}
			return def
		}
		var t matrix
		switch name {
		case "matrix":
			if len(args) != 6 {
				continue
			}
			t = matrix{args[0], args[1], args[2], args[3], args[4], args[5]}
		case "translate":
			t = matrix{1, 0, 0, 1, arg(0, 0), arg(1, 0)}
		case "scale":
			sx := arg(0, 1)
			t = matrix{sx, 0, 0, arg(1, sx), 0, 0}
		case "rotate":
			a := arg(0, 0) * math.Pi / 180
			r := matrix{math.Cos(a), math.Sin(a), -math.Sin(a), math.Cos(a), 0, 0}
			if len(args) >= 3 {
				t = matrix{1, 0, 0, 1, args[1], args[2]}.mul(r).mul(matrix{1, 0, 0, 1, -args[1], -args[2]})
			} else {
				t = r
			}
		case "skewX":
			t = matrix{1, 0, math.Tan(arg(0, 0) * math.Pi / 180), 1, 0, 0}
		case "skewY":
			t = matrix{1, math.Tan(arg(0, 0) * math.Pi / 180), 0, 1, 0, 0}
		default:
			continue
		}
		m = m.mul(t)
	}
	return m
}

type paint struct {
	none  bool
	color pdf.Color
}

type style struct {
	fill, stroke               paint
	strokeWidth                float64
	fillOpacity, strokeOpacity float64
	evenOdd                    bool
	lineCap, lineJoin          int
	miter                      float64
	dash                       []float64
	color                      pdf.Color
}

var namedColors = map[string]string{
	"black": "000000", "white": "FFFFFF", "red": "FF0000", "green": "008000", "blue": "0000FF", "yellow": "FFFF00",
	"cyan": "00FFFF", "aqua": "00FFFF", "magenta": "FF00FF", "fuchsia": "FF00FF", "gray": "808080", "grey": "808080",
	"silver": "C0C0C0", "maroon": "800000", "olive": "808000", "lime": "00FF00", "teal": "008080", "navy": "000080",
	"purple": "800080", "orange": "FFA500", "pink": "FFC0CB", "brown": "A52A2A", "gold": "FFD700", "indigo": "4B0082",
	"violet": "EE82EE", "darkgray": "A9A9A9", "darkgrey": "A9A9A9", "lightgray": "D3D3D3", "lightgrey": "D3D3D3",
	"darkblue": "00008B", "darkgreen": "006400", "darkred": "8B0000", "lightblue": "ADD8E6", "lightgreen": "90EE90",
	"steelblue": "4682B4", "royalblue": "4169E1", "skyblue": "87CEEB", "tomato": "FF6347", "crimson": "DC143C",
	"coral": "FF7F50", "salmon": "FA8072", "khaki": "F0E68C", "beige": "F5F5DC", "ivory": "FFFFF0", "tan": "D2B48C",
	"turquoise": "40E0D0", "orchid": "DA70D6", "plum": "DDA0DD", "dimgray": "696969", "dimgrey": "696969",
	"slategray": "708090", "slategrey": "708090", "whitesmoke": "F5F5F5", "gainsboro": "DCDCDC", "firebrick": "B22222",
	"forestgreen": "228B22", "seagreen": "2E8B57", "midnightblue": "191970", "chocolate": "D2691E", "sienna": "A0522D",
}

func (img *Image) parsePaint(s string, cur paint, current pdf.Color) paint {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "inherit":
		return cur
	case s == "none" || s == "transparent":
		return paint{none: true}
	case s == "currentColor":
		return paint{color: current}
	case strings.HasPrefix(s, "#"):
		if c := pdf.ParseColor(s); c.IsSet {
			return paint{color: c}
		}
	case strings.HasPrefix(s, "rgb"):
		open, close := strings.IndexByte(s, '('), strings.IndexByte(s, ')')
		if open > 0 && close > open {
			parts := strings.FieldsFunc(s[open+1:close], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
			if len(parts) >= 3 {
				var v [3]float64
				for i := 0; i < 3; i++ {
					if strings.HasSuffix(parts[i], "%") {
						f, _ := strconv.ParseFloat(strings.TrimSuffix(parts[i], "%"), 64)
						v[i] = f / 100
					} else {
						f, _ := strconv.ParseFloat(parts[i], 64)
						v[i] = f / 255
					}
				}
				return paint{color: pdf.Color{IsSet: true, V: [4]float64{v[0], v[1], v[2]}}}
			}
		}
	case strings.HasPrefix(s, "url("):
		// グラデーションは最初の色で近似する
		id := strings.Trim(s[4:strings.IndexByte(s, ')')], "#'\" ")
		if ref := img.ids[id]; ref != nil {
			for _, stop := range img.gradientStops(ref, 0) {
				if c := stop.attrs["stop-color"]; c != "" {
					return img.parsePaint(c, cur, current)
				}
			}
		}
		return paint{none: true}
	default:
		if hex, ok := namedColors[strings.ToLower(s)]; ok {
			return paint{color: pdf.ParseColor(hex)}
		}
	}
	return cur
}

func (img *Image) gradientStops(n *node, depth int) []*node {
	var stops []*node
	for _, c := range n.children {
		if c.name == "stop" {
			stops = append(stops, c)
		}
	}
	if len(stops) == 0 && depth < 5 {
		if href := strings.TrimPrefix(n.attrs["href"], "#"); href != "" {
			if ref := img.ids[href]; ref != nil {
				return img.gradientStops(ref, depth+1)
			}
		}
	}
	return stops
}

func (img *Image) applyStyle(st style, n *node) style {
	a := n.attrs
	if v, ok := a["color"]; ok {
		if p := img.parsePaint(v, paint{color: st.color}, st.color); !p.none {
			st.color = p.color
		}
	}
	if v, ok := a["fill"]; ok {
		st.fill = img.parsePaint(v, st.fill, st.color)
	}
	if v, ok := a["stroke"]; ok {
		st.stroke = img.parsePaint(v, st.stroke, st.color)
	}
	if v, ok := length(a["stroke-width"], 0); ok {
		st.strokeWidth = v
	}
	num := func(key string, cur float64) float64 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(a[key]), 64); err == nil {
			return v
		}
		return cur
	}
	st.fillOpacity = num("fill-opacity", st.fillOpacity)
	st.strokeOpacity = num("stroke-opacity", st.strokeOpacity)
	if o := num("opacity", 1); o < 1 {
		st.fillOpacity *= o
		st.strokeOpacity *= o
	}
	switch a["fill-rule"] {
	case "evenodd":
		st.evenOdd = true
	case "nonzero":
		st.evenOdd = false
	}
	switch a["stroke-linecap"] {
	case "butt":
		st.lineCap = 0
	case "round":
		st.lineCap = 1
	case "square":
		st.lineCap = 2
	}
	switch a["stroke-linejoin"] {
	case "miter":
		st.lineJoin = 0
	case "round":
		st.lineJoin = 1
	case "bevel":
		st.lineJoin = 2
	}
	st.miter = num("stroke-miterlimit", st.miter)
	if v, ok := a["stroke-dasharray"]; ok {
		if v == "none" {
			st.dash = nil
		} else {
			st.dash = fields(v)
		}
	}
	return st
}

// Render は画像を PDF 座標の矩形 (左下 x, y、幅 w、高さ h) に描画します。
func (img *Image) Render(g *pdf.G, doc *pdf.Document, x, y, w, h float64) {
	g.Save()
	// SVG は y 軸が下向きなので反転する
	g.Transform(w/img.vb[2], 0, 0, -h/img.vb[3], x-img.vb[0]*w/img.vb[2], y+h+img.vb[1]*h/img.vb[3])
	st := style{fill: paint{color: pdf.ParseColor("000000")}, stroke: paint{none: true}, strokeWidth: 1, fillOpacity: 1, strokeOpacity: 1, miter: 4, color: pdf.ParseColor("000000")}
	r := &renderer{img: img, g: g, doc: doc}
	for _, c := range img.root.children {
		r.render(c, img.applyStyle(st, img.root), 0)
	}
	g.Restore()
}

type renderer struct {
	img *Image
	g   *pdf.G
	doc *pdf.Document
}

func (r *renderer) warn(msg string) {
	for _, w := range r.img.Warnings {
		if w == msg {
			return
		}
	}
	r.img.Warnings = append(r.img.Warnings, msg)
}

func (r *renderer) render(n *node, st style, depth int) {
	if depth > 32 || n.attrs["display"] == "none" || n.attrs["visibility"] == "hidden" {
		return
	}
	a := n.attrs
	num := func(key string) float64 {
		v, _ := length(a[key], 0)
		return v
	}
	switch n.name {
	case "defs", "clipPath", "mask", "linearGradient", "radialGradient", "pattern", "marker", "symbol", "style", "title", "desc", "metadata", "filter":
		return
	}
	st = r.img.applyStyle(st, n)
	g := r.g
	transform := a["transform"]
	if transform != "" {
		m := parseTransform(transform)
		g.Save()
		g.Transform(m[0], m[1], m[2], m[3], m[4], m[5])
		defer g.Restore()
	}
	switch n.name {
	case "g", "a", "svg", "switch":
		for _, c := range n.children {
			r.render(c, st, depth+1)
		}
	case "use":
		href := strings.TrimPrefix(a["href"], "#")
		if ref := r.img.ids[href]; ref != nil {
			g.Save()
			g.Transform(1, 0, 0, 1, num("x"), num("y"))
			if ref.name == "symbol" {
				for _, c := range ref.children {
					r.render(c, st, depth+1)
				}
			} else {
				r.render(ref, st, depth+1)
			}
			g.Restore()
		}
	case "path":
		r.paint(st, func() bool { return r.path(a["d"]) })
	case "rect":
		x, y, w, h := num("x"), num("y"), num("width"), num("height")
		rx, ry := num("rx"), num("ry")
		if rx == 0 {
			rx = ry
		}
		if w > 0 && h > 0 {
			r.paint(st, func() bool { g.RoundedRect(x, y, w, h, rx); return true })
		}
	case "circle":
		if rad := num("r"); rad > 0 {
			r.paint(st, func() bool { g.Circle(num("cx"), num("cy"), rad); return true })
		}
	case "ellipse":
		rx, ry := num("rx"), num("ry")
		if rx > 0 && ry > 0 {
			r.paint(st, func() bool {
				g.Save()
				g.Transform(1, 0, 0, ry/rx, num("cx"), num("cy"))
				g.Circle(0, 0, rx)
				g.Restore()
				// 変形を戻した後でパスを塗るため、同じ形状をベジェ曲線で直接描く
				ellipse(g, num("cx"), num("cy"), rx, ry)
				return true
			})
		}
	case "line":
		ls := st
		ls.fill = paint{none: true}
		r.paint(ls, func() bool { g.MoveTo(num("x1"), num("y1")).LineTo(num("x2"), num("y2")); return true })
	case "polyline", "polygon":
		pts := fields(a["points"])
		if len(pts) >= 4 {
			r.paint(st, func() bool {
				g.MoveTo(pts[0], pts[1])
				for i := 2; i+1 < len(pts); i += 2 {
					g.LineTo(pts[i], pts[i+1])
				}
				if n.name == "polygon" {
					g.ClosePath()
				}
				return true
			})
		}
	case "image":
		r.image(n, num("x"), num("y"), num("width"), num("height"))
	case "text", "tspan", "textPath":
		r.warn("text elements are not supported")
	case "foreignObject":
		r.warn("foreignObject elements are not supported")
	}
}

func ellipse(g *pdf.G, cx, cy, rx, ry float64) {
	const k = 0.5522847498
	g.MoveTo(cx+rx, cy)
	g.CurveTo(cx+rx, cy+ry*k, cx+rx*k, cy+ry, cx, cy+ry)
	g.CurveTo(cx-rx*k, cy+ry, cx-rx, cy+ry*k, cx-rx, cy)
	g.CurveTo(cx-rx, cy-ry*k, cx-rx*k, cy-ry, cx, cy-ry)
	g.CurveTo(cx+rx*k, cy-ry, cx+rx, cy-ry*k, cx+rx, cy)
	g.ClosePath()
}

// paint はパスを構築する build を呼び、スタイルに従って塗り・線を適用します。
func (r *renderer) paint(st style, build func() bool) {
	fill := !st.fill.none && st.fillOpacity > 0
	stroke := !st.stroke.none && st.strokeWidth > 0 && st.strokeOpacity > 0
	if !fill && !stroke {
		return
	}
	g := r.g
	g.Save()
	if st.fillOpacity < 1 || st.strokeOpacity < 1 {
		g.GState(r.doc.Opacity(st.fillOpacity, st.strokeOpacity))
	}
	if fill {
		g.FillColor(st.fill.color)
	}
	if stroke {
		g.StrokeColor(st.stroke.color).LineWidth(st.strokeWidth).LineCap(st.lineCap)
		g.Raw(fmt.Sprintf("%d j %s M", st.lineJoin, strconv.FormatFloat(st.miter, 'f', -1, 64)))
		if len(st.dash) > 0 {
			parts := make([]string, len(st.dash))
			for i, d := range st.dash {
				parts[i] = strconv.FormatFloat(d, 'f', -1, 64)
			}
			g.Raw("[" + strings.Join(parts, " ") + "] 0 d")
		}
	}
	if build() {
		switch {
		case fill && stroke && st.evenOdd:
			g.Raw("B*")
		case fill && stroke:
			g.FillStroke()
		case fill && st.evenOdd:
			g.Raw("f*")
		case fill:
			g.Fill()
		default:
			g.Stroke()
		}
	}
	g.Restore()
}

func (r *renderer) image(n *node, x, y, w, h float64) {
	href := n.attrs["href"]
	if w <= 0 || h <= 0 || href == "" {
		return
	}
	pimg, ok := r.img.images[href]
	if !ok {
		if strings.HasPrefix(href, "data:") {
			meta, payload, found := strings.Cut(href[5:], ",")
			if found && strings.HasSuffix(meta, ";base64") {
				payload = strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(payload)
				if raw, err := base64.StdEncoding.DecodeString(payload); err == nil {
					pimg, _ = r.doc.AddImage(raw)
				}
			}
		} else if r.img.Loader != nil {
			if raw, err := r.img.Loader(href); err == nil {
				pimg, _ = r.doc.AddImage(raw)
			}
		}
		r.img.images[href] = pimg
	}
	if pimg == nil {
		r.warn("could not load image " + href)
		return
	}
	// 縦横比を保って領域の中央に収める (preserveAspectRatio の既定値)
	iw, ih := float64(pimg.W), float64(pimg.H)
	if n.attrs["preserveAspectRatio"] != "none" && iw > 0 && ih > 0 {
		s := math.Min(w/iw, h/ih)
		nw, nh := iw*s, ih*s
		x, y, w, h = x+(w-nw)/2, y+(h-nh)/2, nw, nh
	}
	g := r.g
	g.Save()
	// y 軸が反転しているので画像は上下を戻して描く
	g.Transform(1, 0, 0, -1, 0, 2*y+h)
	g.Image(pimg, x, y, w, h)
	g.Restore()
}

// path は d 属性のパスデータを PDF のパスに変換します。
func (r *renderer) path(d string) bool {
	g := r.g
	p := &pathParser{s: d}
	var cx, cy, sx, sy float64
	var lastCtrlX, lastCtrlY float64
	var lastCmd byte
	started := false
	var cmd byte
	for {
		p.skip()
		if p.pos >= len(p.s) {
			break
		}
		if c := p.s[p.pos]; (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && c != 'e' && c != 'E' {
			cmd = c
			p.pos++
		} else if cmd == 0 {
			break
		} else if cmd == 'M' {
			cmd = 'L'
		} else if cmd == 'm' {
			cmd = 'l'
		}
		rel := cmd >= 'a' && cmd <= 'z'
		off := func(v, base float64) float64 {
			if rel {
				return v + base
			}
			return v
		}
		ok := true
		num := func() float64 {
			v, good := p.number()
			if !good {
				ok = false
			}
			return v
		}
		switch cmd {
		case 'M', 'm':
			x, y := num(), num()
			if !ok {
				return started
			}
			cx, cy = off(x, cx), off(y, cy)
			sx, sy = cx, cy
			g.MoveTo(cx, cy)
			started = true
		case 'L', 'l':
			x, y := num(), num()
			if !ok {
				return started
			}
			cx, cy = off(x, cx), off(y, cy)
			g.LineTo(cx, cy)
		case 'H', 'h':
			x := num()
			if !ok {
				return started
			}
			cx = off(x, cx)
			g.LineTo(cx, cy)
		case 'V', 'v':
			y := num()
			if !ok {
				return started
			}
			cy = off(y, cy)
			g.LineTo(cx, cy)
		case 'C', 'c':
			x1, y1, x2, y2, x, y := num(), num(), num(), num(), num(), num()
			if !ok {
				return started
			}
			x1, y1, x2, y2 = off(x1, cx), off(y1, cy), off(x2, cx), off(y2, cy)
			cx, cy = off(x, cx), off(y, cy)
			g.CurveTo(x1, y1, x2, y2, cx, cy)
			lastCtrlX, lastCtrlY = x2, y2
		case 'S', 's':
			x2, y2, x, y := num(), num(), num(), num()
			if !ok {
				return started
			}
			x1, y1 := cx, cy
			if lastCmd == 'C' || lastCmd == 'c' || lastCmd == 'S' || lastCmd == 's' {
				x1, y1 = 2*cx-lastCtrlX, 2*cy-lastCtrlY
			}
			x2, y2 = off(x2, cx), off(y2, cy)
			cx, cy = off(x, cx), off(y, cy)
			g.CurveTo(x1, y1, x2, y2, cx, cy)
			lastCtrlX, lastCtrlY = x2, y2
		case 'Q', 'q', 'T', 't':
			var qx, qy float64
			if cmd == 'Q' || cmd == 'q' {
				qx, qy = num(), num()
				qx, qy = off(qx, cx), off(qy, cy)
			} else if lastCmd == 'Q' || lastCmd == 'q' || lastCmd == 'T' || lastCmd == 't' {
				qx, qy = 2*cx-lastCtrlX, 2*cy-lastCtrlY
			} else {
				qx, qy = cx, cy
			}
			x, y := num(), num()
			if !ok {
				return started
			}
			x, y = off(x, cx), off(y, cy)
			// 2 次ベジェ曲線を 3 次に変換する
			g.CurveTo(cx+2.0/3*(qx-cx), cy+2.0/3*(qy-cy), x+2.0/3*(qx-x), y+2.0/3*(qy-y), x, y)
			cx, cy = x, y
			lastCtrlX, lastCtrlY = qx, qy
		case 'A', 'a':
			rx, ry, rot := num(), num(), num()
			large, sweep := p.flag(), p.flag()
			x, y := num(), num()
			if !ok {
				return started
			}
			x, y = off(x, cx), off(y, cy)
			arc(g, cx, cy, rx, ry, rot, large, sweep, x, y)
			cx, cy = x, y
		case 'Z', 'z':
			g.ClosePath()
			cx, cy = sx, sy
		default:
			return started
		}
		lastCmd = cmd
	}
	return started
}

type pathParser struct {
	s   string
	pos int
}

func (p *pathParser) skip() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', ',', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *pathParser) number() (float64, bool) {
	p.skip()
	start := p.pos
	if p.pos < len(p.s) && (p.s[p.pos] == '-' || p.s[p.pos] == '+') {
		p.pos++
	}
	dot := false
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c >= '0' && c <= '9' {
			p.pos++
		} else if c == '.' && !dot {
			dot = true
			p.pos++
		} else if (c == 'e' || c == 'E') && p.pos > start {
			p.pos++
			if p.pos < len(p.s) && (p.s[p.pos] == '-' || p.s[p.pos] == '+') {
				p.pos++
			}
		} else {
			break
		}
	}
	v, err := strconv.ParseFloat(p.s[start:p.pos], 64)
	return v, err == nil
}

func (p *pathParser) flag() bool {
	p.skip()
	if p.pos < len(p.s) && (p.s[p.pos] == '0' || p.s[p.pos] == '1') {
		p.pos++
		return p.s[p.pos-1] == '1'
	}
	return false
}

// arc は楕円弧を 3 次ベジェ曲線に分割して描きます。
func arc(g *pdf.G, x1, y1, rx, ry, rotDeg float64, large, sweep bool, x2, y2 float64) {
	if rx == 0 || ry == 0 {
		g.LineTo(x2, y2)
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := rotDeg * math.Pi / 180
	cosPhi, sinPhi := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p := cosPhi*dx + sinPhi*dy
	y1p := -sinPhi*dx + cosPhi*dy
	if lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}
	numer := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	denom := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := 0.0
	if denom != 0 && numer > 0 {
		coef = math.Sqrt(numer / denom)
	}
	if large == sweep {
		coef = -coef
	}
	cxp, cyp := coef*rx*y1p/ry, -coef*ry*x1p/rx
	cx := cosPhi*cxp - sinPhi*cyp + (x1+x2)/2
	cy := sinPhi*cxp + cosPhi*cyp + (y1+y2)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	theta := angle(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	delta := angle((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	} else if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	segments := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	if segments < 1 {
		segments = 1
	}
	step := delta / float64(segments)
	t := 4.0 / 3 * math.Tan(step/4)
	point := func(a float64) (float64, float64) {
		return cx + rx*math.Cos(a)*cosPhi - ry*math.Sin(a)*sinPhi, cy + rx*math.Cos(a)*sinPhi + ry*math.Sin(a)*cosPhi
	}
	deriv := func(a float64) (float64, float64) {
		return -rx*math.Sin(a)*cosPhi - ry*math.Cos(a)*sinPhi, -rx*math.Sin(a)*sinPhi + ry*math.Cos(a)*cosPhi
	}
	for i := 0; i < segments; i++ {
		a0, a1 := theta+float64(i)*step, theta+float64(i+1)*step
		p0x, p0y := point(a0)
		p1x, p1y := point(a1)
		d0x, d0y := deriv(a0)
		d1x, d1y := deriv(a1)
		g.CurveTo(p0x+t*d0x, p0y+t*d0y, p1x-t*d1x, p1y-t*d1y, p1x, p1y)
	}
}
