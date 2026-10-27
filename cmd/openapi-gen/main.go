// Command openapi-gen writes the OpenAPI document of the agent-web API. It
// is run by `go generate ./...`; the frontend generates its client from it.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/jclab-joseph/agent-web/internal/server"
	"github.com/jclab-joseph/agent-web/internal/version"
)

func main() {
	out := flag.String("o", "openapi/openapi.yaml", "output file")
	flag.Parse()
	// The document must not change with the build's version.
	version.Version = "1.0.0"
	b, err := server.New(nil, server.Options{}).API().OpenAPI().YAML()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}
}
