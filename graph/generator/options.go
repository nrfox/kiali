package generator

import (
	"k8s.io/client-go/kubernetes"
)

// Options used to configure a Generator.
type Options struct {
	// Cluster is the name of the cluster all nodes will live in.
	Cluster *string

	// IncludeBoxing determines whether nodes will include boxing or not.
	IncludeBoxing *bool

	// KubeClient if passed enables talking to the kube api to get/create namespaces.
	KubeClient kubernetes.Interface

	// NumberOfApps sets how many apps to create.
	NumberOfApps *int

	// NumberOfIngress sets how many ingress to create.
	NumberOfIngress *int

	// PopulationStrategy determines how many connections from ingress i.e. dense or sparse.
	PopulationStrategy *string
}
