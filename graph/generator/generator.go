package generator

import (
	"context"
	"crypto/md5"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	kubeerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/kiali/kiali/graph"
	"github.com/kiali/kiali/graph/config/cytoscape"
	"github.com/kiali/kiali/log"
)

const (
	// Dense creates a graph with many nodes.
	Dense = "dense"
	// Sparse creates a graph with few nodes.
	Sparse = "sparse"

	maxWorkloadVersions = 3
)

type app struct {
	Box       string
	Name      string
	Namespace string
	IsIngress bool
}

// Generator creates cytoscape graph data based on the options provided.
// It is used for testing a variety of graph layouts, large dense graphs in particular,
// without needing to deploy the actual resources. It is not intended to be used for
// anything other than testing.
type Generator struct {
	// Cluster is the name of the cluster all nodes will live in.
	Cluster string

	// IncludeBoxing determines whether nodes will include boxing or not.
	IncludeBoxing bool

	// NumberOfApps sets how many apps to create.
	NumberOfApps int

	// NumberOfIngress sets how many ingress to create.
	NumberOfIngress int

	// PopulationStrategy determines how many connections from ingress i.e. dense or sparse.
	PopulationStrategy string

	kubeClient      kubernetes.Interface
	namespaceLister corev1listers.NamespaceLister
}

// New create a new Generator. Options can be nil.
func New(opts Options) (*Generator, error) {
	g := Generator{
		Cluster:            "test",
		IncludeBoxing:      true,
		NumberOfApps:       10,
		NumberOfIngress:    1,
		PopulationStrategy: Dense,
	}

	// Kube specific options
	if opts.KubeClient != nil {
		g.kubeClient = opts.KubeClient

		kubeInformerFactory := kubeinformers.NewSharedInformerFactory(g.kubeClient, time.Second*30)
		g.namespaceLister = kubeInformerFactory.Core().V1().Namespaces().Lister()
		namespacesSynced := kubeInformerFactory.Core().V1().Namespaces().Informer().HasSynced

		stopCh := make(<-chan struct{})
		kubeInformerFactory.Start(stopCh)

		if ok := cache.WaitForCacheSync(stopCh, namespacesSynced); !ok {
			log.Fatalf("Failed waiting for caches to sync")
		}
	}

	if opts.Cluster != nil {
		g.Cluster = *opts.Cluster
	}
	if opts.IncludeBoxing != nil {
		g.IncludeBoxing = *opts.IncludeBoxing
	}
	if opts.NumberOfApps != nil {
		g.NumberOfApps = *opts.NumberOfApps
	}
	if opts.NumberOfIngress != nil {
		g.NumberOfIngress = *opts.NumberOfIngress
	}
	if opts.PopulationStrategy != nil {
		g.PopulationStrategy = *opts.PopulationStrategy
	}

	return &g, nil
}

// EnsureNamespaces makes sure a kube namespace exists for the nodes. 
// The namespaces need to actually exist in order for the UI to render the graph.
// Does nothing if a kubeclient is not configured.
func (g *Generator) EnsureNamespaces(cyGraph cytoscape.Config) error {
	if g.kubeClient != nil {
		log.Info("Ensuring namespaces exist for graph...")
		for _, node := range cyGraph.Elements.Nodes {
			if err := g.ensureNamespace(node.Data.Namespace); err != nil {
				return err
			}
		}
	}
	return nil
}

// Generate produces cytoscape data that can be used by the UI.
func (g *Generator) Generate() cytoscape.Config {
	nodes, edges := g.generate()

	elements := cytoscape.Elements{}
	for i := range nodes {
		wrapper := &cytoscape.NodeWrapper{Data: &nodes[i]}
		elements.Nodes = append(elements.Nodes, wrapper)
	}
	for i := range edges {
		wrapper := &cytoscape.EdgeWrapper{Data: &edges[i]}
		elements.Edges = append(elements.Edges, wrapper)
	}

	// Hard coding some of these for now. In the future, the generator can
	// support multiple graph types.
	cyGraph := cytoscape.Config{
		Elements:  elements,
		Timestamp: time.Now().Unix(),
		Duration:  int64(15),
		GraphType: graph.GraphTypeVersionedApp,
	}
	
	if err := g.EnsureNamespaces(cyGraph); err != nil {
		log.Errorf("unable to ensure namespaces exist. Err: %s", err)
	}

	return cyGraph
}

