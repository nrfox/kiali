package cache

import (
	"time"

	istiofake "istio.io/client-go/pkg/clientset/versioned/fake"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/kiali/kiali/config"
)

// NewFakeKialiCache creates a new fake KialiCache.
// For testing purposes only!
// TODO: Should probably go in a separate package like kubetest.
func NewFakeKialiCache(kubeObjects []runtime.Object, istioObjects []runtime.Object) *KialiCache {
	// This is needed so that the registries don't refresh since mocking out the registry will require
	// more refactoring.
	oneHourFromNow := time.Now().Add(time.Hour)
	kialiCache := &KialiCache{
		k8sApi:                kubefake.NewSimpleClientset(kubeObjects...),
		istioApi:              istiofake.NewSimpleClientset(istioObjects...),
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
	return kialiCache
}
