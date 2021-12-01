package business

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	security_v1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	core_v1 "k8s.io/api/core/v1"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/observability"
	"github.com/kiali/kiali/util/mtls"
)

type TLSService interface {
	MeshWidemTLSStatus(ctx context.Context, namespaces []string) (models.MTLSStatus, error)
	NamespaceWidemTLSStatus(ctx context.Context, namespace string) (models.MTLSStatus, error)
	GetAllDestinationRules(ctx context.Context, namespaces []string) ([]networking_v1alpha3.DestinationRule, error)
}

type tlsService struct {
	k8s             kubernetes.ClientInterface
	businessLayer   *Layer
	enabledAutoMtls *bool
}

const (
	MTLSEnabled          = "MTLS_ENABLED"
	MTLSPartiallyEnabled = "MTLS_PARTIALLY_ENABLED"
	MTLSNotEnabled       = "MTLS_NOT_ENABLED"
	MTLSDisabled         = "MTLS_DISABLED"
)

func (in *tlsService) MeshWidemTLSStatus(ctx context.Context, namespaces []string) (models.MTLSStatus, error) {
	pas, error := in.getMeshPeerAuthentications(ctx)
	if error != nil {
		return models.MTLSStatus{}, error
	}

	drs, error := in.GetAllDestinationRules(ctx, namespaces)
	if error != nil {
		return models.MTLSStatus{}, error
	}

	mtlsStatus := mtls.MtlsStatus{
		PeerAuthentications: pas,
		DestinationRules:    drs,
		AutoMtlsEnabled:     in.hasAutoMTLSEnabled(),
		AllowPermissive:     false,
	}

	return models.MTLSStatus{
		Status: mtlsStatus.MeshMtlsStatus().OverallStatus,
	}, nil
}

func (in *tlsService) getMeshPeerAuthentications(ctx context.Context) ([]security_v1beta1.PeerAuthentication, error) {
	criteria := IstioConfigCriteria{
		Namespace:                  config.Get().ExternalServices.Istio.RootNamespace,
		IncludePeerAuthentications: true,
	}
	istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(ctx, criteria)
	return istioConfigList.PeerAuthentications, err
}

func (in *tlsService) GetAllDestinationRules(ctx context.Context, namespaces []string) ([]networking_v1alpha3.DestinationRule, error) {
	drChan := make(chan []networking_v1alpha3.DestinationRule, len(namespaces))
	errChan := make(chan error, 1)
	wg := sync.WaitGroup{}

	wg.Add(len(namespaces))

	for _, namespace := range namespaces {
		go func(ctx context.Context, ns string) {
			defer wg.Done()
			criteria := IstioConfigCriteria{
				Namespace:               ns,
				IncludeDestinationRules: true,
			}
			istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(ctx, criteria)
			if err != nil {
				errChan <- err
				return
			}

			drChan <- istioConfigList.DestinationRules
		}(ctx, namespace)
	}

	wg.Wait()
	close(errChan)
	close(drChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	allDestinationRules := make([]networking_v1alpha3.DestinationRule, 0)
	for drs := range drChan {
		allDestinationRules = append(allDestinationRules, drs...)
	}

	return allDestinationRules, nil
}

func (in *tlsService) NamespaceWidemTLSStatus(ctx context.Context, namespace string) (models.MTLSStatus, error) {
	pas, err := in.getPeerAuthentications(ctx, namespace)
	if err != nil {
		return models.MTLSStatus{}, nil
	}

	nss, err := in.getNamespaces(ctx)
	if err != nil {
		return models.MTLSStatus{}, nil
	}

	drs, err := in.GetAllDestinationRules(ctx, nss)
	if err != nil {
		return models.MTLSStatus{}, nil
	}

	mtlsStatus := mtls.MtlsStatus{
		Namespace:           namespace,
		PeerAuthentications: pas,
		DestinationRules:    drs,
		AutoMtlsEnabled:     in.hasAutoMTLSEnabled(),
		AllowPermissive:     false,
	}

	return models.MTLSStatus{
		Status: mtlsStatus.NamespaceMtlsStatus().OverallStatus,
	}, nil
}

func (in *tlsService) getPeerAuthentications(ctx context.Context, namespace string) ([]security_v1beta1.PeerAuthentication, error) {
	if config.IsRootNamespace(namespace) {
		return []security_v1beta1.PeerAuthentication{}, nil
	}
	criteria := IstioConfigCriteria{
		Namespace:                  namespace,
		IncludePeerAuthentications: true,
	}
	istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(ctx, criteria)
	return istioConfigList.PeerAuthentications, err
}

func (in *tlsService) getNamespaces(ctx context.Context) ([]string, error) {
	nss, nssErr := in.businessLayer.Namespace.GetNamespaces()
	if nssErr != nil {
		return nil, nssErr
	}

	nsNames := make([]string, 0)
	for _, ns := range nss {
		nsNames = append(nsNames, ns.Name)
	}
	return nsNames, nil
}

func (in *tlsService) hasAutoMTLSEnabled() bool {
	if in.enabledAutoMtls != nil {
		return *in.enabledAutoMtls
	}

	cfg := config.Get()
	var istioConfig *core_v1.ConfigMap
	var err error
	if IsNamespaceCached(cfg.IstioNamespace) {
		istioConfig, err = kialiCache.GetConfigMap(cfg.IstioNamespace, cfg.ExternalServices.Istio.ConfigMapName)
	} else {
		istioConfig, err = in.k8s.GetConfigMap(cfg.IstioNamespace, cfg.ExternalServices.Istio.ConfigMapName)
	}
	if err != nil {
		return true
	}
	mc, err := kubernetes.GetIstioConfigMap(istioConfig)
	if err != nil {
		return true
	}
	autoMtls := mc.GetEnableAutoMtls()
	in.enabledAutoMtls = &autoMtls
	return autoMtls
}

type tlsServiceWithTracing struct {
	TLSService
}

func (in *tlsServiceWithTracing) MeshWidemTLSStatus(ctx context.Context, namespaces []string) (models.MTLSStatus, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "MeshWidemTLSStatus",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.StringSlice("namespaces", namespaces),
			),
		)
		defer span.End()
	}

	return in.TLSService.MeshWidemTLSStatus(ctx, namespaces)
}

func (in *tlsServiceWithTracing) NamespaceWidemTLSStatus(ctx context.Context, namespace string) (models.MTLSStatus, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "NamespaceWidemTLSStatus",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.String("namespace", namespace),
			),
		)
		defer span.End()
	}

	return in.TLSService.NamespaceWidemTLSStatus(ctx, namespace)
}

func (in *tlsServiceWithTracing) GetAllDestinationRules(ctx context.Context, namespaces []string) ([]networking_v1alpha3.DestinationRule, error) {
	if config.Get().Server.Observability.Tracing.Enabled {
		var span trace.Span
		ctx, span = otel.Tracer(observability.TracerName()).Start(ctx, "GetAllDestinationRules",
			trace.WithAttributes(
				attribute.String("package", "business"),
				attribute.StringSlice("namespaces", namespaces),
			),
		)
		defer span.End()
	}

	return in.TLSService.GetAllDestinationRules(ctx, namespaces)
}
