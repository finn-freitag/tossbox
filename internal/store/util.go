package store

import "strings"

func splitCols(cols string) []string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func joinCols(parts []string) string { return strings.Join(parts, ", ") }
