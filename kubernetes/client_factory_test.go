package kubernetes

import (
	_ "embed"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/kiali/kiali/config"
)

var (
	//go:embed testdata/remote-cluster-exec.yaml
	remoteClusterExecYAML string

	//go:embed testdata/remote-cluster.yaml
	remoteClusterYAML string
)

// newTestingClientFactory creates a client factory and a temporary token file.
// Without this token file, the client factory will try to read the token from
// the default path at /var/run/secrets/... which probably doesn't exist and
// we probably don't want to use it even if it does.
func newTestingClientFactory(t *testing.T) *clientFactory {
	t.Helper()

	// Reset global vars after test
	originalToken := KialiTokenForHomeCluster
	originalPath := DefaultServiceAccountPath
	t.Cleanup(func() {
		KialiTokenForHomeCluster = originalToken
		DefaultServiceAccountPath = originalPath
	})

	DefaultServiceAccountPath = fmt.Sprintf("%s/kiali-testing-token-%s", t.TempDir(), time.Now())
	if err := os.WriteFile(DefaultServiceAccountPath, []byte("test-token"), 0o644); err != nil {
		t.Fatalf("Unable to create token file for testing. Err: %s", err)
	}

	clientConfig := rest.Config{}
	client, err := newClientFactory(&clientConfig)
	if err != nil {
		t.Fatalf("Error creating client factory: %v", err)
	}

	return client
}

// TestClientExpiration Verify the details that clients expire are correct
func TestClientExpiration(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	conf := config.Get()
	conf.Auth.Strategy = config.AuthStrategyOpenId
	conf.Auth.OpenId.DisableRBAC = false
	SetConfig(t, *conf)

	clientFactory := newTestingClientFactory(t)

	// Make sure we are starting off with an empty set of clients
	assert.Equal(0, clientFactory.getClientsLength())

	// Create a single initial test clients
	authInfo := api.NewAuthInfo()
	authInfo.Token = "foo-token"
	_, err := clientFactory.getRecycleClient(authInfo, 100*time.Millisecond, conf.KubernetesConfig.ClusterName)
	require.NoError(err)

	// Verify we have the client
	assert.Equal(1, clientFactory.getClientsLength())
	_, found := clientFactory.hasClient(authInfo)
	assert.True(found)

	// Sleep for a bit and add another client
	time.Sleep(time.Millisecond * 60)
	authInfo1 := api.NewAuthInfo()
	authInfo1.Token = "bar-token"
	_, err = clientFactory.getRecycleClient(authInfo1, 100*time.Millisecond, conf.KubernetesConfig.ClusterName)
	require.NoError(err)

	// Verify we have both the foo and bar clients
	assert.Equal(2, clientFactory.getClientsLength())
	_, found = clientFactory.hasClient(authInfo)
	assert.True(found)
	_, found = clientFactory.hasClient(authInfo1)
	assert.True(found)

	// Wait for foo to be expired
	time.Sleep(time.Millisecond * 60)
	// Verify the client has been removed
	assert.Equal(1, clientFactory.getClientsLength())
	_, found = clientFactory.hasClient(authInfo)
	assert.False(found)
	_, found = clientFactory.hasClient(authInfo1)
	assert.True(found)

	// Wait for bar to be expired
	time.Sleep(time.Millisecond * 60)
	assert.Equal(0, clientFactory.getClientsLength())
}

// TestConcurrentClientExpiration Verify Concurrent clients are expired correctly
func TestConcurrentClientExpiration(t *testing.T) {
	assert := assert.New(t)

	clientFactory := newTestingClientFactory(t)
	count := 100

	wg := sync.WaitGroup{}
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			authInfo := api.NewAuthInfo()
			authInfo.Token = fmt.Sprintf("%d", rand.Intn(10000000000))
			_, innerErr := clientFactory.getRecycleClient(authInfo, 10*time.Millisecond, config.Get().KubernetesConfig.ClusterName)
			assert.NoError(innerErr)
		}()
	}

	wg.Wait()
	time.Sleep(3 * time.Second)

	assert.Equal(0, clientFactory.getClientsLength())
}

// TestConcurrentClientFactory test Concurrently create ClientFactory
func TestConcurrentClientFactory(t *testing.T) {
	count := 100

	wg := sync.WaitGroup{}
	wg.Add(count)

	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			newTestingClientFactory(t)
		}()
	}

	wg.Wait()
}

func TestSAHomeClientUpdatesWhenKialiTokenChanges(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	kialiConfig := config.NewConfig()
	config.Set(kialiConfig)
	currentToken := KialiTokenForHomeCluster
	currentTime := tokenRead
	t.Cleanup(func() {
		// Other tests use this global var so we need to reset it.
		tokenRead = currentTime
		KialiTokenForHomeCluster = currentToken
	})

	tokenRead = time.Now()
	KialiTokenForHomeCluster = "current-token"

	restConfig := rest.Config{}
	clientFactory, err := newClientFactory(&restConfig)
	require.NoError(err)

	currentClient := clientFactory.GetSAHomeClusterClient()
	assert.Equal(KialiTokenForHomeCluster, currentClient.GetToken())
	assert.Equal(currentClient, clientFactory.GetSAHomeClusterClient())

	KialiTokenForHomeCluster = "new-token"

	// Assert that the token has changed and the client has changed.
	newClient := clientFactory.GetSAHomeClusterClient()
	assert.Equal(KialiTokenForHomeCluster, newClient.GetToken())
	assert.NotEqual(currentClient, newClient)
}

