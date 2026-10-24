package cli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Verifies SPEC-0019-R06: kavryctl imports no Gateway or Operator internals,
// only shared API types and its own packages.
func TestKavryctlImportsNoServerInternals(t *testing.T) {
	const module = "github.com/kavrynt/kavrynt/"
	checked := 0
	for _, dir := range []string{"../../../cmd/kavryctl", "../../../internal/kavryctl"} {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			checked++
			for _, spec := range file.Imports {
				imported, _ := strconv.Unquote(spec.Path.Value)
				internal := strings.TrimPrefix(imported, module)
				if internal == imported || strings.HasPrefix(internal, "api/") || strings.HasPrefix(internal, "internal/kavryctl/") {
					continue
				}
				t.Errorf("%s imports %s", path, imported)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 4 {
		t.Fatalf("checked %d files; the paths are wrong", checked)
	}
}
