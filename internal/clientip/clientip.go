// Package clientip determines the client address of an HTTP request, taking
// X-Forwarded-For into account only when the request comes from a trusted
// reverse proxy (otherwise clients could spoof their address).
package clientip

import (
	"net/http"
	"net/netip"
	"strings"
)

// From returns the client IP of r. If the direct peer is in trusted, the
// X-Forwarded-For chain is walked from the right and the first address that
// is not a trusted proxy is returned.
func From(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := parseAddr(r.RemoteAddr)
	if !peer.IsValid() || !contains(trusted, peer) {
		return peer
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a := parseAddr(strings.TrimSpace(hops[i]))
		if !a.IsValid() {
			break
		}
		client = a
		if !contains(trusted, a) {
			break
		}
	}
	return client
}

func parseAddr(s string) netip.Addr {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap()
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap()
	}
	return netip.Addr{}
}

func contains(list []netip.Prefix, a netip.Addr) bool {
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
