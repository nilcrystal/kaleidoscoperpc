package kaleidoscoperpc

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable machine-readable category for a protocol failure.
type ErrorCode string

const (
	ErrInvalidLimits          ErrorCode = "invalid_limits"
	ErrMissingVersion         ErrorCode = "missing_version"
	ErrDuplicateHeader        ErrorCode = "duplicate_header"
	ErrUnsupportedVersion     ErrorCode = "unsupported_version"
	ErrInvalidFunction        ErrorCode = "invalid_function"
	ErrMissingFunction        ErrorCode = "missing_function"
	ErrFunctionMismatch       ErrorCode = "function_mismatch"
	ErrInvalidPath            ErrorCode = "invalid_path"
	ErrQueryNotAllowed        ErrorCode = "query_not_allowed"
	ErrBodyNotAllowed         ErrorCode = "body_not_allowed"
	ErrUnexpectedFunction     ErrorCode = "unexpected_function"
	ErrInvalidArgumentName    ErrorCode = "invalid_argument_name"
	ErrInvalidArgumentValue   ErrorCode = "invalid_argument_value"
	ErrTooManyArguments       ErrorCode = "too_many_arguments"
	ErrTooManyProtocolHeaders ErrorCode = "too_many_protocol_headers"
	ErrHeaderValueTooLarge    ErrorCode = "header_value_too_large"
	ErrMessageTooLarge        ErrorCode = "message_too_large"
	ErrInvalidService         ErrorCode = "invalid_service"
	ErrUnknownDirective       ErrorCode = "unknown_directive"
	ErrDuplicateDirective     ErrorCode = "duplicate_directive"
	ErrInvalidFragmentCount   ErrorCode = "invalid_fragment_count"
	ErrDirectiveCollision     ErrorCode = "directive_collision"
	ErrFieldCollision         ErrorCode = "field_collision"
	ErrMissingArgument        ErrorCode = "missing_argument"
	ErrMissingFragment        ErrorCode = "missing_fragment"
	ErrUnexpectedFragment     ErrorCode = "unexpected_fragment"
	ErrBaseAndFragments       ErrorCode = "base_and_fragments"
	ErrInvalidBase85          ErrorCode = "invalid_base85"
	ErrDecodedValueTooLarge   ErrorCode = "decoded_value_too_large"
	ErrEncode                 ErrorCode = "encode_error"
)

// ProtocolError reports a malformed or unrepresentable KaleidoscopeRPC
// message. Code is stable; Detail is intended for logs, not for protocol
// responses.
type ProtocolError struct {
	Code   ErrorCode
	Field  string
	Detail string
	Err    error
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return "<nil>"
	}
	base := "kaleidoscoperpc: " + string(e.Code)
	if e.Field != "" {
		base += " (" + e.Field + ")"
	}
	if e.Detail != "" {
		base += ": " + e.Detail
	}
	return base
}

func (e *ProtocolError) Unwrap() error { return e.Err }

func protocolError(code ErrorCode, field, detail string) error {
	return &ProtocolError{Code: code, Field: field, Detail: detail}
}

func wrapProtocolError(code ErrorCode, field, detail string, err error) error {
	return &ProtocolError{Code: code, Field: field, Detail: detail, Err: err}
}

// IsProtocolError reports whether err or one of its causes is a ProtocolError.
func IsProtocolError(err error) bool {
	var pe *ProtocolError
	return errors.As(err, &pe)
}

// ProtocolErrorCode extracts the first ProtocolError code from err.
func ProtocolErrorCode(err error) (ErrorCode, bool) {
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		return "", false
	}
	return pe.Code, true
}

// StatusError lets a business handler deliberately return a non-200 HTTP
// status while still carrying a valid KaleidoscopeRPC header response.
type StatusError struct {
	Status   int
	Response *Response
	Err      error
}

func (e *StatusError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("kaleidoscoperpc: status %d: %v", e.Status, e.Err)
	}
	return fmt.Sprintf("kaleidoscoperpc: status %d", e.Status)
}

func (e *StatusError) Unwrap() error { return e.Err }

// PanicError is reported to Server.OnError when a business handler panics.
// Recovered is deliberately typed as any so callers can log it with their
// own redaction policy.
type PanicError struct {
	Recovered any
	Stack     []byte
}

func (e *PanicError) Error() string { return "kaleidoscoperpc: handler panic recovered" }
