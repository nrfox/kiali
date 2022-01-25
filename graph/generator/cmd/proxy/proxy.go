package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kiali/kiali/graph/config/cytoscape"
	"github.com/kiali/kiali/graph/generator"
	"github.com/kiali/kiali/log"
)

var (
	certFileFlag string
	dataDirFlag  string
	httpsFlag bool
	keyFileFlag  string
	urlFlag      string
)

func init() {
	flag.StringVar(&certFileFlag, "cert-file", "", "path to cert file for https. Default is '~/.minikube/ca.crt'")
	flag.StringVar(&dataDirFlag, "data-dir", "", "path to dir where json graph data is.")
	flag.BoolVar(&httpsFlag, "https", false, "use https. Uses minikube certs by default")
	// TODO: Fix flag bool
	flag.StringVar(&keyFileFlag, "key-file", "", "path to key file for https. Default is '~/.minikube/ca.key'")
	flag.StringVar(&urlFlag, "kiali-url", "", "Required. url for the kiali api. Example: 'https://192.168.39.57/kiali'")
}

func loadGraphFromFile(filename string) (*cytoscape.Config, error) {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	cyGraph := &cytoscape.Config{}
	err = json.Unmarshal(contents, cyGraph)
	if err != nil {
		return nil, err
	}

	return cyGraph, nil
}

type graphProxy struct {
	httpProxy *httputil.ReverseProxy
	generator *generator.Generator
	graph *cytoscape.Config
}

func (p graphProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/api/namespaces/graph" {
		log.Debug("Serving mock graph data...")
		graph := p.generator.UpdateGraph(*p.graph)
		content, err := json.Marshal(graph)
		if err != nil {
			log.Errorf("Unable to marshal graph to JSON. Err: %s", err)
			rw.WriteHeader(500)
			return
		}

		_, err = rw.Write(content)
		if err != nil {
			log.Errorf("Unable to write content. Err: %s", err)
			rw.WriteHeader(500)
		}

		return
	}

	p.httpProxy.ServeHTTP(rw, req)
}

func restConfigOrDie() *rest.Config {
	kubeconfig := os.Getenv("KUBECONFIG")

	if len(kubeconfig) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal("Unable to find user home dir")
		}
		kubeconfig = fmt.Sprintf("%s/.kube/config", home)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Unable to build config from flags: %s", err)
	}

	return restConfig
}

func main() {
	flag.Parse()
	if urlFlag == "" {
		log.Fatal("KIALI url required")
	}
	if certFileFlag == "" || keyFileFlag == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}

		if certFileFlag == "" {
			certFileFlag = filepath.Join(home, ".minikube/ca.crt")
		}
		if keyFileFlag == "" {
			keyFileFlag = filepath.Join(home, ".minikube/ca.key")
		}
	}

	u, err := url.Parse(urlFlag)
	if err != nil {
		log.Fatal(err)
	}

	kubeClient := kubernetes.NewForConfigOrDie(restConfigOrDie())
	apps := 30
	ingresses := 3
	opts := generator.Options{KubeClient: kubeClient, NumberOfApps: &apps, NumberOfIngress: &ingresses}
	gen, err := generator.New(opts)
	if err != nil {
		log.Fatal(err)
	}

	var graph *cytoscape.Config
	if dataDirFlag == "" {
		log.Info("Populating graph data...")
		g := gen.Generate()
		graph = &g
	} else {
		graph, err = loadGraphFromFile(dataDirFlag)
		if err != nil {
			log.Fatalf("Unable to load graph from file. Err: %s", err)
		}

		err = gen.EnsureNamespaces(*graph)
		if err != nil {
			log.Fatalf("Unable to ensure namespaces. Err: %s", err)
		}
	}

	proxy := graphProxy{
		httpProxy: httputil.NewSingleHostReverseProxy(u),
		generator: gen,
		graph: graph,
	}

	log.Info("Ready to handle requests on: 'localhost:10201'")
	if httpsFlag {
		customTransport := &(*http.DefaultTransport.(*http.Transport))
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		proxy.httpProxy.Transport = customTransport
		log.Fatal(http.ListenAndServeTLS(":10201", certFileFlag, keyFileFlag, proxy))
	} else {
		log.Fatal(http.ListenAndServe(":10201", proxy))
	}
}
