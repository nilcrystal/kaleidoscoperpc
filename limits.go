package kaleidoscoperpc

import "fmt"

// Limits bounds all attacker-controlled work performed by the codec.
// Zero fields are replaced by DefaultLimits values.
type Limits struct {
	MaxArguments            int
	MaxDirectives           int
	MaxFragmentsPerArgument int
	MaxArgumentNameBytes    int
	MaxFunctionNameBytes    int
	MaxProtocolHeaderFields int
	MaxHeaderValueBytes     int
	MaxServiceBytes         int
	MaxTotalWireBytes       int
	MaxDecodedBytes         int
}

// DefaultLimits are conservative enough for common reverse proxies while
// still allowing large values through fragmentation. Raise them explicitly
// when your entire HTTP chain is configured for larger headers.
var DefaultLimits = Limits{
	MaxArguments:            128,
	MaxDirectives:           256,
	MaxFragmentsPerArgument: 1024,
	MaxArgumentNameBytes:    128,
	MaxFunctionNameBytes:    128,
	MaxProtocolHeaderFields: 2048,
	MaxHeaderValueBytes:     8 << 10,
	MaxServiceBytes:         8 << 10,
	MaxTotalWireBytes:       1 << 20,
	MaxDecodedBytes:         1 << 20,
}

func (l Limits) normalized() (Limits, error) {
	d := DefaultLimits
	fill := func(dst *int, def int) {
		if *dst == 0 {
			*dst = def
		}
	}
	fill(&l.MaxArguments, d.MaxArguments)
	fill(&l.MaxDirectives, d.MaxDirectives)
	fill(&l.MaxFragmentsPerArgument, d.MaxFragmentsPerArgument)
	fill(&l.MaxArgumentNameBytes, d.MaxArgumentNameBytes)
	fill(&l.MaxFunctionNameBytes, d.MaxFunctionNameBytes)
	fill(&l.MaxProtocolHeaderFields, d.MaxProtocolHeaderFields)
	fill(&l.MaxHeaderValueBytes, d.MaxHeaderValueBytes)
	fill(&l.MaxServiceBytes, d.MaxServiceBytes)
	fill(&l.MaxTotalWireBytes, d.MaxTotalWireBytes)
	fill(&l.MaxDecodedBytes, d.MaxDecodedBytes)

	vals := []struct {
		name string
		v    int
	}{
		{"MaxArguments", l.MaxArguments},
		{"MaxDirectives", l.MaxDirectives},
		{"MaxFragmentsPerArgument", l.MaxFragmentsPerArgument},
		{"MaxArgumentNameBytes", l.MaxArgumentNameBytes},
		{"MaxFunctionNameBytes", l.MaxFunctionNameBytes},
		{"MaxProtocolHeaderFields", l.MaxProtocolHeaderFields},
		{"MaxHeaderValueBytes", l.MaxHeaderValueBytes},
		{"MaxServiceBytes", l.MaxServiceBytes},
		{"MaxTotalWireBytes", l.MaxTotalWireBytes},
		{"MaxDecodedBytes", l.MaxDecodedBytes},
	}
	for _, x := range vals {
		if x.v <= 0 {
			return Limits{}, protocolError(ErrInvalidLimits, x.name, fmt.Sprintf("must be > 0, got %d", x.v))
		}
	}
	if l.MaxFragmentsPerArgument > 1024 {
		return Limits{}, protocolError(ErrInvalidLimits, "MaxFragmentsPerArgument", "v1 permits at most 1024 fragments")
	}
	if l.MaxDirectives < l.MaxArguments {
		// Legal configurations may choose fewer directives than arguments, so this
		// is not an error. Keep normalization intentionally non-coupled.
	}
	return l, nil
}

// Validate verifies the limit set after applying defaults.
func (l Limits) Validate() error {
	_, err := l.normalized()
	return err
}
