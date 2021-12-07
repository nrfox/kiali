package business

import (
	"context"
	"sort"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/observability"
	"github.com/kiali/kiali/prometheus"
)

// AppService deals with fetching Workloads group by "app" label, which will be identified as an "application"
type AppService interface {
	GetAppList(ctx context.Context, namespace string, linkIstioResources bool) (models.AppList, error)
	GetApp(ctx context.Context, namespace string, appName string) (models.App, error)
}

type appService struct {
	prom          prometheus.ClientInterface
	k8s           kubernetes.ClientInterface
	businessLayer *Layer
}

func joinMap(m1 map[string][]string, m2 map[string]string) {
	for k, v2 := range m2 {
		dup := false
		for _, v1 := range m1[k] {
			if v1 == v2 {
				dup = true
				break
			}
		}
		if !dup {
			m1[k] = append(m1[k], v2)
		}
	}
}

func buildFinalLabels(m map[string][]string) map[string]string {
	consolidated := make(map[string]string, len(m))
	for k, list := range m {
		sort.Strings(list)
		consolidated[k] = strings.Join(list, ",")
	}
	return consolidated
}

// GetAppList is the API handler to fetch the list of applications in a given namespace
func (in *appService) GetAppList(ctx context.Context, namespace string, linkIstioResources bool) (models.AppList, error) {
	appList := &models.AppList{
		Namespace: models.Namespace{Name: namespace},
		Apps:      []models.AppListItem{},
	}

	var err error
	var apps namespaceApps

	nFetches := 1
	if linkIstioResources {
		nFetches = 2
	}

	wg := sync.WaitGroup{}
	wg.Add(nFetches)
	errChan := make(chan error, nFetches)

	go func(ctx context.Context) {
		defer wg.Done()
		var err2 error
		apps, err2 = fetchNamespaceApps(ctx, in.businessLayer, namespace, "")
		if err2 != nil {
			log.Errorf("Error fetching Applications per namespace %s: %s", namespace, err2)
			errChan <- err2
		}
	}(ctx)

	criteria := IstioConfigCriteria{
		Namespace:                     namespace,
		IncludeAuthorizationPolicies:  true,
		IncludeDestinationRules:       true,
		IncludeEnvoyFilters:           true,
		IncludeGateways:               true,
		IncludePeerAuthentications:    true,
		IncludeRequestAuthentications: true,
		IncludeSidecars:               true,
		IncludeVirtualServices:        true,
	}
	var istioConfigList models.IstioConfigList

	if linkIstioResources {
		go func(ctx context.Context) {
			defer wg.Done()
			var err2 error
			istioConfigList, err2 = in.businessLayer.IstioConfig.GetIstioConfigList(ctx, criteria)
			if err2 != nil {
				log.Errorf("Error fetching Istio Config per namespace %s: %s", namespace, err2)
				errChan <- err2
			}
		}(ctx)
	}

	wg.Wait()
	if len(errChan) != 0 {
		err = <-errChan
		return *appList, err
	}

	for keyApp, valueApp := range apps {
		appItem := &models.AppListItem{
			Name:         keyApp,
			IstioSidecar: true,
		}
		applabels := make(map[string][]string)
		svcReferences := make([]*models.IstioValidationKey, 0)
		for _, srv := range valueApp.Services {
			joinMap(applabels, srv.Labels)
			if linkIstioResources {
				vsFiltered := kubernetes.FilterVirtualServicesByService(istioConfigList.VirtualServices, srv.Namespace, srv.Name)
				for _, v := range vsFiltered {
					ref := models.BuildKey(v.Kind, v.Namespace, v.Name)
					svcReferences = append(svcReferences, &ref)
				}
				drFiltered := kubernetes.FilterDestinationRulesByService(istioConfigList.DestinationRules, srv.Namespace, srv.Name)
				for _, d := range drFiltered {
					ref := models.BuildKey(d.Kind, d.Namespace, d.Name)
					svcReferences = append(svcReferences, &ref)
				}
				gwFiltered := kubernetes.FilterGatewaysByVirtualServices(istioConfigList.Gateways, istioConfigList.VirtualServices)
				for _, g := range gwFiltered {
					ref := models.BuildKey(g.Kind, g.Namespace, g.Name)
					svcReferences = append(svcReferences, &ref)
				}

			}

		}

		wkdReferences := make([]*models.IstioValidationKey, 0)
		for _, wrk := range valueApp.Workloads {
			joinMap(applabels, wrk.Labels)
			if linkIstioResources {
				wSelector := labels.Set(wrk.Labels).AsSelector().String()
				wkdReferences = append(wkdReferences, FilterWorkloadReferences(wSelector, istioConfigList)...)
			}
		}
		appItem.Labels = buildFinalLabels(applabels)
		appItem.IstioReferences = FilterUniqueIstioReferences(append(svcReferences, wkdReferences...))

		for _, w := range valueApp.Workloads {
			if appItem.IstioSidecar = w.IstioSidecar; !appItem.IstioSidecar {
				break
			}
		}
		(*appList).Apps = append((*appList).Apps, *appItem)
	}

	return *appList, nil
}

