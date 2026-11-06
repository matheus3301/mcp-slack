// Command mcp-slack is a stdio Model Context Protocol server that exposes a
// configured catalog of Slack collaboration tools with explicit capabilities,
// workspace identity, and independent read/write destination policies.
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
	"github.com/matheus3301/mcp-slack/internal/receipts"
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
	// message content. The channel count and mode are safe operational metadata.
	logger := log.New(os.Stderr, "mcp-slack ", log.LstdFlags|log.LUTC)
	allow := cfg.Allowlist()
	if allow.Wildcard() {
		logger.Printf("starting version=%s read_allowlist=wildcard(member-scoped)", version)
	} else {
		logger.Printf("starting version=%s read_allowlist=%d channels", version, allow.Len())
	}

	api := slackclient.New(cfg.BotToken())
	tools := &mcpserver.Tools{API: api, Allow: cfg.Allowlist(), Policy: cfg.Policy}
	tools.Slack = slackclient.NewCollaboration(cfg.BotToken())
	if cfg.Policy.Enabled("slack_chat_delete_scheduled_message") {
		tools.Canceler = slackclient.NewScheduledCanceler(cfg.BotToken())
	}
	if cfg.Policy.StateDir != "" {
		store, err := receipts.Open(cfg.Policy.StateDir)
		if err != nil {
			return err
		}
		defer store.Close()
		tools.Store = store
	}
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
