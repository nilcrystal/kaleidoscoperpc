package kaleidoscoperpc

import "testing"

func TestFunctionNames(t *testing.T) {
	for _, s := range []string{"echo", "ping_2", "v2Lookup", "_x", "9"} {
		if !validFunctionName(s, 128) {
			t.Errorf("valid function rejected: %q", s)
		}
	}
	for _, s := range []string{"", "a-b", "a b", "å", "a/b"} {
		if validFunctionName(s, 128) {
			t.Errorf("invalid function accepted: %q", s)
		}
	}
}

func TestArgumentNameRoundTrip(t *testing.T) {
	for _, name := range []string{"answer", "data_blob", "zero_data", "a1_b2", "9"} {
		if err := validateArgumentName(name, 128); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		h := argumentHeaderName(name)
		got, err := decodeArgumentHeaderName(h, 128)
		if err != nil || got != name {
			t.Fatalf("%q -> %q -> %q, %v", name, h, got, err)
		}
	}
}

func TestArgumentNamesRejectAmbiguousForms(t *testing.T) {
	for _, name := range []string{"rpc", "ZeroData", "zeroData", "_x", "x_", "x__y", "x-y", "x y", ""} {
		if err := validateArgumentName(name, 128); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, h := range []string{
		"X-Kaleidoscope-Foo_Bar",
		"X-Kaleidoscope--Foo",
		"X-Kaleidoscope-Foo--Bar",
		"X-Kaleidoscope-Foo-",
	} {
		if _, err := decodeArgumentHeaderName(h, 128); err == nil {
			t.Errorf("accepted header %q", h)
		}
	}
}

func TestArgumentHeaderCaseIsSemanticallyIgnored(t *testing.T) {
	got, err := decodeArgumentHeaderName("x-kAlEiDoScOpE-uSeR-iNfOrMaTiOn", 128)
	if err != nil || got != "user_information" {
		t.Fatalf("got %q, %v", got, err)
	}
}
