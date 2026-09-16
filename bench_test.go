package kaleidoscoperpc

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkBase85Encode1KiB(b *testing.B) {
	in := bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 128)
	b.ReportAllocs()
	b.SetBytes(int64(len(in)))
	for i := 0; i < b.N; i++ {
		_ = EncodeBase85(in)
	}
}

func BenchmarkBase85Decode1KiB(b *testing.B) {
	enc := EncodeBase85(bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 128))
	b.ReportAllocs()
	b.SetBytes(1024)
	for i := 0; i < b.N; i++ {
		if _, err := DecodeBase85(enc); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeRequest64KiBBinary(b *testing.B) {
	a := NewArgs()
	_ = a.SetBytes("payload", bytes.Repeat([]byte{0xab}, 64<<10))
	r := httptest.NewRequest(http.MethodPost, "http://example/", nil)
	if err := EncodeRequest(r, "store", a, EncodeOptions{FragmentSize: 4096, Limits: Limits{MaxDecodedBytes: 128 << 10, MaxTotalWireBytes: 256 << 10}}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(64 << 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeRequest(r, Limits{MaxDecodedBytes: 128 << 10, MaxTotalWireBytes: 256 << 10}); err != nil {
			b.Fatal(err)
		}
	}
}
