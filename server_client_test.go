package kaleidoscoperpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(t *testing.T, cfg ServerConfig) *Server {
	t.Helper()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestServerHappyPathAndNoBody(t *testing.T) {
	mux := NewMux()
	if err := mux.HandleFunc("echo", func(ctx context.Context, req *Request) (*Response, error) {
		if req.Fn() != "echo" {
			t.Fatalf("fn=%q", req.Fn())
		}
		v, ok := req.Arg("message")
		if !ok || v != "hello" {
			t.Fatalf("message=%q,%v", v, ok)
		}
		blob, ok := req.Bytes("blob")
		if !ok || !bytes.Equal(blob, []byte{0, 1, 2, 255}) {
			t.Fatalf("blob=%x,%v", blob, ok)
		}
		resp := NewResponse()
		_ = resp.Set("message", v)
		_ = resp.SetBytes("blob", blob)
		return resp, nil
	}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, ServerConfig{Mux: mux, FragmentSize: 5})

	a := NewArgs()
	_ = a.Set("message", "hello")
	_ = a.SetBytes("blob", []byte{0, 1, 2, 255})
	r := httptest.NewRequest(http.MethodPatch, "http://example/", nil)
	if err := EncodeRequest(r, "echo", a, EncodeOptions{FragmentSize: 5, Placement: FunctionInHeader}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	res := w.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d headers=%v", res.StatusCode, res.Header)
	}
	if got, _ := io.ReadAll(res.Body); len(got) != 0 {
		t.Fatalf("response body=%q", got)
	}
	decoded, err := DecodeResponse(res, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := decoded.Arg("message"); v != "hello" {
		t.Fatalf("message=%q", v)
	}
	if v, _ := decoded.Bytes("blob"); !bytes.Equal(v, []byte{0, 1, 2, 255}) {
		t.Fatalf("blob=%x", v)
	}
}

func TestServerRejectsMalformedBeforeHandler(t *testing.T) {
	var calls atomic.Int32
	mux := NewMux()
	_ = mux.HandleFunc("x", func(context.Context, *Request) (*Response, error) { calls.Add(1); return nil, nil })
	s := newTestServer(t, ServerConfig{Mux: mux})
	r := httptest.NewRequest(http.MethodPost, "http://example/x", nil) // missing version
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("handler called for malformed request")
	}
	if w.Header().Get(HeaderVersion) != Version {
		t.Fatalf("version=%q", w.Header().Get(HeaderVersion))
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body=%q", w.Body.String())
	}
}

func TestServerUnknownFunction(t *testing.T) {
	s := newTestServer(t, ServerConfig{})
	r := validRequest(t, "http://example/missing")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d", w.Code)
	}
	if w.Header().Get(HeaderVersion) != Version {
		t.Fatal("missing version header")
	}
	if w.Body.Len() != 0 {
		t.Fatal("unexpected body")
	}
}

func TestServerStatusError(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("conflict", func(context.Context, *Request) (*Response, error) {
		r := NewResponse()
		_ = r.Set("reason", "duplicate")
		return nil, &StatusError{Status: http.StatusConflict, Response: r, Err: errors.New("duplicate")}
	})
	s := newTestServer(t, ServerConfig{Mux: mux})
	r := validRequest(t, "http://example/conflict")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	res := w.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", res.StatusCode)
	}
	d, err := DecodeResponse(res, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Arg("reason"); v != "duplicate" {
		t.Fatalf("reason=%q", v)
	}
}

func TestServerHandlerErrorAndPanicAreContained(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("err", func(context.Context, *Request) (*Response, error) { return nil, errors.New("boom") })
	_ = mux.HandleFunc("panic", func(context.Context, *Request) (*Response, error) { panic("boom") })
	var observed atomic.Int32
	var sawPanic atomic.Bool
	s := newTestServer(t, ServerConfig{Mux: mux, OnError: func(_ context.Context, err error) {
		observed.Add(1)
		var pe *PanicError
		if errors.As(err, &pe) {
			sawPanic.Store(true)
			if len(pe.Stack) == 0 {
				t.Error("panic stack missing")
			}
		}
	}})
	for _, fn := range []string{"err", "panic"} {
		r := validRequest(t, "http://example/"+fn)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s status=%d", fn, w.Code)
		}
		if w.Header().Get(HeaderVersion) != Version {
			t.Fatalf("%s missing version", fn)
		}
		if w.Body.Len() != 0 {
			t.Fatalf("%s body=%q", fn, w.Body.String())
		}
	}
	if observed.Load() != 2 {
		t.Fatalf("observed=%d", observed.Load())
	}
	if !sawPanic.Load() {
		t.Fatal("panic was not observed")
	}
}

