package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/kiali/kiali/business"
	"github.com/kiali/kiali/business/authentication"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes/cache"
	"github.com/kiali/kiali/kubernetes/kubetest"
	"github.com/kiali/kiali/prometheus"
	"github.com/kiali/kiali/prometheus/prometheustest"
)

// TODO: MOve to testing.go
type noPrivledge struct{ *kubetest.FakeK8sClient }

func (n *noPrivledge) GetNamespace(namespace string) (*core_v1.Namespace, error) {
	if namespace == "nsNil" {
		return nil, errors.New("no privileges")
	}
	return n.FakeK8sClient.GetNamespace(namespace)
}

// Setup mock
func utilSetupMocks(t *testing.T, kubeObjects ...runtime.Object) promClientSupplier {
	conf := config.NewConfig()
	config.Set(conf)
	kubeObjects = append(kubeObjects,
		&core_v1.Namespace{ObjectMeta: meta_v1.ObjectMeta{Name: "ns1"}},
		&core_v1.Namespace{ObjectMeta: meta_v1.ObjectMeta{Name: "ns2"}},
	)
	k8s := &noPrivledge{FakeK8sClient: kubetest.NewFakeK8sClient(kubeObjects...)}

	promAPI := new(prometheustest.PromAPIMock)
	prom, err := prometheus.NewClient()
	if err != nil {
		t.Fatal(err)
		return nil
	}
	prom.Inject(promAPI)

	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	cache := cache.NewFakeKialiCache(k8s.KubeClientset, k8s.IstioClientset)
	business.SetWithBackends(mockClientFactory, nil, cache)
	return func() (*prometheus.Client, error) { return prom, nil }
}

func TestCreateMetricsServiceForNamespace(t *testing.T) {
	assert := assert.New(t)
	prom := utilSetupMocks(t)

	req := httptest.NewRequest("GET", "/foo", nil)
	req = req.WithContext(authentication.SetAuthInfoContext(req.Context(), &api.AuthInfo{Token: "test"}))

	w := httptest.NewRecorder()
	srv, info := createMetricsServiceForNamespace(w, req, prom, "ns1")

	assert.NotNil(srv)
	assert.NotNil(info)
	assert.Equal("ns1", info.Name)
	assert.Equal(http.StatusOK, w.Code)
}

func TestCreateMetricsServiceForNamespaceForbidden(t *testing.T) {
	assert := assert.New(t)
	prom := utilSetupMocks(t)

	req := httptest.NewRequest("GET", "/foo", nil)
	req = req.WithContext(authentication.SetAuthInfoContext(req.Context(), &api.AuthInfo{Token: "test"}))

	w := httptest.NewRecorder()
	srv, info := createMetricsServiceForNamespace(w, req, prom, "nsNil")

	assert.Nil(srv)
	assert.Nil(info)
	assert.Equal(http.StatusForbidden, w.Code)
}

func TestCreateMetricsServiceForSeveralNamespaces(t *testing.T) {
	assert := assert.New(t)
	prom := utilSetupMocks(t)

	req := httptest.NewRequest("GET", "/foo", nil)
	req = req.WithContext(authentication.SetAuthInfoContext(req.Context(), &api.AuthInfo{Token: "test"}))

	w := httptest.NewRecorder()
	srv, info := createMetricsServiceForNamespaces(w, req, prom, []string{"ns1", "ns2", "nsNil"})

	assert.NotNil(srv)
	assert.Len(info, 3)
	assert.Equal(http.StatusOK, w.Code)
	assert.Equal("ns1", info["ns1"].info.Name)
	assert.Nil(info["ns1"].err)
	assert.Equal("ns2", info["ns2"].info.Name)
	assert.Nil(info["ns2"].err)
	assert.Nil(info["nsNil"].info)
	assert.Equal("no privileges", info["nsNil"].err.Error())
}