func (g *Generator) UpdateGraph(cyGraph cytoscape.Config) cytoscape.Config {
	return cytoscape.Config{
		Elements: cyGraph.Elements,
		Timestamp: time.Now().Unix(),
		Duration: int64(15),
		GraphType: graph.GraphTypeVersionedApp,
	}	
}

func (g *Generator) strategyLimit() int {
	switch g.PopulationStrategy {
	case Dense:
		return g.NumberOfApps
	case Sparse:
		return g.NumberOfApps / 2
	}

	return -1
}

func (g *Generator) edgeID() string {
	return id()
}

func (g *Generator) nodeID() string {
	return id()
}

// generate produces the nodes/edges that make up the graph. This assumes that
// 1. Workloads send requests to services.
// 2. Services send requests to the workloads in their app.
// 3. Ingress workloads are root nodes.
func (g *Generator) genAppsWithIngress(index int, numApps int) ([]cytoscape.NodeData, []cytoscape.EdgeData) {
	var nodes []cytoscape.NodeData
	var edges []cytoscape.EdgeData

	// Create ingress workload first.
	ingress := app{
		Name:      fmt.Sprintf("istio-ingressgateway-%d", index),
		Namespace: "istio-system",
		IsIngress: true,
	}
	iNodes := []cytoscape.NodeData{g.genWorkload(ingress, "latest")}

	// Then create the rest of them.
	for i := 1; i <= numApps; i++ {
		app := app{
			Name: fmt.Sprintf("app-%d", i),
			// Creates at most a namespace per app.
			// Multiple apps can land in the same namespace.
			// TODO: Provide option to control this.
			Namespace: getRandomNamespace(1, g.NumberOfApps),
		}
		appNodes, appEdges := g.genApp(app)
		nodes = append(nodes, appNodes...)
		edges = append(edges, appEdges...)
	}

	// Add edges from the ingress workload to each of the app's service node.
	// This simulates traffic coming in from ingress and going out to each of
	// the service nodes in the graph.
	iWorkloads := filterByApp(iNodes)
	svcs := filterByService(nodes)

	for _, wk := range iWorkloads {
		for i := 0; i < g.strategyLimit() && i < len(svcs); i++ {
			svc := svcs[i]
			edge := cytoscape.EdgeData{
				ID:     g.edgeID(),
				Source: wk.ID,
				Target: svc.ID,
				Traffic: cytoscape.ProtocolTraffic{
					Protocol: "http",
					Rates: map[string]string{
						"http":           "1.00",
						"httpPercentReq": "100.0",
					},
					Responses: cytoscape.Responses{
						"200": &cytoscape.ResponseDetail{
							Flags: cytoscape.ResponseFlags{"-": "100.0"},
							Hosts: cytoscape.ResponseHosts{
								svc.App: "100.0",
							},
						},
					},
				},
			}
			edges = append(edges, edge)
		}
	}

	nodes = append(nodes, iNodes...)

	return nodes, edges
}

func (g *Generator) generate() ([]cytoscape.NodeData, []cytoscape.EdgeData) {
	rand.Seed(time.Now().UnixNano())

	var nodes []cytoscape.NodeData
	var edges []cytoscape.EdgeData

	// TODO: Random connections, port number variable, instructions page for the proxy, handle some URL options changing along with namespace boxing.
	appsPerIngress := g.NumberOfApps / g.NumberOfIngress
	for i := 0; i < g.NumberOfIngress; i++ {
		n, e := g.genAppsWithIngress(i, appsPerIngress)
		nodes = append(nodes, n...)
		edges = append(edges, e...)
	}
	// TODO: Random connections to other services

	return nodes, edges
}

