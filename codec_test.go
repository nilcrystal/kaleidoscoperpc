package kaleidoscoperpc

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set(HeaderVersion, Version)
	return r
}

func requireCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", want)
	}
	got, ok := ProtocolErrorCode(err)
	if !ok || got != want {
		t.Fatalf("error = %v, code = %q/%v, want %q", err, got, ok, want)
	}
}

func TestDecodeRequestFunctionPlacements(t *testing.T) {
	cases := []struct {
		name, target, hdr, want string
	}{
		{"path", "http://example/echo", "", "echo"},
		{"header", "http://example/", "echo", "echo"},
		{"both", "http://example/echo", "echo", "echo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest(t, tc.target)
			if tc.hdr != "" {
				r.Header.Set(HeaderFunction, tc.hdr)
			}
			got, err := DecodeRequest(r, Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Fn() != tc.want {
				t.Fatalf("Fn=%q want %q", got.Fn(), tc.want)
			}
		})
	}
}

func TestDecodeRequestFunctionErrors(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		r := validRequest(t, "http://example/")
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrMissingFunction)
	})
	t.Run("mismatch", func(t *testing.T) {
		r := validRequest(t, "http://example/echo")
		r.Header.Set(HeaderFunction, "ping")
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrFunctionMismatch)
	})
	t.Run("nested", func(t *testing.T) {
		r := validRequest(t, "http://example/a/b")
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrInvalidPath)
	})
	t.Run("query", func(t *testing.T) {
		r := validRequest(t, "http://example/echo?x=1")
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrQueryNotAllowed)
	})
	t.Run("percent encoded", func(t *testing.T) {
		r := validRequest(t, "http://example/%65cho")
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrInvalidPath)
	})
	t.Run("body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "http://example/echo", strings.NewReader("x"))
		r.Header.Set(HeaderVersion, Version)
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrBodyNotAllowed)
	})
}

func TestDecodeRequestVersionValidation(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example/echo", nil)
	_, err := DecodeRequest(r, Limits{})
	requireCode(t, err, ErrMissingVersion)

	r.Header.Set(HeaderVersion, "v2")
	_, err = DecodeRequest(r, Limits{})
	requireCode(t, err, ErrUnsupportedVersion)

	r.Header[http.CanonicalHeaderKey(HeaderVersion)] = []string{"v1", "v1"}
	_, err = DecodeRequest(r, Limits{})
	requireCode(t, err, ErrDuplicateHeader)
}

func TestDecodePlainArgumentsAndEmpty(t *testing.T) {
	r := validRequest(t, "http://example/user_create")
	r.Header.Set("X-Kaleidoscope-User-Information", "Иван Иванов")
	r.Header.Set("X-Kaleidoscope-Empty", "")
	got, err := DecodeRequest(r, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got.Arg("user_information"); !ok || v != "Иван Иванов" {
		t.Fatalf("arg=%q,%v", v, ok)
	}
	if v, ok := got.Arg("empty"); !ok || v != "" {
		t.Fatalf("empty=%q,%v", v, ok)
	}
	if _, ok := got.Arg("missing"); ok {
		t.Fatal("missing arg reported present")
	}
	if names := got.Names(); strings.Join(names, ",") != "empty,user_information" {
		t.Fatalf("names=%v", names)
	}
}

func TestDecodeBinaryAndReassemble(t *testing.T) {
	r := validRequest(t, "http://example/store")
	payload := []byte("Hello World\x00\xff")
	enc := EncodeBase85(payload)
	r.Header.Set(HeaderService, "reassemble(payload, 3), binary(payload)")
	parts := []string{enc[:4], enc[4:9], enc[9:]}
	for i, p := range parts {
		r.Header.Set("X-Kaleidoscope-Payload"+string(rune('1'+i)), p)
	}
	got, err := DecodeRequest(r, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if raw, ok := got.Arg("payload"); !ok || raw != enc {
		t.Fatalf("raw=%q,%v want %q", raw, ok, enc)
	}
	b, ok := got.Bytes("payload")
	if !ok || !bytes.Equal(b, payload) {
		t.Fatalf("bytes=%x,%v want %x", b, ok, payload)
	}
	if !got.IsBinary("payload") {
		t.Fatal("payload should be binary")
	}
	b[0] ^= 0xff
	b2, _ := got.Bytes("payload")
	if bytes.Equal(b, b2) {
		t.Fatal("Bytes returned mutable internal storage")
	}
}

func TestDecodeReassembleTextAndSeparateSuffixArg(t *testing.T) {
	r := validRequest(t, "http://example/x")
	r.Header.Set(HeaderService, "reassemble(user, 2)")
	r.Header.Set("X-Kaleidoscope-User1", "hello")
	r.Header.Set("X-Kaleidoscope-User2", "world")
	r.Header.Set("X-Kaleidoscope-User-Extra", "separate")
	got, err := DecodeRequest(r, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := got.Arg("user"); v != "helloworld" {
		t.Fatalf("user=%q", v)
	}
	if v, _ := got.Arg("user_extra"); v != "separate" {
		t.Fatalf("user_extra=%q", v)
	}
}

func TestDecodeReassembleErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*http.Request)
		code   ErrorCode
	}{
		{"missing", func(r *http.Request) {
			r.Header.Set(HeaderService, "reassemble(a, 2)")
			r.Header.Set("X-Kaleidoscope-A1", "x")
		}, ErrMissingFragment},
		{"bare", func(r *http.Request) {
			r.Header.Set(HeaderService, "reassemble(a, 1)")
			r.Header.Set("X-Kaleidoscope-A", "x")
			r.Header.Set("X-Kaleidoscope-A1", "x")
		}, ErrBaseAndFragments},
		{"extra", func(r *http.Request) {
			r.Header.Set(HeaderService, "reassemble(a, 1)")
			r.Header.Set("X-Kaleidoscope-A1", "x")
			r.Header.Set("X-Kaleidoscope-A2", "x")
		}, ErrUnexpectedFragment},
		{"zero", func(r *http.Request) {
			r.Header.Set(HeaderService, "reassemble(a, 1)")
			r.Header.Set("X-Kaleidoscope-A1", "x")
			r.Header.Set("X-Kaleidoscope-A0", "x")
		}, ErrUnexpectedFragment},
		{"leading zero", func(r *http.Request) {
			r.Header.Set(HeaderService, "reassemble(a, 1)")
			r.Header.Set("X-Kaleidoscope-A1", "x")
			r.Header.Set("X-Kaleidoscope-A01", "x")
		}, ErrUnexpectedFragment},
		{"edge whitespace", func(r *http.Request) {
			r.Header.Set(HeaderService, "reassemble(a, 2)")
			r.Header.Set("X-Kaleidoscope-A1", "x ")
			r.Header.Set("X-Kaleidoscope-A2", "y")
		}, ErrInvalidArgumentValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest(t, "http://example/x")
			tc.mutate(r)
			_, err := DecodeRequest(r, Limits{})
			requireCode(t, err, tc.code)
		})
	}
}

