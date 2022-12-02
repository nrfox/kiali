package cache

import (
	"time"

	istio "istio.io/client-go/pkg/clientset/versioned"
	// "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"

	"github.com/kiali/kiali/config"
	// "github.com/kiali/kiali/kubernetes/kubetest"
)

// NewFakeKialiCache creates a new fake KialiCache.
// For testing purposes only!
// TODO: Should probably go in a separate package like kubetest.
func NewFakeKialiCache(kubeClientset kubernetes.Interface, istioClientset istio.Interface) *KialiCache {
	// This is needed so that the registries don't refresh since mocking out the registry will require
	// more refactoring.
	oneHourFromNow := time.Now().Add(time.Hour)
	kialiCache := &KialiCache{
		k8sApi:                kubeClientset,
		istioApi:              istioClientset,
		clusterScoped:         config.Get().KubernetesConfig.CacheClusterScoped,
		stopClusterScopedChan: make(chan struct{}),
		stopNSChans:           make(map[string]chan struct{}),
		nsCacheLister:         make(map[string]*cacheLister),
		clusterCacheLister:    &cacheLister{},
		// TODO: Not sure if we should pass in opts here or what but we don't want to trigger the registry or proxy cache.
		proxyStatusCreated:    &oneHourFromNow,
		registryStatusCreated: &oneHourFromNow,
	}
	// Ensures that the registry won't get refreshed.
	doNothingHandler := func() {}
	kialiCache.registryRefreshHandler = NewRegistryHandler(doNothingHandler)
	// TODO: This isn't great
	if kubeClientset != nil && istioClientset != nil {
		kialiCache.Refresh("")
	}
	return kialiCache
}

// func NewCachingClientWithFakes(objects ...runtime.Object) (*CachingClient, *KialiCache) {
// 	k8s := kubetest.NewFakeK8sClient(objects...)
// 	cache := NewFakeKialiCache(k8s.KubeClientset, k8s.IstioClientset)
// 	cache.Refresh("")
// 	cache.

// 	return NewCachingClient(cache, k8s), cache
// }
