package business

import (
	"context"
	"time"

	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/observability"
	"github.com/kiali/kiali/prometheus"
)

// HealthService deals with fetching health from various sources and convert to kiali model
type HealthService interface {
	GetServiceHealth(ctx context.Context, namespace, service, rateInterval string, queryTime time.Time) (models.ServiceHealth, error)
	GetAppHealth(ctx context.Context, namespace, app, rateInterval string, queryTime time.Time) (models.AppHealth, error)
	GetWorkloadHealth(ctx context.Context, namespace, workload, workloadType, rateInterval string, queryTime time.Time) (models.WorkloadHealth, error)
	GetNamespaceServiceHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceServiceHealth, error)
	GetNamespaceWorkloadHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceWorkloadHealth, error)
	GetNamespaceAppHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceAppHealth, error)
}

type healthService struct {
	prom          prometheus.ClientInterface
	k8s           kubernetes.ClientInterface
	businessLayer *Layer
}

// Annotation Filter for Health
var HealthAnnotation = []models.AnnotationKey{models.RateHealthAnnotation}

// GetServiceHealth returns a service health (service request error rate)
func (in *healthService) GetServiceHealth(ctx context.Context, namespace, service, rateInterval string, queryTime time.Time) (models.ServiceHealth, error) {
	rqHealth, err := in.getServiceRequestsHealth(ctx, namespace, service, rateInterval, queryTime)
	return models.ServiceHealth{Requests: rqHealth}, err
}

// GetAppHealth returns an app health from just Namespace and app name (thus, it fetches data from K8S and Prometheus)
func (in *healthService) GetAppHealth(ctx context.Context, namespace, app, rateInterval string, queryTime time.Time) (models.AppHealth, error) {
	appLabel := config.Get().IstioLabels.AppLabelName

	selectorLabels := make(map[string]string)
	selectorLabels[appLabel] = app
	labelSelector := labels.FormatLabels(selectorLabels)

	ws, err := fetchWorkloads(ctx, in.businessLayer, namespace, labelSelector)
	if err != nil {
		log.Errorf("Error fetching Workloads per namespace %s and app %s: %s", namespace, app, err)
		return models.AppHealth{}, err
	}

	return in.getAppHealth(namespace, app, rateInterval, queryTime, ws)
}

func (in *healthService) getAppHealth(namespace, app, rateInterval string, queryTime time.Time, ws models.Workloads) (models.AppHealth, error) {
	health := models.EmptyAppHealth()

	// Perf: do not bother fetching request rate if there are no workloads or no workload has sidecar
	hasSidecar := false
	for _, w := range ws {
		if w.IstioSidecar {
			hasSidecar = true
			break
		}
	}

	// Fetch services requests rates
	var errRate error
	if hasSidecar {
		rate, err := in.getAppRequestsHealth(namespace, app, rateInterval, queryTime)
		health.Requests = rate
		errRate = err
	}

	// Deployment status
	health.WorkloadStatuses = ws.CastWorkloadStatuses()

	return health, errRate
}

// GetWorkloadHealth returns a workload health from just Namespace and workload (thus, it fetches data from K8S and Prometheus)
func (in *healthService) GetWorkloadHealth(ctx context.Context, namespace, workload, workloadType, rateInterval string, queryTime time.Time) (models.WorkloadHealth, error) {
	w, err := fetchWorkload(ctx, in.businessLayer, namespace, workload, workloadType)
	if err != nil {
		return models.WorkloadHealth{}, err
	}

	status := w.CastWorkloadStatus()

	// Perf: do not bother fetching request rate if workload has no sidecar
	if !w.IstioSidecar {
		return models.WorkloadHealth{
			WorkloadStatus: status,
			Requests:       models.NewEmptyRequestHealth(),
		}, nil
	}

	// Add Telemetry info
	rate, err := in.getWorkloadRequestsHealth(ctx, namespace, workload, rateInterval, queryTime)
	return models.WorkloadHealth{
		WorkloadStatus: status,
		Requests:       rate,
	}, err
}

