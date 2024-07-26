// Kiali
//
// # Kiali Project, The Console for Istio Service Mesh
//
// NOTE! The Kiali API is not for public use and is not supported for any use outside of the Kiali UI itself.
// The API can and will change from version to version with no guarantee of backwards compatibility.
//
// To generate this API document:
// ```
//
//	> alias swagger='docker run --rm -it  --user $(id -u):$(id -g) -e GOCACHE=/tmp -e GOPATH=$(go env GOPATH):/go -v $HOME:$HOME -w $(pwd) quay.io/goswagger/swagger'
//	> swagger generate spec -o ./swagger.json
//	> swagger generate markdown --quiet --spec ./swagger.json --output ./kiali_internal_api.md
//
// ```
//
//	Schemes: http, https
//	BasePath: /api
//	Version: _
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
// swagger:meta
package main

import (
	"embed"
	"flag"
	"io/fs"
	"os"
	"strings"

	_ "go.uber.org/automaxprocs"

	"github.com/kiali/kiali/cmd/local"
	"github.com/kiali/kiali/cmd/server"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/util"
)

//go:embed _output/docker/console/*
var folder embed.FS

// Identifies the build. These are set via ldflags during the build (see Makefile).
var (
	version    = "unknown"
	commitHash = "unknown"
	goVersion  = "unknown"
)

// Command line arguments
var (
	argConfigFile = flag.String("config", "", "Path to the YAML configuration file. If not specified, environment variables will be used for configuration.")
)

func init() {
	// log everything to stderr so that it can be easily gathered by logs, separate log files are problematic with containers
	_ = flag.Set("logtostderr", "true")
}

func main() {
	log.InitializeLogger()
	util.Clock = util.RealClock{}

	// process command line
	flag.Parse()
	validateFlags()

	if len(flag.CommandLine.Args()) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	// log startup information
	log.Infof("Kiali: Version: %v, Commit: %v, Go: %v", version, commitHash, goVersion)
	log.Debugf("Kiali: Command line: [%v]", strings.Join(os.Args, " "))

	// load config file if specified, otherwise, rely on environment variables to configure us
	if *argConfigFile != "" {
		c, err := config.LoadFromFile(*argConfigFile)
		if err != nil {
			log.Fatal(err)
		}
		config.Set(c)
	} else {
		log.Infof("No configuration file specified. Will rely on environment for configuration.")
		config.Set(config.NewConfig())
	}

	f, err := fs.Sub(folder, "_output/docker/console")
	if err != nil {
		log.Fatalf("Error getting subfolder: %v", err)
	}

	switch flag.CommandLine.Arg(0) {
	case "server":
		server.Run(config.Get(), version, commitHash, goVersion, f)
	case "open":
		local.Run(config.Get(), version, commitHash, goVersion, f)
	default:
		flag.Usage()
		os.Exit(1)
	}
}

func validateFlags() {
	if *argConfigFile != "" {
		if _, err := os.Stat(*argConfigFile); err != nil {
			if os.IsNotExist(err) {
				log.Debugf("Configuration file [%v] does not exist.", *argConfigFile)
			}
		}
	}
}
