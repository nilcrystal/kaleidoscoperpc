package kaleidoscoperpc

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Base85Alphabet is the fixed v1 alphabet. It is the 85-character RFC 1924
// alphabet: every character is an HTTP VCHAR and comma, quote, backslash,
// slash and colon are intentionally absent. KaleidoscopeRPC uses the alphabet
// with 32-bit groups and no shorthand characters.
const Base85Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~"

var base85DecodeTable = func() [256]int16 {
	var table [256]int16
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < len(Base85Alphabet); i++ {
		table[Base85Alphabet[i]] = int16(i)
	}
	return table
}()

// EncodeBase85 encodes arbitrary bytes using the KaleidoscopeRPC v1 Base85
// representation. The empty byte slice encodes as the empty string.
func EncodeBase85(src []byte) string {
	if len(src) == 0 {
		return ""
	}
	full := len(src) / 4
	rem := len(src) % 4
	outLen := full * 5
	if rem != 0 {
		outLen += rem + 1
	}
	out := make([]byte, 0, outLen)
	for len(src) >= 4 {
		out = appendBase85Block(out, binary.BigEndian.Uint32(src[:4]), 5)
		src = src[4:]
	}
	if len(src) != 0 {
		var block [4]byte
		copy(block[:], src)
		out = appendBase85Block(out, binary.BigEndian.Uint32(block[:]), len(src)+1)
	}
	return string(out)
}

func appendBase85Block(dst []byte, v uint32, n int) []byte {
	var digits [5]byte
	x := uint64(v)
	for i := 4; i >= 0; i-- {
		digits[i] = Base85Alphabet[x%85]
		x /= 85
	}
	return append(dst, digits[:n]...)
}

// DecodeBase85 strictly decodes a v1 Base85 value. Non-canonical partial
// groups, invalid characters, one-character tails and values above uint32 are
// rejected.
func DecodeBase85(s string) ([]byte, error) {
	return decodeBase85Limit(s, int(^uint(0)>>1))
}

func decodeBase85Limit(s string, limit int) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}
	rem := len(s) % 5
	if rem == 1 {
		return nil, protocolError(ErrInvalidBase85, HeaderService, "base85 has a one-character trailing group")
	}
	full := len(s) / 5
	if full > (int(^uint(0)>>1)-4)/4 {
		return nil, protocolError(ErrDecodedValueTooLarge, "base85", "decoded length overflows int")
	}
	outLen := full * 4
	if rem != 0 {
		outLen += rem - 1
	}
	if outLen > limit {
		return nil, protocolError(ErrDecodedValueTooLarge, "base85", fmt.Sprintf("decoded value exceeds %d bytes", limit))
	}
	out := make([]byte, 0, outLen)
	pos := 0
	for ; pos+5 <= len(s); pos += 5 {
		v, err := decodeBase85Block(s[pos : pos+5])
		if err != nil {
			return nil, err
		}
		var block [4]byte
		binary.BigEndian.PutUint32(block[:], v)
		out = append(out, block[:]...)
	}
	if pos < len(s) {
		tail := s[pos:]
		var digits [5]byte
		copy(digits[:], tail)
		for i := len(tail); i < 5; i++ {
			digits[i] = Base85Alphabet[84]
		}
		v, err := decodeBase85Block(string(digits[:]))
		if err != nil {
			return nil, err
		}
		var block [4]byte
		binary.BigEndian.PutUint32(block[:], v)
		partial := block[:len(tail)-1]
		// Strict canonicality matters because multiple textual forms for the
		// same bytes make signatures and cache keys unsafe.
		if EncodeBase85(partial) != tail {
			return nil, protocolError(ErrInvalidBase85, "base85", "non-canonical trailing group")
		}
		out = append(out, partial...)
	}
	return out, nil
}

func decodeBase85Block(block string) (uint32, error) {
	if len(block) != 5 {
		return 0, protocolError(ErrInvalidBase85, "base85", "internal block must contain five characters")
	}
	var v uint64
	for i := 0; i < 5; i++ {
		c := block[i]
		d := base85DecodeTable[c]
		if d < 0 {
			return 0, protocolError(ErrInvalidBase85, "base85", fmt.Sprintf("invalid character 0x%02x", c))
		}
		v = v*85 + uint64(d)
		if v > math.MaxUint32 {
			return 0, protocolError(ErrInvalidBase85, "base85", "five-character group exceeds uint32")
		}
	}
	return uint32(v), nil
}
