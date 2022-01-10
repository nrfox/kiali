// generator is a small CLI program to generate sample graph data
// intended to be consumed by the front end. It's purpose is to
// enable testing large topologies independent of the backend.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path"

	"github.com/kiali/kiali/graph/generator"
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
	numAppsFlag  int
	popStratFlag popStratValue = generator.Sparse
)

func init() {
	flag.BoolVar(&boxFlag, "box", false, "adds boxing to the graph")
	flag.IntVar(&numAppsFlag, "apps", 5, "number of apps to create")
	flag.Var(&popStratFlag, "population-strategy", "whether the graph should have many or few connections")
}

// writeJSONToFile writes the contents to a JSON encoded file.
func writeJSONToFile(contents interface{}) error {
	// If the file doesn't exist, create it, or append to the file
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	outputPath := path.Join(wd, "output.json")
	fmt.Printf("Outputting graph data to file: %s\n", outputPath)

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

func main() {
	flag.Parse()

	g := generator.Generator{
		Cluster:            "test",
		IncludeBoxing:      boxFlag,
		NumberOfApps:       numAppsFlag,
		PopulationStrategy: string(popStratFlag),
	}

	fmt.Println("Generating graph...")
	graph := g.Generate()

	if err := writeJSONToFile(graph); err != nil {
		log.Fatal(err)
	}
}
