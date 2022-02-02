// This is a small CLI program to generate sample graph data
// intended to be consumed by the front end. It's purpose is to
// enable testing large topologies independent of the backend.
package main

import (
	"fmt"
	"os"

	"github.com/kiali/kiali/graph/generator/cmd/generate"
)

func main() {
	if len(os.Args) <= 1 {
		fmt.Println("generator must be called with a subcommand")
		os.Exit(1)
	}

	switch os.Args[1] {
	case generate.GenerateCmd:
		generate.GenerateFlags.Parse(os.Args[2:])
		generate.RunGenerate()
	default:
		fmt.Printf("Unrecognized subcommand: '%s'", os.Args[1])
		os.Exit(1)
	}
}
