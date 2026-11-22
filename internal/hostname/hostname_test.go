package hostname

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"windowsGitlabBuildMachine1": "windowsgitlabbuildmachine1",
		"My Test_VM":                 "my-test-vm",
		"--a--b--":                   "a-b",
		"Grüße":                      "gruesse",
		"debian 13 (trixie)":         "debian-13-trixie",
	}
	for in, want := range cases {
		got, err := Slug(in)
		if err != nil || got != want {
			t.Errorf("Slug(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Slug("!!!"); err == nil {
		t.Error("expected error for empty slug")
	}
	long, _ := Slug(strings.Repeat("a", 70))
	if len(long) != 63 {
		t.Errorf("len %d", len(long))
	}
	if s, _ := Slug(strings.Repeat("a", 62) + " b"); strings.HasSuffix(s, "-") {
		t.Errorf("trailing dash: %q", s)
	}
}

func TestReserved(t *testing.T) {
	if !IsReserved("guacamole") || IsReserved("foo") || !IsReserved("dash", "dash") {
		t.Fatal("reserved check broken")
	}
}