func TestServerOnErrorPanicCannotEscape(t *testing.T) {
	s := newTestServer(t, ServerConfig{OnError: func(context.Context, error) { panic("observer") }})
	r := httptest.NewRequest(http.MethodGet, "http://example/x", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestServerInvalidOutboundBecomes500WithoutPartialHeaders(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("x", func(context.Context, *Request) (*Response, error) {
		r := NewResponse()
		_ = r.Set("large", strings.Repeat("a", 1025))
		return r, nil
	})
	s := newTestServer(t, ServerConfig{Mux: mux, FragmentSize: 1})
	r := validRequest(t, "http://example/x")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", w.Code)
	}
	if w.Header().Get(HeaderService) != "" {
		t.Fatalf("partial service header=%q", w.Header().Get(HeaderService))
	}
	if w.Header().Get("X-Kaleidoscope-Large1") != "" {
		t.Fatal("partial arg header leaked")
	}
	if w.Header().Get(HeaderVersion) != Version {
		t.Fatal("version missing")
	}
}

func TestServerRejectsOutboundFragmentCollision(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("x", func(context.Context, *Request) (*Response, error) {
		r := NewResponse()
		// 30 bytes with FragmentSize=10 → three fragments:
		// X-Kaleidoscope-A-Long1, -A-Long2, -A-Long3
		if err := r.Set("a_long", strings.Repeat("x", 30)); err != nil {
			return nil, err
		}
		// 3 bytes with FragmentSize=10 → NOT fragmented,
		// written directly into X-Kaleidoscope-A-Long1, overwriting
		// the first fragment of a_long.
		if err := r.Set("a_long1", "hi!"); err != nil {
			return nil, err
		}
		return r, nil
	})
	s := newTestServer(t, ServerConfig{Mux: mux, FragmentSize: 10})
	r := validRequest(t, "http://example/x")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", w.Code)
	}
	if w.Header().Get(HeaderVersion) != Version {
		t.Fatal("version missing")
	}
	if w.Header().Get(HeaderService) != "" {
		t.Fatalf("partial service header leaked: %q", w.Header().Get(HeaderService))
	}
	if w.Header().Get("X-Kaleidoscope-A-Long1") != "" {
		t.Fatal("partial argument header leaked")
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body=%q", w.Body.String())
	}
}