// genApp creates the nodes/edges for an app.
// An app is a service node + some number of workload nodes for each version.
// The number of workload nodes is determined randomly.
func (g *Generator) genApp(app app) ([]cytoscape.NodeData, []cytoscape.EdgeData) {
	var nodes []cytoscape.NodeData
	var edges []cytoscape.EdgeData

	if g.IncludeBoxing {
		namespaceBox := cytoscape.NodeData{
			ID:        g.nodeID(),
			IsBox:     "namespace",
			Cluster:   g.Cluster,
			Namespace: app.Namespace,
			NodeType:  graph.NodeTypeBox,
		}
		appBox := cytoscape.NodeData{
			ID:        g.nodeID(),
			IsBox:     "app",
			App:       app.Name,
			Cluster:   g.Cluster,
			Namespace: app.Namespace,
			NodeType:  graph.NodeTypeBox,
			Parent:    namespaceBox.ID,
		}
		nodes = append(nodes, namespaceBox, appBox)
		app.Box = appBox.ID
	}

	svc := g.genSVC(app)
	nodes = append(nodes, svc)

	// Determine how many workload versions there will be.
	numVersions := rand.Intn(maxWorkloadVersions) + 1 // Start at v1 instead of 0
	for i := 1; i <= numVersions; i++ {
		workload := g.genWorkload(app, fmt.Sprintf("v%d", i))
		nodes = append(nodes, workload)

		// Create an edge going from the app's svc to its workload.
		edge := cytoscape.EdgeData{
			ID:     g.edgeID(),
			Source: svc.ID,
			Target: workload.ID,
			// TODO: Randomzied traffic data
			Traffic: cytoscape.ProtocolTraffic{
				Protocol: "http",
				Rates: map[string]string{
					"http":           "1.00",
					"httpPercentReq": "50.0",
				},
				Responses: cytoscape.Responses{
					"200": &cytoscape.ResponseDetail{
						Flags: cytoscape.ResponseFlags{"-": "100.0"},
						Hosts: cytoscape.ResponseHosts{
							svc.App: "100.0",
						},
					},
				},
			},
		}
		edges = append(edges, edge)
	}

	return nodes, edges
}

func (g *Generator) genSVC(app app) cytoscape.NodeData {
	node := cytoscape.NodeData{
		ID:        g.nodeID(),
		NodeType:  graph.NodeTypeService,
		Cluster:   g.Cluster,
		Namespace: app.Namespace,
		App:       app.Name,
		Service:   app.Name,
		Traffic: []cytoscape.ProtocolTraffic{{
			Protocol: "http",
			Rates: map[string]string{
				"httpIn": "1.00",
			},
		}},
	}

	if app.Box != "" {
		node.Parent = app.Box
	}

	return node
}

func (g *Generator) genWorkload(app app, version string) cytoscape.NodeData {
	node := cytoscape.NodeData{
		ID:        g.nodeID(),
		NodeType:  graph.NodeTypeApp,
		Cluster:   g.Cluster,
		Namespace: app.Namespace,
		App:       app.Name,
		Version:   version,
		Workload:  app.Name + "-" + version,
	}

	if app.IsIngress {
		node.IsRoot = true
		node.IsGateway = &cytoscape.GWInfo{IngressInfo: cytoscape.GWInfoIngress{Hostnames: []string{"*"}}}
		node.IsOutside = true
	}

	if app.Box != "" {
		node.Parent = app.Box
	}

	return node
}

func (g *Generator) ensureNamespace(name string) error {
	if _, err := g.namespaceLister.Get(name); err != nil {
		if kubeerrors.IsNotFound(err) {
			log.Infof("Namespace: '%s' does not exist. Creating...", name)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
			_, err = g.kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
			if err != nil && !kubeerrors.IsAlreadyExists(err) {
				return err
			}
		} else {
			return err
		}
	}

	return nil
}

// This needs to be a hash for cytoscape to render the edges.
func id() string {
	// This is probably a big enough number to not worry about a lot of collisions.
	return fmt.Sprintf("%x", md5.Sum([]byte(strconv.Itoa(rand.Int()))))
}

func filterByApp(nodes []cytoscape.NodeData) []cytoscape.NodeData {
	var workloads []cytoscape.NodeData
	for _, n := range nodes {
		if n.NodeType == graph.NodeTypeApp {
			workloads = append(workloads, n)
		}
	}
	return workloads
}

func filterByService(nodes []cytoscape.NodeData) []cytoscape.NodeData {
	var services []cytoscape.NodeData
	for _, n := range nodes {
		if n.NodeType == graph.NodeTypeService {
			services = append(services, n)
		}
	}
	return services
}

func generateNamespaceName(numNamespace int) string {
	return fmt.Sprintf("n%d", numNamespace)
}

func getRandomNamespace(from, to int) string {
	numNamespace := from + rand.Intn(to)
	return generateNamespaceName(numNamespace)
}
