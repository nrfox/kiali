package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	osproject_v1 "github.com/openshift/api/project/v1"
	prom_v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	"github.com/kiali/kiali/prometheus"
	"github.com/kiali/kiali/prometheus/prometheustest"
	"github.com/kiali/kiali/util"
)

func TestAppMetricsDefault(t *testing.T) {
	ts, api, _ := setupAppMetricsEndpoint(t)
	defer ts.Close()

	url := ts.URL + "/api/namespaces/ns/apps/my_app/metrics"
	now := time.Now()
	delta := 15 * time.Second
	var gaugeSentinel uint32

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		query := args[1].(string)
		assert.IsType(t, prom_v1.Range{}, args[2])
		r := args[2].(prom_v1.Range)
		assert.Contains(t, query, "_canonical_service=\"my_app\"")
		assert.Contains(t, query, "_namespace=\"ns\"")
		assert.Contains(t, query, "[1m]")
		assert.NotContains(t, query, "histogram_quantile")
		atomic.AddUint32(&gaugeSentinel, 1)
		assert.Equal(t, 15*time.Second, r.Step)
		assert.WithinDuration(t, now, r.End, delta)
		assert.WithinDuration(t, now.Add(-30*time.Minute), r.Start, delta)
	})

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}

	actual, _ := io.ReadAll(resp.Body)

	assert.NotEmpty(t, actual)
	assert.Equal(t, 200, resp.StatusCode, string(actual))
	// Assert branch coverage
	assert.NotZero(t, gaugeSentinel)
}

func TestAppMetricsWithParams(t *testing.T) {
	ts, api, _ := setupAppMetricsEndpoint(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/namespaces/ns/apps/my-app/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	q.Add("rateInterval", "5h")
	q.Add("rateFunc", "rate")
	q.Add("step", "2")
	q.Add("queryTime", "1523364075")
	q.Add("duration", "1000")
	q.Add("byLabels[]", "response_code")
	q.Add("quantiles[]", "0.5")
	q.Add("quantiles[]", "0.95")
	q.Add("filters[]", "request_count")
	q.Add("filters[]", "request_size")
	req.URL.RawQuery = q.Encode()

	queryTime := time.Unix(1523364075, 0)
	delta := 2 * time.Second
	var histogramSentinel, gaugeSentinel uint32

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		query := args[1].(string)
		assert.IsType(t, prom_v1.Range{}, args[2])
		r := args[2].(prom_v1.Range)
		assert.Contains(t, query, "rate(")
		assert.Contains(t, query, "[5h]")
		if strings.Contains(query, "histogram_quantile") {
			// Histogram specific queries
			assert.Contains(t, query, " by (le,response_code)")
			assert.Contains(t, query, "istio_request_bytes")
			atomic.AddUint32(&histogramSentinel, 1)
		} else {
			assert.Contains(t, query, " by (response_code)")
			atomic.AddUint32(&gaugeSentinel, 1)
		}
		assert.Equal(t, 2*time.Second, r.Step)
		assert.WithinDuration(t, queryTime, r.End, delta)
		assert.WithinDuration(t, queryTime.Add(-1000*time.Second), r.Start, delta)
	})

	httpclient := &http.Client{}
	resp, err := httpclient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.NotEmpty(t, actual)
	assert.Equal(t, 200, resp.StatusCode, string(actual))
	// Assert branch coverage
	assert.NotZero(t, histogramSentinel)
	assert.NotZero(t, gaugeSentinel)
}

func TestAppMetricsInaccessibleNamespace(t *testing.T) {
	ts, _, k8s := setupAppMetricsEndpoint(t)
	defer ts.Close()

	url := ts.URL + "/api/namespaces/my_namespace/apps/my_app/metrics"

	var nsNil *core_v1.Namespace
	k8s.On("GetNamespace", "my_namespace").Return(nsNil, errors.New("no privileges"))

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	k8s.AssertCalled(t, "GetNamespace", "my_namespace")
}

