package api

import (
	"fmt"
	"net/http"

	"github.com/kiali/kiali/business"
	"github.com/kiali/kiali/graph"
	"github.com/kiali/kiali/graph/config/cytoscape"
	"github.com/kiali/kiali/graph/generator"
	"github.com/kiali/kiali/graph/telemetry/istio"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/prometheus"
	"github.com/kiali/kiali/prometheus/internalmetrics"
)

// GraphNamespaces generates a namespaces graph using the provided options
func GraphNamespaces(business *business.Layer, o graph.Options) (code int, config interface{}) {
	// time how long it takes to generate this graph
	promtimer := internalmetrics.GetGraphGenerationTimePrometheusTimer(o.GetGraphKind(), o.TelemetryOptions.GraphType, o.InjectServiceNodes)
	defer promtimer.ObserveDuration()

	switch o.TelemetryVendor {
	case graph.VendorIstio:
		prom, err := prometheus.NewClient()
		graph.CheckError(err)
		code, config = graphNamespacesIstio(business, prom, o)
	default:
		graph.Error(fmt.Sprintf("TelemetryVendor [%s] not supported", o.TelemetryVendor))
	}

	// update metrics
	internalmetrics.SetGraphNodes(o.GetGraphKind(), o.TelemetryOptions.GraphType, o.InjectServiceNodes, 0)

	return code, config
}

// graphNamespacesIstio provides a test hook that accepts mock clients
func graphNamespacesIstio(business *business.Layer, prom *prometheus.Client, o graph.Options) (code int, config interface{}) {

	// Create a 'global' object to store the business. Global only to the request.
	globalInfo := graph.NewAppenderGlobalInfo()
	globalInfo.Business = business

	trafficMap := istio.BuildNamespacesTrafficMap(o.TelemetryOptions, prom, globalInfo)
	code, config = generateGraph(trafficMap, o)

	return code, config
}

// GraphNode generates a node graph using the provided options
func GraphNode(business *business.Layer, o graph.Options) (code int, config interface{}) {
	if len(o.Namespaces) != 1 {
		graph.Error(fmt.Sprintf("Node graph does not support the 'namespaces' query parameter or the 'all' namespace"))
	}

	// time how long it takes to generate this graph
	promtimer := internalmetrics.GetGraphGenerationTimePrometheusTimer(o.GetGraphKind(), o.TelemetryOptions.GraphType, o.InjectServiceNodes)
	defer promtimer.ObserveDuration()

	switch o.TelemetryVendor {
	case graph.VendorIstio:
		prom, err := prometheus.NewClient()
		graph.CheckError(err)
		code, config = graphNodeIstio(business, prom, o)
	default:
		graph.Error(fmt.Sprintf("TelemetryVendor [%s] not supported", o.TelemetryVendor))
	}
	// update metrics
	internalmetrics.SetGraphNodes(o.GetGraphKind(), o.TelemetryOptions.GraphType, o.InjectServiceNodes, 0)

	return code, config
}

// graphNodeIstio provides a test hook that accepts mock clients
func graphNodeIstio(business *business.Layer, client *prometheus.Client, o graph.Options) (code int, config interface{}) {

	// Create a 'global' object to store the business. Global only to the request.
	globalInfo := graph.NewAppenderGlobalInfo()
	globalInfo.Business = business

	trafficMap := istio.BuildNodeTrafficMap(o.TelemetryOptions, client, globalInfo)
	code, config = generateGraph(trafficMap, o)

	return code, config
}

func generateGraph(trafficMap graph.TrafficMap, o graph.Options) (int, interface{}) {
	log.Tracef("Generating config for [%s] graph...", o.ConfigVendor)

	promtimer := internalmetrics.GetGraphMarshalTimePrometheusTimer(o.GetGraphKind(), o.TelemetryOptions.GraphType, o.InjectServiceNodes)
	defer promtimer.ObserveDuration()

	var vendorConfig interface{}
	switch o.ConfigVendor {
	case graph.VendorCytoscape:
		vendorConfig = cytoscape.NewConfig(trafficMap, o.ConfigOptions)
	case "cytoscape-generator":
		g := generator.Generator{
			Cluster:   o.Cluster,
			NumberOfApps: 10,
			PopulationStrategy: generator.Dense,
			IncludeBoxing: true,
		}
		vendorConfig = g.Generate()
	default:
		graph.Error(fmt.Sprintf("ConfigVendor [%s] not supported", o.ConfigVendor))
	}

	log.Tracef("Done generating config for [%s] graph", o.ConfigVendor)
	return http.StatusOK, vendorConfig
}
