package business

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	osproject_v1 "github.com/openshift/api/project/v1"
	"github.com/stretchr/testify/assert"
	apps_v1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/jaeger"
	"github.com/kiali/kiali/jaeger/jaegertest"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/kubetest"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/models"
)

type addOnsSetup struct {
	Url        string
	StatusCode int
	CallCount  *int
}

var notReadyStatus = apps_v1.DeploymentStatus{
	Replicas:            0,
	AvailableReplicas:   0,
	UnavailableReplicas: 0,
}

var healthyStatus = apps_v1.DeploymentStatus{
	Replicas:            2,
	AvailableReplicas:   2,
	UnavailableReplicas: 0,
}

var unhealthyStatus = apps_v1.DeploymentStatus{
	Replicas:            2,
	AvailableReplicas:   1,
	UnavailableReplicas: 1,
}

var healthyDaemonSetStatus = apps_v1.DaemonSetStatus{
	DesiredNumberScheduled: 2,
	CurrentNumberScheduled: 2,
	NumberAvailable:        2,
	NumberUnavailable:      0,
}

var unhealthyDaemonSetStatus = apps_v1.DaemonSetStatus{
	DesiredNumberScheduled: 2,
	CurrentNumberScheduled: 2,
	NumberAvailable:        1,
	NumberUnavailable:      1,
}

func TestComponentNotRunning(t *testing.T) {
	assert := assert.New(t)

	dss := []apps_v1.DeploymentStatus{
		{
			Replicas:            3,
			AvailableReplicas:   2,
			UnavailableReplicas: 1,
		},
		{
			Replicas:            1,
			AvailableReplicas:   0,
			UnavailableReplicas: 0,
		},
	}

	for _, ds := range dss {
		d := fakeDeploymentWithStatus(
			"istio-egressgateway",
			map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"},
			ds,
		)
		wl := &models.Workload{}
		wl.ParseDeployment(d)
		assert.Equal(kubernetes.ComponentUnhealthy, GetWorkloadStatus(*wl))
	}
}

func TestComponentRunning(t *testing.T) {
	assert := assert.New(t)

	d := fakeDeploymentWithStatus(
		"istio-egressgateway",
		map[string]string{"app": "istio-egressgateway"},
		apps_v1.DeploymentStatus{
			Replicas:            2,
			AvailableReplicas:   2,
			UnavailableReplicas: 0,
		})

	wl := &models.Workload{}
	wl.ParseDeployment(d)

	assert.Equal(kubernetes.ComponentHealthy, GetWorkloadStatus(*wl))
}

func TestComponentNamespaces(t *testing.T) {
	a := assert.New(t)

	conf := confWithComponentNamespaces()
	config.Set(conf)

	nss := getComponentNamespaces()

	a.Contains(nss, "istio-system")
	a.Contains(nss, "istio-admin")
	a.Contains(nss, "ingress-egress")
	a.Len(nss, 4)
}

func mockAddOnsCalls(t *testing.T, objects []runtime.Object, isIstioReachable bool, overrideAddonURLs bool) (kubernetes.ClientInterface, *httptest.Server, *int, *int) {
	// Prepare the Call counts for each Addon
	grafanaCalls, prometheusCalls := 0, 0

	objects = append(objects, &osproject_v1.Project{ObjectMeta: meta_v1.ObjectMeta{Name: "istio-system"}})

	// Mock k8s api calls
	k8s := mockDeploymentCall(objects, isIstioReachable)
	routes := mockAddOnCalls(defaultAddOnCalls(&grafanaCalls, &prometheusCalls))
	httpServer := mockServer(routes)

	// Adapt the AddOns URLs to the mock Server
	conf := addonAddMockUrls(httpServer.URL, config.NewConfig(), overrideAddonURLs)
	config.Set(conf)

	return k8s, httpServer, &grafanaCalls, &prometheusCalls
}

