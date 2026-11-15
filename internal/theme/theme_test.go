package theme

import (
	"math"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	th, err := Load("", "", func(m string) { t.Log("warn:", m) })
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]float64{
		"base_font_size":                     10.5,
		"base_line_height":                   12 / 10.5,
		"base_font_size_large":               13,
		"base_font_size_small":               9,
		"heading_h1_font_size":               27,
		"heading_h2_font_size":               22,
		"heading_h3_font_size":               18,
		"heading_margin_top":                 4.8,
		"heading_margin_bottom":              10.8,
		"code_font_size":                     11,
		"callout_list_margin_top_after_code": -6,
		"footer_height":                      30,
		"list_indent":                        18,
		"conum_line_height":                  4.0 / 3,
	}
	for k, want := range checks {
		got, ok := th.Num(k)
		if !ok || math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %v (%v), want %v", k, th.Get(k), ok, want)
		}
	}
	if m := th.List("page_margin"); len(m) != 4 || m[0].(float64) != 36 || math.Abs(m[1].(float64)-48.24) > 1e-9 {
		t.Errorf("page_margin = %v", m)
	}
	if got := th.Str("base_font_color"); got != "333333" {
		t.Errorf("base_font_color = %q", got)
	}
	if got := th.Str("footer_verso_left_content"); got != "{page-number}" {
		t.Errorf("footer_verso_left_content = %q", got)
	}
	if got := th.Str("role_big_font_size"); got != "1.2em" {
		t.Errorf("role_big_font_size = %q", got)
	}
	if got := th.Str("title_page_title_top"); got != "55%" {
		t.Errorf("title_page_title_top = %q", got)
	}
	if p := th.List("quote_padding"); len(p) != 4 || p[3].(float64) != 14 {
		t.Errorf("quote_padding = %v", p)
	}
	if fc := th.FontCatalog(); fc["Noto Serif"]["bold_italic"] != "GEM_FONTS_DIR/notoserif-bold_italic-subset.ttf" {
		t.Errorf("font catalog = %v", fc)
	}
}

func TestLoadVariants(t *testing.T) {
	th, err := Load("default-with-font-fallbacks", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fb := th.FontFallbacks(); len(fb) != 2 || fb[0] != "M+ 1p Fallback" {
		t.Errorf("fallbacks = %v", fb)
	}
	if fc := th.FontCatalog(); fc["Noto Serif"] == nil || fc["M+ 1p Fallback"]["italic"] == "" {
		t.Errorf("catalog not merged: %v", fc)
	}
	th, err = Load("default-for-print", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if th.Get("link_font_color") != nil || th.Str("base_font_color") != "000000" || th.Str("heading_font_color") != "000000" {
		t.Errorf("print theme: link=%v base=%v heading=%v", th.Get("link_font_color"), th.Get("base_font_color"), th.Get("heading_font_color"))
	}
	th, err = Load("base", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if th.Str("base_font_family") != "Helvetica" || th.Float("base_font_size", 0) != 12 {
		t.Errorf("base theme: %v %v", th.Get("base_font_family"), th.Get("base_font_size"))
	}
}
