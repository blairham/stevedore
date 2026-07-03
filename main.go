// Command stevedore releases Docker/OCI images from a declarative config,
// the way goreleaser releases binaries.
package main

import (
	"fmt"
	"os"

	"github.com/blairham/stevedore/cmd"
)

// version is set at build time via -ldflags "-X main.version=<v>".
var version = "dev"

func main() {
	cmd.SetVersion(version)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "stevedore: "+err.Error())
		os.Exit(1)
	}
}
