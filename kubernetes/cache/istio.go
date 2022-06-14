package cache

import (
	extentions_v1alpha1 "istio.io/client-go/pkg/apis/extensions/v1alpha1"
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	networking_v1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	security_v1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"

	// TODO: make this import consistent
	"istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	istio "istio.io/client-go/pkg/informers/externalversions"
	"k8s.io/apimachinery/pkg/labels"
	gatewayapi "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gateway "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions"

	"github.com/kiali/kiali/kubernetes"
)

// type IstioCache interface {
// 	GetDestinationRule(namespace, name string) (*networking_v1beta1.DestinationRule, error)
// 	GetDestinationRules(namespace, labelSelector string) ([]*networking_v1beta1.DestinationRule, error)
// 	GetEnvoyFilter(namespace, name string) (*networking_v1alpha3.EnvoyFilter, error)
// 	GetEnvoyFilters(namespace, labelSelector string) ([]*networking_v1alpha3.EnvoyFilter, error)
// 	GetGateway(namespace, name string) (*networking_v1beta1.Gateway, error)
// 	GetGateways(namespace, labelSelector string) ([]*networking_v1beta1.Gateway, error)
// 	GetServiceEntry(namespace, name string) (*networking_v1beta1.ServiceEntry, error)
// 	GetServiceEntries(namespace, labelSelector string) ([]*networking_v1beta1.ServiceEntry, error)
// 	GetSidecar(namespace, name string) (*networking_v1beta1.Sidecar, error)
// 	GetSidecars(namespace, labelSelector string) ([]*networking_v1beta1.Sidecar, error)
// 	GetTelemetry(namespace, name string) (*v1alpha1.Telemetry, error)
// 	GetTelemetries(namespace, labelSelector string) ([]*v1alpha1.Telemetry, error)
// 	GetVirtualService(namespace, name string) (*networking_v1beta1.VirtualService, error)
// 	GetVirtualServices(namespace, labelSelector string) ([]*networking_v1beta1.VirtualService, error)
// 	GetWorkloadEntry(namespace, name string) (*networking_v1beta1.WorkloadEntry, error)
// 	GetWorkloadEntries(namespace, labelSelector string) ([]*networking_v1beta1.WorkloadEntry, error)
// 	GetWorkloadGroup(namespace, name string) (*networking_v1beta1.WorkloadGroup, error)
// 	GetWorkloadGroups(namespace, labelSelector string) ([]*networking_v1beta1.WorkloadGroup, error)
// 	GetWasmPlugin(namespace, name string) (*extentions_v1alpha1.WasmPlugin, error)
// 	GetWasmPlugins(namespace, labelSelector string) ([]*extentions_v1alpha1.WasmPlugin, error)

// 	GetK8sGateway(namespace, name string) (*gatewayapi.Gateway, error)
// 	GetK8sGateways(namespace, labelSelector string) ([]*gatewayapi.Gateway, error)
// 	GetK8sHTTPRoute(namespace, name string) (*gatewayapi.HTTPRoute, error)
// 	GetK8sHTTPRoutes(namespace, labelSelector string) ([]*gatewayapi.HTTPRoute, error)

// 	GetAuthorizationPolicy(namespace, name string) (*security_v1beta1.AuthorizationPolicy, error)
// 	GetAuthorizationPolicies(namespace, labelSelector string) ([]*security_v1beta1.AuthorizationPolicy, error)
// 	GetPeerAuthentication(namespace, name string) (*security_v1beta1.PeerAuthentication, error)
// 	GetPeerAuthentications(namespace, labelSelector string) ([]*security_v1beta1.PeerAuthentication, error)
// 	GetRequestAuthentication(namespace, name string) (*security_v1beta1.RequestAuthentication, error)
// 	GetRequestAuthentications(namespace, labelSelector string) ([]*security_v1beta1.RequestAuthentication, error)
// }

