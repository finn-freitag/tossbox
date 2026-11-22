package secrets

import (
	"strings"
	"testing"
)

func TestBoxRoundTrip(t *testing.T) {
	b, err := NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := b.Encrypt("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Decrypt(enc)
	if err != nil || got != "hunter2" {
		t.Fatalf("got %q %v", got, err)
	}
	if e, _ := b.Encrypt(""); e != "" {
		t.Fatal("empty should stay empty")
	}
}

func TestPassword(t *testing.T) {
	for i := 0; i < 200; i++ {
		p, err := Password(24)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 24 {
			t.Fatalf("len %d", len(p))
		}
		for _, set := range []string{lower, upper, digits, symbols} {
			if !strings.ContainsAny(p, set) {
				t.Fatalf("%q misses class %q", p, set)
			}
		}
		if !strings.ContainsAny(p[:1], lower+upper) {
			t.Fatalf("%q starts with non-letter", p)
		}
	}
	if p, _ := Password(5); len(p) != 20 {
		t.Fatal("min length 20")
	}
}

func TestParseKey(t *testing.T) {
	if _, err := ParseKey(strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKey("short"); err == nil {
		t.Fatal("expected error")
	}
}
