package kubernetes

import (
	"context"

	istio "istio.io/client-go/pkg/clientset/versioned"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	kube "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	gatewayapiclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	kialiconfig "github.com/kiali/kiali/config"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/util/httputil"
)

// RemoteSecretData is used to identify the remote cluster Kiali will connect to as its "local cluster".
// This is to support installing Kiali in the control plane, but observing only the data plane in the remote cluster.
// Experimental feature. See: https://github.com/kiali/kiali/issues/3002
const RemoteSecretData = "/kiali-remote-secret/kiali"

var (
	emptyGetOptions  = meta_v1.GetOptions{}
	emptyListOptions = meta_v1.ListOptions{}
)

type PodLogs struct {
	Logs string `json:"logs,omitempty"`
}

// ClusterInfo is basically a rest.Config with a few extra fields that are useful to Kiali.
type ClusterInfo struct {
	// ClientConfig is the rest.Config is used to create clients for the various APIs.
	ClientConfig *rest.Config

	// Name is the name of the cluster this client is connected to.
	Name string

	// SecretName is the name of the secret that contains the credentials for this cluster.
	SecretName string
}

// ClientInterface for mocks (only mocked function are necessary here)
type ClientInterface interface {
	GetServerVersion() (*version.Info, error)
	GetToken() string
	IsOpenShift() bool
	IsGatewayAPI() bool
	IsIstioAPI() bool
	// ClusterInfo returns some information about the cluster this client is connected to.
	// This gets set when the client is first created.
	ClusterInfo() ClusterInfo
	K8SClientInterface
	IstioClientInterface
	OSClientInterface
}

// K8SClient is the client struct for Kubernetes and Istio APIs
// It hides the way it queries each API
type K8SClient struct {
	token          string
	k8s            kube.Interface
	istioClientset istio.Interface
	// Used for portforwarding requests.
	restConfig *rest.Config
	// Used in REST queries after bump to client-go v0.20.x
	ctx context.Context
	// isOpenShift private variable will check if kiali is deployed under an OpenShift cluster or not
	// It is represented as a pointer to include the initialization phase.
	// See kubernetes_service.go#IsOpenShift() for more details.
	isOpenShift *bool
	// isGatewayAPI private variable will check if K8s Gateway API CRD exists on cluster or not
	isGatewayAPI *bool
	gatewayapi   gatewayapiclient.Interface
	isIstioAPI   *bool
	clusterInfo  ClusterInfo

	// Separated out for testing purposes
	getPodPortForwarderFunc func(namespace, name, portMap string) (httputil.PortForwarder, error)
}

// Ensure the K8SClient implements the ClientInterface
var _ ClientInterface = &K8SClient{}

// GetToken returns the BearerToken used from the config
func (client *K8SClient) GetToken() string {
	return client.token
}

func getConfig(clusterInfo *RemoteClusterInfo) (*rest.Config, error) {
	// TODO: QPS and Burst
	if clusterInfo != nil {
		// clientcmd.R
		return clusterInfo.Config.ClientConfig()
	}

	// If there's no remote cluster info then it must be in cluster.
	return rest.InClusterConfig()
}

// GetConfigForRemoteClusterInfo points the returned k8s client config to a remote cluster's API server.
// The returned config will have the user's token and ExecProvider associated with it.
// If both are set, the bearer token takes precedence.
func GetConfigForRemoteClusterInfo(cluster *RemoteClusterInfo) (*rest.Config, error) {
	return getConfig(cluster)
}

func (client *K8SClient) ClusterInfo() ClusterInfo {
	return client.clusterInfo
}

// GetConfigForLocalCluster return a client with the correct configuration
// Returns configuration if Kiali is in Cluster when InCluster is true
// Returns configuration if Kiali is not in Cluster when InCluster is false
// It returns an error on any problem
// func GetConfigForLocalCluster() (*rest.Config, error) {
// 	c := kialiConfig.Get()

// 	// this is mainly for testing/running Kiali outside of the cluster
// 	if !c.InCluster {
// 		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
// 		if len(host) == 0 || len(port) == 0 {
// 			return nil, fmt.Errorf("unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined")
// 		}

// 		return &rest.Config{
// 			// TODO: switch to using cluster DNS.
// 			Host:  "http://" + net.JoinHostPort(host, port),
// 			QPS:   c.KubernetesConfig.QPS,
// 			Burst: c.KubernetesConfig.Burst,
// 		}, nil
// 	}
// 	clientcmd.BuildConfigFromFlags(masterUrl string, kubeconfigPath string)

// 	if c.InCluster {
// 		var incluster *rest.Config
// 		var err error
// 		if remoteSecret, readErr := GetRemoteSecret(RemoteSecretData); readErr == nil {
// 			for _, cluster := range remoteSecret.Clusters {
// 				// TODO: Assume there's only one
// 				incluster, err = GetConfigForRemoteCluster(*cluster)
// 				break
// 			}
// 		} else {
// 			incluster, err = rest.InClusterConfig()
// 			if err != nil {
// 				log.Errorf("Error '%v' getting config for local cluster", err.Error())
// 				return nil, err
// 			}
// 			incluster.QPS = c.KubernetesConfig.QPS
// 			incluster.Burst = c.KubernetesConfig.Burst
// 		}
// 		if err != nil {
// 			return nil, err
// 		}
// 		return incluster, nil
// 	}

// }

func NewClientWithRemoteClusterInfo(config *rest.Config, remoteClusterInfo *RemoteClusterInfo) (*K8SClient, error) {
	client, err := NewClientFromConfig(config)
	if err != nil {
		return nil, err
	}

	// TODO: Figure out structure of this. Where should we get cluster name from? The kube cluster config is a map.
	if remoteClusterInfo != nil {
		cfg, err := remoteClusterInfo.Config.RawConfig()
		if err != nil {
			return nil, err
		}

		// TODO: Just get the first
		var clusterName string
		for cluster := range cfg.Clusters {
			clusterName = cluster
			break
		}
		client.clusterInfo = ClusterInfo{
			Name:       clusterName,
			SecretName: remoteClusterInfo.SecretName,
		}
	} else {
		client.clusterInfo = ClusterInfo{
			Name: kialiconfig.Get().KubernetesConfig.ClusterName,
		}
	}
	// Copy config
	clientConfig := *config
	client.clusterInfo.ClientConfig = &clientConfig

	return client, nil
}

// NewClientFromConfig creates a new client to the Kubernetes and Istio APIs.
// It takes the assumption that Istio is deployed into the cluster.
// It hides the access to Kubernetes/Openshift credentials.
// It hides the low level use of the API of Kubernetes and Istio, it should be considered as an implementation detail.
// It returns an error on any problem.
func NewClientFromConfig(config *rest.Config) (*K8SClient, error) {
	client := K8SClient{
		token: config.BearerToken,
	}

	log.Debugf("Rest perf config QPS: %f Burst: %d", config.QPS, config.Burst)

	k8s, err := kube.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	client.k8s = k8s
	client.restConfig = config

	client.istioClientset, err = istio.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	client.gatewayapi, err = gatewayapiclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	client.ctx = context.Background()

	return &client, nil
}

// NewClient is just used for testing purposes.
func NewClient(kubeClient kube.Interface, istioClient istio.Interface, gatewayapiClient gatewayapiclient.Interface) *K8SClient {
	return &K8SClient{
		istioClientset: istioClient,
		k8s:            kubeClient,
		gatewayapi:     gatewayapiClient,
	}
}
