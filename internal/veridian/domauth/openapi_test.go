package domauth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Chaque route du module est documentée dans l'OpenAPI du dépôt (openapi/openapi.yaml et son fichier de chemins), avec sa méthode.
func TestOpenAPI_DocumenteChaqueRoute(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(here), "..", "..", "..", "openapi")
	var index struct {
		Paths map[string]struct {
			Ref string `yaml:"$ref"`
		} `yaml:"paths"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "openapi.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &index))
	files := map[string]map[string]map[string]any{}
	for _, r := range Routes {
		method, path := strings.ToLower(r[0]), r[1]
		entry, ok := index.Paths[path]
		require.True(t, ok, "%s absente de openapi/openapi.yaml", path)
		file, _, _ := strings.Cut(entry.Ref, "#")
		if files[file] == nil {
			b, err := os.ReadFile(filepath.Join(root, file))
			require.NoError(t, err, entry.Ref)
			m := map[string]map[string]any{}
			require.NoError(t, yaml.Unmarshal(b, &m))
			files[file] = m
		}
		ops, ok := files[file][path]
		require.True(t, ok, "%s absente de %s", path, file)
		_, ok = ops[method]
		require.True(t, ok, "%s %s : méthode non documentée dans %s", r[0], path, file)
	}
}
