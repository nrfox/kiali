package business

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	networking_v1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	security_v1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	core_v1 "k8s.io/api/core/v1"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/tracing"
	"github.com/kiali/kiali/util/mtls"
)

type TLSService struct {
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

func (in *TLSService) MeshWidemTLSStatus(namespaces []string) (models.MTLSStatus, error) {
	pas, error := in.getMeshPeerAuthentications()
	if error != nil {
		return models.MTLSStatus{}, error
	}

	drs, error := in.getAllDestinationRules(context.TODO(), namespaces)
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

func (in *TLSService) getMeshPeerAuthentications() ([]security_v1beta1.PeerAuthentication, error) {
	criteria := IstioConfigCriteria{
		Namespace:                  config.Get().IstioNamespace,
		IncludePeerAuthentications: true,
	}
	istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(criteria)
	return istioConfigList.PeerAuthentications, err
}

func (in *TLSService) getAllDestinationRules(ctx context.Context, namespaces []string) ([]networking_v1alpha3.DestinationRule, error) {
	if config.Get().Server.TracingEnabled {
		var span trace.Span
		ctx, span = otel.Tracer(tracing.TracerName).Start(ctx, "getAllDestinationRules")
		defer span.End()
	}
	drChan := make(chan []networking_v1alpha3.DestinationRule, len(namespaces))
	errChan := make(chan error, 1)
	wg := sync.WaitGroup{}

	wg.Add(len(namespaces))

	for _, namespace := range namespaces {
		go func(ns string) {
			defer wg.Done()
			criteria := IstioConfigCriteria{
				Namespace:               ns,
				IncludeDestinationRules: true,
			}
			istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(criteria)
			if err != nil {
				errChan <- err
				return
			}

			drChan <- istioConfigList.DestinationRules
		}(namespace)
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

func (in TLSService) NamespaceWidemTLSStatus(ctx context.Context, namespace string) (models.MTLSStatus, error) {
	if config.Get().Server.TracingEnabled {
		var span trace.Span
		ctx, span = otel.Tracer(tracing.TracerName).Start(ctx, "NamespaceWidemTLSStatus")
		defer span.End()
	}
	pas, err := in.getPeerAuthentications(ctx, namespace)
	if err != nil {
		return models.MTLSStatus{}, nil
	}

	nss, err := in.getNamespaces(ctx)
	if err != nil {
		return models.MTLSStatus{}, nil
	}

	drs, err := in.getAllDestinationRules(ctx, nss)
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

func (in TLSService) getPeerAuthentications(ctx context.Context, namespace string) ([]security_v1beta1.PeerAuthentication, error) {
	if config.Get().Server.TracingEnabled {
		var span trace.Span
		ctx, span = otel.Tracer(tracing.TracerName).Start(ctx, "getPeerAuthentications")
		defer span.End()
	}
	if namespace == config.Get().IstioNamespace {
		return []security_v1beta1.PeerAuthentication{}, nil
	}
	criteria := IstioConfigCriteria{
		Namespace:                  namespace,
		IncludePeerAuthentications: true,
	}
	istioConfigList, err := in.businessLayer.IstioConfig.GetIstioConfigList(criteria)
	return istioConfigList.PeerAuthentications, err
}

func (in TLSService) getNamespaces(ctx context.Context) ([]string, error) {
	if config.Get().Server.TracingEnabled {
		var span trace.Span
		ctx, span = otel.Tracer(tracing.TracerName).Start(ctx, "getNamespaces")
		defer span.End()
	}
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

func (in *TLSService) hasAutoMTLSEnabled() bool {
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
