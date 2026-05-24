// Command finemcp is the unified CLI for the FineMCP SDK.
//
// Usage:
//
//	finemcp <command> [flags]
//
// Commands:
//
//	serve    Start a built-in MCP ping server (for testing/connectivity)
//	repl     Connect to an MCP server with an interactive REPL
//	list     Connect to an MCP server and list its tools/resources/prompts
//	call     Call a tool on an MCP server and print the result
//	inspect  Print a full JSON summary of an MCP server's capabilities
//	version  Print version information
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
	thttp "github.com/finemcp/finemcp/client/transport/http"
	"github.com/finemcp/finemcp/client/transport/stdio"
	"github.com/finemcp/finemcp/client/transport/streamable"
	"github.com/finemcp/finemcp/client/transport/ws"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

// rootUsage prints the top-level help text and exits with the given code.
func rootUsage(code int) {
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Welcome to FineMCP %s\n", Version)
	fmt.Fprintf(os.Stderr, "  ─────────────────────────────────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "  Production-grade Model Context Protocol (MCP) for Go.\n")
	fmt.Fprintf(os.Stderr, "  Build, inspect, and connect MCP servers from the command\n")
	fmt.Fprintf(os.Stderr, "  line — batteries included.\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Usage: finemcp <command> [flags]\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Commands:\n")
	fmt.Fprintf(os.Stderr, "    serve    Launch a test MCP ping server (stdio or HTTP)\n")
	fmt.Fprintf(os.Stderr, "    repl     Connect to an MCP server with interactive REPL\n")
	fmt.Fprintf(os.Stderr, "    list     List tools, resources, and prompts from a server\n")
	fmt.Fprintf(os.Stderr, "    call     Call a specific tool on an MCP server\n")
	fmt.Fprintf(os.Stderr, "    inspect  Print full JSON dump of server capabilities\n")
	fmt.Fprintf(os.Stderr, "    version  Show version and build information\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Examples:\n")
	fmt.Fprintf(os.Stderr, "    finemcp serve --http :8080           # Quick test server\n")
	fmt.Fprintf(os.Stderr, "    finemcp inspect stdio ./myserver     # Debug tool visibility\n")
	fmt.Fprintf(os.Stderr, "    finemcp repl http://localhost:8080   # Interactive session\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Docs:   https://github.com/finemcp/finemcp\n")
	fmt.Fprintf(os.Stderr, "  Issues: https://github.com/finemcp/finemcp/issues\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Run 'finemcp <command> --help' for detailed usage.\n")
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(code)
}

func main() {
	if len(os.Args) < 2 {
		rootUsage(0)
	}

	switch os.Args[1] {
	case "-h", "--help":
		rootUsage(0)
	case "serve":
		runServe(os.Args[2:])
	case "repl":
		runREPL(os.Args[2:])
	case "list":
		runList(os.Args[2:])
	case "call":
		runCall(os.Args[2:])
	case "inspect":
		runInspect(os.Args[2:])
	case "version":
		runVersion()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		rootUsage(1)
	}
}

// runServe starts the built-in ping server in stdio or HTTP mode.
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	httpAddr := fs.String("http", "", "Start HTTP server on this address (e.g. :8080). Default: stdio mode.")
	name := fs.String("name", "finemcp", "Server name advertised to clients.")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: finemcp serve [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Start a built-in MCP ping server with a single 'ping' tool.\n")
		fmt.Fprintf(os.Stderr, "Useful for testing MCP client connectivity and debugging transport issues.\n\n")
		fmt.Fprintf(os.Stderr, "The server responds to 'ping' tool calls with 'pong'. Connect Claude Desktop,\n")
		fmt.Fprintf(os.Stderr, "Cursor, or any MCP client to verify your setup works correctly.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  finemcp serve                           # stdio mode (default)\n")
		fmt.Fprintf(os.Stderr, "  finemcp serve --http :8080              # HTTP mode\n")
		fmt.Fprintf(os.Stderr, "  finemcp serve --name myserver --http :8080\n\n")
		fmt.Fprintf(os.Stderr, "Then connect Claude Desktop to http://localhost:8080 and try the 'ping' tool.\n\n")
		fmt.Fprintf(os.Stderr, "Learn more: https://finemcp.dev/docs/cli\n")
	}
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	s := finemcp.NewServer(*name, Version)
	s.Use(middleware.Recovery())

	tool, err := finemcp.NewTypedTool("ping",
		func(_ context.Context, _ struct{}) (string, error) {
			return "pong", nil
		},
		finemcp.WithDescription("Returns pong - useful for testing connectivity"),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := s.RegisterTool(tool); err != nil {
		log.Fatal(err)
	}

	if *httpAddr != "" {
		fmt.Fprintf(os.Stderr, "FineMCP server listening on http://localhost%s\n", *httpAddr)
		log.Fatal(transport.StartHTTP(s, *httpAddr))
	} else {
		fmt.Fprintln(os.Stderr, "FineMCP server running in stdio mode")
		if err := transport.ServeStdio(context.Background(), s); err != nil {
			log.Fatal(err)
		}
	}
}

// runREPL connects to an MCP server and starts an interactive REPL session.
func runREPL(args []string) {
	fs := flag.NewFlagSet("repl", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: finemcp repl <transport> [target] [server-args...]\n\n")
		fmt.Fprintf(os.Stderr, "Connect to an MCP server and start an interactive REPL session.\n\n")
		fmt.Fprintf(os.Stderr, "The REPL provides interactive commands to explore server capabilities,\n")
		fmt.Fprintf(os.Stderr, "call tools, read resources, and debug MCP communication in real-time.\n")
		fmt.Fprintf(os.Stderr, "Perfect for testing and troubleshooting MCP servers during development.\n\n")
		fmt.Fprintf(os.Stderr, "Transports:\n")
		fmt.Fprintf(os.Stderr, "  stdio      <server-command> [args...]  Spawn a server process via stdio\n")
		fmt.Fprintf(os.Stderr, "  http       <base-url>                  Connect to an HTTP server\n")
		fmt.Fprintf(os.Stderr, "  ws         <ws-url>                    Connect via WebSocket\n")
		fmt.Fprintf(os.Stderr, "  streamable <url>                       Connect via Streamable HTTP\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  finemcp repl stdio ./my-mcp-server --flag\n")
		fmt.Fprintf(os.Stderr, "  finemcp repl http http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  finemcp repl ws ws://localhost:8081/mcp\n")
		fmt.Fprintf(os.Stderr, "  finemcp repl streamable http://localhost:8080/mcp\n\n")
		fmt.Fprintf(os.Stderr, "Learn more: https://finemcp.dev/docs/cli#repl\n")
	}

	// repl uses positional args only — parse to consume any leading flags like --help.
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	positional := fs.Args()

	tr, err := buildReplTransport(positional)
	if err != nil {
		fs.Usage()
		log.Fatal(err)
	}

	c, err := client.New(tr, client.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	r := client.NewREPL(c, os.Stdin, os.Stdout)
	if err := r.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// buildReplTransport constructs a client.Transport from the repl positional arguments.
func buildReplTransport(args []string) (client.Transport, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("missing transport mode")
	}
	switch args[0] {
	case "stdio":
		if len(args) < 2 {
			return nil, fmt.Errorf("stdio mode requires a server command")
		}
		return stdio.New(stdio.Config{Command: args[1], Args: args[2:]}), nil
	case "http":
		if len(args) < 2 {
			return nil, fmt.Errorf("http mode requires a base URL")
		}
		return thttp.New(thttp.Config{BaseURL: args[1]}), nil
	case "ws":
		if len(args) < 2 {
			return nil, fmt.Errorf("ws mode requires a WebSocket URL")
		}
		return ws.New(ws.Config{URL: args[1]}), nil
	case "streamable":
		if len(args) < 2 {
			return nil, fmt.Errorf("streamable mode requires a URL")
		}
		return streamable.New(streamable.Config{URL: args[1]}), nil
	default:
		return nil, fmt.Errorf("unknown transport %q", args[0])
	}
}

// runVersion prints version and runtime information.
func runVersion() {
	fmt.Printf("finemcp %s\n", Version)
	fmt.Printf("  Commit:   %s\n", Commit)
	fmt.Printf("  Built:    %s\n", Date)
	fmt.Printf("  Built by: %s\n", BuiltBy)
	fmt.Printf("  Go:       %s\n", runtime.Version())
	fmt.Printf("  OS/Arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

// runList connects to an MCP server and lists its tools, resources, and/or prompts.
func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	doTools := fs.Bool("tools", false, "Include tools in output")
	doResources := fs.Bool("resources", false, "Include resources in output")
	doPrompts := fs.Bool("prompts", false, "Include prompts in output")
	asJSON := fs.Bool("json", false, "Output as JSON instead of human-readable text")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: finemcp list [flags] <transport> <target> [server-args...]\n\n")
		fmt.Fprintf(os.Stderr, "Connect to an MCP server and list its exposed capabilities.\n\n")
		fmt.Fprintf(os.Stderr, "This command helps verify what tools, resources, and prompts your server\n")
		fmt.Fprintf(os.Stderr, "exposes. Perfect for debugging why Claude or other MCP clients can't see\n")
		fmt.Fprintf(os.Stderr, "certain capabilities. By default, lists tools only.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nTransports:\n")
		fmt.Fprintf(os.Stderr, "  stdio      <server-command> [args...]  Spawn a server process via stdio\n")
		fmt.Fprintf(os.Stderr, "  http       <base-url>                  Connect to an HTTP server\n")
		fmt.Fprintf(os.Stderr, "  ws         <ws-url>                    Connect via WebSocket\n")
		fmt.Fprintf(os.Stderr, "  streamable <url>                       Connect via Streamable HTTP\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  finemcp list http http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  finemcp list --tools --resources http http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  finemcp list --json stdio ./my-mcp-server\n")
		fmt.Fprintf(os.Stderr, "  finemcp list --resources --prompts streamable http://localhost:8080/mcp\n\n")
		fmt.Fprintf(os.Stderr, "Learn more: https://finemcp.dev/docs/cli#list\n")
	}
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	positional := fs.Args()

	// Default: tools only when no category flag is explicitly set.
	anyCategory := *doTools || *doResources || *doPrompts
	if !anyCategory {
		*doTools = true
	}

	tr, err := buildReplTransport(positional)
	if err != nil {
		fs.Usage()
		log.Fatal(err)
	}

	c, err := client.New(tr, client.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		log.Fatalf("initialize: %v", err)
	}

	type listOutput struct {
		Tools     []finemcp.ToolInfo     `json:"tools,omitempty"`
		Resources []finemcp.ResourceInfo `json:"resources,omitempty"`
		Prompts   []finemcp.PromptInfo   `json:"prompts,omitempty"`
	}
	var out listOutput

	if *doTools {
		res, err := c.ListTools(ctx, finemcp.ListParams{})
		if err != nil {
			log.Fatalf("list tools: %v", err)
		}
		out.Tools = res.Tools
	}
	if *doResources {
		res, err := c.ListResources(ctx, finemcp.ListParams{})
		if err != nil {
			log.Fatalf("list resources: %v", err)
		}
		out.Resources = res.Resources
	}
	if *doPrompts {
		res, err := c.ListPrompts(ctx, finemcp.ListParams{})
		if err != nil {
			log.Fatalf("list prompts: %v", err)
		}
		out.Prompts = res.Prompts
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			log.Fatalf("encode json: %v", err)
		}
		return
	}

	// Human-readable output.
	if *doTools {
		fmt.Printf("=== Tools (%d) ===\n", len(out.Tools))
		for _, t := range out.Tools {
			fmt.Printf("  %-14s %s\n", t.Name, t.Description)
		}
		if len(out.Tools) == 0 {
			fmt.Println("  (none)")
		}
	}
	if *doResources {
		if *doTools {
			fmt.Println()
		}
		fmt.Printf("=== Resources (%d) ===\n", len(out.Resources))
		for _, r := range out.Resources {
			if r.MimeType != "" {
				fmt.Printf("  %-14s %s  (%s)\n", r.Name, r.URI, r.MimeType)
			} else {
				fmt.Printf("  %-14s %s\n", r.Name, r.URI)
			}
		}
		if len(out.Resources) == 0 {
			fmt.Println("  (none)")
		}
	}
	if *doPrompts {
		if *doTools || *doResources {
			fmt.Println()
		}
		fmt.Printf("=== Prompts (%d) ===\n", len(out.Prompts))
		for _, p := range out.Prompts {
			fmt.Printf("  %-14s %s\n", p.Name, p.Description)
			if len(p.Arguments) > 0 {
				parts := make([]string, 0, len(p.Arguments))
				for _, a := range p.Arguments {
					if a.Required {
						parts = append(parts, a.Name+" (required)")
					} else {
						parts = append(parts, a.Name+" (optional)")
					}
				}
				fmt.Printf("    args: %s\n", strings.Join(parts, ", "))
			}
		}
		if len(out.Prompts) == 0 {
			fmt.Println("  (none)")
		}
	}
}

// runCall calls a single tool on an MCP server and prints the result.
func runCall(args []string) {
	fs := flag.NewFlagSet("call", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "Output result as JSON")
	rawArgs := fs.String("args", "", "JSON object of arguments (overrides key=value pairs)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: finemcp call [flags] <transport> <target> <tool-name> [key=value...]\n\n")
		fmt.Fprintf(os.Stderr, "Call a tool on an MCP server and print the result.\n\n")
		fmt.Fprintf(os.Stderr, "This command allows you to test tool calls from the command line without\n")
		fmt.Fprintf(os.Stderr, "needing to set up a full MCP client. Perfect for testing tool behavior,\n")
		fmt.Fprintf(os.Stderr, "validating outputs, and debugging issues before connecting real clients.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nTransports:\n")
		fmt.Fprintf(os.Stderr, "  stdio      <server-command>  Spawn a server process via stdio (single binary, no extra args)\n")
		fmt.Fprintf(os.Stderr, "  http       <base-url>        Connect to an HTTP server\n")
		fmt.Fprintf(os.Stderr, "  ws         <ws-url>          Connect via WebSocket\n")
		fmt.Fprintf(os.Stderr, "  streamable <url>             Connect via Streamable HTTP\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  finemcp call http http://localhost:8080 ping\n")
		fmt.Fprintf(os.Stderr, "  finemcp call http http://localhost:8080 echo text=hello\n")
		fmt.Fprintf(os.Stderr, "  finemcp call stdio ./my-server greet name=World\n")
		fmt.Fprintf(os.Stderr, "  finemcp call http http://localhost:8080 search query=\"hello world\" limit=5\n")
		fmt.Fprintf(os.Stderr, "  finemcp call --args '{\"name\":\"Widget\",\"tags\":[\"sale\"]}' http http://localhost:8080 create_item\n")
		fmt.Fprintf(os.Stderr, "\nNote: flags (--json, --args) must come before positional arguments.\n")
		fmt.Fprintf(os.Stderr, "      For stdio transport, the server binary must start in MCP mode without extra arguments.\n\n")
		fmt.Fprintf(os.Stderr, "Learn more: https://finemcp.dev/docs/cli#call\n")
	}
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	positional := fs.Args()

	// positional must have at least: <transport> <target> <tool-name>
	if len(positional) < 3 {
		fs.Usage()
		log.Fatal("call requires: <transport> <target> <tool-name>")
	}

	// First two positionals are transport + target (+ optional server args for stdio).
	// Third positional is the tool name.
	// For stdio: positional[0]=stdio, positional[1]=command, positional[2]=tool-name,
	// positional[3:]= key=value pairs (not server args — server args go via --args
	// or the user separates them; we treat everything after tool-name as key=value).
	transportArgs := positional[:2]
	toolName := positional[2]
	kvPairs := positional[3:]

	tr, err := buildReplTransport(transportArgs)
	if err != nil {
		fs.Usage()
		log.Fatal(err)
	}

	// Build the arguments JSON.
	var callArgs json.RawMessage
	if *rawArgs != "" {
		// Validate the provided JSON.
		if !json.Valid([]byte(*rawArgs)) {
			log.Fatalf("--args is not valid JSON: %s", *rawArgs)
		}
		callArgs = json.RawMessage(*rawArgs)
	} else if len(kvPairs) > 0 {
		obj := make(map[string]json.RawMessage, len(kvPairs))
		for _, kv := range kvPairs {
			idx := strings.IndexByte(kv, '=')
			if idx < 0 {
				log.Fatalf("invalid argument %q: expected key=value format", kv)
			}
			key := kv[:idx]
			val := kv[idx+1:]
			// Use the value as-is if it is a valid JSON literal (number, bool,
			// null, array, object); otherwise treat it as a plain string.
			if isJSONLiteral(val) {
				obj[key] = json.RawMessage(val)
			} else {
				quoted, err := json.Marshal(val)
				if err != nil {
					log.Fatalf("marshal value for key %q: %v", key, err)
				}
				obj[key] = json.RawMessage(quoted)
			}
		}
		encoded, err := json.Marshal(obj)
		if err != nil {
			log.Fatalf("marshal arguments: %v", err)
		}
		callArgs = encoded
	}

	c, err := client.New(tr, client.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		log.Fatalf("initialize: %v", err)
	}

	result, err := c.CallTool(ctx, finemcp.CallToolParams{
		Name:      toolName,
		Arguments: callArgs,
	})
	if err != nil {
		log.Fatalf("call tool: %v", err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			log.Fatalf("encode json: %v", err)
		}
		if result.IsError {
			os.Exit(1)
		}
		return
	}

	// Human-readable output: print text content; label other types.
	for _, content := range result.Content {
		switch c := content.(type) {
		case finemcp.TextContent:
			if result.IsError {
				fmt.Fprintf(os.Stderr, "Error: %s\n", c.Text)
			} else {
				fmt.Println(c.Text)
			}
		case finemcp.ImageContent:
			fmt.Println("[image content]")
		case finemcp.AudioContent:
			fmt.Println("[audio content]")
		case finemcp.EmbeddedResource:
			fmt.Println("[embedded resource]")
		default:
			fmt.Printf("[%T content]\n", content)
		}
	}
	if result.IsError {
		os.Exit(1)
	}
}

