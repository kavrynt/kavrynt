package docs_test

import (
	"os"
	"strings"
	"testing"
)

// Verifies SPEC-0014-R05: the install guides show both an authenticated
// install and the explicit opt-out for trials.
func TestInstallGuidesShowAuthenticationAndOptOut(t *testing.T) {
	for _, path := range []string{"../README.md", "../charts/kavrynt/README.md", "DEMO.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"gateway.auth.issuers", "gateway.auth.enabled=false"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not show %s", path, want)
			}
		}
	}
}
