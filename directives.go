package kaleidoscoperpc

import (
	"fmt"
	"strconv"
	"strings"
)

type directiveSpec struct {
	fragments int
	binary    bool
}

type directiveSet map[string]directiveSpec

type serviceParser struct {
	s      string
	pos    int
	limits Limits
	count  int
}

func parseService(s string, limits Limits) (directiveSet, error) {
	if len(s) > limits.MaxServiceBytes {
		return nil, protocolError(ErrHeaderValueTooLarge, HeaderService, "service header exceeds configured limit")
	}
	p := serviceParser{s: s, limits: limits}
	p.ws()
	if p.eof() {
		return nil, protocolError(ErrInvalidService, HeaderService, "empty service header")
	}
	out := make(directiveSet)
	for {
		name, args, err := p.directive()
		if err != nil {
			return nil, err
		}
		p.count++
		if p.count > limits.MaxDirectives {
			return nil, protocolError(ErrInvalidService, HeaderService, "too many directives")
		}
		switch name {
		case "binary":
			if len(args) != 1 {
				return nil, protocolError(ErrInvalidService, HeaderService, "binary requires exactly one argument")
			}
			arg := args[0]
			if err := validateArgumentName(arg, limits.MaxArgumentNameBytes); err != nil {
				return nil, wrapProtocolError(ErrInvalidService, HeaderService, "invalid binary argument name", err)
			}
			d := out[arg]
			if d.binary {
				return nil, protocolError(ErrDuplicateDirective, HeaderService, "duplicate binary("+arg+")")
			}
			d.binary = true
			out[arg] = d
		case "reassemble":
			if len(args) != 2 {
				return nil, protocolError(ErrInvalidService, HeaderService, "reassemble requires exactly two arguments")
			}
			arg := args[0]
			if err := validateArgumentName(arg, limits.MaxArgumentNameBytes); err != nil {
				return nil, wrapProtocolError(ErrInvalidService, HeaderService, "invalid reassemble argument name", err)
			}
			ns := args[1]
			if ns == "" || (len(ns) > 1 && ns[0] == '0') {
				return nil, protocolError(ErrInvalidFragmentCount, HeaderService, "fragment count must be canonical decimal")
			}
			for i := 0; i < len(ns); i++ {
				if ns[i] < '0' || ns[i] > '9' {
					return nil, protocolError(ErrInvalidFragmentCount, HeaderService, "fragment count must contain only decimal digits")
				}
			}
			n, err := strconv.Atoi(ns)
			if err != nil || n < 1 || n > limits.MaxFragmentsPerArgument || n > 1024 {
				return nil, protocolError(ErrInvalidFragmentCount, HeaderService, fmt.Sprintf("fragment count must be 1..%d", min(limits.MaxFragmentsPerArgument, 1024)))
			}
			d := out[arg]
			if d.fragments != 0 {
				return nil, protocolError(ErrDuplicateDirective, HeaderService, "duplicate reassemble("+arg+")")
			}
			d.fragments = n
			out[arg] = d
		default:
			return nil, protocolError(ErrUnknownDirective, HeaderService, "unknown directive "+name)
		}

		p.ws()
		if p.eof() {
			break
		}
		if p.s[p.pos] != ',' {
			return nil, protocolError(ErrInvalidService, HeaderService, "expected comma between directives")
		}
		p.pos++
		p.ws()
		if p.eof() {
			return nil, protocolError(ErrInvalidService, HeaderService, "trailing comma")
		}
	}
	if err := validateDirectiveCollisions(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *serviceParser) directive() (string, []string, error) {
	name := p.word()
	if name == "" {
		return "", nil, protocolError(ErrInvalidService, HeaderService, "expected directive name")
	}
	if p.eof() || p.s[p.pos] != '(' {
		return "", nil, protocolError(ErrInvalidService, HeaderService, "expected '('")
	}
	p.pos++
	p.ws()
	var args []string
	if !p.eof() && p.s[p.pos] != ')' {
		for {
			w := p.word()
			if w == "" {
				return "", nil, protocolError(ErrInvalidService, HeaderService, "expected directive argument")
			}
			args = append(args, w)
			p.ws()
			if p.eof() {
				return "", nil, protocolError(ErrInvalidService, HeaderService, "unterminated directive")
			}
			if p.s[p.pos] == ')' {
				break
			}
			if p.s[p.pos] != ',' {
				return "", nil, protocolError(ErrInvalidService, HeaderService, "expected comma inside directive")
			}
			p.pos++
			p.ws()
		}
	}
	if p.eof() || p.s[p.pos] != ')' {
		return "", nil, protocolError(ErrInvalidService, HeaderService, "unterminated directive")
	}
	p.pos++
	return name, args, nil
}

func (p *serviceParser) word() string {
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if isAlphaNum(c) || c == '_' {
			p.pos++
			continue
		}
		break
	}
	return p.s[start:p.pos]
}

func (p *serviceParser) ws() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}
func (p *serviceParser) eof() bool { return p.pos >= len(p.s) }

type directiveClaim struct {
	name      string
	base      string
	fragments int
}

func validateDirectiveCollisions(dirs directiveSet) error {
	// A reassemble(name,N) reserves base(name) plus base(name)+1..N.
	// Do not materialize all N field names: a tiny service header could
	// otherwise amplify into hundreds of thousands of temporary strings.
	claims := make([]directiveClaim, 0, len(dirs))
	for name, d := range dirs {
		if d.fragments != 0 || d.binary {
			claims = append(claims, directiveClaim{name: name, base: strings.ToLower(argumentHeaderName(name)), fragments: d.fragments})
		}
	}
	for i := 0; i < len(claims); i++ {
		for j := i + 1; j < len(claims); j++ {
			a, b := claims[i], claims[j]
			if a.base == b.base || reservedFragmentEqualsBase(a, b.base) || reservedFragmentEqualsBase(b, a.base) {
				return protocolError(ErrDirectiveCollision, HeaderService, "header reservation collision between "+a.name+" and "+b.name)
			}
		}
	}
	return nil
}

func reservedFragmentEqualsBase(owner directiveClaim, otherBase string) bool {
	if owner.fragments == 0 || !strings.HasPrefix(otherBase, owner.base) {
		return false
	}
	suffix := otherBase[len(owner.base):]
	if suffix == "" || !allDigits(suffix) || (len(suffix) > 1 && suffix[0] == '0') {
		return false
	}
	n, err := strconv.Atoi(suffix)
	return err == nil && n >= 1 && n <= owner.fragments
}

func formatService(dirs directiveSet) string {
	if len(dirs) == 0 {
		return ""
	}
	names := make([]string, 0, len(dirs))
	for name := range dirs {
		names = append(names, name)
	}
	slicesSort(names)
	parts := make([]string, 0, len(dirs)*2)
	for _, name := range names {
		d := dirs[name]
		if d.fragments != 0 {
			parts = append(parts, fmt.Sprintf("reassemble(%s, %d)", name, d.fragments))
		}
		if d.binary {
			parts = append(parts, "binary("+name+")")
		}
	}
	return strings.Join(parts, ", ")
}

// tiny local sort avoids importing slices solely for one operation while
// keeping Go 1.23 compatibility explicit.
func slicesSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
