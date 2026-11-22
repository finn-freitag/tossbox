package guac

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

const key = "4c0b569e4c96df157eee1b65dd0e4d41"

var targets = []Target{
	{Name: "vm1 (rdp)", Protocol: "rdp", Host: "100.64.0.1", Port: 40001, Username: "tossbox", Password: "pw"},
	{Name: "vm2 (ssh)", Protocol: "ssh", Host: "100.64.0.2", Port: 40002, Username: "debian", Password: "pw2"},
}

// payloadOf extracts and decodes the data parameter from a login URL.
func payloadOf(t *testing.T, u string) *Payload {
	t.Helper()
	i := strings.Index(u, "?data=")
	if i < 0 {
		t.Fatalf("no data in %s", u)
	}
	data, err := url.QueryUnescape(u[i+6:])
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(data, key)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundTrip(t *testing.T) {
	u, err := LoginURL("https://guac.example.com/guacamole", key, "finn", targets, "vm2 (ssh)", time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := "https://guac.example.com/guacamole/#/client/" + url.QueryEscape(ClientID("vm2 (ssh)")) + "?data="
	if !strings.HasPrefix(u, wantPrefix) {
		t.Fatalf("bad url %s", u)
	}
	p := payloadOf(t, u)
	if p.Username != "finn" || len(p.Connections) != 2 {
		t.Fatalf("bad payload %+v", p)
	}
	c := p.Connections["vm1 (rdp)"]
	if c.Protocol != "rdp" || c.Parameters["port"] != "40001" || c.Parameters["password"] != "pw" {
		t.Fatalf("bad connection %+v", c)
	}
	if p.Expires < time.Now().UnixMilli() {
		t.Fatal("expired")
	}
	if _, err := Encode(Payload{}, "abc"); err == nil {
		t.Fatal("bad key must fail")
	}
	data, _ := url.QueryUnescape(u[strings.Index(u, "?data=")+6:])
	if _, err := Decode(data, "00000000000000000000000000000000"); err == nil {
		t.Fatal("wrong key must fail")
	}
}

func TestLoginViaHelper(t *testing.T) {
	u, err := LoginURL("https://guacamole.servers.example.com/guacamole", key, "finn", targets, "vm1 (rdp)", time.Minute, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "https://guacamole.servers.example.com"+LoginPath+"#/client/") {
		t.Fatalf("bad url %s", u)
	}
	if p := payloadOf(t, u); len(p.Connections) != 2 {
		t.Fatalf("bad payload %+v", p)
	}
	if _, err := LoginURL("https://g", key, "finn", targets, "missing", time.Minute, false); err == nil {
		t.Fatal("unknown selected connection must fail")
	}
}

func TestClientID(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(ClientID("vm1 (rdp)"))
	if err != nil || string(raw) != "vm1 (rdp)\x00c\x00json" {
		t.Fatalf("bad client id %q", raw)
	}
}

func TestHelperPage(t *testing.T) {
	page := string(HelperPage("/guacamole/"))
	if !strings.Contains(page, `"/guacamole/#"`) || !strings.Contains(page, "GUAC_AUTH_TOKEN") {
		t.Fatalf("bad page %s", page)
	}
}
