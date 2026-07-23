// Command mcp-slack is a stdio Model Context Protocol server that exposes a
// minimal, read-only view of a Slack workspace: channel metadata, channel
// history, and thread replies, restricted to an explicit channel-ID allowlist.
//
// It speaks MCP over stdin/stdout only. There is no HTTP or SSE listener. The
// Slack bot token is read once from the environment and is never logged.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/matheus3301/mcp-slack/internal/buildinfo"
	"github.com/matheus3301/mcp-slack/internal/config"
	"github.com/matheus3301/mcp-slack/internal/mcpserver"
	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These are overridden at build time via
// -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func buildInfo() buildinfo.Info {
	return buildinfo.Info{Version: version, Commit: commit, Date: date}
}

func main() {
	// Handle --version before touching config so it works with no environment.
	if versionRequested(os.Args[1:]) {
		fmt.Fprintln(os.Stdout, buildInfo().String())
		return
	}
	if err := run(); err != nil {
		// Errors are already sanitized; they never contain the token.
		fmt.Fprintln(os.Stderr, "mcp-slack: "+err.Error())
		os.Exit(1)
	}
}

// versionRequested reports whether the args ask for the version banner.
func versionRequested(args []string) bool {
	for _, a := range args {
		switch a {
		case "--version", "-version", "version", "-v":
			return true
		}
	}
	return false
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}

	// Diagnostics go to stderr only, and never include the token or any
	// message content. The channel count is safe operational metadata.
	logger := log.New(os.Stderr, "mcp-slack ", log.LstdFlags|log.LUTC)
	logger.Printf("starting version=%s allowlisted_channels=%d", version, cfg.Allowlist().Len())

	api := slackclient.New(cfg.BotToken())
	tools := &mcpserver.Tools{API: api, Allow: cfg.Allowlist()}
	server := mcpserver.New(tools, version)

	// Terminate cleanly on Ctrl-C / SIGTERM so a supervising process can stop
	// the server without leaking a goroutine.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if ctx.Err() != nil {
			// Shutdown was requested; not an error.
			return nil
		}
		return fmt.Errorf("server: %w", err)
	}
	return nil
}
