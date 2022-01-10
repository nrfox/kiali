// generator is a small CLI program to generate sample graph data
// intended to be consumed by the front end. It's purpose is to
// enable testing large topologies independent of the backend.
package generator

import (
	"crypto/md5"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/kiali/kiali/graph"
	"github.com/kiali/kiali/graph/config/cytoscape"
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

	// NumberOfApps sets how many apps to create.
	NumberOfApps int

	// PopulationStrategy determines how many connections from ingress i.e. dense or sparse.
	PopulationStrategy string

	// IncludeBoxing determines whether nodes will include boxing or not.
	IncludeBoxing bool
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
	return cytoscape.Config{
		Elements:  elements,
		Timestamp: time.Now().Unix(),
		Duration:  int64(15),
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
func (g *Generator) generate() ([]cytoscape.NodeData, []cytoscape.EdgeData) {
	rand.Seed(time.Now().UnixNano())

	var nodes []cytoscape.NodeData
	var edges []cytoscape.EdgeData

	// Create ingress workload first.
	// TODO: Variable number of ingress.
	ingress := app{
		Name:      "istio-ingressgateway",
		Namespace: "istio-system",
		IsIngress: true,
	}
	iNodes := []cytoscape.NodeData{g.genWorkload(ingress, "latest")}

	// Then create the rest of them.
	for i := 1; i <= g.NumberOfApps; i++ {
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

	// TODO: Random connections to other services

	nodes = append(nodes, iNodes...)

	return nodes, edges
}

// genApp creates the nodes/edges for an app.
// An app is a service node + some number of workload nodes for each version.
// The number of workload nodes is determined randomly.
func (g *Generator) genApp(app app) ([]cytoscape.NodeData, []cytoscape.EdgeData) {
	var nodes []cytoscape.NodeData
	var edges []cytoscape.EdgeData

	if g.IncludeBoxing {
		// TODO: namespace boxing
		box := cytoscape.NodeData{
			ID:        g.nodeID(),
			IsBox:     "app",
			App:       app.Name,
			Cluster:   g.Cluster,
			Namespace: app.Namespace,
			NodeType:  graph.NodeTypeBox,
		}
		nodes = append(nodes, box)
		app.Box = box.ID
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