// GetNamespaceAppHealth returns a health for all apps in given Namespace (thus, it fetches data from K8S and Prometheus)
func (in *healthService) GetNamespaceAppHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceAppHealth, error) {
	appEntities, err := fetchNamespaceApps(ctx, in.businessLayer, namespace, "")
	if err != nil {
		return nil, err
	}

	return in.getNamespaceAppHealth(namespace, appEntities, rateInterval, queryTime)
}

func (in *healthService) getNamespaceAppHealth(namespace string, appEntities namespaceApps, rateInterval string, queryTime time.Time) (models.NamespaceAppHealth, error) {
	allHealth := make(models.NamespaceAppHealth)

	// Perf: do not bother fetching request rate if no workloads or no workload has sidecar
	sidecarPresent := false

	// Prepare all data
	for app, entities := range appEntities {
		if app != "" {
			h := models.EmptyAppHealth()
			allHealth[app] = &h
			if entities != nil {
				h.WorkloadStatuses = entities.Workloads.CastWorkloadStatuses()
				for _, w := range entities.Workloads {
					if w.IstioSidecar {
						sidecarPresent = true
						break
					}
				}
			}
		}
	}

	if sidecarPresent {
		// Fetch services requests rates
		rates, err := in.prom.GetAllRequestRates(namespace, rateInterval, queryTime)
		if err != nil {
			return allHealth, errors.NewServiceUnavailable(err.Error())
		}
		// Fill with collected request rates
		fillAppRequestRates(allHealth, rates)
	}

	return allHealth, nil
}

// GetNamespaceServiceHealth returns a health for all services in given Namespace (thus, it fetches data from K8S and Prometheus)
func (in *healthService) GetNamespaceServiceHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceServiceHealth, error) {
	var services *models.ServiceList
	var err error
	// Check if user has access to the namespace (RBAC) in cache scenarios and/or
	// if namespace is accessible from Kiali (Deployment.AccessibleNamespaces)
	if _, err := in.businessLayer.Namespace.GetNamespace(ctx, namespace); err != nil {
		return nil, err
	}

	criteria := ServiceCriteria{
		Namespace:              namespace,
		IncludeOnlyDefinitions: true,
		IncludeIstioResources:  false,
	}
	services, err = in.businessLayer.Svc.GetServiceList(ctx, criteria)
	if err != nil {
		return nil, err
	}
	return in.getNamespaceServiceHealth(namespace, services, rateInterval, queryTime), nil
}

func (in *healthService) getNamespaceServiceHealth(namespace string, services *models.ServiceList, rateInterval string, queryTime time.Time) models.NamespaceServiceHealth {
	allHealth := make(models.NamespaceServiceHealth)

	// Prepare all data (note that it's important to provide data for all services, even those which may not have any health, for overview cards)
	if services != nil {
		for _, service := range services.Services {
			h := models.EmptyServiceHealth()
			h.Requests.HealthAnnotations = service.HealthAnnotations
			allHealth[service.Name] = &h
		}
	}

	// Fetch services requests rates
	rates, _ := in.prom.GetNamespaceServicesRequestRates(namespace, rateInterval, queryTime)
	// Fill with collected request rates
	lblDestSvc := model.LabelName("destination_service_name")
	for _, sample := range rates {
		service := string(sample.Metric[lblDestSvc])
		if health, ok := allHealth[service]; ok {
			health.Requests.AggregateInbound(sample)
		}
	}
	for _, health := range allHealth {
		health.Requests.CombineReporters()
	}
	return allHealth
}

// GetNamespaceWorkloadHealth returns a health for all workloads in given Namespace (thus, it fetches data from K8S and Prometheus)
func (in *healthService) GetNamespaceWorkloadHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceWorkloadHealth, error) {
	wl, err := fetchWorkloads(ctx, in.businessLayer, namespace, "")
	if err != nil {
		return nil, err
	}

	return in.getNamespaceWorkloadHealth(namespace, wl, rateInterval, queryTime)
}

