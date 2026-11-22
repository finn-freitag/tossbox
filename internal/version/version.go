// Package version holds the build version, set via -ldflags.
package version

// Version is overridden at build time:
//
//	go build -ldflags "-X github.com/finn-freitag/tossbox/internal/version.Version=v1.2.3"
var Version = "dev"