func (c *KialiCache) createIstioInformers(namespace string) istio.SharedInformerFactory {
	var opts []istio.SharedInformerOption
	if namespace != "" {
		opts = append(opts, istio.WithNamespace(namespace))
	}
	sharedInformers := istio.NewSharedInformerFactoryWithOptions(c.istioApi, c.refreshDuration, opts...)

	lister := c.getCacheLister(namespace)
	lister.authzLister = sharedInformers.Security().V1beta1().AuthorizationPolicies().Lister()
	lister.destinationRuleLister = sharedInformers.Networking().V1beta1().DestinationRules().Lister()
	lister.envoyFilterLister = sharedInformers.Networking().V1alpha3().EnvoyFilters().Lister()
	lister.gatewayLister = sharedInformers.Networking().V1beta1().Gateways().Lister()
	lister.peerAuthnLister = sharedInformers.Security().V1beta1().PeerAuthentications().Lister()
	lister.requestAuthnLister = sharedInformers.Security().V1beta1().RequestAuthentications().Lister()
	lister.serviceEntryLister = sharedInformers.Networking().V1beta1().ServiceEntries().Lister()
	lister.sidecarLister = sharedInformers.Networking().V1beta1().Sidecars().Lister()
	lister.virtualServiceLister = sharedInformers.Networking().V1beta1().VirtualServices().Lister()
	lister.workloadEntryLister = sharedInformers.Networking().V1beta1().WorkloadEntries().Lister()
	lister.workloadGroupLister = sharedInformers.Networking().V1beta1().WorkloadGroups().Lister()
	lister.telemetryLister = sharedInformers.Telemetry().V1alpha1().Telemetries().Lister()

	sharedInformers.Security().V1beta1().AuthorizationPolicies().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().DestinationRules().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1alpha3().EnvoyFilters().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().Gateways().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Security().V1beta1().PeerAuthentications().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Security().V1beta1().RequestAuthentications().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().ServiceEntries().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().Sidecars().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().VirtualServices().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().WorkloadEntries().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Networking().V1beta1().WorkloadGroups().Informer().AddEventHandler(c.registryRefreshHandler)
	sharedInformers.Telemetry().V1alpha1().Telemetries().Informer().AddEventHandler(c.registryRefreshHandler)

	return sharedInformers
}

func (c *KialiCache) createGatewayInformers(namespace string) gateway.SharedInformerFactory {
	sharedInformers := gateway.NewSharedInformerFactory(c.gatewayApi, c.refreshDuration)
	lister := c.getCacheLister(namespace)

	if c.istioClient.IsGatewayAPI() {
		lister.k8sgatewayLister = sharedInformers.Gateway().V1alpha2().Gateways().Lister()
		lister.k8shttprouteLister = sharedInformers.Gateway().V1alpha2().HTTPRoutes().Lister()
		sharedInformers.Gateway().V1alpha2().Gateways().Informer().AddEventHandler(c.registryRefreshHandler)
		sharedInformers.Gateway().V1alpha2().Gateways().Informer().AddEventHandler(c.registryRefreshHandler)
	}
	return sharedInformers
}

