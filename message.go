package kaleidoscoperpc

import (
	"sort"
	"unicode/utf8"
)

type inboundArgument struct {
	raw     string
	decoded []byte
	binary  bool
}

// Message is an immutable, fully validated logical argument set.
type Message struct {
	args map[string]inboundArgument
}

// Arg returns the logical textual representation. For a binary argument this
// is the reassembled Base85 wire representation; Bytes returns the decoded
// payload.
func (m *Message) Arg(name string) (string, bool) {
	if m == nil {
		return "", false
	}
	a, ok := m.args[name]
	if !ok {
		return "", false
	}
	return a.raw, true
}

// Bytes returns decoded bytes for binary arguments and the UTF-8 bytes of a
// text argument otherwise. The returned slice is always a defensive copy.
func (m *Message) Bytes(name string) ([]byte, bool) {
	if m == nil {
		return nil, false
	}
	a, ok := m.args[name]
	if !ok {
		return nil, false
	}
	if a.binary {
		return append([]byte(nil), a.decoded...), true
	}
	return []byte(a.raw), true
}

// IsBinary reports whether the sender marked an argument with binary(name).
func (m *Message) IsBinary(name string) bool {
	if m == nil {
		return false
	}
	a, ok := m.args[name]
	return ok && a.binary
}

// Names returns all logical argument names in deterministic lexical order.
func (m *Message) Names() []string {
	if m == nil || len(m.args) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.args))
	for name := range m.args {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type outboundArgument struct {
	text   string
	bytes  []byte
	binary bool
}

// Args is a mutable outbound argument builder. It is intentionally not safe
// for concurrent mutation; construct it per call/response and then hand it to
// the encoder.
type Args struct {
	values map[string]outboundArgument
}

// NewArgs returns an empty argument builder.
func NewArgs() *Args { return &Args{values: make(map[string]outboundArgument)} }

func (a *Args) ensure() {
	if a.values == nil {
		a.values = make(map[string]outboundArgument)
	}
}

// Set adds or replaces a textual argument. Text must be valid UTF-8, contain
// no control characters, and must not begin or end with ASCII whitespace.
// Use SetBytes for arbitrary octets or values that do not satisfy those rules.
func (a *Args) Set(name, value string) error {
	if a == nil {
		return protocolError(ErrEncode, "Args", "nil argument builder")
	}
	if err := validateArgumentName(name, int(^uint(0)>>1)); err != nil {
		return err
	}
	if err := validateTextValue(value); err != nil {
		return wrapProtocolError(ErrInvalidArgumentValue, name, "invalid text argument", err)
	}
	a.ensure()
	a.values[name] = outboundArgument{text: value}
	return nil
}

// SetBytes adds or replaces an arbitrary byte argument. The payload is copied
// and will be represented with binary(name) on the wire.
func (a *Args) SetBytes(name string, value []byte) error {
	if a == nil {
		return protocolError(ErrEncode, "Args", "nil argument builder")
	}
	if err := validateArgumentName(name, int(^uint(0)>>1)); err != nil {
		return err
	}
	a.ensure()
	a.values[name] = outboundArgument{bytes: append([]byte(nil), value...), binary: true}
	return nil
}

// Delete removes an argument.
func (a *Args) Delete(name string) {
	if a != nil {
		delete(a.values, name)
	}
}

// Len returns the number of logical arguments.
func (a *Args) Len() int {
	if a == nil {
		return 0
	}
	return len(a.values)
}

// Clone returns an independent builder including copies of binary payloads.
func (a *Args) Clone() *Args {
	out := NewArgs()
	if a == nil {
		return out
	}
	for k, v := range a.values {
		v.bytes = append([]byte(nil), v.bytes...)
		out.values[k] = v
	}
	return out
}

func validateTextValue(s string) error {
	if !utf8.ValidString(s) {
		return protocolError(ErrInvalidArgumentValue, "text", "value is not valid UTF-8")
	}
	if len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		return protocolError(ErrInvalidArgumentValue, "text", "leading or trailing whitespace is not lossless in HTTP field values")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return protocolError(ErrInvalidArgumentValue, "text", "control characters are not permitted")
		}
		if r == ',' {
			return protocolError(ErrInvalidArgumentValue, "text", "comma is reserved so intermediaries cannot hide duplicate singleton fields")
		}
	}
	return nil
}

// Request is the transport-independent input given to business handlers.
type Request struct {
	fn      string
	message *Message
}

func (r *Request) Fn() string {
	if r == nil {
		return ""
	}
	return r.fn
}
func (r *Request) Arg(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	return r.message.Arg(name)
}
func (r *Request) Bytes(name string) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	return r.message.Bytes(name)
}
func (r *Request) IsBinary(name string) bool { return r != nil && r.message.IsBinary(name) }
func (r *Request) Names() []string {
	if r == nil {
		return nil
	}
	return r.message.Names()
}

// Response is a business-layer response. Server serializes it only after the
// handler has returned successfully, so partial wire responses are impossible.
type Response struct{ args *Args }

func NewResponse() *Response { return &Response{args: NewArgs()} }
func (r *Response) ensure() {
	if r.args == nil {
		r.args = NewArgs()
	}
}
func (r *Response) Set(name, value string) error {
	if r == nil {
		return protocolError(ErrEncode, "Response", "nil response")
	}
	r.ensure()
	return r.args.Set(name, value)
}
func (r *Response) SetBytes(name string, value []byte) error {
	if r == nil {
		return protocolError(ErrEncode, "Response", "nil response")
	}
	r.ensure()
	return r.args.SetBytes(name, value)
}
func (r *Response) Delete(name string) {
	if r != nil && r.args != nil {
		r.args.Delete(name)
	}
}
func (r *Response) Args() *Args {
	if r == nil {
		return nil
	}
	r.ensure()
	return r.args
}