func setupAppMetricsEndpoint(t *testing.T) (*httptest.Server, *prometheustest.PromAPIMock, *kubetest.K8SClientMock) {
	conf := config.NewConfig()
	config.Set(conf)
	xapi := new(prometheustest.PromAPIMock)
	k8s := new(kubetest.K8SClientMock)
	prom, err := prometheus.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	prom.Inject(xapi)
	k8s.On("IsOpenShift").Return(false)
	k8s.On("IsGatewayAPI").Return(false)
	k8s.On("GetNamespace", "ns").Return(&core_v1.Namespace{}, nil)

	mr := mux.NewRouter()

	authInfo := &api.AuthInfo{Token: "test"}

	mr.HandleFunc("/api/namespaces/{namespace}/apps/{app}/metrics", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			context := authentication.SetAuthInfoContext(r.Context(), authInfo)
			getAppMetrics(w, r.WithContext(context), func() (*prometheus.Client, error) {
				return prom, nil
			})
		}))

	ts := httptest.NewServer(mr)

	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	cache := cache.NewFakeKialiCache(nil, nil)
	business.SetWithBackends(mockClientFactory, prom, cache)

	return ts, xapi, k8s
}

func setupAppListEndpoint(k8s kubernetes.ClientInterface, cache *cache.KialiCache) (*httptest.Server, *prometheustest.PromClientMock) {
	conf := config.NewConfig()
	config.Set(conf)
	prom := new(prometheustest.PromClientMock)

	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	business.SetWithBackends(mockClientFactory, prom, cache)
	business.SetKialiControlPlaneCluster(&business.Cluster{Name: business.DefaultClusterID})

	mr := mux.NewRouter()
	mr.HandleFunc("/api/namespaces/{namespace}/apps", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			context := authentication.SetAuthInfoContext(r.Context(), &api.AuthInfo{Token: "test"})
			AppList(w, r.WithContext(context))
		}))

	mr.HandleFunc("/api/namespaces/{namespace}/apps/{app}", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			context := authentication.SetAuthInfoContext(r.Context(), &api.AuthInfo{Token: "test"})
			AppDetails(w, r.WithContext(context))
		}))

	ts := httptest.NewServer(mr)
	return ts, prom
}

// TODO: Clock needs to get mocked. Redo name and how this is called
// since it has necessary side effects of the clock mocking.
func newProject() *osproject_v1.Project {
	clockTime := time.Date(2017, 0o1, 15, 0, 0, 0, 0, time.UTC)
	util.Clock = util.ClockMock{Time: clockTime}
	return &osproject_v1.Project{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:              "ns",
			CreationTimestamp: meta_v1.NewTime(clockTime.Add(-17 * time.Second)),
		},
	}
}

func TestAppsEndpoint(t *testing.T) {
	assert := assert.New(t)
	proj := newProject()
	proj.Name = "Namespace"
	kubeObjects := []runtime.Object{proj}
	for _, obj := range business.FakeDeployments() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	k8s := kubetest.NewFakeK8sClient(kubeObjects...)
	kialiCache := cache.NewFakeKialiCache(k8s.KubeClientset, k8s.IstioClientset)
	k8s.OpenShift = true
	ts, _ := setupAppListEndpoint(k8s, kialiCache)
	defer ts.Close()

	url := ts.URL + "/api/namespaces/Namespace/apps"

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.NotEmpty(actual)
	assert.Equal(200, resp.StatusCode, string(actual))
}

func TestAppDetailsEndpoint(t *testing.T) {
	assert := assert.New(t)
	proj := newProject()
	proj.Name = "Namespace"
	kubeObjects := []runtime.Object{proj}
	for _, obj := range business.FakeDeployments() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	for _, obj := range business.FakeServices() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	k8s := kubetest.NewFakeK8sClient(kubeObjects...)
	k8s.OpenShift = true
	kialiCache := cache.NewFakeKialiCache(k8s.KubeClientset, k8s.IstioClientset)
	ts, _ := setupAppListEndpoint(k8s, kialiCache)
	defer ts.Close()

	url := ts.URL + "/api/namespaces/Namespace/apps/httpbin"

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.NotEmpty(actual)
	assert.Equal(200, resp.StatusCode, string(actual))
}
