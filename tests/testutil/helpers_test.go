package testutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanupTestEnvironmentPreservesCallerDatabaseEndpoint(t *testing.T) {
	t.Setenv("TEST_DB_HOST", "database.example.test")
	t.Setenv("TEST_DB_PORT", "15433")

	SetupTestEnvironment()
	CleanupTestEnvironment()

	require.Equal(t, "database.example.test", os.Getenv("TEST_DB_HOST"))
	require.Equal(t, "15433", os.Getenv("TEST_DB_PORT"))
}