func TestDecodeBinaryErrors(t *testing.T) {
	r := validRequest(t, "http://example/x")
	r.Header.Set(HeaderService, "binary(blob)")
	r.Header.Set("X-Kaleidoscope-Blob", "not,base85")
	_, err := DecodeRequest(r, Limits{})
	requireCode(t, err, ErrInvalidBase85)

	r = validRequest(t, "http://example/x")
	r.Header.Set(HeaderService, "binary(blob)")
	_, err = DecodeRequest(r, Limits{})
	requireCode(t, err, ErrMissingArgument)
}

func TestDecodeRejectsDuplicateAndMalformedProtocolHeaders(t *testing.T) {
	t.Run("duplicate values", func(t *testing.T) {
		r := validRequest(t, "http://example/x")
		r.Header["X-Kaleidoscope-A"] = []string{"1", "2"}
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrDuplicateHeader)
	})
	t.Run("case duplicate map keys", func(t *testing.T) {
		r := validRequest(t, "http://example/x")
		r.Header["X-Kaleidoscope-A"] = []string{"1"}
		r.Header["x-kaleidoscope-a"] = []string{"2"}
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrDuplicateHeader)
	})
	t.Run("bad arg header", func(t *testing.T) {
		r := validRequest(t, "http://example/x")
		r.Header["X-Kaleidoscope-Foo_Bar"] = []string{"x"}
		_, err := DecodeRequest(r, Limits{})
		requireCode(t, err, ErrInvalidArgumentName)
	})
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	a := NewArgs()
	if err := a.Set("text", "hello world and more"); err != nil {
		t.Fatal(err)
	}
	blob := bytes.Repeat([]byte{0, 1, 2, 3, 255}, 20)
	if err := a.SetBytes("blob", blob); err != nil {
		t.Fatal(err)
	}
	if err := a.SetBytes("empty_blob", nil); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
	r.Header.Set("X-Other", "keep")
	if err := EncodeRequest(r, "echo", a, EncodeOptions{FragmentSize: 17, Placement: FunctionInBoth}); err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/echo" {
		t.Fatalf("path=%q", r.URL.Path)
	}
	if r.Header.Get(HeaderFunction) != "echo" {
		t.Fatalf("fn hdr=%q", r.Header.Get(HeaderFunction))
	}
	if r.Header.Get("X-Other") != "keep" {
		t.Fatal("non-protocol header was modified")
	}
	if svc := r.Header.Get(HeaderService); !strings.Contains(svc, "reassemble(blob") || !strings.Contains(svc, "binary(blob)") {
		t.Fatalf("service=%q", svc)
	}

	got, err := DecodeRequest(r, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := got.Arg("text"); v != "hello world and more" {
		t.Fatalf("text=%q", v)
	}
	if v, _ := got.Bytes("blob"); !bytes.Equal(v, blob) {
		t.Fatalf("blob mismatch")
	}
	if v, ok := got.Bytes("empty_blob"); !ok || len(v) != 0 {
		t.Fatalf("empty blob=%x,%v", v, ok)
	}
}

