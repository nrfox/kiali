package cache

import (
	apps_v1 "k8s.io/api/apps/v1"
	batch_v1 "k8s.io/api/batch/v1"
	core_v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kiali/kiali/kubernetes"
)

// CachingClient is a wrapper around a ClientInterface that adds caching.
// If the object is cached then it is read from the cache. Not all object
// types are cached. When an object is created, updated, or deleted, then
// the cache is refreshed and updated.
type CachingClient struct {
	cache *KialiCache
	kubernetes.ClientInterface
}

// NewCachingClient creates a new CachingClient out of a cache and a client.
func NewCachingClient(cache *KialiCache, client kubernetes.ClientInterface) *CachingClient {
	return &CachingClient{
		cache:           cache,
		ClientInterface: client,
	}
}

func (cc *CachingClient) GetConfigMap(namespace, name string) (*core_v1.ConfigMap, error) {
	return cc.cache.GetConfigMap(namespace, name)
}

func (cc *CachingClient) GetCronJobs(namespace string) ([]batch_v1.CronJob, error) {
	// TODO: Should we cache cronjobs? Need a separate lister.
	// return cc.cache.GetCronJobs(namespace)
	return cc.ClientInterface.GetCronJobs(namespace)
}

func (cc *CachingClient) GetDaemonSet(namespace string, name string) (*apps_v1.DaemonSet, error) {
	return cc.cache.GetDaemonSet(namespace, name)
}

// func (cc *CachingClient) GetClusterServicesByLabels(labelsSelector string) ([]core_v1.Service, error) {
// 	return cc.cache.GetClusterServicesByLabels(labelsSelector)
// }
func (cc *CachingClient) GetDaemonSets(namespace string) ([]apps_v1.DaemonSet, error) {
	return cc.cache.GetDaemonSets(namespace)
}

func (cc *CachingClient) GetDeployment(namespace string, name string) (*apps_v1.Deployment, error) {
	return cc.cache.GetDeployment(namespace, name)
}

func (cc *CachingClient) GetDeployments(namespace string) ([]apps_v1.Deployment, error) {
	return cc.cache.GetDeployments(namespace)
}

// TODO: Should we cache this?
// func (cc *CachingClient) GetDeploymentConfig(namespace string, name string) (*osapps_v1.DeploymentConfig, error) {
// 	return cc.cache.GetDeploymentConfig(namespace, name)
// }
// TODO:
// func (cc *CachingClient) GetDeploymentConfigs(namespace string) ([]osapps_v1.DeploymentConfig, error) {
// 	return cc.cache.GetDeploymentConfigs(namespace)
// }

func (cc *CachingClient) GetEndpoints(namespace string, name string) (*core_v1.Endpoints, error) {
	return cc.cache.GetEndpoints(namespace, name)
}

// func (cc *CachingClient) GetJobs(namespace string) ([]batch_v1.Job, error) {
// 	return cc.cache.GetJobs(namespace)
// }
// func (cc *CachingClient) GetNamespace(namespace string) (*core_v1.Namespace, error) {
// 	return cc.cache.GetNamespace(namespace)
// }
// func (cc *CachingClient) GetNamespaces(labelSelector string) ([]core_v1.Namespace, error) {
// 	return cc.cache.GetNamespaces(labelSelector)
// }
// func (cc *CachingClient) GetPod(namespace, name string) (*core_v1.Pod, error) {
// 	return cc.cache.GetPod(namespace, name)
// }

func (cc *CachingClient) GetPods(namespace, labelSelector string) ([]core_v1.Pod, error) {
	return cc.cache.GetPods(namespace, labelSelector)
}

// func (cc *CachingClient) GetReplicationControllers(namespace string) ([]core_v1.ReplicationController, error) {
// 	return cc.cache.GetReplicationControllers(namespace)
// }

func (cc *CachingClient) GetReplicaSets(namespace string) ([]apps_v1.ReplicaSet, error) {
	return cc.cache.GetReplicaSets(namespace)
}

