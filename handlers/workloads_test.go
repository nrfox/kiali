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
	corev1 "k8s.io/api/core/v1"
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
	"github.com/kiali/kiali/util"
)

// TODO: MOve this or combine?
func mockClock() {
	clockTime := time.Date(2017, 0o1, 15, 0, 0, 0, 0, time.UTC)
	util.Clock = util.ClockMock{Time: clockTime}
}

func setupWorkloadList(t *testing.T, k8s *kubetest.FakeK8sClient) (*httptest.Server, *prometheustest.PromClientMock) {
	prom := new(prometheustest.PromClientMock)

	business.SetupBusinessLayer(k8s, *config.Get())

	mr := mux.NewRouter()

	mr.HandleFunc("/api/namespaces/{namespace}/workloads", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			context := authentication.SetAuthInfoContext(r.Context(), &api.AuthInfo{Token: "test"})
			WorkloadList(w, r.WithContext(context))
		}))

	ts := httptest.NewServer(mr)
	t.Cleanup(ts.Close)
	return ts, prom
}

func TestWorkloadsEndpoint(t *testing.T) {
	conf := config.NewConfig()
	config.Set(conf)
	mockClock()

	kubeObjects := []runtime.Object{&corev1.Namespace{ObjectMeta: meta_v1.ObjectMeta{Name: "ns"}}}
	for _, obj := range business.FakeDepSyncedWithRS() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	for _, obj := range business.FakeRSSyncedWithPods() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	for _, obj := range business.FakePodsSyncedWithDeployments() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	k8s := kubetest.NewFakeK8sClient(kubeObjects...)
	ts, _ := setupWorkloadList(t, k8s)

	url := ts.URL + "/api/namespaces/ns/workloads"

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.NotEmpty(t, actual)
	assert.Equal(t, 200, resp.StatusCode, string(actual))
}

func TestWorkloadMetricsDefault(t *testing.T) {
	ts, api, _ := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	url := ts.URL + "/api/namespaces/ns/workloads/my_workload/metrics"
	now := time.Now()
	delta := 15 * time.Second
	var gaugeSentinel uint32

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		query := args[1].(string)
		assert.IsType(t, prom_v1.Range{}, args[2])
		r := args[2].(prom_v1.Range)
		assert.Contains(t, query, "_workload=\"my_workload\"")
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

func TestWorkloadMetricsWithParams(t *testing.T) {
	ts, api, _ := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/namespaces/ns/workloads/my-workload/metrics", nil)
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

func TestWorkloadMetricsBadQueryTime(t *testing.T) {
	ts, api, _ := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/namespaces/ns/workloads/my-workload/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	q.Add("rateInterval", "5h")
	q.Add("step", "99")
	q.Add("queryTime", "abc")
	q.Add("duration", "1000")
	req.URL.RawQuery = q.Encode()

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		// Make sure there's no client call and we fail fast
		t.Error("Unexpected call to client while having bad request")
	})

	httpclient := &http.Client{}
	resp, err := httpclient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 400, resp.StatusCode)
	assert.Contains(t, string(actual), "cannot parse query parameter 'queryTime'")
}

func TestWorkloadMetricsBadDuration(t *testing.T) {
	ts, api, _ := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/namespaces/ns/workloads/my-workload/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	q.Add("rateInterval", "5h")
	q.Add("step", "99")
	q.Add("duration", "abc")
	req.URL.RawQuery = q.Encode()

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		// Make sure there's no client call and we fail fast
		t.Error("Unexpected call to client while having bad request")
	})

	httpclient := &http.Client{}
	resp, err := httpclient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 400, resp.StatusCode)
	assert.Contains(t, string(actual), "cannot parse query parameter 'duration'")
}

func TestWorkloadMetricsBadStep(t *testing.T) {
	ts, api, _ := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/namespaces/ns/workloads/my-workload/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	q.Add("rateInterval", "5h")
	q.Add("step", "abc")
	q.Add("duration", "1000")
	req.URL.RawQuery = q.Encode()

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		// Make sure there's no client call and we fail fast
		t.Error("Unexpected call to client while having bad request")
	})

	httpclient := &http.Client{}
	resp, err := httpclient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 400, resp.StatusCode)
	assert.Contains(t, string(actual), "cannot parse query parameter 'step'")
}

func TestWorkloadMetricsBadRateFunc(t *testing.T) {
	ts, api, _ := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/namespaces/ns/workloads/my-workload/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	q.Add("rateInterval", "5h")
	q.Add("rateFunc", "invalid rate func")
	req.URL.RawQuery = q.Encode()

	api.SpyArgumentsAndReturnEmpty(func(args mock.Arguments) {
		// Make sure there's no client call and we fail fast
		t.Error("Unexpected call to client while having bad request")
	})

	httpclient := &http.Client{}
	resp, err := httpclient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 400, resp.StatusCode)
	assert.Contains(t, string(actual), "query parameter 'rateFunc' must be either 'rate' or 'irate'")
}

// TODO:
func TestWorkloadMetricsInaccessibleNamespace(t *testing.T) {
	ts, _, k8s := setupWorkloadMetricsEndpoint(t)
	defer ts.Close()

	url := ts.URL + "/api/namespaces/my_namespace/workloads/my_workload/metrics"

	var nsNil *osproject_v1.Project
	k8s.On("GetProject", "my_namespace").Return(nsNil, errors.New("no privileges"))

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	k8s.AssertCalled(t, "GetProject", "my_namespace")
}

func setupWorkloadMetricsEndpoint(t *testing.T) (*httptest.Server, *prometheustest.PromAPIMock) {
	config.Set(config.NewConfig())
	xapi := new(prometheustest.PromAPIMock)
	prom, err := prometheus.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	prom.Inject(xapi)

	mr := mux.NewRouter()
	mr.HandleFunc("/api/namespaces/{namespace}/workloads/{workload}/metrics", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			context := authentication.SetAuthInfoContext(r.Context(), &api.AuthInfo{Token: "test"})
			getWorkloadMetrics(w, r.WithContext(context), func() (*prometheus.Client, error) {
				return prom, nil
			})
		}))

	ts := httptest.NewServer(mr)

	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	cache := cache.NewKialiCache(mockClientFactory)
	business.SetWithBackendsWithCache(mockClientFactory, prom, cache)

	return ts, xapi, k8s
}
