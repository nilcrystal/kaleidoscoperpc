package kaleidoscoperpc

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestErrorHelpers(t *testing.T) {
	inner := errors.New("inner")
	pe := &ProtocolError{Code: ErrInvalidPath, Field: "URL", Detail: "bad", Err: inner}
	if pe.Error() == "" || !errors.Is(pe, inner) || !IsProtocolError(pe) {
		t.Fatal("protocol error helpers failed")
	}
	if code, ok := ProtocolErrorCode(pe); !ok || code != ErrInvalidPath {
		t.Fatalf("code=%q,%v", code, ok)
	}
	if _, ok := ProtocolErrorCode(inner); ok {
		t.Fatal("non-protocol error reported as protocol")
	}
	var nilPE *ProtocolError
	if nilPE.Error() != "<nil>" {
		t.Fatal("nil ProtocolError string")
	}
	se := &StatusError{Status: 409, Err: inner}
	if se.Error() == "" || !errors.Is(se, inner) {
		t.Fatal("status error helpers failed")
	}
	se2 := &StatusError{Status: 400}
	if se2.Error() == "" {
		t.Fatal("empty status error")
	}
	var nilSE *StatusError
	if nilSE.Error() != "<nil>" {
		t.Fatal("nil StatusError string")
	}
	if (&PanicError{}).Error() == "" {
		t.Fatal("panic error string")
	}
}

func TestLimitsValidation(t *testing.T) {
	if err := (Limits{}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Limits{MaxArguments: -1}).Validate(); err == nil {
		t.Fatal("negative limit accepted")
	}
	if err := (Limits{MaxFragmentsPerArgument: 1025}).Validate(); err == nil {
		t.Fatal("too many fragments accepted")
	}
}

func TestArgsZeroValueCloneDeleteLenAndNilMethods(t *testing.T) {
	var a Args
	if err := a.Set("x", "y"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetBytes("b", []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if a.Len() != 2 {
		t.Fatalf("len=%d", a.Len())
	}
	clone := a.Clone()
	clone.Delete("x")
	if clone.Len() != 1 || a.Len() != 2 {
		t.Fatal("clone/delete aliasing")
	}
	v := clone.values["b"]
	v.bytes[0] = 9
	clone.values["b"] = v
	if a.values["b"].bytes[0] != 1 {
		t.Fatal("binary clone aliased")
	}
	var nilArgs *Args
	if nilArgs.Len() != 0 || nilArgs.Clone().Len() != 0 {
		t.Fatal("nil Args helpers")
	}
	nilArgs.Delete("x")
}

func TestNilReadOnlyMessageMethods(t *testing.T) {
	var m *Message
	if _, ok := m.Arg("x"); ok {
		t.Fatal("nil message arg")
	}
	if _, ok := m.Bytes("x"); ok {
		t.Fatal("nil message bytes")
	}
	if m.IsBinary("x") || m.Names() != nil {
		t.Fatal("nil message helpers")
	}
	var r *Request
	if r.Fn() != "" {
		t.Fatal("nil request fn")
	}
	if _, ok := r.Arg("x"); ok {
		t.Fatal("nil request arg")
	}
	if _, ok := r.Bytes("x"); ok {
		t.Fatal("nil request bytes")
	}
	if r.IsBinary("x") || r.Names() != nil {
		t.Fatal("nil request helpers")
	}
	var dr *DecodedResponse
	if _, ok := dr.Arg("x"); ok {
		t.Fatal("nil decoded arg")
	}
	if _, ok := dr.Bytes("x"); ok {
		t.Fatal("nil decoded bytes")
	}
	if dr.IsBinary("x") || dr.Names() != nil || dr.OK() {
		t.Fatal("nil decoded helpers")
	}
}

func TestResponseHelpers(t *testing.T) {
	r := NewResponse()
	_ = r.Set("a", "b")
	_ = r.SetBytes("bin", []byte{1})
	r.Delete("a")
	if r.Args().Len() != 1 {
		t.Fatalf("len=%d", r.Args().Len())
	}
	var nilR *Response
	if nilR.Args() != nil {
		t.Fatal("nil response args")
	}
}

func TestEncodeOptionAndNilInputErrors(t *testing.T) {
	if err := EncodeRequest(nil, "x", nil, EncodeOptions{}); err == nil {
		t.Fatal("nil request accepted")
	}
	r := &http.Request{}
	if err := EncodeRequest(r, "x", nil, EncodeOptions{}); err == nil {
		t.Fatal("nil URL accepted")
	}
	r = &http.Request{URL: &url.URL{Path: "/"}, Header: make(http.Header), Body: http.NoBody}
	if err := EncodeRequest(r, "bad-name", nil, EncodeOptions{}); err == nil {
		t.Fatal("bad function accepted")
	}
	if err := EncodeRequest(r, "x", nil, EncodeOptions{FragmentSize: 9000}); err == nil {
		t.Fatal("bad fragment size accepted")
	}
	if err := EncodeRequest(r, "x", nil, EncodeOptions{Placement: 99}); err == nil {
		t.Fatal("bad placement accepted")
	}
	if err := EncodeHeaders(nil, nil, EncodeOptions{}); err == nil {
		t.Fatal("nil headers accepted")
	}
	if _, err := DecodeResponse(nil, Limits{}); err == nil {
		t.Fatal("nil response accepted")
	}
}

func TestIncomingProtocolHeaderLimits(t *testing.T) {
	r := validRequest(t, "http://example/x")
	r.Header.Set("X-Kaleidoscope-A", "1")
	_, err := DecodeRequest(r, Limits{MaxProtocolHeaderFields: 1})
	requireCode(t, err, ErrTooManyProtocolHeaders)

	r = validRequest(t, "http://example/x")
	r.Header.Set("X-Kaleidoscope-A", strings.Repeat("x", 64))
	_, err = DecodeRequest(r, Limits{MaxTotalWireBytes: 40, MaxHeaderValueBytes: 128})
	requireCode(t, err, ErrMessageTooLarge)
}

func TestBinaryFragmentInvalidCharacter(t *testing.T) {
	r := validRequest(t, "http://example/x")
	r.Header.Set(HeaderService, "reassemble(a, 2), binary(a)")
	r.Header.Set("X-Kaleidoscope-A1", "00")
	r.Header.Set("X-Kaleidoscope-A2", ",")
	_, err := DecodeRequest(r, Limits{})
	requireCode(t, err, ErrInvalidArgumentValue)
}

func TestNewClientConfigErrorsAndCustomTransport(t *testing.T) {
	for _, cfg := range []ClientConfig{
		{BaseURL: "http://example.com", FragmentSize: 9000},
		{BaseURL: "http://example.com", Placement: 99},
		{BaseURL: "not-a-url"},
		{BaseURL: "http://example.com", Limits: Limits{MaxArguments: -1}},
	} {
		if _, err := NewClient(cfg); err == nil {
			t.Fatalf("accepted config %+v", cfg)
		}
	}
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("transport") })
	in := &http.Client{Transport: rt}
	c, err := NewClient(ClientConfig{BaseURL: "http://example.com", HTTPClient: in})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("err=%v", err)
	}
	var nilC *Client
	if _, err := nilC.Call(context.Background(), "x", nil); err == nil {
		t.Fatal("nil client accepted")
	}
}

