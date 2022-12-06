package business

/*
	This file contains helper methods for unit testing with the business package.
	The utilities in this file are not meant to be used outside of unit tests.
*/

import (
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/cache"
	"github.com/kiali/kiali/kubernetes/kubetest"
	"github.com/kiali/kiali/prometheus"
)

// SetWithBackends allows for specifying the ClientFactory and Prometheus clients to be used.
// Mock friendly. Used only with tests.
// TODO: Change cache back to an interface?
func SetWithBackends(cf kubernetes.ClientFactory, prom prometheus.ClientInterface, cache *cache.KialiCache) {
	clientFactory = cf
	prometheusClient = prom
	kialiCache = cache
}

// SetupBusinessLayer mocks out some global variables in the business package
// such as the kiali cache and the prometheus client.
func SetupBusinessLayer(k8s *kubetest.FakeK8sClient) {
	mockClientFactory := kubetest.NewK8SClientFactoryMock(k8s)
	cache := cache.NewFakeKialiCache(k8s.KubeClientset, k8s.IstioClientset)
	SetWithBackends(mockClientFactory, nil, cache)
}