// func (cc *CachingClient) GetSecret(namespace, name string) (*core_v1.Secret, error)
// func (cc *CachingClient) GetSecrets(namespace string, labelSelector string) ([]core_v1.Secret, error)
// func (cc *CachingClient) GetSelfSubjectAccessReview(ctx context.Context, namespace, api, resourceType string, verbs []string) ([]*auth_v1.SelfSubjectAccessReview, error)
func (cc *CachingClient) GetService(namespace string, name string) (*core_v1.Service, error) {
	return cc.cache.GetService(namespace, name)
}

func (cc *CachingClient) GetServices(namespace string, selectorLabels map[string]string) ([]core_v1.Service, error) {
	return cc.cache.GetServices(namespace, selectorLabels)
}

// func (cc *CachingClient) GetServicesByLabels(namespace string, labelsSelector string) ([]core_v1.Service, error) {
// 	return cc.cache.GetServicesByLabels(namespace, labelsSelector)
// }

func (cc *CachingClient) GetStatefulSet(namespace string, name string) (*apps_v1.StatefulSet, error) {
	return cc.cache.GetStatefulSet(namespace, name)
}

func (cc *CachingClient) GetStatefulSets(namespace string) ([]apps_v1.StatefulSet, error) {
	return cc.cache.GetStatefulSets(namespace)
}

// func (cc *CachingClient) GetTokenSubject(authInfo *api.AuthInfo) (string, error)
// func (cc *CachingClient) StreamPodLogs(namespace, name string, opts *core_v1.PodLogOptions) (io.ReadCloser, error)

func (cc *CachingClient) UpdateNamespace(namespace string, jsonPatch string) (*core_v1.Namespace, error) {
	ns, err := cc.ClientInterface.UpdateNamespace(namespace, jsonPatch)
	if err != nil {
		return nil, err
	}

	// Cache is stopped after a Create/Update/Delete operation to force a refresh
	cc.cache.Refresh(namespace)
	cc.cache.RefreshTokenNamespaces()

	return ns, err
}

func (cc *CachingClient) UpdateService(namespace string, name string, jsonPatch string) error {
	if err := cc.ClientInterface.UpdateService(namespace, name, jsonPatch); err != nil {
		return err
	}

	// Cache is stopped after a Create/Update/Delete operation to force a refresh
	cc.cache.Refresh(namespace)
	return nil
}

// func (cc *CachingClient) UpdateWorkload(namespace string, name string, workloadType string, jsonPatch string) error {
// 	if err := cc.ClientInterface.UpdateWorkload(namespace, name, workloadType, jsonPatch); err != nil {
// 		return err
// 	}

// 	// Cache is stopped after a Create/Update/Delete operation to force a refresh
// 	cc.cache.Refresh(namespace)
// 	return nil
// }

func (cc *CachingClient) DeleteObject(namespace string, name string, kind string) error {
	if err := cc.ClientInterface.DeleteObject(namespace, name, kind); err != nil {
		return err
	}

	// Cache is stopped after a Create/Update/Delete operation to force a refresh
	cc.cache.Refresh(namespace)
	return nil
}

func (cc *CachingClient) PatchObject(namespace string, name string, jsonPatch []byte, object runtime.Object) error {
	if err := cc.ClientInterface.PatchObject(namespace, name, jsonPatch, object); err != nil {
		return err
	}

	// Cache is stopped after a Create/Update/Delete operation to force a refresh
	cc.cache.Refresh(namespace)
	return nil
}

func (cc *CachingClient) CreateObject(namespace string, kind string, object runtime.Object) error {
	if err := cc.ClientInterface.CreateObject(namespace, kind, object); err != nil {
		return err
	}

	// Cache is stopped after a Create/Update/Delete operation to force a refresh
	cc.cache.Refresh(namespace)
	return nil
}
