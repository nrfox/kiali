package business

import (
	"context"
	"fmt"
	"testing"

	osapps_v1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	security_v1beta "istio.io/client-go/pkg/apis/security/v1beta1"
	apps_v1 "k8s.io/api/apps/v1"
	batch_v1 "k8s.io/api/batch/v1"
	batch_v1beta1 "k8s.io/api/batch/v1beta1"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes/kubetest"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/tests/data"
	"github.com/kiali/kiali/tests/testutils/validations"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGetNamespaceValidations(t *testing.T) {
	assert := assert.New(t)
	conf := config.NewConfig()
	config.Set(conf)

	vs := mockCombinedValidationService(fakeCombinedIstioConfigList(),
		[]string{"details", "product", "customer"}, fakePods())

	validations, _ := vs.GetValidations(context.TODO(), "test", "")
	assert.NotEmpty(validations)
	assert.True(validations[models.IstioValidationKey{ObjectType: "virtualservice", Namespace: "test", Name: "product-vs"}].Valid)
}

func TestGetIstioObjectValidations(t *testing.T) {
	assert := assert.New(t)
	conf := config.NewConfig()
	config.Set(conf)

	vs := mockCombinedValidationService(fakeCombinedIstioConfigList(), []string{"details", "product", "customer"}, fakePods())

	validations, _ := vs.GetIstioObjectValidations(context.TODO(), "test", "virtualservices", "product-vs")

	assert.NotEmpty(validations)
}

func TestGatewayValidation(t *testing.T) {
	assert := assert.New(t)
	conf := config.NewConfig()
	config.Set(conf)

	v := mockMultiNamespaceGatewaysValidationService()
	validations, _ := v.GetIstioObjectValidations(context.TODO(), "test", "gateways", "second")
	assert.NotEmpty(validations)
}

func TestFilterExportToNamespacesVS(t *testing.T) {
	assert := assert.New(t)
	conf := config.NewConfig()
	config.Set(conf)

	var currentIstioObjects []networking_v1alpha3.VirtualService
	vs1to3 := loadVirtualService("vs_bookinfo1_to_2_3.yaml", t)
	currentIstioObjects = append(currentIstioObjects, vs1to3)
	vs1tothis := loadVirtualService("vs_bookinfo1_to_this.yaml", t)
	currentIstioObjects = append(currentIstioObjects, vs1tothis)
	vs2to1 := loadVirtualService("vs_bookinfo2_to_1.yaml", t)
	currentIstioObjects = append(currentIstioObjects, vs2to1)
	vs2tothis := loadVirtualService("vs_bookinfo2_to_this.yaml", t)
	currentIstioObjects = append(currentIstioObjects, vs2tothis)
	vs3to2 := loadVirtualService("vs_bookinfo3_to_2.yaml", t)
	currentIstioObjects = append(currentIstioObjects, vs3to2)
	vs3toall := loadVirtualService("vs_bookinfo3_to_all.yaml", t)
	currentIstioObjects = append(currentIstioObjects, vs3toall)
	v := mockEmptyValidationService()
	filteredVSs := v.filterVSExportToNamespaces("bookinfo", currentIstioObjects)
	var expectedVS []networking_v1alpha3.VirtualService
	expectedVS = append(expectedVS, vs2to1)
	expectedVS = append(expectedVS, vs3toall)
	filteredKeys := []string{}
	for _, vs := range filteredVSs {
		filteredKeys = append(filteredKeys, fmt.Sprintf("%s/%s", vs.Name, vs.Namespace))
	}
	expectedKeys := []string{}
	for _, vs := range expectedVS {
		expectedKeys = append(expectedKeys, fmt.Sprintf("%s/%s", vs.Name, vs.Namespace))
	}
	assert.EqualValues(filteredKeys, expectedKeys)
}

func mockWorkLoadService(k8s *kubetest.K8SClientMock) WorkloadService {
	// Setup mocks
	k8s.On("IsOpenShift").Return(true)
	k8s.On("GetDeployments", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(FakeDepSyncedWithRS(), nil)
	k8s.On("GetDeploymentConfigs", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return([]osapps_v1.DeploymentConfig{}, nil)
	k8s.On("GetReplicaSets", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(FakeRSSyncedWithPods(), nil)
	k8s.On("GetReplicationControllers", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return([]core_v1.ReplicationController{}, nil)
	k8s.On("GetStatefulSets", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return([]apps_v1.StatefulSet{}, nil)
	k8s.On("GetDaemonSets", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return([]apps_v1.DaemonSet{}, nil)
	k8s.On("GetJobs", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return([]batch_v1.Job{}, nil)
	k8s.On("GetCronJobs", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return([]batch_v1beta1.CronJob{}, nil)
	k8s.On("GetPods", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(fakePods().Items, nil)
	k8s.On("GetConfigMap", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(&core_v1.ConfigMap{}, nil)

	svc := setupWorkloadService(k8s)
	return svc
}

func mockMultiNamespaceGatewaysValidationService() IstioValidationsService {
	k8s := new(kubetest.K8SClientMock)
	k8s.On("IsOpenShift").Return(false)
	k8s.On("GetNamespace", mock.AnythingOfType("string")).Return(&core_v1.Namespace{}, nil)
	k8s.On("IsMaistraApi").Return(false)
	k8s.On("GetNamespaces", mock.AnythingOfType("string")).Return(fakeNamespaces(), nil)

	fakeIstioObjects := []runtime.Object{}
	istioConfigList := fakeCombinedIstioConfigList()
	for _, d := range istioConfigList.DestinationRules {
		fakeIstioObjects = append(fakeIstioObjects, d.DeepCopyObject())
	}
	for _, s := range istioConfigList.Sidecars {
		fakeIstioObjects = append(fakeIstioObjects, s.DeepCopyObject())
	}
	for _, v := range istioConfigList.VirtualServices {
		fakeIstioObjects = append(fakeIstioObjects, v.DeepCopyObject())
	}
	for _, s := range istioConfigList.ServiceEntries {
		fakeIstioObjects = append(fakeIstioObjects, s.DeepCopyObject())
	}
	for _, p := range fakePolicies() {
		fakeIstioObjects = append(fakeIstioObjects, p.DeepCopyObject())
	}
	for _, g := range getGateway("first", "test") {
		fakeIstioObjects = append(fakeIstioObjects, g.DeepCopyObject())
	}
	for _, g := range getGateway("second", "test2") {
		fakeIstioObjects = append(fakeIstioObjects, g.DeepCopyObject())
	}
	k8s.MockIstio(fakeIstioObjects...)
	mockWorkLoadService(k8s)

	k8s.On("GetServices", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]string")).Return(fakeCombinedServices([]string{""}), nil)
	k8s.On("GetDeployments", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(FakeDepSyncedWithRS(), nil)
	k8s.On("GetMeshPolicies", mock.AnythingOfType("string")).Return(fakeMeshPolicies(), nil)

	return IstioValidationsService{k8s: k8s, businessLayer: NewWithBackends(k8s, nil, nil)}
}

func mockCombinedValidationService(istioConfigList *models.IstioConfigList, services []string, podList *core_v1.PodList) IstioValidationsService {
	k8s := new(kubetest.K8SClientMock)

	fakeIstioObjects := []runtime.Object{}
	for _, s := range istioConfigList.Sidecars {
		fakeIstioObjects = append(fakeIstioObjects, s.DeepCopyObject())
	}
	for _, r := range istioConfigList.RequestAuthentications {
		fakeIstioObjects = append(fakeIstioObjects, r.DeepCopyObject())
	}
	for _, v := range fakeCombinedIstioConfigList().VirtualServices {
		fakeIstioObjects = append(fakeIstioObjects, v.DeepCopyObject())
	}
	for _, d := range fakeCombinedIstioConfigList().DestinationRules {
		fakeIstioObjects = append(fakeIstioObjects, d.DeepCopyObject())
	}
	for _, s := range fakeCombinedIstioConfigList().ServiceEntries {
		fakeIstioObjects = append(fakeIstioObjects, s.DeepCopyObject())
	}
	for _, g := range fakeCombinedIstioConfigList().Gateways {
		fakeIstioObjects = append(fakeIstioObjects, g.DeepCopyObject())
	}
	for _, p := range fakeMeshPolicies() {
		fakeIstioObjects = append(fakeIstioObjects, p.DeepCopyObject())
	}
	for _, p := range fakePolicies() {
		fakeIstioObjects = append(fakeIstioObjects, p.DeepCopyObject())
	}
	for _, g := range getGateway("first", "test") {
		fakeIstioObjects = append(fakeIstioObjects, g.DeepCopyObject())
	}
	for _, g := range getGateway("second", "test2") {
		fakeIstioObjects = append(fakeIstioObjects, g.DeepCopyObject())
	}
	k8s.MockIstio(fakeIstioObjects...)

	k8s.On("GetServices", mock.AnythingOfType("string"), mock.AnythingOfType("map[string]string")).Return(fakeCombinedServices(services), nil)
	k8s.On("GetDeployments", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(FakeDepSyncedWithRS(), nil)
	k8s.On("GetNamespace", mock.AnythingOfType("string")).Return(kubetest.FakeNamespace("test"), nil)
	k8s.On("IsOpenShift").Return(false)
	k8s.On("IsMaistraApi").Return(false)
	k8s.On("GetNamespaces", mock.AnythingOfType("string")).Return(fakeNamespaces(), nil)

	mockWorkLoadService(k8s)

	return IstioValidationsService{k8s: k8s, businessLayer: NewWithBackends(k8s, nil, nil)}
}

func mockEmptyValidationService() IstioValidationsService {
	k8s := new(kubetest.K8SClientMock)
	k8s.MockIstio()
	k8s.On("IsOpenShift").Return(false)
	k8s.On("IsMaistraApi").Return(false)
	return IstioValidationsService{k8s: k8s, businessLayer: NewWithBackends(k8s, nil, nil)}
}

func fakeCombinedIstioConfigList() *models.IstioConfigList {
	istioConfigList := models.IstioConfigList{}

	istioConfigList.VirtualServices = []networking_v1alpha3.VirtualService{
		*data.AddHttpRoutesToVirtualService(data.CreateHttpRouteDestination("product", "v1", -1),
			data.AddTcpRoutesToVirtualService(data.CreateTcpRoute("product", "v1", -1),
				data.CreateEmptyVirtualService("product-vs", "test", []string{"product"})))}

	istioConfigList.DestinationRules = []networking_v1alpha3.DestinationRule{
		*data.AddSubsetToDestinationRule(data.CreateSubset("v1", "v1"), data.CreateEmptyDestinationRule("test", "product-dr", "product")),
		*data.CreateEmptyDestinationRule("test", "customer-dr", "customer"),
	}
	return &istioConfigList
}

func fakeMeshPolicies() []security_v1beta.PeerAuthentication {
	return []security_v1beta.PeerAuthentication{
		*data.CreateEmptyMeshPeerAuthentication("default", nil),
		*data.CreateEmptyMeshPeerAuthentication("test", nil),
	}
}

func fakePolicies() []security_v1beta.PeerAuthentication {
	return []security_v1beta.PeerAuthentication{
		*data.CreateEmptyPeerAuthentication("default", "bookinfo", nil),
		*data.CreateEmptyPeerAuthentication("test", "foo", nil),
	}
}

func fakeNamespaces() []core_v1.Namespace {
	return []core_v1.Namespace{
		{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "test",
			},
		},
		{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "test2",
			},
		},
	}
}

func fakeCombinedServices(services []string) []core_v1.Service {
	items := []core_v1.Service{}

	for _, service := range services {
		items = append(items, core_v1.Service{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: service,
				Labels: map[string]string{
					"app":     service,
					"version": "v1",
				},
			},
		})
	}
	return items
}

func fakePods() *core_v1.PodList {
	return &core_v1.PodList{
		Items: []core_v1.Pod{
			{
				ObjectMeta: meta_v1.ObjectMeta{
					Name: "reviews-12345-hello",
					Labels: map[string]string{
						"app":     "reviews",
						"version": "v2",
					},
				},
			},
			{
				ObjectMeta: meta_v1.ObjectMeta{
					Name: "reviews-54321-hello",
					Labels: map[string]string{
						"app":     "reviews",
						"version": "v1",
					},
				},
			},
		},
	}
}

func getGateway(name, namespace string) []networking_v1alpha3.Gateway {
	return []networking_v1alpha3.Gateway{
		*data.AddServerToGateway(data.CreateServer([]string{"valid"}, 80, "http", "http"),
			data.CreateEmptyGateway(name, namespace, map[string]string{
				"app": "real",
			}))}
}

func loadVirtualService(file string, t *testing.T) networking_v1alpha3.VirtualService {
	loader := yamlFixtureLoaderFor(file)
	err := loader.Load()
	if err != nil {
		t.Error("Error loading test data.")
	}
	return loader.GetResources().VirtualServices[0]
}

func yamlFixtureLoaderFor(file string) *validations.YamlFixtureLoader {
	path := fmt.Sprintf("../tests/data/validations/exportto/cns/%s", file)
	return &validations.YamlFixtureLoader{Filename: path}
}
