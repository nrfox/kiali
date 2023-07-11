package kubernetes

import (
	"fmt"
	"os"
	"testing"

	"github.com/kiali/kiali/config"
)

// ReadFile reads a file's contents and calls t.Fatal if any error occurs.
func ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Error while reading file: %s. Err: %s", path, err)
	}
	return contents
}

// SetConfig sets the global config for a test and restores it after the test.
func SetConfig(t *testing.T, newConfig config.Config) {
	oldConfig := config.Get()
	t.Cleanup(func() {
		config.Set(oldConfig)
	})
	config.Set(&newConfig)
}

func createTestRemoteClusterSecretFile(t *testing.T, parentDir string, name string, content string) {
	childDir := fmt.Sprintf("%s/%s", parentDir, name)
	filename := fmt.Sprintf("%s/%s", childDir, name)
	if err := os.MkdirAll(childDir, 0o777); err != nil {
		t.Fatalf("Failed to create tmp remote cluster secret dir [%v]: %v", childDir, err)
	}
	f, err := os.Create(filename)
	if err != nil {
		t.Fatalf("Failed to create tmp remote cluster secret file [%v]: %v", filename, err)
	}
	defer f.Close()
	if _, err2 := f.WriteString(content); err2 != nil {
		t.Fatalf("Failed to write tmp remote cluster secret file [%v]: %v", filename, err2)
	}
}

// Helper function to create a test remote cluster secret file from a RemoteSecret.
// It will cleanup after itself when the test is done.
func createTestRemoteClusterSecret(t *testing.T, cluster string, contents string) {
	t.Helper()
	// create a mock volume mount directory where the test remote cluster secret content will go
	originalRemoteClusterSecretsDir := RemoteClusterSecretsDir
	t.Cleanup(func() {
		RemoteClusterSecretsDir = originalRemoteClusterSecretsDir
	})
	RemoteClusterSecretsDir = t.TempDir()

	createTestRemoteClusterSecretFile(t, RemoteClusterSecretsDir, cluster, contents)
}
