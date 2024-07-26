package local

import (
	"context"
	"fmt"
	"io"
	"io/fs"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kiali/kiali/cmd/server"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/util/httputil"
)

func Run(conf *config.Config, version string, commitHash string, goVersion string, staticAssetFS fs.FS) {
	// 1. Port forward to prom container
	// 2. Run server in anonymous mode.
	// 3. Need a "remote"
	log.Info("Running Kiali in local mode")
	// Discover and port forward to prom.

	localPort := httputil.Pool.GetFreePort()
	defer httputil.Pool.FreePort(localPort)
	conf.ExternalServices.Prometheus.URL = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	cf, err := kubernetes.NewClientFactory(context.TODO(), *conf)
	if err != nil {
		panic(err)
	}

	localClient := cf.GetSAHomeClusterClient()

	promPods, err := localClient.Kube().CoreV1().Pods("istio-system").List(context.TODO(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=prometheus"})
	if err != nil {
		panic(err)
	}

	if len(promPods.Items) == 0 {
		panic("No Prometheus pod found in istio-system namespace")
	}

	pf, err := httputil.NewPortForwarder(
		localClient.Kube().CoreV1().RESTClient(),
		localClient.ClusterInfo().ClientConfig,
		"istio-system",
		promPods.Items[0].Name,
		"localhost",
		fmt.Sprintf("%d:9090", localPort),
		io.Discard,
	)
	if err != nil {
		panic(err)
	}

	if err := pf.Start(); err != nil {
		panic(err)
	}
	defer pf.Stop()

	server.Run(conf, version, commitHash, goVersion, staticAssetFS)
}
