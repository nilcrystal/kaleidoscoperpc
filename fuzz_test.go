package kaleidoscoperpc

import (
	"bytes"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"unicode/utf8"
)

func FuzzBase85RoundTrip(f *testing.F) {
	for _, seed := range [][]byte{
		nil, {0}, {1}, {255}, []byte("Hello World"), bytes.Repeat([]byte{0}, 64),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		enc := EncodeBase85(data)
		dec, err := DecodeBase85(enc)
		if err != nil {
			t.Fatalf("decode encoded data: %v", err)
		}
		if !bytes.Equal(dec, data) {
			t.Fatalf("round trip mismatch")
		}
	})
}

func FuzzBase85StrictDecode(f *testing.F) {
	for _, seed := range []string{"", "0", "00", "01", "00000", "|NsC0", "~~~~~", "NM&qnZy;B1a%^M", "a,b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 64<<10 {
			t.Skip()
		}
		out, err := DecodeBase85(s)
		if err != nil {
			return
		}
		if got := EncodeBase85(out); got != s {
			t.Fatalf("decoder accepted non-canonical %q; reencoded as %q", s, got)
		}
		out2, err := DecodeBase85(EncodeBase85(out))
		if err != nil || !bytes.Equal(out, out2) {
			t.Fatalf("canonical re-decode failed: %v", err)
		}
	})
}

func FuzzServiceParser(f *testing.F) {
	for _, seed := range []string{
		"binary(a)", "reassemble(a, 1)", "reassemble(payload, 3), binary(payload)",
		"", "binary()", "unknown(x)", "reassemble(a, 0001)",
	} {
		f.Add(seed)
	}
	l, _ := (Limits{}).normalized()
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 64<<10 {
			t.Skip()
		}
		d, err := parseService(s, l)
		if err != nil {
			return
		}
		canonical := formatService(d)
		d2, err := parseService(canonical, l)
		if err != nil {
			t.Fatalf("canonical service rejected: %q: %v", canonical, err)
		}
		if !reflect.DeepEqual(d, d2) {
			t.Fatalf("service canonicalization changed meaning: %#v != %#v", d, d2)
		}
	})
}

func FuzzDecodeRequestNoPanic(f *testing.F) {
	f.Add("/echo", "", "v1", "", "X-Kaleidoscope-A", "hello", int64(0))
	f.Add("/", "", "v1", "echo", "X-Kaleidoscope-A1", "x", int64(0))
	f.Add("/echo", "/%65cho", "v2", "ping", "X-Kaleidoscope-Foo_Bar", "x\ny", int64(-1))
	f.Fuzz(func(t *testing.T, path, rawPath, version, fn, key, value string, contentLength int64) {
		if len(path)+len(rawPath)+len(version)+len(fn)+len(key)+len(value) > 64<<10 {
			t.Skip()
		}
		r := &http.Request{
			Method:        http.MethodPost,
			URL:           &url.URL{Path: path, RawPath: rawPath},
			Header:        make(http.Header),
			ContentLength: contentLength,
			Body:          http.NoBody,
		}
		if version != "" {
			r.Header[HeaderVersion] = []string{version}
		}
		if fn != "" {
			r.Header[HeaderFunction] = []string{fn}
		}
		if key != "" {
			r.Header[key] = []string{value}
		}
		_, _ = DecodeRequest(r, Limits{})
	})
}

func FuzzBinaryMessageRoundTrip(f *testing.F) {
	f.Add([]byte("hello"), uint16(1))
	f.Add(bytes.Repeat([]byte{0}, 100), uint16(17))
	f.Add([]byte{0, 1, 2, 3, 4, 5, 255}, uint16(4096))
	f.Fuzz(func(t *testing.T, data []byte, fs uint16) {
		if len(data) > 128<<10 {
			t.Skip()
		}
		frag := int(fs%8192) + 1
		a := NewArgs()
		if err := a.SetBytes("payload", data); err != nil {
			t.Fatal(err)
		}
		r := &http.Request{Method: http.MethodPost, URL: &url.URL{Path: "/"}, Header: make(http.Header), Body: http.NoBody}
		err := EncodeRequest(r, "f", a, EncodeOptions{FragmentSize: frag, Limits: Limits{MaxTotalWireBytes: 1 << 20, MaxDecodedBytes: 256 << 10}})
		if err != nil {
			// Tiny fragments can legitimately exceed the 1024-fragment v1 cap.
			return
		}
		got, err := DecodeRequest(r, Limits{MaxTotalWireBytes: 1 << 20, MaxDecodedBytes: 256 << 10})
		if err != nil {
			t.Fatalf("decode encoded request: %v", err)
		}
		out, ok := got.Bytes("payload")
		if !ok || !bytes.Equal(out, data) {
			t.Fatalf("binary round trip mismatch")
		}
	})
}

func FuzzTextValueValidation(f *testing.F) {
	for _, s := range []string{"", "hello", "hello world", "Иван", " leading", "trailing ", "a,b", "x\ny"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 64<<10 {
			t.Skip()
		}
		err := validateTextValue(s)
		if err == nil {
			if !utf8.ValidString(s) {
				t.Fatal("accepted invalid UTF-8")
			}
			if len(s) > 0 && (s[0] == ' ' || s[len(s)-1] == ' ') {
				t.Fatal("accepted edge whitespace")
			}
			if bytes.ContainsRune([]byte(s), ',') {
				t.Fatal("accepted comma")
			}
		}
	})
}