func TestEncodeRejectsFragmentFieldCollision(t *testing.T) {
	a := NewArgs()
	if err := a.Set("a_long", strings.Repeat("x", 30)); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("a_long1", "hi!"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
	err := EncodeRequest(r, "x", a, EncodeOptions{FragmentSize: 10})
	requireCode(t, err, ErrFieldCollision)
}

func TestEncodeRejectsFragmentFieldCollisionReversedOrder(t *testing.T) {
	a := NewArgs()
	if err := a.Set("a_long1", "hi!"); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("a_long", strings.Repeat("x", 30)); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
	err := EncodeRequest(r, "x", a, EncodeOptions{FragmentSize: 10})
	requireCode(t, err, ErrFieldCollision)
}

func TestEncodeRejectsFragmentedDirectiveCollision(t *testing.T) {
	a := NewArgs()
	if err := a.Set("a_long", strings.Repeat("x", 30)); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("a_long12", strings.Repeat("y", 6)); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
	err := EncodeRequest(r, "x", a, EncodeOptions{FragmentSize: 2})
	requireCode(t, err, ErrDirectiveCollision)
}

func TestMuxDuplicateAndConcurrentSafeLookup(t *testing.T) {
	mux := NewMux()
	h := HandlerFunc(func(context.Context, *Request) (*Response, error) { return nil, nil })
	if err := mux.Handle("x", h); err != nil {
		t.Fatal(err)
	}
	if err := mux.Handle("x", h); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if _, ok := mux.lookup("x"); !ok {
		t.Fatal("lookup failed")
	}
}

func TestClientServerEndToEnd(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("transform", func(_ context.Context, req *Request) (*Response, error) {
		text, _ := req.Arg("text")
		blob, _ := req.Bytes("blob")
		r := NewResponse()
		_ = r.Set("text", strings.ToUpper(text))
		_ = r.SetBytes("blob", append(blob, 9))
		return r, nil
	})
	srv := httptest.NewServer(newTestServer(t, ServerConfig{Mux: mux, FragmentSize: 13}))
	defer srv.Close()
	client, err := NewClient(ClientConfig{BaseURL: srv.URL, Placement: FunctionInBoth, FragmentSize: 13})
	if err != nil {
		t.Fatal(err)
	}
	a := NewArgs()
	_ = a.Set("text", "hello world")
	input := bytes.Repeat([]byte{1, 2, 3, 4}, 50)
	_ = a.SetBytes("blob", input)
	resp, err := client.Call(context.Background(), "transform", a)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK() || resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if v, _ := resp.Arg("text"); v != "HELLO WORLD" {
		t.Fatalf("text=%q", v)
	}
	want := append(append([]byte(nil), input...), 9)
	if v, _ := resp.Bytes("blob"); !bytes.Equal(v, want) {
		t.Fatalf("blob mismatch")
	}
}

func TestClientReturnsValidNon2xxResponse(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("x", func(context.Context, *Request) (*Response, error) {
		r := NewResponse()
		_ = r.Set("kind", "conflict")
		return nil, &StatusError{Status: 409, Response: r}
	})
	srv := httptest.NewServer(newTestServer(t, ServerConfig{Mux: mux}))
	defer srv.Close()
	c, err := NewClient(ClientConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Call(context.Background(), "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 409 || resp.OK() {
		t.Fatalf("status=%d ok=%v", resp.StatusCode, resp.OK())
	}
	if v, _ := resp.Arg("kind"); v != "conflict" {
		t.Fatalf("kind=%q", v)
	}
}

func TestClientDoesNotFollowRedirectsByDefault(t *testing.T) {
	var sinkHits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sinkHits.Add(1)
		w.Header().Set(HeaderVersion, Version)
		w.WriteHeader(200)
	}))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderVersion, Version)
		w.Header().Set("Location", sink.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	c, err := NewClient(ClientConfig{BaseURL: redirect.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Call(context.Background(), "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if sinkHits.Load() != 0 {
		t.Fatalf("redirect followed: sink hits=%d", sinkHits.Load())
	}
}

func TestClientRejectsMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	c, err := NewClient(ClientConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "x", nil)
	requireCode(t, err, ErrMissingVersion)
}

func TestClientRejectsResponseBodyByFraming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderVersion, Version)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "x")
	}))
	defer srv.Close()
	c, err := NewClient(ClientConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "x", nil)
	requireCode(t, err, ErrBodyNotAllowed)
}

func TestClientHonorsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	c, err := NewClient(ClientConfig{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = c.Call(ctx, "x", nil)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestClientDoesNotMutateProvidedHTTPClient(t *testing.T) {
	marker := func(*http.Request, []*http.Request) error { return errors.New("marker") }
	in := &http.Client{CheckRedirect: marker, Timeout: time.Second, Transport: http.DefaultTransport}
	c, err := NewClient(ClientConfig{BaseURL: "http://example.com", HTTPClient: in})
	if err != nil {
		t.Fatal(err)
	}
	if in.CheckRedirect == nil {
		t.Fatal("input client mutated")
	}
	if c.http == in {
		t.Fatal("client pointer was not cloned")
	}
}

func TestClientServerHTTP2UnicodeAndBinary(t *testing.T) {
	mux := NewMux()
	_ = mux.HandleFunc("echo", func(_ context.Context, req *Request) (*Response, error) {
		text, _ := req.Arg("text")
		blob, _ := req.Bytes("blob")
		r := NewResponse()
		_ = r.Set("text", text)
		_ = r.SetBytes("blob", blob)
		return r, nil
	})
	us := httptest.NewUnstartedServer(newTestServer(t, ServerConfig{Mux: mux, FragmentSize: 7}))
	us.EnableHTTP2 = true
	us.StartTLS()
	defer us.Close()
	c, err := NewClient(ClientConfig{BaseURL: us.URL, HTTPClient: us.Client(), FragmentSize: 7})
	if err != nil {
		t.Fatal(err)
	}
	a := NewArgs()
	if err := a.Set("text", "Иван Иванов"); err != nil {
		t.Fatal(err)
	}
	blob := bytes.Repeat([]byte{0xff, 0, 1, 2, 3, 4, 5, 6}, 32)
	_ = a.SetBytes("blob", blob)
	resp, err := c.Call(context.Background(), "echo", a)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resp.Arg("text"); v != "Иван Иванов" {
		t.Fatalf("text=%q", v)
	}
	if v, _ := resp.Bytes("blob"); !bytes.Equal(v, blob) {
		t.Fatal("blob mismatch")
	}
}
