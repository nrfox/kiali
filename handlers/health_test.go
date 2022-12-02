package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	osproject_v1 "github.com/openshift/api/project/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/kiali/kiali/business"
	"github.com/kiali/kiali/business/authentication"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/cache"
	"github.com/kiali/kiali/kubernetes/kubetest"
	"github.com/kiali/kiali/prometheus/prometheustest"
	"github.com/kiali/kiali/util"
)

func fakeService(namespace, name string) *core_v1.Service {
	return &core_v1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: core_v1.ServiceSpec{
			ClusterIP: "fromservice",
			Type:      "ClusterIP",
			Selector:  map[string]string{"app": name},
			Ports: []core_v1.ServicePort{
				{
					Name:     "http",
					Protocol: "TCP",
					Port:     3001,
				},
				{
					Name:     "http",
					Protocol: "TCP",
					Port:     3000,
				},
			},
		},
	}
}

// TestNamespaceAppHealth is unit test (testing request handling, not the prometheus client behaviour)
func TestNamespaceAppHealth(t *testing.T) {
	conf := config.NewConfig()
	config.Set(conf)
	var cacheObjects []runtime.Object
	kubeObjects := []runtime.Object{fakeService("ns", "reviews"), fakeService("ns", "httpbin")}
	for _, obj := range kubetest.FakePodList() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	cacheObjects = append(cacheObjects, kubeObjects...)
	kubeObjects = append(kubeObjects, setupMockData())
	k8s := kubetest.NewFakeK8sClient(kubeObjects...)
	k8s.OpenShift = true
	ts, prom := setupNamespaceHealthEndpoint(t, k8s, cacheObjects...)

	url := ts.URL + "/api/namespaces/ns/health"

	// Test 17s on rate interval to check that rate interval is adjusted correctly.
	prom.On("GetAllRequestRates", "ns", "17s", util.Clock.Now()).Return(model.Vector{}, nil)

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.NotEmpty(t, actual)
	assert.Equal(t, 200, resp.StatusCode, string(actual))
	prom.AssertNumberOfCalls(t, "GetAllRequestRates", 1)
	// TODO: Need to add some additional assertions here?
}

// TODO: Combine cache and kube clients.
func setupNamespaceHealthEndpoint(t *testing.T, k8s kubernetes.ClientInterface, objects ...runtime.Object) (*httptest.Server, *prometheustest.PromClientMock) {
	prom := new(prometheustest.PromClientMock)

	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	cache := cache.NewFakeKialiCache(objects, nil)
	cache.Refresh("")
	// TODO: Update business layer to mock out registry?
	business.SetWithBackends(mockClientFactory, prom, cache)
	business.SetKialiControlPlaneCluster(&business.Cluster{Name: business.DefaultClusterID})

	mr := mux.NewRouter()

	mr.HandleFunc("/api/namespaces/{namespace}/health", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			context := authentication.SetAuthInfoContext(r.Context(), &api.AuthInfo{Token: "test"})
			NamespaceHealth(w, r.WithContext(context))
		}))

	ts := httptest.NewServer(mr)
	t.Cleanup(ts.Close)
	return ts, prom
}

// TODO: Combine with other one and do away with side effects.
func setupMockData() *osproject_v1.Project {
	clockTime := time.Date(2017, 0o1, 15, 0, 0, 0, 0, time.UTC)
	util.Clock = util.ClockMock{Time: clockTime}

	return &osproject_v1.Project{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:              "ns",
			CreationTimestamp: meta_v1.NewTime(clockTime.Add(-17 * time.Second)),
		},
	}
}
