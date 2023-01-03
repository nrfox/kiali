package business

import (
	"context"
	"testing"

	osproject_v1 "github.com/openshift/api/project/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kiali/kiali/config"
	kialikube "github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/kubetest"
	"github.com/kiali/kiali/prometheus/prometheustest"
)

func setupAppService(k8s kialikube.ClientInterface, config config.Config) *AppService {
	prom := new(prometheustest.PromClientMock)
	SetupBusinessLayer(k8s, config)
	layer := NewWithBackends(k8s, prom, nil)
	setupGlobalMeshConfig()
	return &AppService{k8s: k8s, prom: prom, businessLayer: layer}
}

func TestGetAppListFromDeployments(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	conf := config.NewConfig()
	// Disable Istio API so we don't try to poll the endpoints.
	// Perhaps this should be mocked out since the default is to
	// enable the Istio API.
	conf.ExternalServices.Istio.IstioAPIEnabled = false
	config.Set(conf)

	objs := []runtime.Object{&osproject_v1.Project{ObjectMeta: v1.ObjectMeta{Name: "Namespace"}}}
	for _, deployment := range FakeDeployments(conf.IstioLabels.AppLabelName, conf.IstioLabels.VersionLabelName) {
		fd := deployment
		fd.CreationTimestamp = v1.Time{}
		objs = append(objs, &fd)
	}
	k8s := kubetest.NewFakeK8sClient(objs...)
	k8s.OpenShift = true
	svc := setupAppService(k8s, *conf)

	criteria := AppCriteria{Namespace: "Namespace", IncludeIstioResources: false, IncludeHealth: false}
	appList, err := svc.GetAppList(context.TODO(), criteria)
	require.NoError(err)

	assert.Equal("Namespace", appList.Namespace.Name)

	assert.Equal(1, len(appList.Apps))
	assert.Equal("httpbin", appList.Apps[0].Name)
}

func TestGetAppFromDeployments(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	conf := config.NewConfig()
	conf.ExternalServices.CustomDashboards.Enabled = false
	conf.ExternalServices.Istio.IstioAPIEnabled = false
	config.Set(conf)

	objs := []runtime.Object{&osproject_v1.Project{ObjectMeta: v1.ObjectMeta{Name: "Namespace"}}}
	for _, deployment := range FakeDeployments(conf.IstioLabels.AppLabelName, conf.IstioLabels.VersionLabelName) {
		fd := deployment
		fd.CreationTimestamp = v1.Time{}
		objs = append(objs, &fd)
	}
	for _, obj := range FakeServices() {
		objs = append(objs, &obj)
	}
	k8s := kubetest.NewFakeK8sClient(objs...)
	k8s.OpenShift = true
	svc := setupAppService(k8s, *conf)

	criteria := AppCriteria{Namespace: "Namespace", AppName: "httpbin", IncludeIstioResources: false}
	appDetails, err := svc.GetAppDetails(context.TODO(), criteria)
	require.NoError(err)

	assert.Equal("Namespace", appDetails.Namespace.Name)
	assert.Equal("httpbin", appDetails.Name)

	require.Equal(2, len(appDetails.Workloads))
	assert.Equal("httpbin-v1", appDetails.Workloads[0].WorkloadName)
	assert.Equal("httpbin-v2", appDetails.Workloads[1].WorkloadName)
	require.Equal(1, len(appDetails.ServiceNames))
	assert.Equal("httpbin", appDetails.ServiceNames[0])
}

func TestGetAppListFromReplicaSets(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	conf := config.NewConfig()
	conf.ExternalServices.Istio.IstioAPIEnabled = false
	config.Set(conf)

	kubeObjects := []runtime.Object{&osproject_v1.Project{ObjectMeta: v1.ObjectMeta{Name: "Namespace"}}}
	for _, obj := range FakeReplicaSets(*conf) {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	k8s := kubetest.NewFakeK8sClient(kubeObjects...)
	k8s.OpenShift = true
	svc := setupAppService(k8s, *conf)

	criteria := AppCriteria{Namespace: "Namespace", IncludeIstioResources: false, IncludeHealth: false}
	appList, err := svc.GetAppList(context.TODO(), criteria)
	require.NoError(err)

	assert.Equal("Namespace", appList.Namespace.Name)

	assert.Equal(1, len(appList.Apps))
	assert.Equal("httpbin", appList.Apps[0].Name)
}

func TestGetAppFromReplicaSets(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	conf := config.NewConfig()
	conf.ExternalServices.Istio.IstioAPIEnabled = false
	config.Set(conf)

	kubeObjects := []runtime.Object{&osproject_v1.Project{ObjectMeta: v1.ObjectMeta{Name: "Namespace"}}}
	for _, obj := range FakeReplicaSets(*conf) {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	for _, obj := range FakeServices() {
		o := obj
		kubeObjects = append(kubeObjects, &o)
	}
	k8s := kubetest.NewFakeK8sClient(kubeObjects...)
	k8s.OpenShift = true

	svc := setupAppService(k8s, *conf)

	criteria := AppCriteria{Namespace: "Namespace", AppName: "httpbin"}
	appDetails, err := svc.GetAppDetails(context.TODO(), criteria)
	require.NoError(err)

	assert.Equal("Namespace", appDetails.Namespace.Name)
	assert.Equal("httpbin", appDetails.Name)

	assert.Equal(2, len(appDetails.Workloads))
	assert.Equal("httpbin-v1", appDetails.Workloads[0].WorkloadName)
	assert.Equal("httpbin-v2", appDetails.Workloads[1].WorkloadName)
	assert.Equal(1, len(appDetails.ServiceNames))
	assert.Equal("httpbin", appDetails.ServiceNames[0])
}

func TestJoinMap(t *testing.T) {
	assert := assert.New(t)
	tempLabels := map[string][]string{}
	labelsA := map[string]string{
		"key1": "val1",
		"key2": "val2",
	}

	joinMap(tempLabels, labelsA)
	assert.Len(tempLabels, 2)
	assert.Equal([]string{"val1"}, tempLabels["key1"])
	assert.Equal([]string{"val2"}, tempLabels["key2"])

	// Test with an added value on key1
	labelsB := map[string]string{
		"key1": "val3",
		"key3": "val4",
	}
	joinMap(tempLabels, labelsB)
	assert.Len(tempLabels, 3)
	assert.Equal([]string{"val1", "val3"}, tempLabels["key1"])
	assert.Equal([]string{"val2"}, tempLabels["key2"])
	assert.Equal([]string{"val4"}, tempLabels["key3"])

	// Test with duplicates; val3 is duplicated, al4 is not (is substring)
	// al4 must also appear before val4 on final labels (sorted)
	labelsC := map[string]string{
		"key1": "val3",
		"key3": "al4",
	}
	joinMap(tempLabels, labelsC)
	assert.Len(tempLabels, 3)
	assert.Equal([]string{"val1", "val3"}, tempLabels["key1"])
	assert.Equal([]string{"val2"}, tempLabels["key2"])
	assert.Equal([]string{"val4", "al4"}, tempLabels["key3"])

	labels := buildFinalLabels(tempLabels)
	assert.Len(labels, 3)
	assert.Equal("val1,val3", labels["key1"])
	assert.Equal("val2", labels["key2"])
	assert.Equal("al4,val4", labels["key3"])
}
