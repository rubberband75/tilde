package ringbuf

import (
	"bytes"
	"testing"
)

func TestBuffer(t *testing.T) {
	tests := []struct {
		name   string
		cap    int
		writes []string
		want   string
	}{
		{"empty", 8, nil, ""},
		{"under capacity", 8, []string{"abc", "de"}, "abcde"},
		{"exactly full", 4, []string{"ab", "cd"}, "abcd"},
		{"wraps", 4, []string{"abc", "def"}, "cdef"},
		{"single write larger than capacity", 4, []string{"abcdefgh"}, "efgh"},
		{"many small wraps", 3, []string{"a", "b", "c", "d", "e"}, "cde"},
		{"zero capacity", 0, []string{"abc"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(tt.cap)
			for _, w := range tt.writes {
				n, err := b.Write([]byte(w))
				if err != nil || n != len(w) {
					t.Fatalf("Write(%q) = %d, %v", w, n, err)
				}
			}
			if got := string(b.Bytes()); got != tt.want {
				t.Fatalf("Bytes() = %q, want %q", got, tt.want)
			}
			if b.Len() != len(tt.want) {
				t.Fatalf("Len() = %d, want %d", b.Len(), len(tt.want))
			}
		})
	}
}

func TestBytesIsACopy(t *testing.T) {
	b := New(4)
	b.Write([]byte("abcd"))
	out := b.Bytes()
	out[0] = 'z'
	if !bytes.Equal(b.Bytes(), []byte("abcd")) {
		t.Fatal("Bytes() must return a copy")
	}
}

func TestReset(t *testing.T) {
	b := New(4)
	b.Write([]byte("abc"))
	b.Reset()
	b.Write([]byte("xy"))
	if got := string(b.Bytes()); got != "xy" {
		t.Fatalf("after Reset got %q", got)
	}
}
