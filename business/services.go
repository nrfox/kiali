package business

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	apps_v1 "k8s.io/api/apps/v1"
	core_v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kiali/kiali/business/checkers"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/observability"
	"github.com/kiali/kiali/prometheus"
)

// SvcService deals with fetching istio/kubernetes services related content and convert to kiali model
type SvcService interface {
	GetServiceList(ctx context.Context, criteria ServiceCriteria) (*models.ServiceList, error)
	GetServiceDetails(ctx context.Context, namespace, service, interval string, queryTime time.Time) (*models.ServiceDetails, error)
	UpdateService(ctx context.Context, namespace, service string, interval string, queryTime time.Time, jsonPatch string) (*models.ServiceDetails, error)
	GetService(ctx context.Context, namespace, service string) (models.Service, error)
	GetServiceAppName(ctx context.Context, namespace, service string) (string, error)
}

type svcService struct {
	prom          prometheus.ClientInterface
	k8s           kubernetes.ClientInterface
	businessLayer *Layer
}


type ServiceCriteria struct {
	Namespace              string
	IncludeIstioResources  bool
	IncludeOnlyDefinitions bool
	ServiceSelector        string
}

// GetServiceList returns a list of all services for a given criteria
func (in *svcService) GetServiceList(ctx context.Context, criteria ServiceCriteria) (*models.ServiceList, error) {
	var svcs []core_v1.Service
	var rSvcs []*kubernetes.RegistryService
	var pods []core_v1.Pod
	var deployments []apps_v1.Deployment
	var istioConfigList models.IstioConfigList
	var err error
	// Check if user has access to the namespace (RBAC) in cache scenarios and/or
	// if namespace is accessible from Kiali (Deployment.AccessibleNamespaces)
	if _, err = in.businessLayer.Namespace.GetNamespace(ctx, criteria.Namespace); err != nil {
		return nil, err
	}

	nFetches := 4
	if criteria.IncludeIstioResources {
		nFetches = 5
	}

	wg := sync.WaitGroup{}
	wg.Add(nFetches)
	errChan := make(chan error, nFetches)

	go func() {
		defer wg.Done()
		var err2 error
		var selectorLabels map[string]string
		if criteria.ServiceSelector != "" {
			if selector, err3 := labels.ConvertSelectorToLabelsMap(criteria.ServiceSelector); err3 == nil {
				selectorLabels = selector
			} else {
				log.Warningf("Services not filtered. Selector %s not valid", criteria.ServiceSelector)
			}
		}
		// Check if namespace is cached
		// Namespace access is checked in the upper call
		if IsNamespaceCached(criteria.Namespace) {
			svcs, err2 = kialiCache.GetServices(criteria.Namespace, selectorLabels)
		} else {
			svcs, err2 = in.k8s.GetServices(criteria.Namespace, selectorLabels)
		}
		if err2 != nil {
			log.Errorf("Error fetching Services per namespace %s: %s", criteria.Namespace, err2)
			errChan <- err2
		}
	}()

	go func() {
		defer wg.Done()
		var err2 error
		registryCriteria := RegistryCriteria{
			Namespace:       criteria.Namespace,
			ServiceSelector: criteria.ServiceSelector,
		}
		rSvcs, err2 = in.businessLayer.RegistryStatus.GetRegistryServices(registryCriteria)
		if err2 != nil {
			log.Errorf("Error fetching Registry Services per namespace %s: %s", criteria.Namespace, err2)
			errChan <- err2
		}
	}()

	go func() {
		defer wg.Done()
		var err2 error
		if !criteria.IncludeOnlyDefinitions {
			// Check if namespace is cached
			// Namespace access is checked in the upper call
			if IsNamespaceCached(criteria.Namespace) {
				pods, err2 = kialiCache.GetPods(criteria.Namespace, "")
			} else {
				pods, err2 = in.k8s.GetPods(criteria.Namespace, "")
			}
			if err2 != nil {
				log.Errorf("Error fetching Pods per namespace %s: %s", criteria.Namespace, err2)
				errChan <- err2
			}
		}
	}()

	go func() {
		defer wg.Done()
		var err2 error
		if !criteria.IncludeOnlyDefinitions {
			// Check if namespace is cached
			// Namespace access is checked in the upper call
			if IsNamespaceCached(criteria.Namespace) {
				deployments, err2 = kialiCache.GetDeployments(criteria.Namespace)
			} else {
				deployments, err2 = in.k8s.GetDeployments(criteria.Namespace)
			}
			if err2 != nil {
				log.Errorf("Error fetching Deployments per namespace %s: %s", criteria.Namespace, err2)
				errChan <- err2
			}
		}
	}()

	// Cross-namespace query of all Istio Resources to find references
	// References MAY have visibility for a user but not access if they are not allowed to access to the namespace
	if criteria.IncludeIstioResources {
		criteria := IstioConfigCriteria{
			AllNamespaces:           true,
			Namespace:               criteria.Namespace,
			IncludeDestinationRules: true,
			IncludeGateways:         true,
			IncludeServiceEntries:   true,
			IncludeVirtualServices:  true,
		}
		go func() {
			defer wg.Done()
			var err2 error
			istioConfigList, err2 = in.businessLayer.IstioConfig.GetIstioConfigList(ctx, criteria)
			if err2 != nil {
				log.Errorf("Error fetching IstioConfigList per namespace %s: %s", criteria.Namespace, err2)
				errChan <- err2
			}
		}()
	}

	wg.Wait()
	if len(errChan) != 0 {
		err = <-errChan
		return nil, err
	}

	// Convert to Kiali model
	return in.buildServiceList(models.Namespace{Name: criteria.Namespace}, svcs, rSvcs, pods, deployments, istioConfigList), nil
}