// isJSONLiteral reports whether s is a self-contained JSON literal: a number,
// boolean, null, JSON array, or JSON object. Plain strings are not considered
// literals here — they must be quoted by the caller.
func isJSONLiteral(s string) bool {
	if s == "true" || s == "false" || s == "null" {
		return true
	}
	if len(s) == 0 {
		return false
	}
	// Array or object: delegate to json.Valid.
	if s[0] == '[' || s[0] == '{' {
		return json.Valid([]byte(s))
	}
	// Number: attempt to unmarshal as json.Number.
	var n json.Number
	return json.Unmarshal([]byte(s), &n) == nil
}

// runInspect connects to an MCP server and prints a full JSON summary of its
// capabilities including server info, tools, resources, and prompts.
func runInspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	pretty := fs.Bool("pretty", true, "Pretty-print JSON output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: finemcp inspect [flags] <transport> <target> [server-args...]\n\n")
		fmt.Fprintf(os.Stderr, "Connect to an MCP server and print a complete JSON dump of its capabilities.\n\n")
		fmt.Fprintf(os.Stderr, "This command provides the most comprehensive view of an MCP server, including\n")
		fmt.Fprintf(os.Stderr, "server info, all tools with their full schemas, resources, and prompts.\n")
		fmt.Fprintf(os.Stderr, "Essential for debugging schema issues, comparing server versions, or sharing\n")
		fmt.Fprintf(os.Stderr, "complete server state with support teams.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nTransports:\n")
		fmt.Fprintf(os.Stderr, "  stdio      <server-command> [args...]  Spawn a server process via stdio\n")
		fmt.Fprintf(os.Stderr, "  http       <base-url>                  Connect to an HTTP server\n")
		fmt.Fprintf(os.Stderr, "  ws         <ws-url>                    Connect via WebSocket\n")
		fmt.Fprintf(os.Stderr, "  streamable <url>                       Connect via Streamable HTTP\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  finemcp inspect http http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  finemcp inspect stdio ./my-mcp-server\n")
		fmt.Fprintf(os.Stderr, "  finemcp inspect --pretty=false http http://localhost:8080 | jq .tools\n\n")
		fmt.Fprintf(os.Stderr, "Learn more: https://finemcp.dev/docs/cli#inspect\n")
	}
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	positional := fs.Args()

	tr, err := buildReplTransport(positional)
	if err != nil {
		fs.Usage()
		log.Fatal(err)
	}

	c, err := client.New(tr, client.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		log.Fatalf("initialize: %v", err)
	}

	info := c.ServerInfo()

	toolsRes, err := c.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		log.Fatalf("list tools: %v", err)
	}
	resourcesRes, err := c.ListResources(ctx, finemcp.ListParams{})
	if err != nil {
		log.Fatalf("list resources: %v", err)
	}
	promptsRes, err := c.ListPrompts(ctx, finemcp.ListParams{})
	if err != nil {
		log.Fatalf("list prompts: %v", err)
	}

	type serverSummary struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	type inspectOutput struct {
		Server    serverSummary          `json:"server"`
		Tools     []finemcp.ToolInfo     `json:"tools"`
		Resources []finemcp.ResourceInfo `json:"resources"`
		Prompts   []finemcp.PromptInfo   `json:"prompts"`
	}

	out := inspectOutput{
		Server:    serverSummary{Name: info.Name, Version: info.Version},
		Tools:     toolsRes.Tools,
		Resources: resourcesRes.Resources,
		Prompts:   promptsRes.Prompts,
	}

	// Ensure nil slices marshal as [] rather than null.
	if out.Tools == nil {
		out.Tools = []finemcp.ToolInfo{}
	}
	if out.Resources == nil {
		out.Resources = []finemcp.ResourceInfo{}
	}
	if out.Prompts == nil {
		out.Prompts = []finemcp.PromptInfo{}
	}

	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(out); err != nil {
		log.Fatalf("encode json: %v", err)
	}
}
