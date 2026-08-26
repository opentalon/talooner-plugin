// Command talooner-plugin is the OpenTalon plugin binary. Default mode serves
// PluginService over a host-provided Unix socket (spawned as a subprocess,
// config delivered via the Init RPC) — but talooner's caller is a GitHub
// Actions runner, off-network from wherever this binary runs (protocol.md,
// "The caller is now off-network"), and a Unix socket private to a spawning
// parent process is unreachable from there. TALOONER_GRPC_PORT opts into a
// standalone TCP listener instead, mirroring mcp-plugin's MCP_GRPC_PORT.
package main

import (
	"fmt"
	"log"
	"net"
	"os"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/service"
)

func main() {
	srv := service.New()

	// TCP mode: TALOONER_GRPC_PORT=50051 → listen on TCP; print handshake; serve.
	if port := os.Getenv("TALOONER_GRPC_PORT"); port != "" {
		// No host to call the Init RPC in this mode, so the same config block
		// travels via TALOONER_CONFIG instead — Configure has no other caller.
		if err := srv.Configure(os.Getenv("TALOONER_CONFIG")); err != nil {
			log.Fatalf("talooner-plugin: configure: %v", err)
		}
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			log.Fatalf("talooner-plugin: listen tcp :%s: %v", port, err)
		}
		hs := plugin.Handshake{
			Version: plugin.HandshakeVersion,
			Network: "tcp",
			Address: "0.0.0.0:" + port,
		}
		if _, err := fmt.Fprintln(os.Stdout, hs.String()); err != nil {
			log.Fatalf("talooner-plugin: write handshake: %v", err)
		}
		if err := plugin.ServeListener(ln, srv); err != nil {
			log.Fatalf("talooner-plugin: serve: %v", err)
		}
		return
	}

	// Default: Unix socket mode. Config is received via the Init RPC → Configure.
	if err := plugin.Serve(srv); err != nil {
		os.Exit(1)
	}
}