func sampleIstioComponent() ([]runtime.Object, bool, bool) {
	deployment := fakeDeploymentWithStatus(
		"istio-egressgateway",
		map[string]string{"app": "istio-egressgateway"},
		apps_v1.DeploymentStatus{
			Replicas:            2,
			AvailableReplicas:   2,
			UnavailableReplicas: 0,
		})
	objects := []runtime.Object{deployment}
	for _, obj := range healthyIstiods() {
		o := obj
		objects = append(objects, &o)
	}
	return objects, true, false
}

func healthyIstiods() []v1.Pod {
	return []v1.Pod{
		fakePod("istiod-x3v1kn0l-running", "istio-system", "istiod", "Running"),
		fakePod("istiod-x3v1kn1l-running", "istio-system", "istiod", "Running"),
		fakePod("istiod-x3v1kn0l-terminating", "istio-system", "istiod", "Terminating"),
		fakePod("istiod-x3v1kn1l-terminating", "istio-system", "istiod", "Terminating"),
	}
}

func fakePod(name, namespace, appLabel, phase string) v1.Pod {
	return v1.Pod{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": appLabel,
			},
		},
		Status: v1.PodStatus{
			Phase: v1.PodPhase(phase),
		},
	}
}

func TestGrafanaWorking(t *testing.T) {
	assert := assert.New(t)

	// TODO: Name something better
	objs, b1, b2 := sampleIstioComponent()
	k8s, httpServ, grafanaCalls, promCalls := mockAddOnsCalls(t, objs, b1, b2)
	defer httpServ.Close()

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus
	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)

	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "custom dashboards")
}

func TestGrafanaDisabled(t *testing.T) {
	assert := assert.New(t)

	// TODO: Name something better
	objects := []runtime.Object{
		fakeDeploymentWithStatus(
			"istio-egressgateway",
			map[string]string{"app": "istio-egressgateway"},
			apps_v1.DeploymentStatus{
				Replicas:            2,
				AvailableReplicas:   2,
				UnavailableReplicas: 0,
			}),
	}
	k8s, httpServ, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, true, false)
	defer httpServ.Close()

	// Disable Grafana
	conf := config.Get()
	conf.ExternalServices.Grafana.Enabled = false
	config.Set(conf)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus
	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)

	// Only two Istio components are missing
	assert.Equal(2, len(icsl))

	// No request performed to Grafana endpoint
	assert.Zero(*grafanaCalls)

	// Requests to Jaeger and Prometheus performed once
	assert.Equal(1, *promCalls)

	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "custom dashboards")
}

func TestGrafanaNotWorking(t *testing.T) {
	assert := assert.New(t)
	grafanaCalls, prometheusCalls := 0, 0
	objects := []runtime.Object{
		fakeDeploymentWithStatus(
			"istio-egressgateway",
			map[string]string{"app": "istio-egressgateway"},
			apps_v1.DeploymentStatus{
				Replicas:            2,
				AvailableReplicas:   2,
				UnavailableReplicas: 0,
			}),
	}
	objects = append(objects, &osproject_v1.Project{ObjectMeta: meta_v1.ObjectMeta{Name: "istio-system"}})
	k8s := mockDeploymentCall(objects, true)
	addOnsStetup := defaultAddOnCalls(&grafanaCalls, &prometheusCalls)
	addOnsStetup["grafana"] = addOnsSetup{
		Url:        "/grafana/mock",
		StatusCode: 501,
		CallCount:  &grafanaCalls,
	}
	routes := mockAddOnCalls(addOnsStetup)
	httpServer := mockServer(routes)
	defer httpServer.Close()

	// Adapt the AddOns URLs to the mock Server
	conf := addonAddMockUrls(httpServer.URL, config.NewConfig(), false)
	config.Set(conf)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus
	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)

	// Grafana and two Istio comps missing
	assert.Equal(3, len(icsl))

	// Requests to AddOns have to be 1
	assert.Equal(1, grafanaCalls)
	assert.Equal(1, prometheusCalls)

	assertComponent(assert, icsl, "grafana", kubernetes.ComponentUnreachable, false)
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "custom dashboards")
}