func (in *healthService) getNamespaceWorkloadHealth(namespace string, ws models.Workloads, rateInterval string, queryTime time.Time) (models.NamespaceWorkloadHealth, error) {
	// Perf: do not bother fetching request rate if no workloads or no workload has sidecar
	hasSidecar := false

	allHealth := make(models.NamespaceWorkloadHealth)
	for _, w := range ws {
		allHealth[w.Name] = models.EmptyWorkloadHealth()
		allHealth[w.Name].Requests.HealthAnnotations = models.GetHealthAnnotation(w.HealthAnnotations, HealthAnnotation)
		allHealth[w.Name].WorkloadStatus = w.CastWorkloadStatus()
		if w.IstioSidecar {
			hasSidecar = true
		}
	}

	if hasSidecar {
		// Fetch services requests rates
		rates, err := in.prom.GetAllRequestRates(namespace, rateInterval, queryTime)
		if err != nil {
			return allHealth, errors.NewServiceUnavailable(err.Error())
		}
		// Fill with collected request rates
		fillWorkloadRequestRates(allHealth, rates)
	}

	return allHealth, nil
}

// fillAppRequestRates aggregates requests rates from metrics fetched from Prometheus, and stores the result in the health map.
func fillAppRequestRates(allHealth models.NamespaceAppHealth, rates model.Vector) {
	lblDest := model.LabelName("destination_canonical_service")
	lblSrc := model.LabelName("source_canonical_service")

	for _, sample := range rates {
		name := string(sample.Metric[lblDest])
		if health, ok := allHealth[name]; ok {
			health.Requests.AggregateInbound(sample)
		}
		name = string(sample.Metric[lblSrc])
		if health, ok := allHealth[name]; ok {
			health.Requests.AggregateOutbound(sample)
		}
	}
	for _, health := range allHealth {
		health.Requests.CombineReporters()
	}
}

// fillWorkloadRequestRates aggregates requests rates from metrics fetched from Prometheus, and stores the result in the health map.
func fillWorkloadRequestRates(allHealth models.NamespaceWorkloadHealth, rates model.Vector) {
	lblDest := model.LabelName("destination_workload")
	lblSrc := model.LabelName("source_workload")
	for _, sample := range rates {
		name := string(sample.Metric[lblDest])
		if health, ok := allHealth[name]; ok {
			health.Requests.AggregateInbound(sample)
		}
		name = string(sample.Metric[lblSrc])
		if health, ok := allHealth[name]; ok {
			health.Requests.AggregateOutbound(sample)
		}
	}
	for _, health := range allHealth {
		health.Requests.CombineReporters()
	}
}

func (in *healthService) getServiceRequestsHealth(ctx context.Context, namespace, service, rateInterval string, queryTime time.Time) (models.RequestHealth, error) {
	rqHealth := models.NewEmptyRequestHealth()
	svc, err := in.businessLayer.Svc.GetService(ctx, namespace, service)
	if err != nil {
		return rqHealth, err
	}
	if svc.Type == "External" {
		// ServiceEntry from Istio Registry
		// Telemetry doesn't collect a namespace
		namespace = "unknown"
	}
	inbound, err := in.prom.GetServiceRequestRates(namespace, service, rateInterval, queryTime)
	if err != nil {
		return rqHealth, errors.NewServiceUnavailable(err.Error())
	}
	for _, sample := range inbound {
		rqHealth.AggregateInbound(sample)
	}
	rqHealth.HealthAnnotations = svc.HealthAnnotations
	rqHealth.CombineReporters()
	return rqHealth, nil
}

func (in *healthService) getAppRequestsHealth(namespace, app, rateInterval string, queryTime time.Time) (models.RequestHealth, error) {
	rqHealth := models.NewEmptyRequestHealth()

	inbound, outbound, err := in.prom.GetAppRequestRates(namespace, app, rateInterval, queryTime)
	if err != nil {
		return rqHealth, errors.NewServiceUnavailable(err.Error())
	}
	for _, sample := range inbound {
		rqHealth.AggregateInbound(sample)
	}
	for _, sample := range outbound {
		rqHealth.AggregateOutbound(sample)
	}
	rqHealth.CombineReporters()
	return rqHealth, nil
}

