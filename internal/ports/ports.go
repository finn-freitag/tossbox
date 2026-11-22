// Package ports allocates public ports from the configured range.
package ports

import "errors"

// ErrExhausted is returned when no free port is left.
var ErrExhausted = errors.New("port range exhausted")

// Allocator hands out the lowest free ports in [Start, End].
type Allocator struct {
	Start, End int
}

// Allocate returns n free ports, given the set of ports already in use.
// The returned ports are also added to used.
func (a Allocator) Allocate(used map[int]bool, n int) ([]int, error) {
	out := make([]int, 0, n)
	for p := a.Start; p <= a.End && len(out) < n; p++ {
		if !used[p] {
			used[p] = true
			out = append(out, p)
		}
	}
	if len(out) < n {
		for _, p := range out {
			delete(used, p)
		}
		return nil, ErrExhausted
	}
	return out, nil
}