func TestEncodeRequestPlacements(t *testing.T) {
	for _, p := range []FunctionPlacement{FunctionInPath, FunctionInHeader, FunctionInBoth} {
		r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
		if err := EncodeRequest(r, "echo", nil, EncodeOptions{Placement: p}); err != nil {
			t.Fatal(err)
		}
		got, err := DecodeRequest(r, Limits{})
		if err != nil {
			t.Fatalf("placement %d: %v", p, err)
		}
		if got.Fn() != "echo" {
			t.Fatalf("placement %d fn=%q", p, got.Fn())
		}
	}
}

func TestEncodeRejectsUnsafeTextAndImpossibleFragmentBoundary(t *testing.T) {
	a := NewArgs()
	for _, v := range []string{" leading", "trailing ", "x\ny", "x\ty", "x,y"} {
		if err := a.Set("a", v); err == nil {
			t.Errorf("Set accepted %q", v)
		}
	}
	if err := a.Set("a", "a     b"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
	err := EncodeRequest(r, "x", a, EncodeOptions{FragmentSize: 3})
	requireCode(t, err, ErrEncode)
}

func TestEncodeRejectsBodyQueryAndTooMuchDecodedData(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://example/?x=1", nil)
	err := EncodeRequest(r, "x", nil, EncodeOptions{})
	requireCode(t, err, ErrQueryNotAllowed)

	r = httptest.NewRequest(http.MethodPost, "http://example/", strings.NewReader("x"))
	err = EncodeRequest(r, "x", nil, EncodeOptions{})
	requireCode(t, err, ErrBodyNotAllowed)

	a := NewArgs()
	_ = a.SetBytes("a", bytes.Repeat([]byte{1}, 6))
	_ = a.SetBytes("b", bytes.Repeat([]byte{2}, 6))
	r = httptest.NewRequest(http.MethodPost, "http://example/", nil)
	err = EncodeRequest(r, "x", a, EncodeOptions{Limits: Limits{MaxDecodedBytes: 10}})
	requireCode(t, err, ErrDecodedValueTooLarge)
}

func TestLimitsOnIncoming(t *testing.T) {
	r := validRequest(t, "http://example/x")
	r.Header.Set("X-Kaleidoscope-A", "12345")
	_, err := DecodeRequest(r, Limits{MaxHeaderValueBytes: 4})
	requireCode(t, err, ErrHeaderValueTooLarge)

	r = validRequest(t, "http://example/x")
	r.Header.Set("X-Kaleidoscope-A", "1")
	r.Header.Set("X-Kaleidoscope-B", "2")
	_, err = DecodeRequest(r, Limits{MaxArguments: 1})
	requireCode(t, err, ErrTooManyArguments)
}

func TestDecodeResponse(t *testing.T) {
	h := make(http.Header)
	a := NewArgs()
	_ = a.Set("status", "ok")
	if err := EncodeHeaders(h, a, EncodeOptions{}); err != nil {
		t.Fatal(err)
	}
	r := &http.Response{StatusCode: 201, Header: h, Body: io.NopCloser(strings.NewReader("")), ContentLength: 0}
	got, err := DecodeResponse(r, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != 201 || !got.OK() {
		t.Fatalf("status=%d ok=%v", got.StatusCode, got.OK())
	}
	if v, _ := got.Arg("status"); v != "ok" {
		t.Fatalf("status arg=%q", v)
	}

	r.Header.Set(HeaderFunction, "x")
	_, err = DecodeResponse(r, Limits{})
	requireCode(t, err, ErrUnexpectedFunction)
}

func TestDecodeResponseRejectsKnownBody(t *testing.T) {
	r := &http.Response{StatusCode: 200, Header: http.Header{HeaderVersion: []string{Version}}, ContentLength: 1, Body: io.NopCloser(strings.NewReader("x"))}
	_, err := DecodeResponse(r, Limits{})
	requireCode(t, err, ErrBodyNotAllowed)
}

func TestParseBaseURL(t *testing.T) {
	for _, good := range []string{"http://example.com", "https://example.com/"} {
		if _, err := ParseBaseURL(good); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	for _, bad := range []string{"example.com", "ftp://example.com", "http://example.com/rpc", "http://example.com/?x=1", "http://example.com/#x"} {
		if _, err := ParseBaseURL(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
