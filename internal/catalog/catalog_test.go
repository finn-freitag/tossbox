package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const yamlTpl = `
id: debian-13
name: Debian 13
family: linux
guest_profile: cloud-init
readiness: ssh
supported_services: [ssh, vnc]
min_resources: { vcpus: 1, ram_mb: 512, storage_gb: 8 }
images:
  libvirt:
    url: https://example.com/debian.qcow2
    format: qcow2
`

func TestLoadDirAndURL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "debian-13.yaml"), []byte(yamlTpl), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schema":1,"templates":[{"id":"win11","family":"windows","license":true,"images":{"libvirt":{"url":"local:win11.qcow2","format":"qcow2"}}}]}`))
	}))
	defer srv.Close()

	c := New([]string{dir, srv.URL, "http://127.0.0.1:1/unreachable.json"})
	n, errs := c.Reload(context.Background())
	if n != 2 || len(errs) != 1 {
		t.Fatalf("n=%d errs=%v", n, errs)
	}
	d, ok := c.Get("debian-13")
	if !ok || d.DefaultUser != "tossbox" || d.ServicePort("ssh") != 22 || d.DefaultResources.RAMMB != 512 {
		t.Fatalf("bad template %+v", d)
	}
	w, _ := c.Get("win11")
	if !w.License || w.Drivers()[0] != "libvirt" {
		t.Fatalf("bad win template %+v", w)
	}
}
