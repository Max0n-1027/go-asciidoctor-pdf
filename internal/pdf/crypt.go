package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
)

// decrypter は標準セキュリティハンドラーで暗号化された PDF を空のユーザーパスワードで復号します。
// (pdf-reader と同じく、閲覧にパスワードが不要な「権限の制限のみ」の PDF を読むためのものです)
type decrypter struct {
	key       []byte
	rev       int
	stmMethod string // "RC4" "AESV2" "AESV3" "None"
	strMethod string
	encMeta   bool
	encNum    int
}

var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

func (r *Reader) bytesOf(v any) []byte {
	if s, ok := r.resolve(v).(String); ok {
		return s
	}
	return nil
}

func newDecrypter(r *Reader, enc Dict, encNum int) (*decrypter, error) {
	if f, _ := r.resolve(enc["Filter"]).(Name); f != "Standard" {
		return nil, fmt.Errorf("unsupported security handler %q", string(f))
	}
	v := r.intOf(enc["V"], 0)
	rev := r.intOf(enc["R"], 0)
	dc := &decrypter{rev: rev, encMeta: true, encNum: encNum, stmMethod: "RC4", strMethod: "RC4"}
	if b, ok := r.resolve(enc["EncryptMetadata"]).(bool); ok {
		dc.encMeta = b
	}
	length := r.intOf(enc["Length"], 40)
	switch v {
	case 1:
		length = 40
	case 2, 3:
	case 4, 5:
		cf := r.dict(enc["CF"])
		method := func(key Name) (string, int) {
			name, _ := r.resolve(enc[key]).(Name)
			if name == "" || name == "Identity" {
				return "None", 0
			}
			d := r.dict(cf[name])
			cfm, _ := r.resolve(d["CFM"]).(Name)
			n := r.intOf(d["Length"], 0)
			switch cfm {
			case "V2":
				return "RC4", n
			case "AESV2", "AESV3":
				return string(cfm), n
			}
			return "None", n
		}
		var n int
		dc.stmMethod, n = method("StmF")
		dc.strMethod, _ = method("StrF")
		if v == 4 {
			length = 128
			if n > 0 {
				if n <= 32 {
					length = n * 8
				} else {
					length = n
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported encryption algorithm V=%d", v)
	}
	if rev >= 5 {
		key, err := aes256FileKey(r.bytesOf(enc["U"]), r.bytesOf(enc["UE"]), rev)
		if err != nil {
			return nil, err
		}
		dc.key = key
		return dc, nil
	}
	o := r.bytesOf(enc["O"])
	u := r.bytesOf(enc["U"])
	if len(o) < 32 || len(u) < 16 {
		return nil, errors.New("invalid encryption dictionary")
	}
	var id0 []byte
	if ids, ok := r.resolve(r.trailer["ID"]).(Array); ok && len(ids) > 0 {
		id0 = r.bytesOf(ids[0])
	}
	p := uint32(int32(r.intOf(enc["P"], 0)))
	h := md5.New()
	h.Write(passwordPad)
	h.Write(o[:32])
	h.Write([]byte{byte(p), byte(p >> 8), byte(p >> 16), byte(p >> 24)})
	h.Write(id0)
	if rev >= 4 && !dc.encMeta {
		h.Write([]byte{0xff, 0xff, 0xff, 0xff})
	}
	key := h.Sum(nil)
	n := length / 8
	if rev == 2 {
		n = 5
	}
	if n < 5 {
		n = 5
	}
	if n > 16 {
		n = 16
	}
	if rev >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:n])
			key = s[:]
		}
	}
	key = key[:n]
	// 空のユーザーパスワードで開けるかを確認する
	if rev == 2 {
		if !bytes.Equal(rc4Crypt(key, passwordPad), u[:32]) {
			return nil, ErrEncrypted
		}
	} else {
		s := md5.Sum(append(append([]byte{}, passwordPad...), id0...))
		x := rc4Crypt(key, s[:])
		for i := 1; i <= 19; i++ {
			k := make([]byte, len(key))
			for j := range key {
				k[j] = key[j] ^ byte(i)
			}
			x = rc4Crypt(k, x)
		}
		if !bytes.Equal(x[:16], u[:16]) {
			return nil, ErrEncrypted
		}
	}
	dc.key = key
	return dc, nil
}