func (c *KialiCache) GetDestinationRule(namespace, name string) (*networking_v1beta1.DestinationRule, error) {
	dr, err := c.getCacheLister(namespace).destinationRuleLister.DestinationRules(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	dr.Kind = kubernetes.DestinationRuleType
	return dr, nil
}

func (c *KialiCache) GetDestinationRules(namespace, labelSelector string) ([]*networking_v1beta1.DestinationRule, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	drs, err := c.getCacheLister(namespace).destinationRuleLister.DestinationRules(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if drs == nil {
		return []*networking_v1beta1.DestinationRule{}, nil
	}

	for _, dr := range drs {
		dr.Kind = kubernetes.DestinationRuleType
	}
	return drs, nil
}

func (c *KialiCache) GetEnvoyFilter(namespace, name string) (*networking_v1alpha3.EnvoyFilter, error) {
	ef, err := c.getCacheLister(namespace).envoyFilterLister.EnvoyFilters(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	ef.Kind = kubernetes.EnvoyFilterType
	return ef, nil
}

func (c *KialiCache) GetEnvoyFilters(namespace, labelSelector string) ([]*networking_v1alpha3.EnvoyFilter, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	efs, err := c.getCacheLister(namespace).envoyFilterLister.EnvoyFilters(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if efs == nil {
		return []*networking_v1alpha3.EnvoyFilter{}, nil
	}

	for _, ef := range efs {
		ef.Kind = kubernetes.EnvoyFilterType
	}
	return efs, nil
}

func (c *KialiCache) GetGateway(namespace, name string) (*networking_v1beta1.Gateway, error) {
	gw, err := c.getCacheLister(namespace).gatewayLister.Gateways(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	gw.Kind = kubernetes.GatewayType
	return gw, nil
}

func (c *KialiCache) GetGateways(namespace, labelSelector string) ([]*networking_v1beta1.Gateway, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	gateways, err := c.getCacheLister(namespace).gatewayLister.Gateways(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if gateways == nil {
		return []*networking_v1beta1.Gateway{}, nil
	}

	for _, gw := range gateways {
		gw.Kind = kubernetes.GatewayType
	}
	return gateways, nil
}

func (c *KialiCache) GetServiceEntry(namespace, name string) (*networking_v1beta1.ServiceEntry, error) {
	se, err := c.getCacheLister(namespace).serviceEntryLister.ServiceEntries(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	se.Kind = kubernetes.ServiceEntryType
	return se, nil
}

func (c *KialiCache) GetServiceEntries(namespace, labelSelector string) ([]*networking_v1beta1.ServiceEntry, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	ses, err := c.getCacheLister(namespace).serviceEntryLister.ServiceEntries(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if ses == nil {
		return []*networking_v1beta1.ServiceEntry{}, nil
	}

	for _, se := range ses {
		se.Kind = kubernetes.ServiceEntryType
	}
	return ses, nil
}

func (c *KialiCache) GetSidecar(namespace, name string) (*networking_v1beta1.Sidecar, error) {
	sc, err := c.getCacheLister(namespace).sidecarLister.Sidecars(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	sc.Kind = kubernetes.SidecarType
	return sc, nil
}

func (c *KialiCache) GetSidecars(namespace, labelSelector string) ([]*networking_v1beta1.Sidecar, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	sidecars, err := c.getCacheLister(namespace).sidecarLister.Sidecars(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if sidecars == nil {
		return []*networking_v1beta1.Sidecar{}, nil
	}

	for _, sc := range sidecars {
		sc.Kind = kubernetes.SidecarType
	}
	return sidecars, nil
}

func (c *KialiCache) GetVirtualService(namespace, name string) (*networking_v1beta1.VirtualService, error) {
	vs, err := c.getCacheLister(namespace).virtualServiceLister.VirtualServices(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	vs.Kind = kubernetes.VirtualServiceType
	return vs, nil
}

func (c *KialiCache) GetVirtualServices(namespace, labelSelector string) ([]*networking_v1beta1.VirtualService, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	vs, err := c.getCacheLister(namespace).virtualServiceLister.VirtualServices(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if vs == nil {
		return []*networking_v1beta1.VirtualService{}, nil
	}

	for _, v := range vs {
		v.Kind = kubernetes.VirtualServiceType
	}
	return vs, nil
}

func (c *KialiCache) GetWorkloadEntry(namespace, name string) (*networking_v1beta1.WorkloadEntry, error) {
	we, err := c.getCacheLister(namespace).workloadEntryLister.WorkloadEntries(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	we.Kind = kubernetes.WorkloadEntryType
	return we, nil
}

func (c *KialiCache) GetWorkloadEntries(namespace, labelSelector string) ([]*networking_v1beta1.WorkloadEntry, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	we, err := c.getCacheLister(namespace).workloadEntryLister.WorkloadEntries(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if we == nil {
		return []*networking_v1beta1.WorkloadEntry{}, nil
	}

	for _, w := range we {
		w.Kind = kubernetes.WorkloadEntryType
	}
	return we, nil
}

func (c *KialiCache) GetWorkloadGroup(namespace, name string) (*networking_v1beta1.WorkloadGroup, error) {
	wg, err := c.getCacheLister(namespace).workloadGroupLister.WorkloadGroups(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	wg.Kind = kubernetes.WorkloadGroupType
	return wg, nil
}

func (c *KialiCache) GetWorkloadGroups(namespace, labelSelector string) ([]*networking_v1beta1.WorkloadGroup, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	wg, err := c.getCacheLister(namespace).workloadGroupLister.WorkloadGroups(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if wg == nil {
		return []*networking_v1beta1.WorkloadGroup{}, nil
	}

	for _, w := range wg {
		w.Kind = kubernetes.WorkloadGroupType
	}
	return wg, nil
}

func (c *KialiCache) GetWasmPlugin(namespace, name string) (*extentions_v1alpha1.WasmPlugin, error) {
	wp, err := c.getCacheLister(namespace).wasmPluginLister.WasmPlugins(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	wp.Kind = kubernetes.WasmPluginType
	return wp, nil
}

func (c *KialiCache) GetWasmPlugins(namespace, labelSelector string) ([]*extentions_v1alpha1.WasmPlugin, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	wp, err := c.getCacheLister(namespace).wasmPluginLister.WasmPlugins(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if wp == nil {
		return []*extentions_v1alpha1.WasmPlugin{}, nil
	}

	for _, w := range wp {
		w.Kind = kubernetes.WasmPluginType
	}
	return wp, nil
}

func (c *KialiCache) GetTelemetry(namespace, name string) (*v1alpha1.Telemetry, error) {
	t, err := c.getCacheLister(namespace).telemetryLister.Telemetries(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	t.Kind = kubernetes.TelemetryType
	return t, nil
}

func (c *KialiCache) GetTelemetries(namespace, labelSelector string) ([]*v1alpha1.Telemetry, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	t, err := c.getCacheLister(namespace).telemetryLister.Telemetries(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if t == nil {
		return []*v1alpha1.Telemetry{}, nil
	}

	for _, w := range t {
		w.Kind = kubernetes.TelemetryType
	}

	return t, nil
}

func (c *KialiCache) GetK8sGateway(namespace, name string) (*gatewayapi.Gateway, error) {
	g, err := c.getCacheLister(namespace).k8sgatewayLister.Gateways(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	g.Kind = kubernetes.K8sGatewayType
	return g, nil
}

func (c *KialiCache) GetK8sGateways(namespace, labelSelector string) ([]*gatewayapi.Gateway, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	g, err := c.getCacheLister(namespace).k8sgatewayLister.Gateways(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if g == nil {
		return []*gatewayapi.Gateway{}, nil
	}

	for _, w := range g {
		w.Kind = kubernetes.K8sGatewayType
	}

	return g, nil
}

func (c *KialiCache) GetK8sHTTPRoute(namespace, name string) (*gatewayapi.HTTPRoute, error) {
	g, err := c.getCacheLister(namespace).k8shttprouteLister.HTTPRoutes(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	g.Kind = kubernetes.K8sHTTPRouteType
	return g, nil
}

func (c *KialiCache) GetK8sHTTPRoutes(namespace, labelSelector string) ([]*gatewayapi.HTTPRoute, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	r, err := c.getCacheLister(namespace).k8shttprouteLister.HTTPRoutes(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if r == nil {
		return []*gatewayapi.HTTPRoute{}, nil
	}

	for _, w := range r {
		w.Kind = kubernetes.K8sHTTPRouteType
	}

	return r, nil
}

func (c *KialiCache) GetAuthorizationPolicy(namespace, name string) (*security_v1beta1.AuthorizationPolicy, error) {
	ap, err := c.getCacheLister(namespace).authzLister.AuthorizationPolicies(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	ap.Kind = kubernetes.AuthorizationPoliciesType
	return ap, nil
}

func (c *KialiCache) GetAuthorizationPolicies(namespace, labelSelector string) ([]*security_v1beta1.AuthorizationPolicy, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	authPolicies, err := c.getCacheLister(namespace).authzLister.AuthorizationPolicies(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if authPolicies == nil {
		return []*security_v1beta1.AuthorizationPolicy{}, nil
	}

	for _, ap := range authPolicies {
		ap.Kind = kubernetes.AuthorizationPoliciesType
	}
	return authPolicies, nil
}

func (c *KialiCache) GetPeerAuthentication(namespace, name string) (*security_v1beta1.PeerAuthentication, error) {
	pa, err := c.getCacheLister(namespace).peerAuthnLister.PeerAuthentications(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	pa.Kind = kubernetes.PeerAuthenticationsType
	return pa, nil
}

func (c *KialiCache) GetPeerAuthentications(namespace, labelSelector string) ([]*security_v1beta1.PeerAuthentication, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	peerAuths, err := c.getCacheLister(namespace).peerAuthnLister.PeerAuthentications(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if peerAuths == nil {
		return []*security_v1beta1.PeerAuthentication{}, nil
	}

	for _, pa := range peerAuths {
		pa.Kind = kubernetes.PeerAuthenticationsType
	}
	return peerAuths, nil
}

func (c *KialiCache) GetRequestAuthentication(namespace, name string) (*security_v1beta1.RequestAuthentication, error) {
	ra, err := c.getCacheLister(namespace).requestAuthnLister.RequestAuthentications(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	ra.Kind = kubernetes.RequestAuthenticationsType
	return ra, nil
}

func (c *KialiCache) GetRequestAuthentications(namespace, labelSelector string) ([]*security_v1beta1.RequestAuthentication, error) {
	selector, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, err
	}

	reqAuths, err := c.getCacheLister(namespace).requestAuthnLister.RequestAuthentications(namespace).List(selector)
	if err != nil {
		return nil, err
	}

	// Lister returns nil when there are no results but callers of the cache expect an empty array
	// so keeping the behavior the same since it matters for json marshalling.
	if reqAuths == nil {
		return []*security_v1beta1.RequestAuthentication{}, nil
	}

	for _, ra := range reqAuths {
		ra.Kind = kubernetes.RequestAuthenticationsType
	}
	return reqAuths, nil
}