// GetApp is the API handler to fetch the details for a given namespace and app name
func (in *appService) GetApp(ctx context.Context, namespace string, appName string) (models.App, error) {
	appInstance := &models.App{Namespace: models.Namespace{Name: namespace}, Name: appName}
	ns, err := in.businessLayer.Namespace.GetNamespace(ctx, namespace)
	if err != nil {
		return *appInstance, err
	}
	appInstance.Namespace = *ns
	namespaceApps, err := fetchNamespaceApps(ctx, in.businessLayer, namespace, appName)
	if err != nil {
		return *appInstance, err
	}

	var appDetails *appDetails
	var ok bool
	// Send a NewNotFound if the app is not found in the deployment list, instead to send an empty result
	if appDetails, ok = namespaceApps[appName]; !ok {
		return *appInstance, kubernetes.NewNotFound(appName, "Kiali", "App")
	}

	(*appInstance).Workloads = make([]models.WorkloadItem, len(appDetails.Workloads))
	for i, wkd := range appDetails.Workloads {
		(*appInstance).Workloads[i] = models.WorkloadItem{WorkloadName: wkd.Name, IstioSidecar: wkd.IstioSidecar, ServiceAccountNames: wkd.Pods.ServiceAccounts()}
	}

	(*appInstance).ServiceNames = make([]string, len(appDetails.Services))
	for i, svc := range appDetails.Services {
		(*appInstance).ServiceNames[i] = svc.Name
	}

	pods := models.Pods{}
	for _, workload := range appDetails.Workloads {
		pods = append(pods, workload.Pods...)
	}
	(*appInstance).Runtimes = NewDashboardsService(ns, nil).GetCustomDashboardRefs(namespace, appName, "", pods)

	return *appInstance, nil
}

// AppDetails holds Services and Workloads having the same "app" label
type appDetails struct {
	app       string
	Services  []models.ServiceOverview
	Workloads models.Workloads
}

// NamespaceApps is a map of app_name x AppDetails
type namespaceApps = map[string]*appDetails

func castAppDetails(ss *models.ServiceList, ws models.Workloads) namespaceApps {
	allEntities := make(namespaceApps)
	appLabel := config.Get().IstioLabels.AppLabelName
	if ss != nil {
		for _, service := range ss.Services {
			if app, ok := service.Selector[appLabel]; ok {
				if appEntities, ok := allEntities[app]; ok {
					appEntities.Services = append(appEntities.Services, service)
				} else {
					allEntities[app] = &appDetails{
						app:      app,
						Services: []models.ServiceOverview{service},
					}
				}
			}
		}
	}
	for _, w := range ws {
		if app, ok := w.Labels[appLabel]; ok {
			if appEntities, ok := allEntities[app]; ok {
				appEntities.Workloads = append(appEntities.Workloads, w)
			} else {
				allEntities[app] = &appDetails{
					app:       app,
					Workloads: models.Workloads{w},
				}
			}
		}
	}
	return allEntities
}

// Helper method to fetch all applications for a given namespace.
// Optionally if appName parameter is provided, it filters apps for that name.
// Return an error on any problem.
func fetchNamespaceApps(ctx context.Context, layer *Layer, namespace string, appName string) (namespaceApps, error) {
	var ss *models.ServiceList
	var ws models.Workloads
	cfg := config.Get()

	appNameSelector := ""
	if appName != "" {
		selector := labels.Set(map[string]string{cfg.IstioLabels.AppLabelName: appName})
		appNameSelector = selector.String()
	}

	// Check if user has access to the namespace (RBAC) in cache scenarios and/or
	// if namespace is accessible from Kiali (Deployment.AccessibleNamespaces)
	if _, err := layer.Namespace.GetNamespace(ctx, namespace); err != nil {
		return nil, err
	}

	wg := sync.WaitGroup{}
	wg.Add(2)
	errChan := make(chan error, 2)

	go func(ctx context.Context) {
		defer wg.Done()
		var err error
		// Check if namespace is cached
		criteria := ServiceCriteria{
			Namespace:              namespace,
			IncludeIstioResources:  false,
			IncludeOnlyDefinitions: true,
			ServiceSelector:        appNameSelector,
		}
		ss, err = layer.Svc.GetServiceList(ctx, criteria)
		if err != nil {
			log.Errorf("Error fetching Services per namespace %s: %s", namespace, err)
			errChan <- err
		}
	}(ctx)

	go func(ctx context.Context) {
		defer wg.Done()
		var err error
		ws, err = fetchWorkloads(ctx, layer, namespace, appNameSelector)
		if err != nil {
			log.Errorf("Error fetching Workload per namespace %s: %s", namespace, err)
			errChan <- err
		}
	}(ctx)

	wg.Wait()
	if len(errChan) != 0 {
		err := <-errChan
		return nil, err
	}

	return castAppDetails(ss, ws), nil
}

type appServiceWithTracing struct {
	AppService
}

func (in *appServiceWithTracing) GetAppList(ctx context.Context, namespace string, linkIstioResources bool) (models.AppList, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetAppList",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.Bool("linkIstioResources", linkIstioResources),
			),
		)
		defer span.End()
	}
	return in.AppService.GetAppList(ctx, namespace, linkIstioResources)
}

func (in *appServiceWithTracing) GetApp(ctx context.Context, namespace string, appName string) (models.App, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetApp",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("appName", appName),
			),
		)
		defer span.End()
	}
	return in.AppService.GetApp(ctx, namespace, appName)
}
