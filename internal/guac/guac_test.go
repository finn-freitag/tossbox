package guac

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

const key = "4c0b569e4c96df157eee1b65dd0e4d41"

func TestRoundTrip(t *testing.T) {
	u, err := LoginURL("https://guac.example.com", key, "finn", Target{Name: "vm1 (rdp)", Protocol: "rdp", Host: "100.64.0.1", Port: 40001, Username: "tossbox", Password: "pw"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(u, "data=")
	if !strings.HasPrefix(u, "https://guac.example.com/?data=") || i < 0 {
		t.Fatalf("bad url %s", u)
	}
	data, _ := url.QueryUnescape(u[i+5:])
	p, err := Decode(data, key)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Connections["vm1 (rdp)"]
	if p.Username != "finn" || c.Protocol != "rdp" || c.Parameters["port"] != "40001" || c.Parameters["password"] != "pw" {
		t.Fatalf("bad payload %+v", p)
	}
	if p.Expires < time.Now().UnixMilli() {
		t.Fatal("expired")
	}
	if _, err := Decode(data, "00000000000000000000000000000000"); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := Encode(Payload{}, "abc"); err == nil {
		t.Fatal("bad key must fail")
	}
}