func TestFailingTracingService(t *testing.T) {
	assert := assert.New(t)

	// TODO: Name something better
	objs, b1, b2 := sampleIstioComponent()
	k8s, httpServ, grafanaCalls, promCalls := mockAddOnsCalls(t, objs, b1, b2)
	defer httpServ.Close()

	iss := NewWithBackends(k8s, nil, mockFailingJaeger).IstioStatus
	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)

	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "custom dashboards")
	assertComponent(assert, icsl, "jaeger", kubernetes.ComponentUnreachable, false)
}

func TestOverriddenUrls(t *testing.T) {
	assert := assert.New(t)

	objects, idReachable, _ := sampleIstioComponent()
	k8s, httpServ, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, idReachable, true)
	defer httpServ.Close()

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus
	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)

	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "custom dashboards")
}

func TestCustomDashboardsMainPrometheus(t *testing.T) {
	assert := assert.New(t)

	// TODO: Name something better
	objs, b1, b2 := sampleIstioComponent()
	k8s, httpServ, grafanaCalls, promCalls := mockAddOnsCalls(t, objs, b1, b2)
	defer httpServ.Close()

	// Custom Dashboard prom URL forced to be empty
	conf := config.Get()
	conf.ExternalServices.CustomDashboards.Prometheus.URL = ""
	config.Set(conf)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus
	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(2, *promCalls)

	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "custom dashboards")
}

func TestNoIstioComponentFoundError(t *testing.T) {
	assert := assert.New(t)

	k8s, httpServ, _, _ := mockAddOnsCalls(t, []runtime.Object{}, true, false)
	defer httpServ.Close()

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus
	_, error := iss.GetStatus(context.TODO())
	assert.Error(error)
}

func TestDefaults(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, healthyStatus),
	}

	for _, obj := range healthyIstiods() {
		o := obj
		objects = append(objects, &o)
	}

	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, true, false)
	defer httpServer.Close()

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, err := iss.GetStatus(context.TODO())
	assert.NoError(err)
	assertComponent(assert, icsl, "istio-ingressgateway", kubernetes.ComponentNotFound, true)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "istiod")
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

func TestNonDefaults(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, healthyStatus),
	}

	for _, obj := range healthyIstiods() {
		o := obj
		objects = append(objects, &o)
	}

	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, true, false)
	defer httpServer.Close()

	c := config.Get()
	c.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "istiod", IsCore: false},
			{AppLabel: "istio-egressgateway", IsCore: false},
			{AppLabel: "istio-ingressgateway", IsCore: false},
		},
	}
	config.Set(c)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)
	assertComponent(assert, icsl, "istio-ingressgateway", kubernetes.ComponentNotFound, false)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "istiod")
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

// Istiod replicas is downscaled to 0
// Kiali should notify that in the Istio Component Status
func TestIstiodNotReady(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, notReadyStatus),
	}

	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, false, false)
	defer httpServer.Close()

	c := config.Get()
	c.IstioLabels.AppLabelName = "app.kubernetes.io/name"
	c.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "istiod", IsCore: true},
			{AppLabel: "istio-egressgateway", IsCore: false},
			{AppLabel: "istio-ingressgateway", IsCore: false},
		},
	}
	config.Set(c)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)
	assertComponent(assert, icsl, "istio-ingressgateway", kubernetes.ComponentNotFound, false)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)
	assertComponent(assert, icsl, "istiod", kubernetes.ComponentNotReady, true)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "istiod-x3v1kn0l-terminating")
	assertNotPresent(assert, icsl, "istiod-x3v1kn1l-terminating")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

