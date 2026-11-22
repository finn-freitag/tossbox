package clientip

import (
	"net/http"
	"net/netip"
	"testing"
)

func TestFrom(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("172.16.0.0/12")}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "", "203.0.113.5"},
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"}, // untrusted peer: header ignored
		{"127.0.0.1:1234", "198.51.100.7", "198.51.100.7"},
		{"127.0.0.1:1234", "6.6.6.6, 198.51.100.7, 172.18.0.2", "198.51.100.7"}, // spoofed left entry ignored
		{"127.0.0.1:1234", "garbage", "127.0.0.1"},
		{"127.0.0.1:1234", "", "127.0.0.1"},
		{"[::ffff:127.0.0.1]:80", "2001:db8::1", "2001:db8::1"},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := From(r, trusted).String(); got != c.want {
			t.Errorf("%s / %q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}