func getVSKialiScenario(vs []networking_v1alpha3.VirtualService) string {
	scenario := ""
	for _, v := range vs {
		if scenario, ok := v.Labels["kiali_wizard"]; ok {
			return scenario
		}
	}
	return scenario
}

func getDRKialiScenario(dr []networking_v1alpha3.DestinationRule) string {
	scenario := ""
	for _, d := range dr {
		if scenario, ok := d.Labels["kiali_wizard"]; ok {
			return scenario
		}
	}
	return scenario
}

func (in *svcService) buildServiceList(namespace models.Namespace, svcs []core_v1.Service, rSvcs []*kubernetes.RegistryService, pods []core_v1.Pod, deployments []apps_v1.Deployment, istioConfigList models.IstioConfigList) *models.ServiceList {
	var services []models.ServiceOverview
	validations := in.getServiceValidations(svcs, deployments, pods)

	kubernetesServices := in.buildKubernetesServices(svcs, pods, istioConfigList)
	services = append(services, kubernetesServices...)

	// Add Istio Registry Services that are not present in the Kubernetes list
	rSvcs = kubernetes.FilterRegistryServicesByServices(rSvcs, svcs)
	registryServices := in.buildRegistryServices(rSvcs, istioConfigList)
	services = append(services, registryServices...)

	return &models.ServiceList{Namespace: namespace, Services: services, Validations: validations}
}