// Istiod pods are not reachable from kiali
// Kiali should notify that in the Istio Component Status
func TestIstiodUnreachable(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, healthyStatus),
	}

	var istioStatus kubernetes.IstioComponentStatus
	for _, pod := range healthyIstiods() {
		// Only running pods are considered healthy.
		if pod.Status.Phase == v1.PodRunning && pod.Labels["app"] == "istiod" {
			istioStatus = append(istioStatus, kubernetes.ComponentStatus{
				Name:   pod.Name,
				Status: kubernetes.ComponentUnreachable,
				IsCore: true,
			})
		}
	}
	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, false, false)
	defer httpServer.Close()

	k8s = &fakeIstiodConnecter{k8s, istioStatus}

	c := config.Get()
	c.IstioLabels.AppLabelName = "app.kubernetes.io/name"
	c.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "istiod", IsCore: true},
			{AppLabel: "istio-egressgateway", IsCore: false},
			{AppLabel: "istio-ingressgateway", IsCore: false},
		},
	}
	config.Set(c)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)
	assertComponent(assert, icsl, "istio-ingressgateway", kubernetes.ComponentNotFound, false)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)
	assertComponent(assert, icsl, "istiod-x3v1kn0l-running", kubernetes.ComponentUnreachable, true)
	assertComponent(assert, icsl, "istiod-x3v1kn1l-running", kubernetes.ComponentUnreachable, true)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")
	assertNotPresent(assert, icsl, "istiod-x3v1kn0l-terminating")
	assertNotPresent(assert, icsl, "istiod-x3v1kn1l-terminating")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

// Istio deployments only have the "app" app_label.
// Users can't customize this one. They can only customize it for their own deployments.
func TestCustomizedAppLabel(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, healthyStatus),
	}

	for _, obj := range healthyIstiods() {
		o := obj
		objects = append(objects, &o)
	}

	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, true, false)
	defer httpServer.Close()

	c := config.Get()
	c.IstioLabels.AppLabelName = "app.kubernetes.io/name"
	c.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "istiod", IsCore: false},
			{AppLabel: "istio-egressgateway", IsCore: false},
			{AppLabel: "istio-ingressgateway", IsCore: false},
		},
	}
	config.Set(c)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)
	assertComponent(assert, icsl, "istio-ingressgateway", kubernetes.ComponentNotFound, false)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "istiod")
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

func TestDaemonSetComponentHealthy(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDaemonSetWithStatus("istio-ingressgateway", map[string]string{"app": "istio-ingressgateway", "istio": "ingressgateway"}, healthyDaemonSetStatus),
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, healthyStatus),
	}

	for _, obj := range healthyIstiods() {
		o := obj
		objects = append(objects, &o)
	}

	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, true, false)
	defer httpServer.Close()

	c := config.Get()
	c.IstioLabels.AppLabelName = "app.kubernetes.io/name"
	c.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "istiod", IsCore: false},
			{AppLabel: "istio-egressgateway", IsCore: false},
			{AppLabel: "istio-ingressgateway", IsCore: false},
		},
	}
	config.Set(c)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "istio-ingressgateway")
	assertNotPresent(assert, icsl, "istiod")
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

