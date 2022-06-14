package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/kiali/kiali/business"
	"github.com/kiali/kiali/business/authentication"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/cache"
	"github.com/kiali/kiali/kubernetes/kubetest"
)

type fakeProxy struct{ kubernetes.ClientInterface }

func (f *fakeProxy) SetProxyLogLevel(namespace, podName, level string) error { return nil }

func setupTestLoggingServer(t *testing.T, namespace, pod string) *httptest.Server {
	conf := config.NewConfig()
	config.Set(conf)

	mr := mux.NewRouter()
	path := "/api/namespaces/{namespace}/pods/{pod}/logging"
	mr.HandleFunc(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := authentication.SetAuthInfoContext(r.Context(), &api.AuthInfo{Token: "test"})
		LoggingUpdate(w, r.Clone(ctx))
	}))

	ts := httptest.NewServer(mr)
	t.Cleanup(ts.Close)

	k8s := &fakeProxy{ClientInterface: kubetest.NewFakeK8sClient()}

	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	cache := cache.NewFakeKialiCache(nil, nil)
	business.SetWithBackends(mockClientFactory, nil, cache)

	return ts
}

func TestProxyLoggingSucceeds(t *testing.T) {
	const (
		namespace = "bookinfo"
		pod       = "details-v1-79f774bdb9-hgcch"
	)
	assert := assert.New(t)
	ts := setupTestLoggingServer(t, namespace, pod)

	url := ts.URL + fmt.Sprintf("/api/namespaces/%s/pods/%s/logging?level=info", namespace, pod)
	resp, err := ts.Client().Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equalf(200, resp.StatusCode, "response text: %s", string(body))
}

func TestMissingQueryParamFails(t *testing.T) {
	const (
		namespace = "bookinfo"
		pod       = "details-v1-79f774bdb9-hgcch"
	)
	assert := assert.New(t)
	ts := setupTestLoggingServer(t, namespace, pod)

	url := ts.URL + fmt.Sprintf("/api/namespaces/%s/pods/%s/logging", namespace, pod)
	resp, err := ts.Client().Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equalf(400, resp.StatusCode, "response text: %s", string(body))
}

func TestIncorrectQueryParamFails(t *testing.T) {
	const (
		namespace = "bookinfo"
		pod       = "details-v1-79f774bdb9-hgcch"
	)
	assert := assert.New(t)
	ts := setupTestLoggingServer(t, namespace, pod)

	url := ts.URL + fmt.Sprintf("/api/namespaces/%s/pods/%s/logging?level=peasoup", namespace, pod)
	resp, err := ts.Client().Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equalf(400, resp.StatusCode, "response text: %s", string(body))
}
