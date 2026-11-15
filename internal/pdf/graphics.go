package pdf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Color は RGB もしくは CMYK の色です。
type Color struct {
	CMYK  bool
	V     [4]float64 // RGB は 0..1 の 3 要素、CMYK は 0..1 の 4 要素
	IsSet bool
}

// ParseColor は "RRGGBB" 形式の 16 進表記から色を生成します。
func ParseColor(hex string) Color {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return Color{}
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return Color{}
	}
	return Color{IsSet: true, V: [4]float64{float64(v>>16&0xFF) / 255, float64(v>>8&0xFF) / 255, float64(v&0xFF) / 255}}
}

// CMYKColor は 0..100 の CMYK 値から色を生成します。
func CMYKColor(c, m, y, k float64) Color {
	return Color{IsSet: true, CMYK: true, V: [4]float64{c / 100, m / 100, y / 100, k / 100}}
}

func (c Color) fillOp() string {
	if c.CMYK {
		return fmt.Sprintf("%s %s %s %s k", num(c.V[0]), num(c.V[1]), num(c.V[2]), num(c.V[3]))
	}
	return fmt.Sprintf("%s %s %s rg", num(c.V[0]), num(c.V[1]), num(c.V[2]))
}

func (c Color) strokeOp() string {
	if c.CMYK {
		return fmt.Sprintf("%s %s %s %s K", num(c.V[0]), num(c.V[1]), num(c.V[2]), num(c.V[3]))
	}
	return fmt.Sprintf("%s %s %s RG", num(c.V[0]), num(c.V[1]), num(c.V[2]))
}

// G は内容ストリームの断片を組み立てるビルダーです。
type G struct {
	b bytes.Buffer
}

// NewG は空のビルダーを返します。
func NewG() *G { return &G{} }

// Bytes は組み立てた内容を返します。
func (g *G) Bytes() []byte { return g.b.Bytes() }

// Len は現在のバイト数を返します。
func (g *G) Len() int { return g.b.Len() }

func (g *G) op(format string, args ...any) *G {
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteByte('\n')
	return g
}

func (g *G) Save() *G    { return g.op("q") }
func (g *G) Restore() *G { return g.op("Q") }

func (g *G) FillColor(c Color) *G   { return g.op("%s", c.fillOp()) }
func (g *G) StrokeColor(c Color) *G { return g.op("%s", c.strokeOp()) }
func (g *G) LineWidth(w float64) *G { return g.op("%s w", num(w)) }

// Dash は破線パターンを設定します。on が 0 以下なら実線に戻します。
func (g *G) Dash(on, off float64) *G {
	if on <= 0 {
		return g.op("[] 0 d")
	}
	return g.op("[%s %s] 0 d", num(on), num(off))
}

// LineCap は線端の形状を設定します (0: butt, 1: round, 2: square)。
func (g *G) LineCap(style int) *G { return g.op("%d J", style) }

func (g *G) MoveTo(x, y float64) *G { return g.op("%s %s m", num(x), num(y)) }
func (g *G) LineTo(x, y float64) *G { return g.op("%s %s l", num(x), num(y)) }
func (g *G) CurveTo(x1, y1, x2, y2, x3, y3 float64) *G {
	return g.op("%s %s %s %s %s %s c", num(x1), num(y1), num(x2), num(y2), num(x3), num(y3))
}
func (g *G) ClosePath() *G { return g.op("h") }

// Rect は左下 (x, y) 基準の矩形パスを追加します。
func (g *G) Rect(x, y, w, h float64) *G {
	return g.op("%s %s %s %s re", num(x), num(y), num(w), num(h))
}

// RoundedRect は角丸矩形のパスを追加します (x, y は左下)。
func (g *G) RoundedRect(x, y, w, h, r float64) *G {
	if r <= 0 {
		return g.Rect(x, y, w, h)
	}
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	const k = 0.5522847498
	c := r * k
	g.MoveTo(x+r, y)
	g.LineTo(x+w-r, y)
	g.CurveTo(x+w-r+c, y, x+w, y+r-c, x+w, y+r)
	g.LineTo(x+w, y+h-r)
	g.CurveTo(x+w, y+h-r+c, x+w-r+c, y+h, x+w-r, y+h)
	g.LineTo(x+r, y+h)
	g.CurveTo(x+r-c, y+h, x, y+h-r+c, x, y+h-r)
	g.LineTo(x, y+r)
	g.CurveTo(x, y+r-c, x+r-c, y, x+r, y)
	return g.ClosePath()
}