func rc4Crypt(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return nil
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// aes256FileKey は AES-256 (R5 / R6) のファイル鍵を空のユーザーパスワードから求めます。
func aes256FileKey(u, ue []byte, rev int) ([]byte, error) {
	if len(u) < 48 || len(ue) < 32 {
		return nil, errors.New("invalid encryption dictionary")
	}
	hash := func(salt []byte) []byte {
		if rev == 5 {
			s := sha256.Sum256(salt)
			return s[:]
		}
		return hash2B(nil, salt, nil)
	}
	if !bytes.Equal(hash(u[32:40]), u[:32]) {
		return nil, ErrEncrypted
	}
	ik := hash(u[40:48])
	block, err := aes.NewCipher(ik[:32])
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	cipher.NewCBCDecrypter(block, make([]byte, 16)).CryptBlocks(key, ue[:32])
	return key, nil
}

// hash2B は PDF 2.0 (ISO 32000-2) の鍵導出アルゴリズム 2.B です。
func hash2B(pw, salt, udata []byte) []byte {
	s := sha256.Sum256(append(append(append([]byte{}, pw...), salt...), udata...))
	k := s[:]
	for round := 0; round < 1000; round++ {
		unit := append(append(append([]byte{}, pw...), k...), udata...)
		k1 := bytes.Repeat(unit, 64)
		for len(k1)%16 != 0 {
			k1 = append(k1, 0)
		}
		block, _ := aes.NewCipher(k[:16])
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1)
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			h := sha256.Sum256(e)
			k = h[:]
		case 1:
			h := sha512.Sum384(e)
			k = h[:]
		default:
			h := sha512.Sum512(e)
			k = h[:]
		}
		if round >= 63 && int(e[len(e)-1]) <= round-31 {
			break
		}
	}
	return k[:32]
}

func (dc *decrypter) objectKey(num, gen int, aesSalt bool) []byte {
	if dc.rev >= 5 {
		return dc.key
	}
	b := append(append([]byte{}, dc.key...), byte(num), byte(num>>8), byte(num>>16), byte(gen), byte(gen>>8))
	if aesSalt {
		b = append(b, "sAlT"...)
	}
	s := md5.Sum(b)
	n := len(dc.key) + 5
	if n > 16 {
		n = 16
	}
	return s[:n]
}

func (dc *decrypter) decrypt(data []byte, num, gen int, method string) []byte {
	switch method {
	case "RC4":
		return rc4Crypt(dc.objectKey(num, gen, false), data)
	case "AESV2", "AESV3":
		if len(data) < 32 {
			return []byte{}
		}
		block, err := aes.NewCipher(dc.objectKey(num, gen, true))
		if err != nil {
			return data
		}
		body := data[16:]
		body = body[:len(body)-len(body)%16]
		out := make([]byte, len(body))
		cipher.NewCBCDecrypter(block, data[:16]).CryptBlocks(out, body)
		if n := len(out); n > 0 {
			if pad := int(out[n-1]); pad >= 1 && pad <= 16 && pad <= n {
				out = out[:n-pad]
			}
		}
		return out
	}
	return data
}

// object はオブジェクト内の文字列とストリームを復号します。
func (dc *decrypter) object(v any, num, gen int) any {
	switch x := v.(type) {
	case String:
		return String(dc.decrypt(x, num, gen, dc.strMethod))
	case Array:
		out := make(Array, len(x))
		for i, e := range x {
			out[i] = dc.object(e, num, gen)
		}
		return out
	case Dict:
		out := Dict{}
		for k, e := range x {
			out[k] = dc.object(e, num, gen)
		}
		return out
	case *Stream:
		d := dc.object(x.Dict, num, gen).(Dict)
		data := x.Data
		skip := d["Type"] == Name("XRef") || (d["Type"] == Name("Metadata") && !dc.encMeta)
		switch f := d["Filter"].(type) {
		case Name:
			skip = skip || f == "Crypt"
		case Array:
			for _, e := range f {
				if e == Name("Crypt") {
					skip = true
				}
			}
		}
		if !skip {
			data = dc.decrypt(data, num, gen, dc.stmMethod)
		}
		return &Stream{Dict: d, Data: data}
	}
	return v
}
