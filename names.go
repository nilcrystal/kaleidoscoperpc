package kaleidoscoperpc

import (
	"strings"
)

func validFunctionName(name string, max int) bool {
	if name == "" || len(name) > max {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isAlphaNum(c) && c != '_' {
			return false
		}
	}
	return true
}

func validArgumentName(name string, max int) bool {
	if name == "" || len(name) > max || name == "rpc" {
		return false
	}
	segmentStart := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			segmentStart = false
		case c == '_':
			if segmentStart {
				return false
			}
			segmentStart = true
		default:
			return false
		}
	}
	return !segmentStart
}

func validateArgumentName(name string, max int) error {
	if !validArgumentName(name, max) {
		return protocolError(ErrInvalidArgumentName, name, "must be canonical lower_snake_case using [a-z0-9] segments")
	}
	return nil
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func argumentHeaderName(name string) string {
	var b strings.Builder
	b.Grow(len(ArgumentHeaderPrefix) + len(name))
	b.WriteString(ArgumentHeaderPrefix)
	upperNext := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' {
			b.WriteByte('-')
			upperNext = true
			continue
		}
		if upperNext && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b.WriteByte(c)
		upperNext = false
	}
	return b.String()
}

func decodeArgumentHeaderName(header string, max int) (string, error) {
	if len(header) <= len(ArgumentHeaderPrefix) || !strings.EqualFold(header[:len(ArgumentHeaderPrefix)], ArgumentHeaderPrefix) {
		return "", protocolError(ErrInvalidArgumentName, header, "not an argument header")
	}
	suffix := header[len(ArgumentHeaderPrefix):]
	if len(suffix) > max {
		return "", protocolError(ErrInvalidArgumentName, header, "argument name exceeds configured limit")
	}
	var b strings.Builder
	b.Grow(len(suffix))
	segmentStart := true
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		switch {
		case c == '-':
			if segmentStart {
				return "", protocolError(ErrInvalidArgumentName, header, "non-canonical argument header")
			}
			b.WriteByte('_')
			segmentStart = true
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
			segmentStart = false
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
			segmentStart = false
		default:
			return "", protocolError(ErrInvalidArgumentName, header, "argument headers may contain only alphanumerics and hyphens")
		}
	}
	if segmentStart {
		return "", protocolError(ErrInvalidArgumentName, header, "non-canonical argument header")
	}
	name := b.String()
	if err := validateArgumentName(name, max); err != nil {
		return "", err
	}
	return name, nil
}