func (in *svcService) buildKubernetesServices(svcs []core_v1.Service, pods []core_v1.Pod, istioConfigList models.IstioConfigList) []models.ServiceOverview {
	services := make([]models.ServiceOverview, len(svcs))
	conf := config.Get()

	// Convert each k8s service into our model
	for i, item := range svcs {
		sPods := kubernetes.FilterPodsByService(&item, pods)
		/** Check if Service has istioSidecar deployed */
		mPods := models.Pods{}
		mPods.Parse(sPods)
		hasSidecar := mPods.HasAnyIstioSidecar()
		svcVirtualServices := kubernetes.FilterVirtualServicesByService(istioConfigList.VirtualServices, item.Namespace, item.Name)
		svcDestinationRules := kubernetes.FilterDestinationRulesByService(istioConfigList.DestinationRules, item.Namespace, item.Name)
		svcGateways := kubernetes.FilterGatewaysByVirtualServices(istioConfigList.Gateways, svcVirtualServices)
		svcReferences := make([]*models.IstioValidationKey, 0)
		for _, vs := range svcVirtualServices {
			ref := models.BuildKey(vs.Kind, vs.Name, vs.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		for _, dr := range svcDestinationRules {
			ref := models.BuildKey(dr.Kind, dr.Name, dr.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		for _, gw := range svcGateways {
			ref := models.BuildKey(gw.Kind, gw.Name, gw.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		svcReferences = FilterUniqueIstioReferences(svcReferences)

		kialiWizard := getVSKialiScenario(svcVirtualServices)
		if kialiWizard == "" {
			kialiWizard = getDRKialiScenario(svcDestinationRules)
		}

		/** Check if Service has the label app required by Istio */
		_, appLabel := item.Spec.Selector[conf.IstioLabels.AppLabelName]
		/** Check if Service has additional item icon */
		services[i] = models.ServiceOverview{
			Name:                   item.Name,
			Namespace:              item.Namespace,
			IstioSidecar:           hasSidecar,
			AppLabel:               appLabel,
			AdditionalDetailSample: models.GetFirstAdditionalIcon(conf, item.ObjectMeta.Annotations),
			HealthAnnotations:      models.GetHealthAnnotation(item.Annotations, models.GetHealthConfigAnnotation()),
			Labels:                 item.Labels,
			Selector:               item.Spec.Selector,
			IstioReferences:        svcReferences,
			KialiWizard:            kialiWizard,
			ServiceRegistry:        "Kubernetes",
		}
	}
	return services
}

func (in *svcService) buildRegistryServices(rSvcs []*kubernetes.RegistryService, istioConfigList models.IstioConfigList) []models.ServiceOverview {
	services := make([]models.ServiceOverview, len(rSvcs))
	conf := config.Get()

	for i, item := range rSvcs {
		_, appLabel := item.Attributes.LabelSelectors[conf.IstioLabels.AppLabelName]
		// ServiceEntry/External and Federation will be marked as hasSidecar == true as they will have telemetry
		hasSidecar := true
		if item.Attributes.ServiceRegistry != "External" && item.Attributes.ServiceRegistry != "Federation" {
			hasSidecar = false
		}
		// TODO wildcards may force additional checks on hostnames ?
		svcServiceEntries := kubernetes.FilterServiceEntriesByHostname(istioConfigList.ServiceEntries, item.Hostname)
		svcDestinationRules := kubernetes.FilterDestinationRulesByHostname(istioConfigList.DestinationRules, item.Hostname)
		svcVirtualServices := kubernetes.FilterVirtualServicesByHostname(istioConfigList.VirtualServices, item.Hostname)
		svcGateways := kubernetes.FilterGatewaysByVirtualServices(istioConfigList.Gateways, svcVirtualServices)
		svcReferences := make([]*models.IstioValidationKey, 0)
		for _, se := range svcServiceEntries {
			ref := models.BuildKey(se.Kind, se.Name, se.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		for _, vs := range svcVirtualServices {
			ref := models.BuildKey(vs.Kind, vs.Name, vs.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		for _, dr := range svcDestinationRules {
			ref := models.BuildKey(dr.Kind, dr.Name, dr.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		for _, gw := range svcGateways {
			ref := models.BuildKey(gw.Kind, gw.Name, gw.Namespace)
			svcReferences = append(svcReferences, &ref)
		}
		svcReferences = FilterUniqueIstioReferences(svcReferences)
		// External Istio registries may have references to ServiceEntry and/or Federation
		services[i] = models.ServiceOverview{
			Name:              item.Attributes.Name,
			Namespace:         item.Attributes.Namespace,
			IstioSidecar:      hasSidecar,
			AppLabel:          appLabel,
			HealthAnnotations: map[string]string{},
			Labels:            item.Attributes.Labels,
			Selector:          item.Attributes.LabelSelectors,
			IstioReferences:   svcReferences,
			ServiceRegistry:   item.Attributes.ServiceRegistry,
		}
	}
	return services
}

// GetService returns a single service and associated data using the interval and queryTime
func (in *svcService) GetServiceDetails(ctx context.Context, namespace, service, interval string, queryTime time.Time) (*models.ServiceDetails, error) {
	// Check if user has access to the namespace (RBAC) in cache scenarios and/or
	// if namespace is accessible from Kiali (Deployment.AccessibleNamespaces)
	if _, err := in.businessLayer.Namespace.GetNamespace(ctx, namespace); err != nil {
		return nil, err
	}

	svc, err := in.GetService(ctx, namespace, service)
	if err != nil {
		return nil, err
	}

	var eps *core_v1.Endpoints
	var rEps []*kubernetes.RegistryEndpoint
	var pods []core_v1.Pod
	var hth models.ServiceHealth
	var istioConfigList models.IstioConfigList
	var ws models.Workloads
	var nsmtls models.MTLSStatus

	wg := sync.WaitGroup{}
	wg.Add(6)
	errChan := make(chan error, 6)

	labelsSelector := labels.Set(svc.Selectors).String()
	// If service doesn't have any selector, we can't know which are the pods and workloads applying.
	if labelsSelector != "" {
		wg.Add(2)

		go func() {
			defer wg.Done()
			var err2 error
			// Check if namespace is cached
			// Namespace access is checked in the upper caller
			if IsNamespaceCached(namespace) {
				pods, err2 = kialiCache.GetPods(namespace, labelsSelector)
			} else {
				pods, err2 = in.k8s.GetPods(namespace, labelsSelector)
			}
			if err2 != nil {
				errChan <- err2
			}
		}()

		go func(ctx context.Context) {
			defer wg.Done()
			var err2 error
			ws, err2 = fetchWorkloads(ctx, in.businessLayer, namespace, labelsSelector)
			if err2 != nil {
				log.Errorf("Error fetching Workloads per namespace %s and service %s: %s", namespace, service, err2)
				errChan <- err2
			}
		}(ctx)
	}

	go func() {
		defer wg.Done()
		var err2 error
		criteria := RegistryCriteria{
			Namespace:   namespace,
			ServiceName: service,
		}
		rEps, err2 = in.businessLayer.RegistryStatus.GetRegistryEndpoints(criteria)
		if err2 != nil {
			log.Errorf("Error fetching Registry Endpoints namespace %s and service %s: %s", namespace, service, err2)
			errChan <- err2
		}
	}()

	go func(ctx context.Context) {
		defer wg.Done()
		var err2 error
		if IsNamespaceCached(namespace) {
			// Cache uses Kiali ServiceAccount, check if user can access to the namespace
			if _, err = in.businessLayer.Namespace.GetNamespace(ctx, namespace); err == nil {
				eps, err = kialiCache.GetEndpoints(namespace, service)
			}
		} else {
			eps, err2 = in.k8s.GetEndpoints(namespace, service)
		}
		if err2 != nil && !errors.IsNotFound(err2) {
			log.Errorf("Error fetching Endpoints namespace %s and service %s: %s", namespace, service, err2)
			errChan <- err2
		}
	}(ctx)

	go func(ctx context.Context) {
		defer wg.Done()
		var err2 error
		hth, err2 = in.businessLayer.Health.GetServiceHealth(ctx, namespace, service, interval, queryTime)
		if err2 != nil {
			errChan <- err2
		}
	}(ctx)

	go func(ctx context.Context) {
		defer wg.Done()
		var err2 error
		nsmtls, err2 = in.businessLayer.TLS.NamespaceWidemTLSStatus(ctx, namespace)
		if err2 != nil {
			errChan <- err2
		}
	}(ctx)

	go func(ctx context.Context) {
		defer wg.Done()
		var err2 error
		criteria := IstioConfigCriteria{
			AllNamespaces:           true,
			Namespace:               namespace,
			IncludeDestinationRules: true,
			// TODO the frontend is merging the Gateways per ServiceDetails but it would be a clean design to locate it here
			IncludeGateways:        true,
			IncludeServiceEntries:  true,
			IncludeVirtualServices: true,
		}
		istioConfigList, err2 = in.businessLayer.IstioConfig.GetIstioConfigList(ctx, criteria)
		if err2 != nil {
			log.Errorf("Error fetching IstioConfigList per namespace %s: %s", criteria.Namespace, err2)
			errChan <- err2
		}
	}(ctx)

	var vsCreate, vsUpdate, vsDelete bool
	go func() {
		defer wg.Done()
		/*
			We can safely assume that permissions for VirtualServices will be similar as DestinationRules.

			Synced with:
			https://github.com/kiali/kiali-operator/blob/master/roles/default/kiali-deploy/templates/kubernetes/role.yaml#L62
		*/
		vsCreate, vsUpdate, vsDelete = getPermissions(context.TODO(), in.k8s, namespace, kubernetes.VirtualServices)
	}()

	wg.Wait()
	if len(errChan) != 0 {
		err = <-errChan
		return nil, err
	}

	wo := models.WorkloadOverviews{}
	for _, w := range ws {
		wi := &models.WorkloadListItem{}
		wi.ParseWorkload(w)
		wo = append(wo, wi)
	}

	s := models.ServiceDetails{Workloads: wo, Health: hth, NamespaceMTLS: nsmtls}
	s.Service = svc
	s.SetPods(kubernetes.FilterPodsByEndpoints(eps, pods))
	// ServiceDetail will consider if the Service is a External/Federation entry
	if s.Service.Type == "External" || s.Service.Type == "Federation" {
		s.IstioSidecar = true
	} else {
		s.SetIstioSidecar(wo)
	}
	s.SetEndpoints(eps)
	s.SetRegistryEndpoints(rEps)
	s.IstioPermissions = models.ResourcePermissions{
		Create: vsCreate,
		Update: vsUpdate,
		Delete: vsDelete,
	}
	s.VirtualServices = kubernetes.FilterVirtualServicesByService(istioConfigList.VirtualServices, namespace, service)
	s.DestinationRules = kubernetes.FilterDestinationRulesByService(istioConfigList.DestinationRules, namespace, service)
	if s.Service.Type == "External" || s.Service.Type == "Federation" {
		// On ServiceEntries cases the Service name is the hostname
		s.ServiceEntries = kubernetes.FilterServiceEntriesByHostname(istioConfigList.ServiceEntries, s.Service.Name)
	}
	return &s, nil
}

func (in *svcService) UpdateService(ctx context.Context, namespace, service string, interval string, queryTime time.Time, jsonPatch string) (*models.ServiceDetails, error) {
	// Identify controller and apply patch to workload
	err := updateService(in.businessLayer, namespace, service, jsonPatch)
	if err != nil {
		return nil, err
	}

	// Cache is stopped after a Create/Update/Delete operation to force a refresh
	if kialiCache != nil && err == nil {
		kialiCache.RefreshNamespace(namespace)
	}

	// After the update we fetch the whole workload
	return in.GetServiceDetails(ctx, namespace, service, interval, queryTime)
}

func (in *svcService) GetService(ctx context.Context, namespace, service string) (models.Service, error) {
	var err error
	var kSvc *core_v1.Service
	svc := models.Service{}
	if IsNamespaceCached(namespace) {
		// Cache uses Kiali ServiceAccount, check if user can access to the namespace
		if _, err = in.businessLayer.Namespace.GetNamespace(ctx, namespace); err == nil {
			kSvc, err = kialiCache.GetService(namespace, service)
		}
	} else {
		kSvc, err = in.k8s.GetService(namespace, service)
	}
	// Check if this service is in the Istio Registry
	if kSvc != nil {
		svc.Parse(kSvc)
	} else {
		criteria := RegistryCriteria{
			Namespace: namespace,
		}
		rSvcs, err := in.businessLayer.RegistryStatus.GetRegistryServices(criteria)
		if err != nil {
			return svc, err
		}
		for _, rSvc := range rSvcs {
			if rSvc.Attributes.Name == service {
				svc.ParseRegistryService(rSvc)
				break
			}
		}
		// Service not found in Kubernetes and Istio
		if svc.Name == "" {
			err = kubernetes.NewNotFound(service, "Kiali", "Service")
			return svc, err
		}
	}
	return svc, err
}

func (in *svcService) getServiceValidations(services []core_v1.Service, deployments []apps_v1.Deployment, pods []core_v1.Pod) models.IstioValidations {
	validations := checkers.ServiceChecker{
		Services:    services,
		Deployments: deployments,
		Pods:        pods,
	}.Check()

	return validations
}

// GetServiceAppName returns the "Application" name (app label) that relates to a service
// This label is taken from the service selector, which means it is assumed that pods are selected using that label
func (in *svcService) GetServiceAppName(ctx context.Context, namespace, service string) (string, error) {
	// Check if user has access to the namespace (RBAC) in cache scenarios and/or
	// if namespace is accessible from Kiali (Deployment.AccessibleNamespaces)
	if _, err := in.businessLayer.Namespace.GetNamespace(ctx, namespace); err != nil {
		return "", err
	}

	svc, err := in.GetService(ctx, namespace, service)
	if err != nil {
		return "", fmt.Errorf("Service [namespace: %s] [name: %s] doesn't exist.", namespace, service)
	}

	appLabelName := config.Get().IstioLabels.AppLabelName
	app := svc.Selectors[appLabelName]
	return app, nil
}

func updateService(layer *Layer, namespace string, service string, jsonPatch string) error {
	// Check if user has access to the namespace (RBAC) in cache scenarios and/or
	// if namespace is accessible from Kiali (Deployment.AccessibleNamespaces)
	if _, err := layer.Namespace.GetNamespace(context.TODO(), namespace); err != nil {
		return err
	}

	return layer.k8s.UpdateService(namespace, service, jsonPatch)
}

type svcServiceWithTracing struct {
	SvcService
}

func (in *svcServiceWithTracing) GetServiceList(ctx context.Context, criteria ServiceCriteria) (*models.ServiceList, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetServiceList",
			trace.WithAttributes(
				attribute.String("package", "business"),
			),
		)
		defer span.End()
	}

	return in.SvcService.GetServiceList(ctx, criteria)
}

func (in *svcServiceWithTracing) GetServiceDetails(ctx context.Context, namespace, service, interval string, queryTime time.Time) (*models.ServiceDetails, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetServiceDetails",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("service", service),
				attribute.String("interval", interval),
			),
		)
		defer span.End()
	}

	return in.SvcService.GetServiceDetails(ctx, namespace, service, interval, queryTime)
}

func (in *svcServiceWithTracing) UpdateService(ctx context.Context, namespace, service string, interval string, queryTime time.Time, jsonPatch string) (*models.ServiceDetails, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "UpdateService",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("service", service),
				attribute.String("interval", interval),
				attribute.String("jsonPatch", jsonPatch),
			),
		)
		defer span.End()
	}

	return in.SvcService.UpdateService(ctx, namespace, service, interval, queryTime, jsonPatch)
}

func (in *svcServiceWithTracing) GetService(ctx context.Context, namespace, service string) (models.Service, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetService",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("service", service),
			),
		)
		defer span.End()
	}

	return in.SvcService.GetService(ctx, namespace, service)
}

func (in *svcServiceWithTracing) GetServiceAppName(ctx context.Context, namespace, service string) (string, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetServiceAppName",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("service", service),
			),
		)
		defer span.End()
	}

	return in.SvcService.GetServiceAppName(ctx, namespace, service)
}
