package kaleidoscoperpc

import "testing"

func testLimits(t *testing.T) Limits {
	t.Helper()
	l, err := (Limits{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestParseService(t *testing.T) {
	l := testLimits(t)
	d, err := parseService(" \t reassemble(user_information, 3), binary(data_blob) \t", l)
	if err != nil {
		t.Fatal(err)
	}
	if d["user_information"].fragments != 3 {
		t.Fatalf("wrong fragments: %+v", d)
	}
	if !d["data_blob"].binary {
		t.Fatalf("wrong binary: %+v", d)
	}
}

func TestParseServiceAllowsBinaryReassembleSameName(t *testing.T) {
	l := testLimits(t)
	d, err := parseService("reassemble(payload, 4), binary(payload)", l)
	if err != nil {
		t.Fatal(err)
	}
	if d["payload"].fragments != 4 || !d["payload"].binary {
		t.Fatalf("wrong directives: %+v", d)
	}
}

func TestParseServiceRejectsMalformed(t *testing.T) {
	l := testLimits(t)
	cases := []string{
		"", ",", "binary", "binary()", "binary(a,b)", "Binary(a)",
		"unknown(a)", "reassemble(a)", "reassemble(a,0)", "reassemble(a,01)",
		"reassemble(a,1025)", "reassemble(a,x)", "binary(a),", "binary(a) binary(b)",
		"binary(a),binary(a)", "reassemble(a,1),reassemble(a,2)",
		"binary(rpc)", "reassemble(A,2)",
	}
	for _, s := range cases {
		if _, err := parseService(s, l); err == nil {
			t.Errorf("parseService(%q) succeeded", s)
		}
	}
}

func TestDirectiveCollision(t *testing.T) {
	l := testLimits(t)
	for _, s := range []string{
		"reassemble(a, 1), binary(a1)",
		"reassemble(a, 12), reassemble(a1, 2)",
	} {
		if _, err := parseService(s, l); err == nil {
			t.Errorf("expected collision for %q", s)
		}
	}
}

func TestFormatServiceDeterministic(t *testing.T) {
	d := directiveSet{
		"z": {binary: true},
		"a": {fragments: 2, binary: true},
	}
	if got, want := formatService(d), "reassemble(a, 2), binary(a), binary(z)"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
