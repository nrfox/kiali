package server

import (
	"context"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	_ "go.uber.org/automaxprocs"

	"github.com/kiali/kiali/business"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/istio"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/kubernetes/cache"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/models"
	"github.com/kiali/kiali/prometheus"
	"github.com/kiali/kiali/prometheus/internalmetrics"
	"github.com/kiali/kiali/server"
	"github.com/kiali/kiali/tracing"
)

func Run(conf *config.Config, version string, commitHash string, goVersion string, staticAssetFS fs.FS) {
	updateConfigWithIstioInfo(conf)

	log.Tracef("Kiali Configuration:\n%s", conf)

	if err := config.Validate(*conf); err != nil {
		log.Fatal(err)
	}

	// prepare our internal metrics so Prometheus can scrape them
	internalmetrics.RegisterInternalMetrics()

	// Create the business package dependencies.
	clientFactory, err := kubernetes.GetClientFactory()
	if err != nil {
		log.Fatalf("Failed to create client factory. Err: %s", err)
	}

	log.Info("Initializing Kiali Cache")
	cache, err := cache.NewKialiCache(clientFactory, *conf)
	if err != nil {
		log.Fatalf("Error initializing Kiali Cache. Details: %s", err)
	}
	defer cache.Stop()

	cache.SetBuildInfo(models.BuildInfo{
		CommitHash:       commitHash,
		ContainerVersion: determineContainerVersion(version),
		GoVersion:        goVersion,
		Version:          version,
	})

	discovery := istio.NewDiscovery(clientFactory.GetSAClients(), cache, conf)
	cpm := business.NewControlPlaneMonitor(cache, clientFactory, *conf, discovery)

	// This context is used for polling and for creating some high level clients like tracing.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if conf.ExternalServices.Istio.IstioAPIEnabled {
		cpm.PollIstiodForProxyStatus(ctx)
	}

	// Create shared prometheus client shared by all prometheus requests in the business layer.
	prom, err := prometheus.NewClient()
	if err != nil {
		log.Fatalf("Error creating Prometheus client: %s", err)
	}

	// Create shared tracing client shared by all tracing requests in the business layer.
	// Because tracing is not an essential component, we don't want to block startup
	// of the server if the tracing client fails to initialize. tracing.NewClient will
	// continue to retry until the client is initialized or the context is cancelled.
	// Passing in a loader function allows the tracing client to be used once it is
	// finally initialized.
	var tracingClient tracing.ClientInterface
	tracingLoader := func() tracing.ClientInterface {
		return tracingClient
	}
	if conf.ExternalServices.Tracing.Enabled {
		go func() {
			client, err := tracing.NewClient(ctx, conf, clientFactory.GetSAHomeClusterClient().GetToken())
			if err != nil {
				log.Fatalf("Error creating tracing client: %s", err)
				return
			}
			tracingClient = client
		}()
	} else {
		log.Debug("Tracing is disabled")
	}

	// Start listening to requests
	server, err := server.NewServer(cpm, clientFactory, cache, conf, prom, tracingLoader, discovery, staticAssetFS)
	if err != nil {
		log.Fatal(err)
	}
	server.Start()

	// wait forever, or at least until we are told to exit
	waitForTermination()

	// Shutdown internal components
	log.Info("Shutting down internal components")
	server.Stop()
}

func waitForTermination() {
	// Channel that is notified when we are done and should exit
	// TODO: may want to make this a package variable - other things might want to tell us to exit
	doneChan := make(chan bool)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range signalChan {
			log.Info("Termination Signal Received")
			doneChan <- true
		}
	}()

	<-doneChan
}

// determineContainerVersion will return the version of the image container.
// It does this by looking at an ENV defined in the Dockerfile when the image is built.
// If the ENV is not defined, the version is assumed the same as the given default value.
func determineContainerVersion(defaultVersion string) string {
	v := os.Getenv("KIALI_CONTAINER_VERSION")
	if v == "" {
		return defaultVersion
	}
	return v
}

// This is used to update the config with information about istio that
// comes from the environment such as the cluster name.
func updateConfigWithIstioInfo(conf *config.Config) {
	homeCluster := conf.KubernetesConfig.ClusterName
	if homeCluster != "" {
		// If the cluster name is already set, we don't need to do anything
		return
	}

	err := func() error {
		log.Debug("Cluster name is not set. Attempting to auto-detect the cluster name from the home cluster environment.")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Need to create a temporary client factory here so that we can create a client
		// to auto-detect the istio cluster name from the environment. There's a bit of a
		// chicken and egg problem with the client factory because the client factory
		// uses the cluster id to keep track of all the clients. But in order to create
		// a client to get the cluster id from the environment, you need to create a client factory.
		// To get around that we create a temporary client factory here and then set the kiali
		// config cluster name. We then create the global client factory later in the business
		// package and that global client factory has the cluster id set properly.
		cf, err := kubernetes.NewClientFactory(ctx, *conf)
		if err != nil {
			return err
		}

		// Try to auto-detect the cluster name
		homeCluster, err = kubernetes.ClusterNameFromIstiod(*conf, cf.GetSAHomeClusterClient())
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		log.Warningf("Cannot resolve local cluster name. Err: %s. Falling back to [%s]", err, config.DefaultClusterID)
		homeCluster = config.DefaultClusterID
	}

	log.Debugf("Auto-detected the istio cluster name to be [%s]. Updating the kiali config", homeCluster)
	conf.KubernetesConfig.ClusterName = homeCluster
	config.Set(conf)
}
