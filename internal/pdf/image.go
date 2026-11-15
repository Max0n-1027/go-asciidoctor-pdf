package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // GIF デコーダ
	"image/png"
)

// Image は PDF に埋め込む画像です。
type Image struct {
	W, H int

	dict    string
	data    []byte
	smask   []byte
	resName string
	objID   int
}

// Name は内容ストリームから参照するリソース名を返します。
func (i *Image) Name() string { return i.resName }

// AddImage は画像データ (PNG / JPEG / GIF) を文書に登録します。
func (d *Document) AddImage(data []byte) (*Image, error) {
	var img *Image
	var err error
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
		img, err = jpegImage(data)
	case len(data) > 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		var src image.Image
		if src, err = png.Decode(bytes.NewReader(data)); err == nil {
			img = rasterImage(src)
		}
	case len(data) > 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		var src image.Image
		if src, _, err = image.Decode(bytes.NewReader(data)); err == nil {
			img = rasterImage(src)
		}
	default:
		err = errors.New("unsupported image format")
	}
	if err != nil {
		return nil, err
	}
	d.images = append(d.images, img)
	img.resName = fmt.Sprintf("I%d", len(d.images))
	return img, nil
}

// ImageSize は画像を登録せずにピクセル寸法だけを返します。
func ImageSize(data []byte) (int, int, error) {
	if len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 {
		img, err := jpegImage(data)
		if err != nil {
			return 0, 0, err
		}
		return img.W, img.H, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func jpegImage(data []byte) (*Image, error) {
	p := 2
	adobe := false
	for p+4 <= len(data) {
		if data[p] != 0xFF {
			return nil, errors.New("invalid JPEG marker")
		}
		marker := data[p+1]
		if marker == 0xFF {
			p++
			continue
		}
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			p += 2
			continue
		}
		length := int(data[p+2])<<8 | int(data[p+3])
		if marker == 0xEE && p+4+5 <= len(data) && string(data[p+4:p+9]) == "Adobe" {
			adobe = true
		}
		if (marker >= 0xC0 && marker <= 0xCF) && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
			if p+10 > len(data) {
				break
			}
			bits := int(data[p+4])
			h := int(data[p+5])<<8 | int(data[p+6])
			w := int(data[p+7])<<8 | int(data[p+8])
			comps := int(data[p+9])
			cs := "/DeviceRGB"
			decode := ""
			switch comps {
			case 1:
				cs = "/DeviceGray"
			case 4:
				cs = "/DeviceCMYK"
				if adobe {
					decode = " /Decode [1 0 1 0 1 0 1 0]"
				}
			}
			return &Image{W: w, H: h, data: data,
				dict: fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent %d /Filter /DCTDecode%s", w, h, cs, bits, decode)}, nil
		}
		p += 2 + length
	}
	return nil, errors.New("could not determine JPEG dimensions")
}

func rasterImage(src image.Image) *Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	gray := false
	switch src.(type) {
	case *image.Gray, *image.Gray16:
		gray = true
	}
	comps := 3
	if gray {
		comps = 1
	}
	pix := make([]byte, 0, w*h*comps)
	alpha := make([]byte, 0, w*h)
	hasAlpha := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			if gray {
				pix = append(pix, c.R)
			} else {
				pix = append(pix, c.R, c.G, c.B)
			}
			alpha = append(alpha, c.A)
			if c.A != 255 {
				hasAlpha = true
			}
		}
	}
	cs := "/DeviceRGB"
	if gray {
		cs = "/DeviceGray"
	}
	img := &Image{W: w, H: h, data: deflate(pix),
		dict: fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /FlateDecode", w, h, cs)}
	if hasAlpha {
		img.smask = deflate(alpha)
	}
	return img
}

func (d *Document) writeImage(w *writer, img *Image) int {
	dict := img.dict
	if img.smask != nil {
		id := w.alloc()
		w.objs[id-1] = rawStream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", img.W, img.H), img.smask)
		dict += fmt.Sprintf(" /SMask %d 0 R", id)
	}
	id := w.alloc()
	w.objs[id-1] = rawStream(dict, img.data)
	return id
}

func rawStream(dict string, data []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< %s /Length %d >>\nstream\n", dict, len(data))
	b.Write(data)
	b.WriteString("\nendstream")
	return b.Bytes()
}