func TestClientInvalidMethodFailsAtCall(t *testing.T) {
	c, err := NewClient(ClientConfig{BaseURL: "http://example.com", Method: "bad\nmethod"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "x", nil); err == nil {
		t.Fatal("invalid method accepted")
	}
}

func TestMuxAndServerConfigErrors(t *testing.T) {
	var nilMux *Mux
	h := HandlerFunc(func(context.Context, *Request) (*Response, error) { return nil, nil })
	if err := nilMux.Handle("x", h); err == nil {
		t.Fatal("nil mux accepted")
	}
	m := NewMux()
	if err := m.Handle("bad-name", h); err == nil {
		t.Fatal("bad fn accepted")
	}
	if err := m.Handle("x", nil); err == nil {
		t.Fatal("nil handler accepted")
	}
	if _, ok := nilMux.lookup("x"); ok {
		t.Fatal("nil mux lookup")
	}
	if _, err := NewServer(ServerConfig{FragmentSize: 9000}); err == nil {
		t.Fatal("bad server fragment accepted")
	}
	if _, err := NewServer(ServerConfig{Limits: Limits{MaxArguments: -1}}); err == nil {
		t.Fatal("bad server limits accepted")
	}
}

func TestServerDisablePanicRecovery(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("x", func(context.Context, *Request) (*Response, error) { panic("boom") })
	s := newTestServer(t, ServerConfig{Mux: mux, DisablePanicRecovery: true})
	defer func() {
		if recover() == nil {
			t.Fatal("panic was unexpectedly recovered")
		}
	}()
	s.ServeHTTP(&panicWriter{}, validRequest(t, "http://example/x"))
}

type panicWriter struct{}

func (*panicWriter) Header() http.Header       { return make(http.Header) }
func (*panicWriter) Write([]byte) (int, error) { return 0, nil }
func (*panicWriter) WriteHeader(int)           {}

func TestInvalidStatusErrorFallsBackTo500(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("x", func(context.Context, *Request) (*Response, error) { return nil, &StatusError{Status: 199} })
	s := newTestServer(t, ServerConfig{Mux: mux})
	w := &recordWriter{h: make(http.Header)}
	s.ServeHTTP(w, validRequest(t, "http://example/x"))
	if w.status != 500 {
		t.Fatalf("status=%d", w.status)
	}
}

type recordWriter struct {
	h      http.Header
	status int
}

func (w *recordWriter) Header() http.Header         { return w.h }
func (w *recordWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *recordWriter) WriteHeader(s int)           { w.status = s }

func TestParseBaseURLRejectsFragmentRawAndDecodeEmptyURLPath(t *testing.T) {
	if _, err := ParseBaseURL("http://example.com/#frag"); err == nil {
		t.Fatal("fragment accepted")
	}
	r := &http.Request{URL: &url.URL{}, Header: make(http.Header), Body: http.NoBody}
	r.Header.Set(HeaderVersion, Version)
	r.Header.Set(HeaderFunction, "x")
	if _, err := DecodeRequest(r, Limits{}); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestNilMutableBuildersReturnErrors(t *testing.T) {
	var a *Args
	if err := a.Set("x", "y"); err == nil {
		t.Fatal("nil Args.Set did not fail")
	}
	if err := a.SetBytes("x", []byte{1}); err == nil {
		t.Fatal("nil Args.SetBytes did not fail")
	}
	var r *Response
	if err := r.Set("x", "y"); err == nil {
		t.Fatal("nil Response.Set did not fail")
	}
	if err := r.SetBytes("x", []byte{1}); err == nil {
		t.Fatal("nil Response.SetBytes did not fail")
	}
}
