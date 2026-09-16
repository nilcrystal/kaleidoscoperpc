package kaleidoscoperpc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ClientConfig configures the high-level client.
type ClientConfig struct {
	BaseURL      string
	HTTPClient   *http.Client
	Method       string
	Placement    FunctionPlacement
	Limits       Limits
	FragmentSize int

	// AllowRedirects is false by default. Following redirects can replay RPC
	// argument headers to an unintended endpoint and can rewrite methods.
	AllowRedirects bool
}

// Client sends fully validated, bodyless KaleidoscopeRPC calls.
type Client struct {
	base         *url.URL
	http         *http.Client
	method       string
	placement    FunctionPlacement
	limits       Limits
	fragmentSize int
}

func NewClient(cfg ClientConfig) (*Client, error) {
	base, err := ParseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
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
	placement := cfg.Placement
	if placement == 0 {
		placement = FunctionInPath
	}
	if placement < FunctionInPath || placement > FunctionInBoth {
		return nil, protocolError(ErrEncode, "Placement", "invalid function placement")
	}
	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}

	hc := cloneHTTPClient(cfg.HTTPClient, l)
	if !cfg.AllowRedirects {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &Client{base: base, http: hc, method: method, placement: placement, limits: l, fragmentSize: frag}, nil
}

func cloneHTTPClient(in *http.Client, limits Limits) *http.Client {
	var out http.Client
	if in == nil {
		out.Timeout = 30 * time.Second
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			tr := dt.Clone()
			tr.MaxResponseHeaderBytes = int64(limits.MaxTotalWireBytes + (64 << 10))
			out.Transport = tr
		}
		return &out
	}
	out = *in
	if in.Transport == nil {
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			tr := dt.Clone()
			tr.MaxResponseHeaderBytes = int64(limits.MaxTotalWireBytes + (64 << 10))
			out.Transport = tr
		}
	} else if tr, ok := in.Transport.(*http.Transport); ok {
		clone := tr.Clone()
		capBytes := int64(limits.MaxTotalWireBytes + (64 << 10))
		if clone.MaxResponseHeaderBytes == 0 || clone.MaxResponseHeaderBytes > capBytes {
			clone.MaxResponseHeaderBytes = capBytes
		}
		out.Transport = clone
	}
	return &out
}

// Call sends one RPC call. Any syntactically valid protocol response is
// returned regardless of HTTP status; callers can inspect StatusCode or OK().
// Transport failures and malformed protocol responses are returned as errors.
func (c *Client) Call(ctx context.Context, fn string, args *Args) (*DecodedResponse, error) {
	if c == nil || c.base == nil || c.http == nil {
		return nil, protocolError(ErrEncode, "Client", "client is not initialized")
	}
	u := *c.base
	req, err := http.NewRequestWithContext(ctx, c.method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("kaleidoscoperpc: create request: %w", err)
	}
	if err := EncodeRequest(req, fn, args, EncodeOptions{
		Limits: c.limits, FragmentSize: c.fragmentSize, Placement: c.placement,
	}); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kaleidoscoperpc: HTTP call: %w", err)
	}
	defer resp.Body.Close()
	decoded, err := DecodeResponse(resp, c.limits)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}
