package kubetest

import (
	"context"

	extentions_v1alpha1 "istio.io/client-go/pkg/apis/extensions/v1alpha1"
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	networking_v1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	security_v1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	"istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	istio "istio.io/client-go/pkg/clientset/versioned"
	istio_fake "istio.io/client-go/pkg/clientset/versioned/fake"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	gatewayapi "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayapiclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gatewayapifake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"

	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
)

func (o *K8SClientMock) MockIstio(objects ...runtime.Object) {
	o.istioClientset = istio_fake.NewSimpleClientset(objects...)
	// Istio Fake client has a problem with Gateways
	// Invoking a NewSimpleClientset() stores a wrong "gatewais" entry, that logic is not even the istio.io but
	// in the k8s.io/apimachinery, so the workaround is to invoke "Create" for those objects with problems
	for _, ob := range objects {
		if gw, ok := ob.(*networking_v1beta1.Gateway); ok {
			_, err := o.istioClientset.NetworkingV1beta1().Gateways(gw.Namespace).Create(context.TODO(), gw, v1.CreateOptions{})
			if err != nil {
				log.Errorf("Error initializing Gateways in MockIstio: %s", err)
			}
		}
	}
}

func (o *K8SClientMock) MockGatewayApi(objects ...runtime.Object) {
	o.gatewayapiClientSet = gatewayapifake.NewSimpleClientset(objects...)
}

func (o *K8SClientMock) Istio() istio.Interface {
	return o.istioClientset
}

func (o *K8SClientMock) GatewayAPI() gatewayapiclient.Interface {
	return o.gatewayapiClientSet
}

func (o *K8SClientMock) CanConnectToIstiod() (kubernetes.IstioComponentStatus, error) {
	args := o.Called()
	return args.Get(0).(kubernetes.IstioComponentStatus), args.Error(1)
}

func (o *K8SClientMock) GetProxyStatus() ([]*kubernetes.ProxyStatus, error) {
	args := o.Called()
	return args.Get(0).([]*kubernetes.ProxyStatus), args.Error(1)
}

func (o *K8SClientMock) GetConfigDump(namespace string, podName string) (*kubernetes.ConfigDump, error) {
	args := o.Called(namespace, podName)
	return args.Get(0).(*kubernetes.ConfigDump), args.Error(1)
}

func (o *K8SClientMock) GetRegistryConfiguration() (*kubernetes.RegistryConfiguration, error) {
	args := o.Called()
	return args.Get(0).(*kubernetes.RegistryConfiguration), args.Error(1)
}

func (o *K8SClientMock) GetRegistryServices() ([]*kubernetes.RegistryService, error) {
	args := o.Called()
	return args.Get(0).([]*kubernetes.RegistryService), args.Error(1)
}

func (o *K8SClientMock) GetRegistryEndpoints() ([]*kubernetes.RegistryEndpoint, error) {
	args := o.Called()
	return args.Get(0).([]*kubernetes.RegistryEndpoint), args.Error(1)
}

func (o *K8SClientMock) SetProxyLogLevel(namespace, podName, level string) error {
	args := o.Called()
	return args.Error(0)
}

func (o *K8SClientMock) DeleteObject(namespace, name, kind string) error {
	args := o.Called()
	return args.Error(0)
}

func (o *K8SClientMock) PatchObject(namespace string, name string, jsonPatch []byte, object runtime.Object) error {
	args := o.Called()
	return args.Error(0)
}

func (o *K8SClientMock) CreateObject(namespace string, kind string, object runtime.Object) error {
	args := o.Called()
	return args.Error(0)
}

func (o *K8SClientMock) GetDestinationRule(namespace, name string) (*networking_v1beta1.DestinationRule, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.DestinationRule), args.Error(1)
}

func (o *K8SClientMock) GetDestinationRules(namespace, labelSelector string) ([]*networking_v1beta1.DestinationRule, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.DestinationRule), args.Error(1)
}

func (o *K8SClientMock) GetEnvoyFilter(namespace, name string) (*networking_v1alpha3.EnvoyFilter, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1alpha3.EnvoyFilter), args.Error(1)
}

func (o *K8SClientMock) GetEnvoyFilters(namespace, labelSelector string) ([]*networking_v1alpha3.EnvoyFilter, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1alpha3.EnvoyFilter), args.Error(1)
}

func (o *K8SClientMock) GetGateway(namespace, name string) (*networking_v1beta1.Gateway, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.Gateway), args.Error(1)
}

func (o *K8SClientMock) GetGateways(namespace, labelSelector string) ([]*networking_v1beta1.Gateway, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.Gateway), args.Error(1)
}

func (o *K8SClientMock) GetServiceEntry(namespace, name string) (*networking_v1beta1.ServiceEntry, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.ServiceEntry), args.Error(1)
}

func (o *K8SClientMock) GetServiceEntries(namespace, labelSelector string) ([]*networking_v1beta1.ServiceEntry, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.ServiceEntry), args.Error(1)
}