func (in *healthService) getWorkloadRequestsHealth(ctx context.Context, namespace, workload, rateInterval string, queryTime time.Time) (models.RequestHealth, error) {
	rqHealth := models.NewEmptyRequestHealth()
	inbound, outbound, err := in.prom.GetWorkloadRequestRates(namespace, workload, rateInterval, queryTime)
	if err != nil {
		return rqHealth, err
	}
	for _, sample := range inbound {
		rqHealth.AggregateInbound(sample)
	}
	for _, sample := range outbound {
		rqHealth.AggregateOutbound(sample)
	}
	w, err := in.businessLayer.Workload.GetWorkload(ctx, namespace, workload, "", false)
	if err != nil {
		return rqHealth, err
	}
	if len(w.Pods) > 0 {
		rqHealth.HealthAnnotations = models.GetHealthAnnotation(w.HealthAnnotations, HealthAnnotation)
	}
	rqHealth.CombineReporters()
	return rqHealth, err
}

type healthServiceWithTracing struct {
	HealthService
}

func (in *healthServiceWithTracing) GetServiceHealth(ctx context.Context, namespace, service, rateInterval string, queryTime time.Time) (models.ServiceHealth, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetServiceHealth",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("service", service),
				attribute.String("rateInterval", rateInterval),
				attribute.Stringer("queryTime", queryTime),
			),
		)
		defer span.End()
	}

	return in.HealthService.GetServiceHealth(ctx, namespace, service, rateInterval, queryTime)
}

func (in *healthServiceWithTracing) GetAppHealth(ctx context.Context, namespace, app, rateInterval string, queryTime time.Time) (models.AppHealth, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetAppHealth",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("app", app),
				attribute.String("rateInterval", rateInterval),
				attribute.Stringer("queryTime", queryTime),
			),
		)
		defer span.End()
	}

	return in.HealthService.GetAppHealth(ctx, namespace, app, rateInterval, queryTime)
}

func (in *healthServiceWithTracing) GetWorkloadHealth(ctx context.Context, namespace, workload, workloadType, rateInterval string, queryTime time.Time) (models.WorkloadHealth, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetWorkloadHealth",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("workload", workload),
				attribute.String("workloadType", workloadType),
				attribute.String("rateInterval", rateInterval),
				attribute.Stringer("queryTime", queryTime),
			),
		)
		defer span.End()
	}

	return in.HealthService.GetWorkloadHealth(ctx, namespace, workload, workloadType, rateInterval, queryTime)
}

func (in *healthServiceWithTracing) GetNamespaceServiceHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceServiceHealth, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetNamespaceServiceHealth",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("rateInterval", rateInterval),
				attribute.Stringer("queryTime", queryTime),
			),
		)
		defer span.End()
	}

	return in.HealthService.GetNamespaceServiceHealth(ctx, namespace, rateInterval, queryTime)
}
func (in *healthServiceWithTracing) GetNamespaceWorkloadHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceWorkloadHealth, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetNamespaceWorkloadHealth",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("rateInterval", rateInterval),
				attribute.Stringer("queryTime", queryTime),
			),
		)
		defer span.End()
	}

	return in.HealthService.GetNamespaceWorkloadHealth(ctx, namespace, rateInterval, queryTime)
}

func (in *healthServiceWithTracing) GetNamespaceAppHealth(ctx context.Context, namespace, rateInterval string, queryTime time.Time) (models.NamespaceAppHealth, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetNamespaceAppHealth",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
				attribute.String("rateInterval", rateInterval),
				attribute.Stringer("queryTime", queryTime),
			),
		)
		defer span.End()
	}

	return in.HealthService.GetNamespaceAppHealth(ctx, namespace, rateInterval, queryTime)
}
