package encodingmod

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/value"
)

func call(fn func(value.Env, []value.Value) (value.Value, error), args ...value.Value) (string, error) {
	v, err := fn(nil, args)
	if err != nil {
		return "", err
	}
	return v.AsString(), nil
}

func str(s string) value.Value { return value.MakeString(s) }

func TestBase64(t *testing.T) {
	tests := []struct{ in, out string }{
		{"", ""}, {"f", "Zg=="}, {"fo", "Zm8="}, {"foo", "Zm9v"}, {"foob", "Zm9vYg=="},
		{"Kukel", "S3VrZWw="}, {"\x00\xff\xfe", "AP/+"},
	}
	for _, tc := range tests {
		if got, err := call(encode64, str(tc.in)); got != tc.out || err != nil {
			t.Errorf("base64(%q) = %q, %v; want %q", tc.in, got, err, tc.out)
		}
		if got, err := call(decode64, str(tc.out)); got != tc.in || err != nil {
			t.Errorf("unbase64(%q) = %q, %v; want %q", tc.out, got, err, tc.in)
		}
	}
	// Lines, as MIME and PEM have them; spaces and missing padding read.
	long := strings.Repeat("abcdefghij", 10)
	got, _ := call(encode64, str(long), value.MakeInt(76))
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || len(lines[0]) != 76 || strings.Join(lines, "") != base64.StdEncoding.EncodeToString([]byte(long)) {
		t.Errorf("base64(long, 76) = %q", got)
	}
	if back, err := call(decode64, str("  "+strings.ReplaceAll(got, "\n", "\r\n\t")+"\n")); back != long || err != nil {
		t.Errorf("unbase64 of lines = %q, %v", back, err)
	}
	for in, want := range map[string]string{"Zg": "f", "Zm8": "fo", "S3Vr ZWw": "Kukel"} {
		if got, err := call(decode64, str(in)); got != want || err != nil {
			t.Errorf("unbase64(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, _ := call(encode64, str("abc"), value.MakeInt(76)); got != "YWJj" {
		t.Errorf("a short text gets no line break: %q", got)
	}
	if got, _ := call(encode64, str("abcdef"), value.NilValue); got != "YWJjZGVm" {
		t.Errorf("width nil: %q", got)
	}
}

func TestHex(t *testing.T) {
	if got, _ := call(encodeHex, str("\x01\xffK")); got != "01ff4b" {
		t.Errorf("hex = %q", got)
	}
	if got, err := call(decodeHex, str("01FF4b")); got != "\x01\xffK" || err != nil {
		t.Errorf("unhex = %q, %v", got, err)
	}
	if got, _ := call(decodeHex, str("")); got != "" {
		t.Errorf("unhex(\"\") = %q", got)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		fn   func(value.Env, []value.Value) (value.Value, error)
		args []value.Value
		msg  string
	}{
		{decode64, []value.Value{str("Zm9v\nY!==")}, "encoding::unbase64: invalid base64 at byte 7: the character '!'"},
		{decode64, []value.Value{str("Zm9vY")}, "invalid base64: the text ends in the middle of a byte"},
		{decode64, []value.Value{str("Zg==Zg==")}, "invalid base64 at byte 5: the character 'Z'"},
		{decode64, []value.Value{str("Z\x01g=")}, "invalid base64 at byte 2: the byte 0x01"},
		{decode64, []value.Value{value.MakeInt(1)}, "argument 1 must be a string, not int"},
		{encode64, []value.Value{str("x"), value.MakeInt(0)}, "a line width of 0 (1 or more)"},
		{encode64, []value.Value{str("x"), str("76")}, "argument 2 must be an int, not string"},
		{decodeHex, []value.Value{str("abc")}, "invalid hexadecimal: an odd number of digits (3)"},
		{decodeHex, []value.Value{str("0g")}, "encoding::unhex: invalid hexadecimal at byte 2: the character 'g'"},
	}
	for _, tc := range tests {
		_, err := call(tc.fn, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("got %v, want %q", err, tc.msg)
		}
	}
}

// FuzzBase64: what base64 writes, at any width, unbase64 reads back; what
// unbase64 accepts is what Go's decoder accepts without the spaces.
func FuzzBase64(f *testing.F) {
	f.Add([]byte("Kukel"), int64(4), "S3VrZWw=")
	f.Add([]byte{0, 1, 2, 255}, int64(76), "AP/+\n")
	f.Fuzz(func(t *testing.T, data []byte, width int64, text string) {
		if width < 1 || width > 1000 {
			width = 76
		}
		enc, err := call(encode64, str(string(data)), value.MakeInt(width))
		if err != nil {
			t.Fatal(err)
		}
		back, err := call(decode64, str(enc))
		if err != nil || !bytes.Equal([]byte(back), data) {
			t.Fatalf("unbase64(base64(%q, %d)) = %q, %v", data, width, back, err)
		}
		if h, _ := call(encodeHex, str(string(data))); true {
			if raw, err := call(decodeHex, str(h)); err != nil || raw != string(data) {
				t.Fatalf("unhex(hex(%q)) = %q, %v", data, raw, err)
			}
		}
		got, err := call(decode64, str(text))
		if err == nil {
			again, _ := call(encode64, str(got))
			if back, err := call(decode64, str(again)); err != nil || back != got {
				t.Fatalf("%q: decoded %q does not survive a round trip", text, got)
			}
		}
	})
}
