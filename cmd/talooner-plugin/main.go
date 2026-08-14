// Command talooner-plugin is the OpenTalon plugin binary. It serves the
// PluginService over the host-provided Unix socket and dispatches action calls
// to the Talooner service registry.
package main

import (
	"os"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/service"
)

func main() {
	if err := plugin.Serve(service.New()); err != nil {
		os.Exit(1)
	}
}