// Users may use DaemonSets to deploy istio components
func TestDaemonSetComponentUnhealthy(t *testing.T) {
	assert := assert.New(t)

	objects := []runtime.Object{
		fakeDaemonSetWithStatus("istio-ingressgateway", map[string]string{"app": "istio-ingressgateway", "istio": "ingressgateway"}, unhealthyDaemonSetStatus),
		fakeDeploymentWithStatus("istio-egressgateway", map[string]string{"app": "istio-egressgateway", "istio": "egressgateway"}, unhealthyStatus),
		fakeDeploymentWithStatus("istiod", map[string]string{"app": "istiod", "istio": "pilot"}, healthyStatus),
	}

	k8s, httpServer, grafanaCalls, promCalls := mockAddOnsCalls(t, objects, true, false)
	defer httpServer.Close()

	c := config.Get()
	c.IstioLabels.AppLabelName = "app.kubernetes.io/name"
	c.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "istiod", IsCore: false},
			{AppLabel: "istio-egressgateway", IsCore: false},
			{AppLabel: "istio-ingressgateway", IsCore: false},
		},
	}
	config.Set(c)

	iss := NewWithBackends(k8s, nil, mockJaeger).IstioStatus

	icsl, error := iss.GetStatus(context.TODO())
	assert.NoError(error)
	assertComponent(assert, icsl, "istio-ingressgateway", kubernetes.ComponentUnhealthy, false)
	assertComponent(assert, icsl, "istio-egressgateway", kubernetes.ComponentUnhealthy, false)

	// Don't return kubernetes.ComponentHealthy deployments
	assertNotPresent(assert, icsl, "istiod")
	assertNotPresent(assert, icsl, "grafana")
	assertNotPresent(assert, icsl, "prometheus")
	assertNotPresent(assert, icsl, "jaeger")

	// Requests to AddOns have to be 1
	assert.Equal(1, *grafanaCalls)
	assert.Equal(1, *promCalls)
}

func assertComponent(assert *assert.Assertions, icsl kubernetes.IstioComponentStatus, name string, status string, isCore bool) {
	componentFound := false
	for _, ics := range icsl {
		if ics.Name == name {
			assert.Equal(status, ics.Status)
			assert.Equal(isCore, ics.IsCore)
			componentFound = true
		}
	}

	assert.True(componentFound)
}

func assertNotPresent(assert *assert.Assertions, icsl kubernetes.IstioComponentStatus, name string) {
	componentFound := false
	for _, ics := range icsl {
		if ics.Name == name {
			componentFound = true
		}
	}
	assert.False(componentFound)
}

func mockJaeger() (jaeger.ClientInterface, error) {
	j := new(jaegertest.JaegerClientMock)
	j.On("GetServiceStatus").Return(true, nil)
	return jaeger.ClientInterface(j), nil
}

func mockFailingJaeger() (jaeger.ClientInterface, error) {
	j := new(jaegertest.JaegerClientMock)
	j.On("GetServiceStatus").Return(false, errors.New("error connecting with jaeger service"))
	return jaeger.ClientInterface(j), nil
}

// func newFakeIstiodConnector()
type fakeIstiodConnecter struct {
	kubernetes.ClientInterface
	status kubernetes.IstioComponentStatus
}

func (in *fakeIstiodConnecter) CanConnectToIstiod() (kubernetes.IstioComponentStatus, error) {
	return in.status, nil
}

// Setup K8S api call to fetch Pods
func mockDeploymentCall(objects []runtime.Object, isIstioReachable bool) kubernetes.ClientInterface {
	k8s := kubetest.NewFakeK8sClient(objects...)
	k8s.OpenShift = true

	var istioStatus kubernetes.IstioComponentStatus
	if !isIstioReachable {
		for _, obj := range objects {
			if pod, isPod := obj.(*v1.Pod); isPod {
				// Only running pods are considered healthy.
				if pod.Status.Phase == v1.PodRunning && pod.Labels["app"] == "istiod" {
					istioStatus = append(istioStatus, kubernetes.ComponentStatus{
						Name:   pod.Name,
						Status: kubernetes.ComponentUnreachable,
						IsCore: true,
					})
				}
			}
		}
	}

	return &fakeIstiodConnecter{k8s, istioStatus}
}

func fakeDeploymentWithStatus(name string, labels map[string]string, status apps_v1.DeploymentStatus) *apps_v1.Deployment {
	return &apps_v1.Deployment{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      name,
			Namespace: "istio-system",
			Labels:    labels,
		},
		Status: status,
		Spec: apps_v1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:   "",
					Labels: labels,
				},
			},
			Replicas: &status.Replicas,
		},
	}
}