func (o *K8SClientMock) GetSidecar(namespace, name string) (*networking_v1beta1.Sidecar, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.Sidecar), args.Error(1)
}

func (o *K8SClientMock) GetSidecars(namespace, labelSelector string) ([]*networking_v1beta1.Sidecar, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.Sidecar), args.Error(1)
}

func (o *K8SClientMock) GetTelemetry(namespace, name string) (*v1alpha1.Telemetry, error) {
	args := o.Called()
	return args.Get(0).(*v1alpha1.Telemetry), args.Error(1)
}

func (o *K8SClientMock) GetTelemetries(namespace, labelSelector string) ([]*v1alpha1.Telemetry, error) {
	args := o.Called()
	return args.Get(0).([]*v1alpha1.Telemetry), args.Error(1)
}

func (o *K8SClientMock) GetVirtualService(namespace, name string) (*networking_v1beta1.VirtualService, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.VirtualService), args.Error(1)
}

func (o *K8SClientMock) GetVirtualServices(namespace, labelSelector string) ([]*networking_v1beta1.VirtualService, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.VirtualService), args.Error(1)
}

func (o *K8SClientMock) GetWorkloadEntry(namespace, name string) (*networking_v1beta1.WorkloadEntry, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.WorkloadEntry), args.Error(1)
}

func (o *K8SClientMock) GetWorkloadEntries(namespace, labelSelector string) ([]*networking_v1beta1.WorkloadEntry, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.WorkloadEntry), args.Error(1)
}

func (o *K8SClientMock) GetWorkloadGroup(namespace, name string) (*networking_v1beta1.WorkloadGroup, error) {
	args := o.Called()
	return args.Get(0).(*networking_v1beta1.WorkloadGroup), args.Error(1)
}

func (o *K8SClientMock) GetWorkloadGroups(namespace, labelSelector string) ([]*networking_v1beta1.WorkloadGroup, error) {
	args := o.Called()
	return args.Get(0).([]*networking_v1beta1.WorkloadGroup), args.Error(1)
}

func (o *K8SClientMock) GetWasmPlugin(namespace, name string) (*extentions_v1alpha1.WasmPlugin, error) {
	args := o.Called()
	return args.Get(0).(*extentions_v1alpha1.WasmPlugin), args.Error(1)
}

func (o *K8SClientMock) GetWasmPlugins(namespace, labelSelector string) ([]*extentions_v1alpha1.WasmPlugin, error) {
	args := o.Called()
	return args.Get(0).([]*extentions_v1alpha1.WasmPlugin), args.Error(1)
}

func (o *K8SClientMock) GetK8sGateway(namespace, name string) (*gatewayapi.Gateway, error) {
	args := o.Called()
	return args.Get(0).(*gatewayapi.Gateway), args.Error(1)
}

func (o *K8SClientMock) GetK8sGateways(namespace, labelSelector string) ([]*gatewayapi.Gateway, error) {
	args := o.Called()
	return args.Get(0).([]*gatewayapi.Gateway), args.Error(1)
}

func (o *K8SClientMock) GetK8sHTTPRoute(namespace, name string) (*gatewayapi.HTTPRoute, error) {
	args := o.Called()
	return args.Get(0).(*gatewayapi.HTTPRoute), args.Error(1)
}

func (o *K8SClientMock) GetK8sHTTPRoutes(namespace, labelSelector string) ([]*gatewayapi.HTTPRoute, error) {
	args := o.Called()
	return args.Get(0).([]*gatewayapi.HTTPRoute), args.Error(1)
}

func (o *K8SClientMock) GetAuthorizationPolicy(namespace, name string) (*security_v1beta1.AuthorizationPolicy, error) {
	args := o.Called()
	return args.Get(0).(*security_v1beta1.AuthorizationPolicy), args.Error(1)
}

func (o *K8SClientMock) GetAuthorizationPolicies(namespace, labelSelector string) ([]*security_v1beta1.AuthorizationPolicy, error) {
	args := o.Called()
	return args.Get(0).([]*security_v1beta1.AuthorizationPolicy), args.Error(1)
}

func (o *K8SClientMock) GetPeerAuthentication(namespace, name string) (*security_v1beta1.PeerAuthentication, error) {
	args := o.Called()
	return args.Get(0).(*security_v1beta1.PeerAuthentication), args.Error(1)
}

func (o *K8SClientMock) GetPeerAuthentications(namespace, labelSelector string) ([]*security_v1beta1.PeerAuthentication, error) {
	args := o.Called()
	return args.Get(0).([]*security_v1beta1.PeerAuthentication), args.Error(1)
}

func (o *K8SClientMock) GetRequestAuthentication(namespace, name string) (*security_v1beta1.RequestAuthentication, error) {
	args := o.Called()
	return args.Get(0).(*security_v1beta1.RequestAuthentication), args.Error(1)
}

func (o *K8SClientMock) GetRequestAuthentications(namespace, labelSelector string) ([]*security_v1beta1.RequestAuthentication, error) {
	args := o.Called()
	return args.Get(0).([]*security_v1beta1.RequestAuthentication), args.Error(1)
}