func TestSAClientsUpdateWhenKialiTokenChanges(t *testing.T) {
	require := require.New(t)
	conf := config.NewConfig()
	config.Set(conf)
	t.Cleanup(func() {
		// Other tests use this global var so we need to reset it.
		KialiTokenForHomeCluster = ""
	})

	tokenRead = time.Now()
	KialiTokenForHomeCluster = "current-token"

	restConfig := rest.Config{}
	clientFactory, err := newClientFactory(&restConfig)
	require.NoError(err)

	client := clientFactory.GetSAClient(conf.KubernetesConfig.ClusterName)
	require.Equal(KialiTokenForHomeCluster, client.GetToken())

	KialiTokenForHomeCluster = "new-token"

	client = clientFactory.GetSAClient(conf.KubernetesConfig.ClusterName)
	require.Equal(KialiTokenForHomeCluster, client.GetToken())
}

func TestClientCreatedWithClusterInfo(t *testing.T) {
	// Create a fake cluster info file.
	// Ensure client gets created with this.
	// Need to test newClient and newSAClient
	// Need to test that home cluster gets this info as well
	require := require.New(t)
	assert := assert.New(t)

	conf := config.NewConfig()
	config.Set(conf)

	const testClusterName = "TestRemoteCluster"
	createTestRemoteClusterSecret(t, testClusterName, remoteClusterYAML)

	clientFactory := newTestingClientFactory(t)

	// Service account clients
	saClients := clientFactory.GetSAClients()
	require.Contains(saClients, testClusterName)
	require.Contains(saClients, conf.KubernetesConfig.ClusterName)
	assert.Equal(testClusterName, saClients[testClusterName].ClusterInfo().Name)
	assert.Equal("https://192.168.1.2:1234", saClients[testClusterName].ClusterInfo().ClientConfig.Host)
	assert.Contains(saClients[conf.KubernetesConfig.ClusterName].ClusterInfo().Name, conf.KubernetesConfig.ClusterName)

	// User clients
	userClients, err := clientFactory.GetClients(api.NewAuthInfo())
	require.NoError(err)

	require.Contains(userClients, testClusterName)
	require.Contains(userClients, conf.KubernetesConfig.ClusterName)
	assert.Equal(testClusterName, userClients[testClusterName].ClusterInfo().Name)
	assert.Equal("https://192.168.1.2:1234", userClients[testClusterName].ClusterInfo().ClientConfig.Host)
	assert.Contains(userClients[conf.KubernetesConfig.ClusterName].ClusterInfo().Name, conf.KubernetesConfig.ClusterName)
}

func TestSAClientCreatedWithExecProvider(t *testing.T) {
	// by default, ExecProvider support should be disabled
	cases := map[string]struct {
		remoteSecretContents string
		expected             rest.Config
	}{
		"Only bearer token": {
			remoteSecretContents: remoteClusterYAML,
			expected: rest.Config{
				BearerToken:  "token",
				ExecProvider: nil,
			},
		},
		"Use bearer token and exec credentials (which should be ignored)": {
			remoteSecretContents: remoteClusterExecYAML,
			expected: rest.Config{
				BearerToken:  "token",
				ExecProvider: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require := require.New(t)
			const clusterName = "TestRemoteCluster"

			originalSecretsDir := RemoteClusterSecretsDir
			t.Cleanup(func() {
				RemoteClusterSecretsDir = originalSecretsDir
			})
			RemoteClusterSecretsDir = t.TempDir()

			createTestRemoteClusterSecretFile(t, RemoteClusterSecretsDir, clusterName, tc.remoteSecretContents)
			cf := newTestingClientFactory(t)

			saClients := cf.GetSAClients()
			// Should be home cluster client and one remote client
			require.Equal(2, len(saClients))
			require.Contains(saClients, clusterName)

			clientConfig := saClients[clusterName].ClusterInfo().ClientConfig
			require.Equal(tc.expected.BearerToken, clientConfig.BearerToken)
			require.Nil(clientConfig.ExecProvider)
		})
	}

	// now enable ExecProvider support
	conf := config.NewConfig()
	conf.KialiFeatureFlags.Clustering.EnableExecProvider = true
	SetConfig(t, *conf)

	cases = map[string]struct {
		remoteSecretContents string
		expected             rest.Config
	}{
		"Only bearer token": {
			remoteSecretContents: remoteClusterYAML,
			expected: rest.Config{
				BearerToken:  "token",
				ExecProvider: nil,
			},
		},
		"Use bearer token and exec credentials": {
			remoteSecretContents: remoteClusterExecYAML,
			expected: rest.Config{
				BearerToken: "token",
				ExecProvider: &api.ExecConfig{
					Command: "command",
					Args:    []string{"arg1", "arg2"},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require := require.New(t)
			const clusterName = "TestRemoteCluster"

			originalSecretsDir := RemoteClusterSecretsDir
			t.Cleanup(func() {
				RemoteClusterSecretsDir = originalSecretsDir
			})
			RemoteClusterSecretsDir = t.TempDir()

			createTestRemoteClusterSecretFile(t, RemoteClusterSecretsDir, clusterName, tc.remoteSecretContents)
			cf := newTestingClientFactory(t)

			saClients := cf.GetSAClients()
			// Should be home cluster client and one remote client
			require.Equal(2, len(saClients))
			require.Contains(saClients, clusterName)

			clientConfig := saClients[clusterName].ClusterInfo().ClientConfig
			require.Equal(tc.expected.BearerToken, clientConfig.BearerToken)
			if tc.expected.ExecProvider != nil {
				// Just check a few fields for sanity
				require.Equal(tc.expected.ExecProvider.Command, clientConfig.ExecProvider.Command)
				require.Equal(tc.expected.ExecProvider.Args, clientConfig.ExecProvider.Args)
			}
		})
	}
}
