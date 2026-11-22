// Package config loads the dashboard configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultTemplateSource is the official tb-templates catalog.
const DefaultTemplateSource = "https://raw.githubusercontent.com/finn-freitag/tb-templates/main/catalog.json"

// Config is the complete runtime configuration.
type Config struct {
	DatabaseURL string
	DataDir     string
	SecretKey   string

	BaseDomain    string // e.g. servers.finnfreitag.com
	DashboardHost string // e.g. dashboard.finnfreitag.com (optional)
	PublicURL     string // public URL of the dashboard, used in emails/links (optional)

	HTTPListen string // UI/API listener, default :8080
	GRPCListen string // agent listener, default :7443
	GRPCHosts  []string
	// GRPCEndpoint is what agents should use as dashboard.endpoint (shown to the admin).
	GRPCEndpoint string

	PortRangeStart int
	PortRangeEnd   int

	GatewayHTTPListen       string // default :80, empty = off
	GatewayHTTPSListen      string // default :443, empty = off
	GatewayPublicAddress    string // bind address for exposure "public", default 0.0.0.0
	GatewayTailscaleAddress string // bind address for exposure "tailscale"
	GatewayFallbackHTTP     string // host:port for unknown hosts on :80
	GatewayFallbackHTTPS    string // host:port for unknown SNI on :443
	GatewayRateLimit        int    // new connections per source IP per minute, 0 = off

	ACMEEmail    string
	ACMEDirURL   string // optional, e.g. Let's Encrypt staging
	ACMECacheDir string

	GuacamoleURL        string
	GuacamoleJSONSecret string
	GuacamoleUpstream   string // e.g. http://127.0.0.1:8081

	TemplateSources []string

	AdminUsername string
	AdminPassword string

	SessionTTL    time.Duration
	SecureCookies bool
	LogLevel      string
	LogFormat     string // text | json
}

// Load reads the configuration from the environment.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:             env("TOSSBOX_DATABASE_URL", "postgres://tossbox:tossbox@127.0.0.1:5432/tossbox?sslmode=disable"),
		DataDir:                 env("TOSSBOX_DATA_DIR", "/data"),
		SecretKey:               env("TOSSBOX_SECRET_KEY", ""),
		BaseDomain:              strings.ToLower(strings.Trim(env("TOSSBOX_BASE_DOMAIN", "servers.localhost"), ".")),
		DashboardHost:           strings.ToLower(env("TOSSBOX_DASHBOARD_HOST", "")),
		PublicURL:               strings.TrimRight(env("TOSSBOX_PUBLIC_URL", ""), "/"),
		HTTPListen:              env("TOSSBOX_HTTP_LISTEN", ":8080"),
		GRPCListen:              env("TOSSBOX_GRPC_LISTEN", ":7443"),
		GRPCHosts:               list(env("TOSSBOX_GRPC_HOSTS", "")),
		GRPCEndpoint:            env("TOSSBOX_GRPC_ENDPOINT", ""),
		GatewayHTTPListen:       env("TOSSBOX_GATEWAY_HTTP_LISTEN", ":80"),
		GatewayHTTPSListen:      env("TOSSBOX_GATEWAY_HTTPS_LISTEN", ":443"),
		GatewayPublicAddress:    env("TOSSBOX_GATEWAY_PUBLIC_ADDRESS", "0.0.0.0"),
		GatewayTailscaleAddress: env("TOSSBOX_GATEWAY_TAILSCALE_ADDRESS", ""),
		GatewayFallbackHTTP:     env("TOSSBOX_GATEWAY_FALLBACK_HTTP", ""),
		GatewayFallbackHTTPS:    env("TOSSBOX_GATEWAY_FALLBACK_HTTPS", ""),
		ACMEEmail:               env("TOSSBOX_ACME_EMAIL", ""),
		ACMEDirURL:              env("TOSSBOX_ACME_DIRECTORY_URL", ""),
		GuacamoleURL:            strings.TrimRight(env("TOSSBOX_GUACAMOLE_URL", ""), "/"),
		GuacamoleJSONSecret:     env("TOSSBOX_GUACAMOLE_JSON_SECRET", ""),
		GuacamoleUpstream:       env("TOSSBOX_GUACAMOLE_UPSTREAM", "http://127.0.0.1:8081"),
		TemplateSources:         list(env("TOSSBOX_TEMPLATE_SOURCES", DefaultTemplateSource)),
		AdminUsername:           env("TOSSBOX_ADMIN_USERNAME", "admin"),
		AdminPassword:           env("TOSSBOX_ADMIN_PASSWORD", ""),
		LogLevel:                env("TOSSBOX_LOG_LEVEL", "info"),
		LogFormat:               env("TOSSBOX_LOG_FORMAT", "text"),
	}
	c.ACMECacheDir = env("TOSSBOX_ACME_CACHE_DIR", c.DataDir+"/acme")

	var err error
	if c.PortRangeStart, c.PortRangeEnd, err = portRange(env("TOSSBOX_PORT_RANGE", "40000-49999")); err != nil {
		return nil, err
	}
	if c.GatewayRateLimit, err = strconv.Atoi(env("TOSSBOX_GATEWAY_RATE_LIMIT", "60")); err != nil {
		return nil, fmt.Errorf("TOSSBOX_GATEWAY_RATE_LIMIT: %w", err)
	}
	ttl, err := time.ParseDuration(env("TOSSBOX_SESSION_TTL", "720h"))
	if err != nil {
		return nil, fmt.Errorf("TOSSBOX_SESSION_TTL: %w", err)
	}
	c.SessionTTL = ttl
	c.SecureCookies = env("TOSSBOX_SECURE_COOKIES", "auto") == "true" ||
		(env("TOSSBOX_SECURE_COOKIES", "auto") == "auto" && strings.HasPrefix(c.PublicURL, "https://"))

	if c.GRPCEndpoint == "" {
		host := c.DashboardHost
		if host == "" && len(c.GRPCHosts) > 0 {
			host = c.GRPCHosts[0]
		}
		if host == "" {
			host = "localhost"
		}
		c.GRPCEndpoint = host + ":" + portOf(c.GRPCListen)
	}
	if c.GuacamoleURL != "" && len(c.GuacamoleJSONSecret) != 32 {
		return nil, fmt.Errorf("TOSSBOX_GUACAMOLE_JSON_SECRET must be 32 hex characters when TOSSBOX_GUACAMOLE_URL is set")
	}
	return c, nil
}

// GuacamoleEnabled reports whether the optional Guacamole integration is configured.
func (c *Config) GuacamoleEnabled() bool { return c.GuacamoleURL != "" && c.GuacamoleJSONSecret != "" }

// GuacamoleHost is the hostname Guacamole is served on by the gateway.
func (c *Config) GuacamoleHost() string { return "guacamole." + c.BaseDomain }

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func portRange(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("TOSSBOX_PORT_RANGE: want START-END, got %q", s)
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || lo < 1 || hi > 65535 || lo > hi {
		return 0, 0, fmt.Errorf("TOSSBOX_PORT_RANGE: invalid range %q", s)
	}
	return lo, hi, nil
}

func portOf(listen string) string {
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		return listen[i+1:]
	}
	return listen
}
