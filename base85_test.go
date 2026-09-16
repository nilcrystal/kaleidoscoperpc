package kaleidoscoperpc

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

func TestBase85Alphabet(t *testing.T) {
	if got := len(Base85Alphabet); got != 85 {
		t.Fatalf("alphabet length = %d, want 85", got)
	}
	seen := map[byte]bool{}
	for i := 0; i < len(Base85Alphabet); i++ {
		c := Base85Alphabet[i]
		if c < 0x21 || c > 0x7e {
			t.Fatalf("alphabet contains non-VCHAR 0x%02x", c)
		}
		if seen[c] {
			t.Fatalf("duplicate alphabet character %q", c)
		}
		seen[c] = true
	}
	for _, forbidden := range []byte{',', '"', '\\', '/', ':'} {
		if seen[forbidden] {
			t.Fatalf("alphabet unexpectedly contains %q", forbidden)
		}
	}
}

func TestBase85GoldenVectors(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{nil, ""},
		{[]byte{0}, "00"},
		{[]byte{1}, "0R"},
		{[]byte{255}, "{{"},
		{[]byte{0, 0, 0, 0}, "00000"},
		{[]byte("Hello"), "NM&qnZv"},
		{[]byte("Hello World"), "NM&qnZy;B1a%^M"},
		{[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, "009C61O)~M2nh-c3=Iws"},
	}
	for _, tc := range cases {
		if got := EncodeBase85(tc.in); got != tc.want {
			t.Fatalf("EncodeBase85(%x) = %q, want %q", tc.in, got, tc.want)
		}
		got, err := DecodeBase85(tc.want)
		if err != nil {
			t.Fatalf("DecodeBase85(%q): %v", tc.want, err)
		}
		if !bytes.Equal(got, tc.in) {
			t.Fatalf("DecodeBase85(%q) = %x, want %x", tc.want, got, tc.in)
		}
	}
}

func TestBase85ExhaustiveShortInputs(t *testing.T) {
	for x := 0; x < 256; x++ {
		in := []byte{byte(x)}
		got, err := DecodeBase85(EncodeBase85(in))
		if err != nil || !bytes.Equal(got, in) {
			t.Fatalf("1-byte round trip %x: %x, %v", in, got, err)
		}
	}
	for x := 0; x <= 0xffff; x++ {
		in := []byte{byte(x >> 8), byte(x)}
		got, err := DecodeBase85(EncodeBase85(in))
		if err != nil || !bytes.Equal(got, in) {
			t.Fatalf("2-byte round trip %x: %x, %v", in, got, err)
		}
	}
}

func TestBase85RandomRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for n := 0; n <= 4096; n++ {
		if n > 256 && n%31 != 0 {
			continue
		}
		in := make([]byte, n)
		_, _ = r.Read(in)
		enc := EncodeBase85(in)
		got, err := DecodeBase85(enc)
		if err != nil {
			t.Fatalf("length %d: %v", n, err)
		}
		if !bytes.Equal(got, in) {
			t.Fatalf("length %d mismatch", n)
		}
	}
}

func TestBase85RejectsMalformed(t *testing.T) {
	for _, s := range []string{
		"0",      // impossible one-char tail
		"01",     // valid digits, non-canonical partial
		"~~~~~",  // > uint32
		"abcd,",  // comma is not in alphabet
		"abc d",  // whitespace
		"abcd/",  // slash is not in alphabet
		"abcd\\", // backslash is not in alphabet
		strings.Repeat("~", 10),
	} {
		if _, err := DecodeBase85(s); err == nil {
			t.Fatalf("DecodeBase85(%q) unexpectedly succeeded", s)
		}
	}
	max, err := DecodeBase85("|NsC0")
	if err != nil || !bytes.Equal(max, []byte{0xff, 0xff, 0xff, 0xff}) {
		t.Fatalf("max uint32 vector: %x, %v", max, err)
	}
}

func TestBase85Limit(t *testing.T) {
	enc := EncodeBase85(make([]byte, 16))
	if _, err := decodeBase85Limit(enc, 15); err == nil {
		t.Fatal("expected decoded size limit error")
	}
}
