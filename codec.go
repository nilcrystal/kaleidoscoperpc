package kaleidoscoperpc

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type normalizedHeader struct {
	original string
	values   []string
	seen     bool
}

type protocolHeaders map[string]*normalizedHeader

func isProtocolHeaderName(k string) bool {
	return strings.EqualFold(k, HeaderVersion) || strings.EqualFold(k, HeaderFunction) ||
		strings.EqualFold(k, HeaderService) || hasPrefixFold(k, ArgumentHeaderPrefix)
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func collectProtocolHeaders(h http.Header, limits Limits) (protocolHeaders, error) {
	out := make(protocolHeaders)
	fields := 0
	total := 0
	for key, vals := range h {
		if !isProtocolHeaderName(key) {
			continue
		}
		fields++
		if len(vals) > 1 {
			fields += len(vals) - 1
		}
		if fields > limits.MaxProtocolHeaderFields {
			return nil, protocolError(ErrTooManyProtocolHeaders, key, "too many KaleidoscopeRPC header fields")
		}
		total += len(key)
		if total > limits.MaxTotalWireBytes {
			return nil, protocolError(ErrMessageTooLarge, key, "protocol headers exceed configured wire limit")
		}
		lower := strings.ToLower(key)
		e := out[lower]
		if e == nil {
			e = &normalizedHeader{original: key, seen: true}
			out[lower] = e
		}
		for _, v := range vals {
			if len(v) > limits.MaxHeaderValueBytes {
				return nil, protocolError(ErrHeaderValueTooLarge, key, "header value exceeds configured limit")
			}
			total += len(v)
			if total > limits.MaxTotalWireBytes {
				return nil, protocolError(ErrMessageTooLarge, key, "protocol headers exceed configured wire limit")
			}
			e.values = append(e.values, v)
		}
	}
	return out, nil
}

func singleton(ph protocolHeaders, name string, required bool) (string, bool, error) {
	e := ph[strings.ToLower(name)]
	if e == nil || !e.seen {
		if required {
			return "", false, protocolError(ErrMissingVersion, name, "required header is missing")
		}
		return "", false, nil
	}
	if len(e.values) != 1 {
		return "", false, protocolError(ErrDuplicateHeader, name, "header must occur exactly once")
	}
	return e.values[0], true, nil
}

func decodeMessageHeaders(h http.Header, limits Limits, response bool) (*Message, error) {
	ph, err := collectProtocolHeaders(h, limits)
	if err != nil {
		return nil, err
	}
	version, _, err := singleton(ph, HeaderVersion, true)
	if err != nil {
		return nil, err
	}
	if version != Version {
		return nil, protocolError(ErrUnsupportedVersion, HeaderVersion, "expected "+Version)
	}
	if response {
		if _, ok, err := singleton(ph, HeaderFunction, false); err != nil {
			return nil, err
		} else if ok {
			return nil, protocolError(ErrUnexpectedFunction, HeaderFunction, "responses must not carry a function header")
		}
	}
	service, hasService, err := singleton(ph, HeaderService, false)
	if err != nil {
		return nil, err
	}
	dirs := make(directiveSet)
	if hasService {
		dirs, err = parseService(service, limits)
		if err != nil {
			return nil, err
		}
	}

	type argHeader struct {
		name  string
		value string
	}
	entries := make(map[string]argHeader)
	for lower, e := range ph {
		if lower == strings.ToLower(HeaderVersion) {
			continue
		}
		if lower == strings.ToLower(HeaderFunction) || lower == strings.ToLower(HeaderService) {
			continue
		}
		if !hasPrefixFold(e.original, ArgumentHeaderPrefix) {
			continue
		}
		if len(e.values) != 1 {
			return nil, protocolError(ErrDuplicateHeader, e.original, "argument header must occur exactly once")
		}
		name, err := decodeArgumentHeaderName(e.original, limits.MaxArgumentNameBytes)
		if err != nil {
			return nil, err
		}
		entries[lower] = argHeader{name: name, value: e.values[0]}
	}

	consumed := make(map[string]bool)
	logical := make(map[string]inboundArgument)
	decodedTotal := 0
	for name, d := range dirs {
		base := strings.ToLower(argumentHeaderName(name))
		var raw string
		if d.fragments != 0 {
			if _, exists := entries[base]; exists {
				return nil, protocolError(ErrBaseAndFragments, argumentHeaderName(name), "bare header is forbidden when reassemble is present")
			}
			var b strings.Builder
			for i := 1; i <= d.fragments; i++ {
				key := base + strconv.Itoa(i)
				e, ok := entries[key]
				if !ok {
					return nil, protocolError(ErrMissingFragment, argumentHeaderName(name), fmt.Sprintf("missing fragment %d of %d", i, d.fragments))
				}
				if err := validateFragmentWireValue(e.value, d.binary); err != nil {
					return nil, wrapProtocolError(ErrInvalidArgumentValue, e.name, "invalid fragment value", err)
				}
				b.WriteString(e.value)
				consumed[key] = true
			}
			for key := range entries {
				if key == base || !strings.HasPrefix(key, base) {
					continue
				}
				suffix := key[len(base):]
				if suffix == "" || !allDigits(suffix) {
					continue
				}
				if len(suffix) > 1 && suffix[0] == '0' {
					return nil, protocolError(ErrUnexpectedFragment, key, "fragment numbers must not contain leading zeros")
				}
				n, convErr := strconv.Atoi(suffix)
				if convErr != nil || n < 1 || n > d.fragments {
					return nil, protocolError(ErrUnexpectedFragment, key, "fragment number is outside the declared range")
				}
			}
			raw = b.String()
		} else {
			e, ok := entries[base]
			if !ok {
				return nil, protocolError(ErrMissingArgument, argumentHeaderName(name), "directive references a missing argument")
			}
			raw = e.value
			consumed[base] = true
		}

		arg := inboundArgument{raw: raw, binary: d.binary}
		if d.binary {
			decoded, err := decodeBase85Limit(raw, limits.MaxDecodedBytes-decodedTotal)
			if err != nil {
				return nil, wrapProtocolError(ErrInvalidBase85, name, "binary argument is not valid Base85", err)
			}
			decodedTotal += len(decoded)
			if decodedTotal > limits.MaxDecodedBytes {
				return nil, protocolError(ErrDecodedValueTooLarge, name, "decoded message exceeds configured limit")
			}
			arg.decoded = decoded
		} else {
			if err := validateTextValue(raw); err != nil {
				return nil, wrapProtocolError(ErrInvalidArgumentValue, name, "reassembled text is invalid", err)
			}
		}
		logical[name] = arg
	}

	for key, e := range entries {
		if consumed[key] {
			continue
		}
		if _, exists := logical[e.name]; exists {
			return nil, protocolError(ErrDirectiveCollision, e.name, "physical header aliases an already decoded logical argument")
		}
		if err := validateTextValue(e.value); err != nil {
			return nil, wrapProtocolError(ErrInvalidArgumentValue, e.name, "plain text argument is invalid", err)
		}
		logical[e.name] = inboundArgument{raw: e.value}
	}
	if len(logical) > limits.MaxArguments {
		return nil, protocolError(ErrTooManyArguments, "arguments", "logical argument count exceeds configured limit")
	}
	return &Message{args: logical}, nil
}

func validateFragmentWireValue(s string, binary bool) error {
	if binary {
		for i := 0; i < len(s); i++ {
			if base85DecodeTable[s[i]] < 0 {
				return protocolError(ErrInvalidBase85, "fragment", "binary fragment contains a non-Base85 character")
			}
		}
		return nil
	}
	return validateTextValue(s)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// FunctionPlacement controls where EncodeRequest writes the request type.
type FunctionPlacement uint8

const (
	FunctionInPath FunctionPlacement = iota + 1
	FunctionInHeader
	FunctionInBoth
)

// EncodeOptions control deterministic wire generation.
type EncodeOptions struct {
	Limits       Limits
	FragmentSize int
	Placement    FunctionPlacement
}

func normalizeEncodeOptions(o EncodeOptions) (EncodeOptions, error) {
	l, err := o.Limits.normalized()
	if err != nil {
		return EncodeOptions{}, err
	}
	o.Limits = l
	if o.FragmentSize == 0 {
		o.FragmentSize = min(4096, l.MaxHeaderValueBytes)
	}
	if o.FragmentSize <= 0 || o.FragmentSize > l.MaxHeaderValueBytes {
		return EncodeOptions{}, protocolError(ErrInvalidLimits, "FragmentSize", "must be 1..MaxHeaderValueBytes")
	}
	if o.Placement == 0 {
		o.Placement = FunctionInPath
	}
	if o.Placement < FunctionInPath || o.Placement > FunctionInBoth {
		return EncodeOptions{}, protocolError(ErrEncode, "Placement", "invalid function placement")
	}
	return o, nil
}

// EncodeRequest mutates r into a canonical bodyless KaleidoscopeRPC request.
// It owns and replaces the KaleidoscopeRPC header namespace, but leaves other
// headers untouched.
func EncodeRequest(r *http.Request, fn string, args *Args, options EncodeOptions) error {
	if r == nil || r.URL == nil {
		return protocolError(ErrEncode, "request", "request and URL must be non-nil")
	}
	o, err := normalizeEncodeOptions(options)
	if err != nil {
		return err
	}
	if !validFunctionName(fn, o.Limits.MaxFunctionNameBytes) {
		return protocolError(ErrInvalidFunction, fn, "function must match [A-Za-z0-9_]+ and configured length")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return protocolError(ErrQueryNotAllowed, "URL", "queries are not part of KaleidoscopeRPC v1")
	}
	if r.Body != nil && r.Body != http.NoBody {
		return protocolError(ErrBodyNotAllowed, "Body", "request body must be absent")
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		return protocolError(ErrBodyNotAllowed, "Body", "request body framing must be absent")
	}
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	clearProtocolHeaders(r.Header)
	r.Header.Set(HeaderVersion, Version)
	switch o.Placement {
	case FunctionInPath:
		r.URL.Path = "/" + fn
		r.URL.RawPath = ""
	case FunctionInHeader:
		r.URL.Path = "/"
		r.URL.RawPath = ""
		r.Header.Set(HeaderFunction, fn)
	case FunctionInBoth:
		r.URL.Path = "/" + fn
		r.URL.RawPath = ""
		r.Header.Set(HeaderFunction, fn)
	}
	encoded, err := encodeArgs(args, o)
	if err != nil {
		return err
	}
	mergeHeaders(r.Header, encoded)
	return nil
}

// EncodeHeaders writes only the version, service and argument fields. It is
// suitable for constructing a response or for integrations that own their own
// HTTP request target.
func EncodeHeaders(h http.Header, args *Args, options EncodeOptions) error {
	if h == nil {
		return protocolError(ErrEncode, "Header", "header map must be non-nil")
	}
	o, err := normalizeEncodeOptions(options)
	if err != nil {
		return err
	}
	clearProtocolHeaders(h)
	h.Set(HeaderVersion, Version)
	encoded, err := encodeArgs(args, o)
	if err != nil {
		return err
	}
	mergeHeaders(h, encoded)
	return nil
}

func encodeArgs(args *Args, o EncodeOptions) (http.Header, error) {
	h := make(http.Header)
	if args == nil || len(args.values) == 0 {
		return h, nil
	}
	if len(args.values) > o.Limits.MaxArguments {
		return nil, protocolError(ErrTooManyArguments, "arguments", "outbound argument count exceeds configured limit")
	}
	names := make([]string, 0, len(args.values))
	for name := range args.values {
		names = append(names, name)
	}
	sort.Strings(names)
	dirs := make(directiveSet)
	fields := 0
	total := 0
	decodedTotal := 0
	for _, name := range names {
		if err := validateArgumentName(name, o.Limits.MaxArgumentNameBytes); err != nil {
			return nil, err
		}
		v := args.values[name]
		var wire string
		if v.binary {
			if len(v.bytes) > o.Limits.MaxDecodedBytes-decodedTotal {
				return nil, protocolError(ErrDecodedValueTooLarge, name, "outbound decoded message exceeds configured limit")
			}
			decodedTotal += len(v.bytes)
			wire = EncodeBase85(v.bytes)
			d := dirs[name]
			d.binary = true
			dirs[name] = d
		} else {
			if err := validateTextValue(v.text); err != nil {
				return nil, wrapProtocolError(ErrInvalidArgumentValue, name, "invalid outbound text", err)
			}
			wire = v.text
		}
		base := argumentHeaderName(name)
		if len(wire) <= o.FragmentSize {
			h.Set(base, wire)
			fields++
			total += len(base) + len(wire)
		} else {
			chunks, err := splitWireValue(wire, o.FragmentSize, v.binary)
			if err != nil {
				return nil, wrapProtocolError(ErrEncode, name, "cannot safely fragment value", err)
			}
			if len(chunks) > o.Limits.MaxFragmentsPerArgument || len(chunks) > 1024 {
				return nil, protocolError(ErrInvalidFragmentCount, name, "value requires too many fragments")
			}
			d := dirs[name]
			d.fragments = len(chunks)
			dirs[name] = d
			for i, chunk := range chunks {
				key := base + strconv.Itoa(i+1)
				h.Set(key, chunk)
				fields++
				total += len(key) + len(chunk)
			}
		}
		if fields > o.Limits.MaxProtocolHeaderFields || total > o.Limits.MaxTotalWireBytes {
			return nil, protocolError(ErrMessageTooLarge, name, "outbound protocol headers exceed configured limit")
		}
	}
	directiveCount := 0
	for _, d := range dirs {
		if d.fragments != 0 {
			directiveCount++
		}
		if d.binary {
			directiveCount++
		}
	}
	if directiveCount > o.Limits.MaxDirectives {
		return nil, protocolError(ErrInvalidService, HeaderService, "outbound message needs too many directives")
	}
	if err := validateDirectiveCollisions(dirs); err != nil {
		return nil, err
	}
	service := formatService(dirs)
	if service != "" {
		if len(service) > o.Limits.MaxServiceBytes || len(service) > o.Limits.MaxHeaderValueBytes {
			return nil, protocolError(ErrHeaderValueTooLarge, HeaderService, "generated service header exceeds configured limit")
		}
		h.Set(HeaderService, service)
		fields++
		total += len(HeaderService) + len(service)
	}
	if fields > o.Limits.MaxProtocolHeaderFields || total > o.Limits.MaxTotalWireBytes {
		return nil, protocolError(ErrMessageTooLarge, "headers", "outbound protocol headers exceed configured limit")
	}
	return h, nil
}

func splitWireValue(s string, max int, binary bool) ([]string, error) {
	if s == "" {
		return []string{""}, nil
	}
	if binary {
		out := make([]string, 0, (len(s)+max-1)/max)
		for len(s) > max {
			out = append(out, s[:max])
			s = s[max:]
		}
		out = append(out, s)
		return out, nil
	}
	var out []string
	for len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		for cut > 0 && (s[cut-1] == ' ' || s[cut] == ' ') {
			cut--
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
		}
		if cut == 0 {
			return nil, protocolError(ErrInvalidArgumentValue, "text", "no lossless HTTP fragment boundary exists; encode the value with SetBytes")
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if s == "" || s[0] == ' ' || s[len(s)-1] == ' ' {
		return nil, protocolError(ErrInvalidArgumentValue, "text", "fragment would expose edge whitespace")
	}
	out = append(out, s)
	return out, nil
}

func clearProtocolHeaders(h http.Header) {
	for key := range h {
		if isProtocolHeaderName(key) {
			delete(h, key)
		}
	}
}

func mergeHeaders(dst, src http.Header) {
	for key, vals := range src {
		dst[key] = append([]string(nil), vals...)
	}
}

// DecodeRequest validates an incoming net/http request without reading its
// body. Known or unknown body framing is rejected before business logic runs.
func DecodeRequest(r *http.Request, limits Limits) (*Request, error) {
	if r == nil || r.URL == nil {
		return nil, protocolError(ErrInvalidPath, "URL", "request and URL must be non-nil")
	}
	l, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		return nil, protocolError(ErrBodyNotAllowed, "Body", "KaleidoscopeRPC requests must not carry a body")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, protocolError(ErrQueryNotAllowed, "URL", "queries are not part of v1")
	}
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	if escaped := r.URL.EscapedPath(); escaped != path {
		return nil, protocolError(ErrInvalidPath, "URL", "percent-encoded request types are forbidden")
	}
	var pathFn string
	if path == "/" {
		pathFn = ""
	} else if strings.HasPrefix(path, "/") && !strings.Contains(path[1:], "/") {
		pathFn = path[1:]
		if !validFunctionName(pathFn, l.MaxFunctionNameBytes) {
			return nil, protocolError(ErrInvalidFunction, pathFn, "invalid function in path")
		}
	} else {
		return nil, protocolError(ErrInvalidPath, "URL", "path must be / or /<function>")
	}
	ph, err := collectProtocolHeaders(r.Header, l)
	if err != nil {
		return nil, err
	}
	hdrFn, hasHdrFn, err := singleton(ph, HeaderFunction, false)
	if err != nil {
		return nil, err
	}
	if hasHdrFn && !validFunctionName(hdrFn, l.MaxFunctionNameBytes) {
		return nil, protocolError(ErrInvalidFunction, HeaderFunction, "invalid function header")
	}
	var fn string
	switch {
	case pathFn == "" && !hasHdrFn:
		return nil, protocolError(ErrMissingFunction, HeaderFunction, "function must be supplied by path or header")
	case pathFn != "" && hasHdrFn && pathFn != hdrFn:
		return nil, protocolError(ErrFunctionMismatch, HeaderFunction, "path and header functions differ")
	case pathFn != "":
		fn = pathFn
	default:
		fn = hdrFn
	}
	msg, err := decodeMessageHeaders(r.Header, l, false)
	if err != nil {
		return nil, err
	}
	return &Request{fn: fn, message: msg}, nil
}

// DecodedResponse is a validated protocol response including the underlying
// HTTP status code.
type DecodedResponse struct {
	StatusCode int
	message    *Message
}

func (r *DecodedResponse) Arg(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	return r.message.Arg(name)
}
func (r *DecodedResponse) Bytes(name string) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	return r.message.Bytes(name)
}
func (r *DecodedResponse) IsBinary(name string) bool { return r != nil && r.message.IsBinary(name) }
func (r *DecodedResponse) Names() []string {
	if r == nil {
		return nil
	}
	return r.message.Names()
}
func (r *DecodedResponse) OK() bool { return r != nil && r.StatusCode >= 200 && r.StatusCode < 300 }

// DecodeResponse validates response headers. It never reads the response body;
// callers should close it. Positive Content-Length or Transfer-Encoding is a
// protocol error. HTTP/2 cannot expose a future DATA frame from headers alone,
// so a peer can only be fully policed at the HTTP transport boundary.
func DecodeResponse(r *http.Response, limits Limits) (*DecodedResponse, error) {
	if r == nil {
		return nil, protocolError(ErrEncode, "response", "response must be non-nil")
	}
	l, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) != 0 {
		return nil, protocolError(ErrBodyNotAllowed, "Body", "KaleidoscopeRPC responses must not carry a body")
	}
	msg, err := decodeMessageHeaders(r.Header, l, true)
	if err != nil {
		return nil, err
	}
	return &DecodedResponse{StatusCode: r.StatusCode, message: msg}, nil
}

// ParseBaseURL validates the strict base URL accepted by Client.
func ParseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, protocolError(ErrEncode, "BaseURL", "must be an absolute http(s) URL")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, protocolError(ErrInvalidPath, "BaseURL", "v1 does not define nested base paths")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return nil, protocolError(ErrQueryNotAllowed, "BaseURL", "query and fragment are not allowed")
	}
	u.Path = "/"
	u.RawPath = ""
	return u, nil
}
