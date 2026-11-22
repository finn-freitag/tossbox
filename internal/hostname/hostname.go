// Package hostname derives DNS-safe device hostnames from display names.
package hostname

import (
	"errors"
	"strings"
)

// Reserved hostnames that devices may never use (they are routed to built-in
// services or are commonly needed for the base domain itself).
var Reserved = map[string]bool{
	"guacamole": true, "www": true, "api": true, "dashboard": true, "admin": true,
	"tossbox": true, "mail": true, "smtp": true, "ns1": true, "ns2": true,
	"localhost": true, "gateway": true, "grpc": true,
}

// ErrEmpty is returned when a name contains no usable characters.
var ErrEmpty = errors.New("name must contain at least one letter or digit")

// Slug converts a display name into a DNS label: lowercase, [a-z0-9-],
// no leading/trailing or repeated dashes, max 63 characters.
// "windowsGitlabBuildMachine1" -> "windowsgitlabbuildmachine1",
// "My Test_VM" -> "my-test-vm".
func Slug(name string) (string, error) {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			// Umlauts and friends: transliterate the common ones.
			if t, ok := translit[r]; ok {
				b.WriteString(t)
				dash = false
				continue
			}
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	if s == "" {
		return "", ErrEmpty
	}
	return s, nil
}

var translit = map[rune]string{'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss", 'é': "e", 'è': "e", 'á': "a", 'à': "a", 'ñ': "n"}

// IsReserved reports whether a hostname may not be used by a device. extra
// holds additional reserved labels (e.g. the dashboard host's first label).
func IsReserved(h string, extra ...string) bool {
	if Reserved[h] {
		return true
	}
	for _, e := range extra {
		if e != "" && h == e {
			return true
		}
	}
	return false
}
