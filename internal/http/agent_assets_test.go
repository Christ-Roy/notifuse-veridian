package http

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===

func TestBuildAgentSkillTarball(t *testing.T) {
	data, err := buildAgentSkillTarball()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gz.Close()

	tr := tar.NewReader(gz)
	found := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		buf, err := io.ReadAll(tr)
		require.NoError(t, err)
		found[hdr.Name] = buf
	}

	skillMD, ok := found["notifuse/SKILL.md"]
	require.True(t, ok, "tarball must contain notifuse/SKILL.md")
	assert.Contains(t, string(skillMD), "Emailing en masse")

	agentsMD, ok := found["notifuse/AGENTS.md"]
	require.True(t, ok, "tarball must contain notifuse/AGENTS.md")
	assert.Contains(t, string(agentsMD), "NOTIFUSE_API_KEY")
	assert.Contains(t, string(agentsMD), "Ne jamais afficher cette clé", "AGENTS.md doit INTERDIRE explicitement l'affichage de la clé")
}

func TestBuildAgentSkillTarball_CachedAcrossCalls(t *testing.T) {
	data1, err := buildAgentSkillTarball()
	require.NoError(t, err)
	data2, err := buildAgentSkillTarball()
	require.NoError(t, err)
	// sync.Once means the SAME byte slice is returned both times.
	assert.Equal(t, data1, data2)
}
