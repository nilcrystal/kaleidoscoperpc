package kaleidoscoperpc

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"sync"
)

// Handler is the business-logic boundary. It has no access to an
// http.ResponseWriter, which makes it impossible for application code to emit
// a response body or partially commit protocol headers.
type Handler interface {
	HandleKaleidoscope(context.Context, *Request) (*Response, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, *Request) (*Response, error)

func (f HandlerFunc) HandleKaleidoscope(ctx context.Context, r *Request) (*Response, error) {
	return f(ctx, r)
}

// Mux routes validated function names to business handlers. Registration and
// lookup are safe to use concurrently.
type Mux struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewMux() *Mux { return &Mux{handlers: make(map[string]Handler)} }

// Handle registers a function. Duplicate registrations are rejected rather
// than silently replaced.
func (m *Mux) Handle(fn string, h Handler) error {
	if m == nil {
		return protocolError(ErrEncode, "Mux", "nil mux")
	}
	if !validFunctionName(fn, int(^uint(0)>>1)) {
		return protocolError(ErrInvalidFunction, fn, "function must match [A-Za-z0-9_]+")
	}
	if h == nil {
		return protocolError(ErrEncode, fn, "handler must be non-nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handlers == nil {
		m.handlers = make(map[string]Handler)
	}
	if _, exists := m.handlers[fn]; exists {
		return protocolError(ErrEncode, fn, "handler already registered")
	}
	m.handlers[fn] = h
	return nil
}

// HandleFunc is the function form of Handle.
func (m *Mux) HandleFunc(fn string, h HandlerFunc) error { return m.Handle(fn, h) }

func (m *Mux) lookup(fn string) (Handler, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	h, ok := m.handlers[fn]
	m.mu.RUnlock()
	return h, ok
}

// ServerConfig configures a header-only RPC server.
type ServerConfig struct {
	Mux          *Mux
	Limits       Limits
	FragmentSize int

	// DisablePanicRecovery restores net/http-style panic propagation to the
	// outer server. The default is false: handler panics are contained and
	// converted into a bodyless 500 response.
	DisablePanicRecovery bool

	// OnError receives protocol, application, encoding and recovered-panic
	// errors. It is observational only; panics from OnError are swallowed.
	OnError func(context.Context, error)
}

// Server is an http.Handler that fully validates KaleidoscopeRPC before
// invoking business logic.
type Server struct {
	mux          *Mux
	limits       Limits
	fragmentSize int
	recoverPanic bool
	onError      func(context.Context, error)
}

func NewServer(cfg ServerConfig) (*Server, error) {
	l, err := cfg.Limits.normalized()
	if err != nil {
		return nil, err
	}
	frag := cfg.FragmentSize
	if frag == 0 {
		frag = min(4096, l.MaxHeaderValueBytes)
	}
	if frag <= 0 || frag > l.MaxHeaderValueBytes {
		return nil, protocolError(ErrInvalidLimits, "FragmentSize", "must be 1..MaxHeaderValueBytes")
	}
	mux := cfg.Mux
	if mux == nil {
		mux = NewMux()
	}
	return &Server{
		mux: mux, limits: l, fragmentSize: frag,
		recoverPanic: !cfg.DisablePanicRecovery, onError: cfg.OnError,
	}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		writeBareStatus(w, http.StatusInternalServerError)
		return
	}
	if s.recoverPanic {
		defer func() {
			if v := recover(); v != nil {
				pe := &PanicError{Recovered: v, Stack: debug.Stack()}
				s.observe(r.Context(), pe)
				s.write(w, http.StatusInternalServerError, nil)
			}
		}()
	}

	req, err := DecodeRequest(r, s.limits)
	if err != nil {
		s.observe(r.Context(), err)
		s.write(w, http.StatusBadRequest, nil)
		return
	}
	h, ok := s.mux.lookup(req.Fn())
	if !ok {
		s.write(w, http.StatusNotFound, nil)
		return
	}
	resp, err := h.HandleKaleidoscope(r.Context(), req)
	if err != nil {
		s.observe(r.Context(), err)
		var se *StatusError
		if errors.As(err, &se) && se.Status >= 200 && se.Status <= 599 {
			s.write(w, se.Status, se.Response)
			return
		}
		s.write(w, http.StatusInternalServerError, nil)
		return
	}
	s.write(w, http.StatusOK, resp)
}

func (s *Server) write(w http.ResponseWriter, status int, resp *Response) {
	tmp := make(http.Header)
	var args *Args
	if resp != nil {
		args = resp.Args()
	}
	err := EncodeHeaders(tmp, args, EncodeOptions{Limits: s.limits, FragmentSize: s.fragmentSize})
	if err != nil {
		s.observe(context.Background(), err)
		tmp = make(http.Header)
		tmp.Set(HeaderVersion, Version)
		status = http.StatusInternalServerError
	}
	clearProtocolHeaders(w.Header())
	mergeHeaders(w.Header(), tmp)
	w.WriteHeader(status)
}

func (s *Server) observe(ctx context.Context, err error) {
	if s.onError == nil || err == nil {
		return
	}
	defer func() { _ = recover() }()
	s.onError(ctx, err)
}

func writeBareStatus(w http.ResponseWriter, status int) {
	clearProtocolHeaders(w.Header())
	w.Header().Set(HeaderVersion, Version)
	w.WriteHeader(status)
}
