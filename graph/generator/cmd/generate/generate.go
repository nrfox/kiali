package generate

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kiali/kiali/graph/generator"
	"github.com/kiali/kiali/log"
)

const (
	defaultOutputLocation = "../kiali-ui/cypress/fixtures/generated"
	GenerateCmd = "generate"
)

var (
	// Adapted from: https://stackoverflow.com/a/38644571
	_, b, _, _ = runtime.Caller(0)
	basepath   = filepath.Dir(b)
	// This works so long as the current dir structure stays the same.
	kialiProjectRoot = path.Dir(path.Dir(path.Dir(basepath)))
)

var (
	GenerateFlags = flag.NewFlagSet("Generate", flag.ExitOnError)
)

type popStratValue string

func (p *popStratValue) String() string {
	return fmt.Sprint(*p)
}

func (i *popStratValue) Set(value string) error {
	if value != generator.Dense && value != generator.Sparse {
		return fmt.Errorf("%s is not valid. Use: '%s' or '%s'", value, generator.Dense, generator.Sparse)
	}
	return nil
}

var (
	boxFlag      bool
	clusterFlag  string
	numAppsFlag  int
	outputFlag   string
	popStratFlag popStratValue = generator.Sparse
)

func init() {
	GenerateFlags.BoolVar(&boxFlag, "box", false, "adds boxing to the graph")
	GenerateFlags.StringVar(&clusterFlag, "cluster", "test", "nodes' cluster name")
	GenerateFlags.IntVar(&numAppsFlag, "apps", 5, "number of apps to create")
	GenerateFlags.StringVar(&outputFlag, "output", path.Join(kialiProjectRoot, defaultOutputLocation), "path to output the generated json")
	GenerateFlags.Var(&popStratFlag, "population-strategy", "whether the graph should have many or few connections")
}

func filename() string {
	return "generated_graph_data.json"
}

// writeJSONToFile writes the contents to a JSON encoded file.
func writeJSONToFile(fpath string, contents interface{}) error {
	// If the file doesn't exist, create it, or append to the file
	outputPath := path.Join(fpath, filename())
	log.Infof("Outputting graph data to file: %s", outputPath)

	b, err := json.Marshal(contents)
	if err != nil {
		return err
	}

	err = os.WriteFile(outputPath, b, 0644)
	if err != nil {
		return err
	}

	return nil
}

func getKubeConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")

	if len(kubeconfig) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("Unable to find user home dir. Err: %w", err)
		}
		kubeconfig = fmt.Sprintf("%s/.kube/config", home)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("Unable to build config from flags: %w", err)
	}

	return restConfig, nil
}

func configureKialiLogger() {
	// Kiali logger is configured through env vars and the default format is json which isn't very nice for cmdline.
	if err := os.Setenv("LOG_FORMAT", "text"); err != nil {
		log.Errorf("Unable to configure logger. Err: %s", err)
	} else {
		log.InitializeLogger()
	}
}

func RunGenerate() {
	configureKialiLogger()

	kubeCfg, err := getKubeConfig()
	if err != nil {
		log.Errorf("Unable to get kube config because: '%s'. Using generator without kubeclient works but some functionality such as automatic namespace creation won't be available.", err)
	}

	popStrat := string(popStratFlag)
	opts := generator.Options{
		Cluster:            &clusterFlag,
		IncludeBoxing:      &boxFlag,
		NumberOfApps:       &numAppsFlag,
		PopulationStrategy: &popStrat,
	}

	if kubeCfg != nil {
		kubeClient, err := kubernetes.NewForConfig(kubeCfg)
		if err != nil {
			log.Errorf("Unable to create kube client because: '%s'. Using generator without kubeclient works but some functionality such as automatic namespace creation won't be available.", err)
		} else {
			opts.KubeClient = kubeClient
		}
	}

	g, err := generator.New(opts)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Generating graph...")
	graph := g.Generate()

	err = writeJSONToFile(outputFlag, graph)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Success!!")
}
