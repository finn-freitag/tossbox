package core

import (
	"encoding/base64"
	"encoding/binary"
	"testing"
)

func TestParseADBKey(t *testing.T) {
	raw := make([]byte, 12+8*64)
	binary.LittleEndian.PutUint32(raw, 64)
	key := base64.StdEncoding.EncodeToString(raw)
	if got, err := parseADBKey(key + "  me@laptop"); err != nil || got != key+" me@laptop" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"ssh-ed25519 AAAA x", key[:100], key + "\nssh-rsa x", "QUFBQQ=="} {
		if _, err := parseADBKey(bad); err == nil {
			t.Errorf("accepted %.20q", bad)
		}
	}
}
