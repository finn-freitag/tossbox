// Package model contains domain types shared by the store, scheduler, core
// service and API (JSON shapes follow docs/API.md).
package model

import (
	"errors"
	"fmt"
	"net/http"
)

// Resources of a device.
type Resources struct {
	VCPUs         int   `json:"vcpus"`
	CPUCapPercent int   `json:"cpu_cap_percent"`
	RAMMB         int64 `json:"ram_mb"`
	StorageGB     int64 `json:"storage_gb"`
}

// Quota limits per user; 0 / empty = unlimited / all allowed.
type Quota struct {
	MaxMachines         int      `json:"max_machines"`
	MaxVCPUs            int      `json:"max_vcpus"`
	MaxRAMMB            int64    `json:"max_ram_mb"`
	MaxStorageGB        int64    `json:"max_storage_gb"`
	MaxPhones           int      `json:"max_phones"`
	MaxGPUs             int      `json:"max_gpus"`
	AllowedDrivers      []string `json:"allowed_drivers"`
	AllowedTemplates    []string `json:"allowed_templates"`
	AllowedLicensePools []string `json:"allowed_license_pools"`
	AllowedExposures    []string `json:"allowed_exposures"`
	MaxTTLHours         int      `json:"max_ttl_hours"`
}

// Normalize replaces nil slices with empty ones (stable JSON).
func (q Quota) Normalize() Quota {
	for _, s := range []*[]string{&q.AllowedDrivers, &q.AllowedTemplates, &q.AllowedLicensePools, &q.AllowedExposures} {
		if *s == nil {
			*s = []string{}
		}
	}
	return q
}

// Allows reports whether v is allowed by an allowlist (empty = all).
func Allows(list []string, v string) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Usage of a user (counted over non-deleting devices).
type Usage struct {
	Machines  int   `json:"machines"`
	VCPUs     int   `json:"vcpus"`
	RAMMB     int64 `json:"ram_mb"`
	StorageGB int64 `json:"storage_gb"`
	Phones    int   `json:"phones"`
	GPUs      int   `json:"gpus"`
}

// Settings are the admin-editable runtime settings.
type Settings struct {
	OvercommitRAM           bool     `json:"overcommit_ram"`
	RAMOvercommitRatio      float64  `json:"ram_overcommit_ratio"`
	OvercommitCPU           bool     `json:"overcommit_cpu"`
	CPUOvercommitRatio      float64  `json:"cpu_overcommit_ratio"`
	StorageAccounting       string   `json:"storage_accounting"` // provisioned | actual
	StoppedKeepsReservation bool     `json:"stopped_keeps_reservation"`
	DefaultServices         []string `json:"default_services"`
	PasswordLength          int      `json:"password_length"`
	DefaultTTLHours         int      `json:"default_ttl_hours"`
	APIRequiresTTL          bool     `json:"api_requires_ttl"`
	PublicVNCAllowed        bool     `json:"public_vnc_allowed"`
	// PublicADBAllowed: adb has no authentication and gives a root shell on
	// redroid (a privileged container), so it is off by default.
	PublicADBAllowed bool `json:"public_adb_allowed"`
}

// DefaultSettings are used until an admin changes them.
func DefaultSettings() Settings {
	return Settings{
		OvercommitRAM:           false,
		RAMOvercommitRatio:      1.0,
		OvercommitCPU:           true,
		CPUOvercommitRatio:      4.0,
		StorageAccounting:       "provisioned",
		StoppedKeepsReservation: true,
		DefaultServices:         []string{"ssh", "rdp"},
		PasswordLength:          24,
		DefaultTTLHours:         0,
		APIRequiresTTL:          true,
		PublicVNCAllowed:        true,
		PublicADBAllowed:        false,
	}
}

// Validate checks settings constraints.
func (s Settings) Validate() error {
	if s.PasswordLength < 20 {
		return Errf(CodeValidation, "password_length must be at least 20")
	}
	if s.StorageAccounting != "provisioned" && s.StorageAccounting != "actual" {
		return Errf(CodeValidation, "storage_accounting must be provisioned or actual")
	}
	if s.RAMOvercommitRatio < 1 || s.CPUOvercommitRatio < 1 {
		return Errf(CodeValidation, "overcommit ratios must be >= 1")
	}
	if s.DefaultTTLHours < 0 {
		return Errf(CodeValidation, "default_ttl_hours must be >= 0")
	}
	for _, svc := range s.DefaultServices {
		if !IsService(svc) {
			return Errf(CodeValidation, "unknown service %q", svc)
		}
	}
	return nil
}

// Services known to Tossbox, in display order.
var Services = []string{"ssh", "rdp", "vnc", "adb", "http", "https"}

// Network policies of a device (outbound access).
const (
	NetworkInternet = "internet" // internet only; the node firewall blocks private networks
	NetworkOffline  = "offline"  // no outbound traffic; still reachable through its services
)

// NetworkPolicies in display order.
var NetworkPolicies = []string{NetworkInternet, NetworkOffline}

// CredentialService reports whether a service of a device of this template
// family gets the default user and password. Browser devices protect all
// their services (VNC and the web UI) with the device password.
func CredentialService(family, svc string) bool {
	return CredentialServices[svc] || family == "browser"
}

// CredentialServices use the device's default user and password.
var CredentialServices = map[string]bool{"ssh": true, "rdp": true, "vnc": true}

// IsService reports whether s is a known service.
func IsService(s string) bool { return Allows(Services, s) && s != "" }

// Exposure modes.
const (
	ExposurePublic    = "public"
	ExposureTailscale = "tailscale"
	ExposureNone      = "none"
)

// Exposures lists all exposure modes.
var Exposures = []string{ExposurePublic, ExposureTailscale, ExposureNone}

// Power states (desired).
const (
	PowerRunning = "running"
	PowerStopped = "stopped"
)

// Roles.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Error codes (API.md).
const (
	CodeUnauthorized  = "unauthorized"
	CodeForbidden     = "forbidden"
	CodeNotFound      = "not_found"
	CodeValidation    = "validation"
	CodeQuotaExceeded = "quota_exceeded"
	CodeNoCapacity    = "no_capacity"
	CodeConflict      = "conflict"
	CodeInternal      = "internal"
)

var codeStatus = map[string]int{
	CodeUnauthorized:  http.StatusUnauthorized,
	CodeForbidden:     http.StatusForbidden,
	CodeNotFound:      http.StatusNotFound,
	CodeValidation:    http.StatusBadRequest,
	CodeQuotaExceeded: http.StatusUnprocessableEntity,
	CodeNoCapacity:    http.StatusServiceUnavailable,
	CodeConflict:      http.StatusConflict,
	CodeInternal:      http.StatusInternalServerError,
}

// Error is an error with an API error code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Status returns the HTTP status for the error code.
func (e *Error) Status() int {
	if s, ok := codeStatus[e.Code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Errf creates an *Error.
func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// AsError extracts an *Error (or wraps err as internal).
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Msg: err.Error()}
}
