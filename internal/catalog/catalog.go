// Package catalog loads device templates ("eggs") from catalog.json URLs or
// local directories of YAML files (format: tb-templates/docs/FORMAT.md).
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Resources mirrors min_resources / default_resources.
type Resources struct {
	VCPUs         int   `json:"vcpus" yaml:"vcpus"`
	CPUCapPercent int   `json:"cpu_cap_percent" yaml:"cpu_cap_percent"`
	RAMMB         int64 `json:"ram_mb" yaml:"ram_mb"`
	StorageGB     int64 `json:"storage_gb" yaml:"storage_gb"`
}

// Image is the image entry of one driver.
type Image struct {
	URL    string `json:"url" yaml:"url"`
	Format string `json:"format" yaml:"format"`
	SHA256 string `json:"sha256,omitempty" yaml:"sha256"`
}

// Template is one device template.
type Template struct {
	ID                string            `json:"id" yaml:"id"`
	Name              string            `json:"name" yaml:"name"`
	Description       string            `json:"description" yaml:"description"`
	Version           string            `json:"version" yaml:"version"`
	Family            string            `json:"family" yaml:"family"`
	Icon              string            `json:"icon,omitempty" yaml:"icon"`
	GuestProfile      string            `json:"guest_profile" yaml:"guest_profile"`
	Readiness         string            `json:"readiness" yaml:"readiness"`
	DefaultUser       string            `json:"default_user" yaml:"default_user"`
	SupportedServices []string          `json:"supported_services" yaml:"supported_services"`
	ServicePorts      map[string]int    `json:"service_ports,omitempty" yaml:"service_ports"`
	MinResources      Resources         `json:"min_resources" yaml:"min_resources"`
	DefaultResources  Resources         `json:"default_resources" yaml:"default_resources"`
	License           bool              `json:"license" yaml:"license"`
	Arch              []string          `json:"arch,omitempty" yaml:"arch"`
	Images            map[string]Image  `json:"images" yaml:"images"`
	Params            map[string]string `json:"params,omitempty" yaml:"params"`

	// Source is set by the loader.
	Source string `json:"-" yaml:"-"`
}

// DefaultServicePorts are the internal ports used unless a template overrides them.
var DefaultServicePorts = map[string]int{"ssh": 22, "rdp": 3389, "vnc": 5900, "adb": 5555, "http": 80, "https": 443}

// ServicePort returns the internal port of a service for this template.
func (t *Template) ServicePort(svc string) int {
	if p, ok := t.ServicePorts[svc]; ok && p > 0 {
		return p
	}
	return DefaultServicePorts[svc]
}

// Drivers returns the compatible drivers (keys of Images), sorted.
func (t *Template) Drivers() []string {
	out := make([]string, 0, len(t.Images))
	for d := range t.Images {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Supports reports whether the template supports a service.
func (t *Template) Supports(svc string) bool {
	for _, s := range t.SupportedServices {
		if s == svc {
			return true
		}
	}
	return false
}

func (t *Template) normalize() error {
	if t.ID == "" {
		return fmt.Errorf("template without id")
	}
	if len(t.Images) == 0 {
		return fmt.Errorf("template %s: no images", t.ID)
	}
	if t.Name == "" {
		t.Name = t.ID
	}
	if t.Version == "" {
		t.Version = "1"
	}
	if t.DefaultUser == "" {
		t.DefaultUser = "tossbox"
	}
	if t.Readiness == "" {
		t.Readiness = "none"
	}
	if t.MinResources.VCPUs <= 0 {
		t.MinResources.VCPUs = 1
	}
	if t.DefaultResources.VCPUs < t.MinResources.VCPUs {
		t.DefaultResources.VCPUs = max(t.MinResources.VCPUs, 2)
	}
	if t.DefaultResources.RAMMB < t.MinResources.RAMMB {
		t.DefaultResources.RAMMB = t.MinResources.RAMMB
	}
	if t.DefaultResources.StorageGB < t.MinResources.StorageGB {
		t.DefaultResources.StorageGB = t.MinResources.StorageGB
	}
	return nil
}

type catalogFile struct {
	Schema    int        `json:"schema"`
	Templates []Template `json:"templates"`
}

// Catalog holds the merged templates of all sources.
type Catalog struct {
	sources []string
	client  *http.Client

	mu        sync.RWMutex
	templates map[string]*Template
	// lastGood keeps the last successful result per source so a temporarily
	// unreachable source doesn't make its templates disappear.
	lastGood map[string][]Template
}

// New creates a catalog for the given sources (URLs or directories).
func New(sources []string) *Catalog {
	return &Catalog{
		sources:   sources,
		client:    &http.Client{Timeout: 20 * time.Second},
		templates: map[string]*Template{},
		lastGood:  map[string][]Template{},
	}
}

// Reload re-reads all sources. Later sources override earlier ones on ID
// collisions. It returns the number of templates and per-source errors.
func (c *Catalog) Reload(ctx context.Context) (int, []string) {
	var errs []string
	merged := map[string]*Template{}
	for _, src := range c.sources {
		tpls, err := c.load(ctx, src)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src, err))
			c.mu.RLock()
			tpls = c.lastGood[src]
			c.mu.RUnlock()
		} else {
			c.mu.Lock()
			c.lastGood[src] = tpls
			c.mu.Unlock()
		}
		for i := range tpls {
			t := tpls[i]
			t.Source = src
			if err := t.normalize(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", src, err))
				continue
			}
			merged[t.ID] = &t
		}
	}
	c.mu.Lock()
	c.templates = merged
	c.mu.Unlock()
	return len(merged), errs
}

func (c *Catalog) load(ctx context.Context, src string) ([]Template, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return c.loadURL(ctx, src)
	}
	src = strings.TrimPrefix(src, "file://")
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return loadFile(src)
	}
	return loadDir(src)
}

func (c *Catalog) loadURL(ctx context.Context, url string) ([]Template, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return parseCatalog(body)
}

func parseCatalog(body []byte) ([]Template, error) {
	var f catalogFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	return f.Templates, nil
}

func loadFile(path string) ([]Template, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".json") {
		return parseCatalog(b)
	}
	var t Template
	if err := yaml.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return []Template{t}, nil
}

func loadDir(dir string) ([]Template, error) {
	var out []Template
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		tpls, err := loadFile(path)
		if err != nil {
			return err
		}
		out = append(out, tpls...)
		return nil
	})
	return out, err
}

// Get returns a template by ID.
func (c *Catalog) Get(id string) (*Template, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.templates[id]
	return t, ok
}

// All returns all templates sorted by family, then name.
func (c *Catalog) All() []*Template {
	c.mu.RLock()
	out := make([]*Template, 0, len(c.templates))
	for _, t := range c.templates {
		out = append(out, t)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Set replaces the templates (used by tests).
func (c *Catalog) Set(tpls ...Template) {
	m := map[string]*Template{}
	for i := range tpls {
		t := tpls[i]
		_ = t.normalize()
		m[t.ID] = &t
	}
	c.mu.Lock()
	c.templates = m
	c.mu.Unlock()
}