func fakeDaemonSetWithStatus(name string, labels map[string]string, status apps_v1.DaemonSetStatus) *apps_v1.DaemonSet {
	return &apps_v1.DaemonSet{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      name,
			Namespace: "istio-system",
			Labels:    labels,
		},
		Status: status,
		Spec: apps_v1.DaemonSetSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:   "",
					Labels: labels,
				},
			},
		},
	}
}

func confWithComponentNamespaces() *config.Config {
	conf := config.NewConfig()
	conf.ExternalServices.Istio.ComponentStatuses = config.ComponentStatuses{
		Enabled: true,
		Components: []config.ComponentStatus{
			{AppLabel: "pilot", IsCore: true},
			{AppLabel: "ingress", IsCore: true, Namespace: "ingress-egress"},
			{AppLabel: "egress", IsCore: false, Namespace: "ingress-egress"},
			{AppLabel: "sds", IsCore: false, Namespace: "istio-admin"},
		},
	}

	return conf
}

func mockServer(mr *mux.Router) *httptest.Server {
	return httptest.NewServer(mr)
}

func addAddOnRoute(mr *mux.Router, mu *sync.Mutex, url string, statusCode int, callNum *int) {
	mr.HandleFunc(url, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if callNum != nil {
			*callNum = *callNum + 1
		}
		mu.Unlock()
		if statusCode > 299 {
			http.Error(w, "Not a success", statusCode)
		} else {
			if c, err := w.Write([]byte("OK")); err != nil {
				log.Errorf("Error when mocking the addon call: %s (%d)", url, c)
			}
		}
	})
}

func mockAddOnCalls(addons map[string]addOnsSetup) *mux.Router {
	var mu sync.Mutex
	mr := mux.NewRouter()
	for _, addon := range addons {
		addAddOnRoute(mr, &mu, addon.Url, addon.StatusCode, addon.CallCount)
	}
	return mr
}

func defaultAddOnCalls(grafana, prom *int) map[string]addOnsSetup {
	return map[string]addOnsSetup{
		"prometheus": {
			Url:        "/prometheus/mock",
			StatusCode: 200,
			CallCount:  prom,
		},
		"grafana": {
			Url:        "/grafana/mock",
			StatusCode: 200,
			CallCount:  grafana,
		},
		"custom dashboards": {
			Url:        "/prometheus-dashboards/mock",
			StatusCode: 200,
			CallCount:  nil,
		},
	}
}

func addonAddMockUrls(baseUrl string, conf *config.Config, overrideUrl bool) *config.Config {
	conf.ExternalServices.Grafana.Enabled = true
	conf.ExternalServices.Grafana.InClusterURL = baseUrl + "/grafana/mock"
	conf.ExternalServices.Grafana.IsCore = false

	conf.ExternalServices.Tracing.Enabled = true
	conf.ExternalServices.Tracing.InClusterURL = baseUrl + "/jaeger/mock"
	conf.ExternalServices.Tracing.IsCore = false

	conf.ExternalServices.Prometheus.URL = baseUrl + "/prometheus/mock"

	conf.ExternalServices.CustomDashboards.Enabled = true
	conf.ExternalServices.CustomDashboards.Prometheus.URL = baseUrl + "/prometheus-dashboards/mock"
	conf.ExternalServices.CustomDashboards.IsCore = false

	if overrideUrl {
		conf.ExternalServices.Grafana.HealthCheckUrl = conf.ExternalServices.Grafana.InClusterURL
		conf.ExternalServices.Grafana.InClusterURL = baseUrl + "/grafana/wrong"

		conf.ExternalServices.Prometheus.HealthCheckUrl = conf.ExternalServices.Prometheus.URL
		conf.ExternalServices.Prometheus.URL = baseUrl + "/prometheus/wrong"

		conf.ExternalServices.CustomDashboards.Prometheus.HealthCheckUrl = conf.ExternalServices.CustomDashboards.Prometheus.URL
		conf.ExternalServices.CustomDashboards.Prometheus.URL = baseUrl + "/prometheus/wrong"

	}
	return conf
}
