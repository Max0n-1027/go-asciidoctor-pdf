package converter

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/asciidoc"
	"github.com/Max0n-1027/go-asciidoctor-pdf/internal/data"
)

var iconSets = []string{"fab", "far", "fas", "fi", "pf"}
var fontAwesomeIconSets = []string{"fab", "far", "fas"}

var iconLegends = map[string]map[string]string{}
var legacyIconMapping map[string]string

// iconGlyph はアイコンセット内の名前に対応する文字を返します。
func iconGlyph(set, name string) (string, bool) {
	legend, loaded := iconLegends[set]
	if !loaded {
		legend = map[string]string{}
		if raw, ok := data.IconLegend(set); ok {
			var doc map[string]map[string]string
			if err := yaml.Unmarshal(raw, &doc); err == nil {
				legend = doc[set]
			}
		}
		iconLegends[set] = legend
	}
	if name == "__font_version__" {
		return "", false
	}
	glyph, ok := legend[name]
	return glyph, ok
}

func isIconSet(set string) bool {
	for _, s := range iconSets {
		if s == set {
			return true
		}
	}
	return false
}

// resolveLegacyIconName は Font Awesome 4 以前の名前を新しいセット付きの名前に変換します。
func resolveLegacyIconName(name string) (string, bool) {
	if legacyIconMapping == nil {
		legacyIconMapping = map[string]string{}
		if raw, ok := data.Font("fa-legacy-mapping.yml"); ok {
			_ = yaml.Unmarshal(raw, &legacyIconMapping)
		}
	}
	v, ok := legacyIconMapping["fa-"+name]
	return v, ok
}

// validIconSetPrefix は "fas-fire" のようにセット名の接頭辞が付いた名前かどうかを返します。
func validIconSetPrefix(name string) (string, bool) {
	i := strings.Index(name, "-")
	if i < 0 || i > 3 {
		return "", false
	}
	set := name[:i]
	if set == "fa" || !isIconSet(set) {
		return "", false
	}
	return set, true
}

// resolveIcon は "fas-fire" や旧式の "fire" からセットと文字を解決します。
func resolveIcon(name string) (set, glyph string, ok bool) {
	if s, valid := validIconSetPrefix(name); valid {
		if g, found := iconGlyph(s, name[len(s)+1:]); found {
			return s, g, true
		}
		return "", "", false
	}
	name = strings.TrimPrefix(name, "fa-")
	if remapped, found := resolveLegacyIconName(name); found {
		s, n, _ := strings.Cut(remapped, "-")
		if g, found := iconGlyph(s, n); found {
			return s, g, true
		}
	}
	for _, s := range fontAwesomeIconSets {
		if g, found := iconGlyph(s, name); found {
			return s, g, true
		}
	}
	return "", "", false
}

type admonitionIcon struct {
	name   string
	color  clr
	size   float64
	scale  float64
	hasClr bool
}

var defaultAdmonitionIcons = map[string]admonitionIcon{
	"caution":   {name: "fas-fire", color: hexClr("BF3400"), hasClr: true},
	"important": {name: "fas-exclamation-circle", color: hexClr("BF0000"), hasClr: true},
	"note":      {name: "fas-info-circle", color: hexClr("19407C"), hasClr: true},
	"tip":       {name: "far-lightbulb", color: hexClr("111111"), hasClr: true},
	"warning":   {name: "fas-exclamation-triangle", color: hexClr("BF6900"), hasClr: true},
}

func (c *Converter) admonitionIconData(typ string) admonitionIcon {
	icon, known := defaultAdmonitionIcons[typ]
	if !known {
		icon = admonitionIcon{}
	}
	icon.size, icon.scale = 36, 2.0/3
	if custom, ok := c.theme.Get("admonition_icon_" + typ).(map[string]any); ok {
		if v, ok := custom["name"].(string); ok && v != "" {
			icon.name = v
			if _, valid := validIconSetPrefix(v); !valid && !strings.HasPrefix(v, "fa-") {
				icon.name = "fa-" + v
			}
		}
		if v, ok := custom["stroke_color"]; ok {
			icon.color, icon.hasClr = toClr(v), toClr(v).set
		}
		if v, ok := custom["size"]; ok {
			icon.size = toF(v)
		}
		if v, ok := custom["scale"]; ok {
			icon.scale = toF(v)
		}
	}
	if icon.name == "" {
		icon.name = defaultAdmonitionIcons["note"].name
	}
	return icon
}

func (c *Converter) inlineIcon(n *asciidoc.Inline) string {
	doc := n.Doc
	alt := n.Attrs["alt"]
	icons, has := doc.Attributes["icons"]
	if !has {
		return "[" + alt + "&#93;"
	}
	if icons != "font" {
		// 画像アイコン
		path := c.resolveIconImagePath(doc, n.Target)
		if c.loadImage(path, n.Target) != nil {
			return `<img src="` + path + `" format="` + doc.AttrOr("icontype", "png") + `" alt="` + encodeQuotes(alt) + `" fit="line">`
		}
		c.logf("WARN", "image icon for '%s' not found or not readable: %s", n.Target, path)
		return "[" + n.Target + "&#93;"
	}
	name := n.Target
	set, explicit := "", false
	if i := strings.Index(name, "@"); i >= 0 {
		name, set, explicit = name[:i], name[i+1:], true
	} else if v, ok := n.Attrs["set"]; ok {
		set, explicit = v, true
	} else {
		set = doc.AttrOr("icon-set", "fa")
	}
	glyph, found := "", false
	if set == "fa" || !isIconSet(set) {
		set = "fa"
		if remapped, ok := resolveLegacyIconName(name); ok {
			var newName string
			set, newName, _ = strings.Cut(remapped, "-")
			glyph, found = iconGlyph(set, newName)
		} else {
			for _, cand := range fontAwesomeIconSets {
				if g, ok := iconGlyph(cand, name); ok {
					set, glyph, found = cand, g, true
					break
				}
			}
		}
	} else {
		glyph, found = iconGlyph(set, name)
	}
	if !found && !explicit {
		if s, valid := validIconSetPrefix(name); valid {
			set = s
			glyph, found = iconGlyph(s, name[len(s)+1:])
		}
	}
	if !found {
		c.logf("WARN", "%s is not a valid icon name in the %s icon set", name, set)
		return "[" + alt + "&#93;"
	}
	sizeAttr := ""
	if size, ok := n.Attrs["size"]; ok {
		switch size {
		case "lg":
			sizeAttr = ` size="1.333em"`
		case "fw":
			sizeAttr = ` width="1em"`
		default:
			sizeAttr = ` size="` + strings.Replace(size, "x", "em", 1) + `"`
		}
	}
	classAttr := ""
	if role := n.Role(); role != "" {
		classAttr = ` class="` + role + `"`
	}
	return `<font name="` + set + `"` + sizeAttr + classAttr + ">" + glyph + "</font>"
}

func (c *Converter) resolveIconImagePath(doc *asciidoc.Document, name string) string {
	root := c.rootDoc(doc)
	iconsdir := root.Attr("iconsdir")
	if iconsdir == "" {
		iconsdir = "./images/icons"
		if v := root.Attr("imagesdir"); v != "" {
			iconsdir = v + "/icons"
		}
	}
	return c.resolveImagePath(nil, iconsdir+"/"+name+"."+root.AttrOr("icontype", "png"), false)
}