// Circle は円のパスを追加します。
func (g *G) Circle(cx, cy, r float64) *G {
	const k = 0.5522847498
	c := r * k
	g.MoveTo(cx+r, cy)
	g.CurveTo(cx+r, cy+c, cx+c, cy+r, cx, cy+r)
	g.CurveTo(cx-c, cy+r, cx-r, cy+c, cx-r, cy)
	g.CurveTo(cx-r, cy-c, cx-c, cy-r, cx, cy-r)
	g.CurveTo(cx+c, cy-r, cx+r, cy-c, cx+r, cy)
	return g.ClosePath()
}

func (g *G) Fill() *G       { return g.op("f") }
func (g *G) Stroke() *G     { return g.op("S") }
func (g *G) FillStroke() *G { return g.op("B") }
func (g *G) Clip() *G       { return g.op("W n") }

// Transform は現在の変換行列に行列を連結します。
func (g *G) Transform(a, b, c, d, e, f float64) *G {
	return g.op("%s %s %s %s %s %s cm", num(a), num(b), num(c), num(d), num(e), num(f))
}

// Raw は任意の演算子列をそのまま追加します。
func (g *G) Raw(ops string) *G { return g.op("%s", ops) }

// GState は ExtGState リソースを適用します。
func (g *G) GState(name string) *G { return g.op("/%s gs", name) }

// Image は画像を左下 (x, y)、幅 w、高さ h で描画します。
func (g *G) Image(img *Image, x, y, w, h float64) *G {
	g.Save()
	g.Transform(w, 0, 0, h, x, y)
	g.op("/%s Do", img.resName)
	return g.Restore()
}

// Glyph はテキスト描画時の 1 グリフです。
type Glyph struct {
	ID uint16
	// Adjust はこのグリフの後ろに加える追加の送り量 (pt)。負値で詰めます。
	Adjust float64
}

// TextRun は 1 つのフォント・サイズで描画するグリフ列です。
type TextRun struct {
	Font   *Font
	Size   float64
	Color  Color
	Glyphs []Glyph
	// Mode はテキスト描画モード (0: 塗り, 1: 線, 2: 塗り+線, 3: 不可視)
	Mode        int
	StrokeWidth float64
	StrokeColor Color
	// Skew は合成イタリック用の傾き (0 なら無効)
	Skew float64
}

// Text はベースライン左端 (x, y) からテキストを描画します。
func (g *G) Text(x, y float64, run TextRun) *G {
	if len(run.Glyphs) == 0 {
		return g
	}
	g.op("BT")
	g.op("/%s %s Tf", run.Font.resName, num(run.Size))
	g.op("%s", run.Color.fillOp())
	if run.Mode != 0 {
		g.op("%d Tr", run.Mode)
		if run.Mode == 1 || run.Mode == 2 {
			g.op("%s w", num(run.StrokeWidth))
			sc := run.StrokeColor
			if !sc.IsSet {
				sc = run.Color
			}
			g.op("%s", sc.strokeOp())
		}
	}
	if run.Skew != 0 {
		g.op("1 0 %s 1 %s %s Tm", num(run.Skew), num(x), num(y))
	} else {
		g.op("1 0 0 1 %s %s Tm", num(x), num(y))
	}
	g.b.WriteString("[<")
	for i, gl := range run.Glyphs {
		fmt.Fprintf(&g.b, "%04x", gl.ID)
		if gl.Adjust != 0 && i < len(run.Glyphs)-1 {
			// TJ の調整値は 1/1000 em 単位で、正の値が左方向への移動
			fmt.Fprintf(&g.b, "> %s <", num(-gl.Adjust*1000/run.Size))
		}
	}
	g.b.WriteString(">] TJ\n")
	if run.Mode != 0 {
		g.op("0 Tr")
	}
	return g.op("ET")
}
