// Package encodingmod is the native module "encoding": bytes as base64 or
// hexadecimal text, and back (TODO.md Q113, docs/future.md §11.3).
//
//	use encoding;
//	println(encoding::base64("Kukel"));          // S3VrZWw=
//	println(encoding::unbase64("S3VrZWw="));     // Kukel
//	println(encoding::hex("\x01\xff"));          // 01ff
//
// It is a module of its own, not part of strings, so that a Go program
// that embeds Kukel can allow it apart. It is in Go because a Kukel loop
// over the bytes of an attachment of megabytes would be slow.
package encodingmod

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "encoding",
		Doc:  "base64 and hexadecimal",
		Funcs: map[string]native.Func{
			"base64":   encode64,
			"unbase64": decode64,
			"hex":      encodeHex,
			"unhex":    decodeHex,
		},
	})
}

// encoding::base64(s [, width]): the bytes of s in base64 (RFC 4648, the
// standard alphabet, with = padding); with width, broken into lines of
// that many characters, joined by "\n" (MIME uses 76).
func encode64(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("encoding::base64", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	width := int64(0)
	if a.Has(1) && !a.V[1].IsNil() {
		if width, err = a.Int(1); err != nil {
			return value.NilValue, err
		}
		if width < 1 {
			return value.NilValue, a.Errorf(value.ValueError, "a line width of %d (1 or more)", width)
		}
	}
	text := base64.StdEncoding.EncodeToString([]byte(s))
	if width == 0 || int64(len(text)) <= width {
		return value.MakeString(text), nil
	}
	var b strings.Builder
	b.Grow(len(text) + len(text)/int(width))
	for i := 0; i < len(text); i += int(width) {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text[i:min(i+int(width), len(text))])
	}
	return value.MakeString(b.String()), nil
}

// isSpace is what unbase64 skips: the line breaks and indents of base64
// in mail and PEM files.
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// encoding::unbase64(s): the bytes base64 text s stands for. Spaces, tabs
// and line breaks are skipped; the = padding may be left out. Other text
// is a ValueError naming the byte (counted from 1).
func decode64(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("encoding::unbase64", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	// The text without spaces, and where each of its bytes was in s.
	clean := make([]byte, 0, len(s))
	at := make([]int, 0, len(s))
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			clean = append(clean, s[i])
			at = append(at, i)
		}
	}
	switch len(clean) % 4 {
	case 1:
		return value.NilValue, a.Errorf(value.ValueError, "invalid base64: the text ends in the middle of a byte")
	case 2:
		if !strings.HasSuffix(string(clean), "=") {
			clean = append(clean, '=', '=')
		}
	case 3:
		if !strings.HasSuffix(string(clean), "=") {
			clean = append(clean, '=')
		}
	}
	out := make([]byte, base64.StdEncoding.DecodedLen(len(clean)))
	n, err := base64.StdEncoding.Decode(out, clean)
	if err != nil {
		var ce base64.CorruptInputError
		if errors.As(err, &ce) {
			off := int(ce)
			if off >= len(at) {
				return value.NilValue, a.Errorf(value.ValueError, "invalid base64: the padding at the end is wrong")
			}
			return value.NilValue, a.Errorf(value.ValueError, "invalid base64 at byte %d: %s", at[off]+1, shown(s[at[off]]))
		}
		return value.NilValue, a.Errorf(value.ValueError, "invalid base64: %v", err)
	}
	return value.MakeString(string(out[:n])), nil
}

// shown names a byte in a message: 'x', or 0x01 for one that is not
// printable.
func shown(c byte) string {
	if c >= 0x20 && c < 0x7f && c != '\'' {
		return "the character '" + string(c) + "'"
	}
	return "the byte 0x" + hex.EncodeToString([]byte{c})
}

// encoding::hex(s): the bytes of s as hexadecimal digits, two a byte, in
// lower case.
func encodeHex(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("encoding::hex", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	return value.MakeString(hex.EncodeToString([]byte(s))), nil
}

// encoding::unhex(s): the bytes hexadecimal text s stands for (upper or
// lower case); an odd number of digits, or anything else, is a
// ValueError naming the byte.
func decodeHex(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("encoding::unhex", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return value.NilValue, a.Errorf(value.ValueError, "invalid hexadecimal at byte %d: %s", i+1, shown(c))
		}
	}
	if len(s)%2 != 0 {
		return value.NilValue, a.Errorf(value.ValueError, "invalid hexadecimal: an odd number of digits (%d)", len(s))
	}
	out, _ := hex.DecodeString(s)
	return value.MakeString(string(out)), nil
}
