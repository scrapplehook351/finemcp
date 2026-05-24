package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/finemcp/finemcp"
)

var errREPLExit = errors.New("repl: exit")

type replClient interface {
	Initialize(ctx context.Context) (*finemcp.InitializeResult, error)
	Ping(ctx context.Context) error
	ListTools(ctx context.Context, params finemcp.ListParams) (*finemcp.ListToolsResult, error)
	CallTool(ctx context.Context, params finemcp.CallToolParams) (*finemcp.CallToolResult, error)
	ListResources(ctx context.Context, params finemcp.ListParams) (*finemcp.ListResourcesResult, error)
	ReadResource(ctx context.Context, params finemcp.ReadResourceParams) (*finemcp.ReadResourceResult, error)
	ListPrompts(ctx context.Context, params finemcp.ListParams) (*finemcp.ListPromptsResult, error)
	GetPrompt(ctx context.Context, params finemcp.GetPromptParams) (*finemcp.GetPromptResult, error)
	Close() error
}

// REPL provides an interactive command-line shell for exploring an MCP server.
type REPL struct {
	client replClient
	in     io.Reader
	out    io.Writer
	prompt string
}

// NewREPL creates a new interactive MCP REPL bound to the provided client.
func NewREPL(c *Client, in io.Reader, out io.Writer) *REPL {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	return &REPL{
		client: c,
		in:     in,
		out:    out,
		prompt: "finemcp> ",
	}
}

func (r *REPL) printLine(a ...any) {
	_, _ = fmt.Fprintln(r.out, a...)
}

func (r *REPL) printFmt(format string, a ...any) {
	_, _ = fmt.Fprintf(r.out, format, a...)
}

func (r *REPL) printRaw(s string) {
	_, _ = fmt.Fprint(r.out, s)
}

func (r *REPL) printHelp() {
	r.printLine("Commands:")
	r.printLine("  help")
	r.printLine("  initialize")
	r.printLine("  ping")
	r.printLine("  list-tools")
	r.printLine("  call-tool <name> [json-arguments]")
	r.printLine("  list-resources")
	r.printLine("  read-resource <uri>")
	r.printLine("  list-prompts")
	r.printLine("  get-prompt <name> [json-args]")
	r.printLine("  quit | exit")
}

// Run starts the interactive loop and blocks until the user exits.
func (r *REPL) Run(ctx context.Context) error {
	s := bufio.NewScanner(r.in)
	r.printLine("FineMCP Interactive REPL")
	r.printLine("Type 'help' for commands.")
	// Auto-initialize on startup so users don't need to run 'initialize' manually.
	if info, err := r.client.Initialize(ctx); err != nil {
		r.printFmt("warning: auto-initialize failed: %v\n", err)
		r.printLine("Run 'initialize' manually to connect.")
	} else {
		r.printFmt("connected: %s %s\n", info.ServerInfo.Name, info.ServerInfo.Version)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		r.printRaw(r.prompt)
		if !s.Scan() {
			if err := s.Err(); err != nil {
				return err
			}
			return nil
		}

		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		err := r.exec(ctx, line)
		if err == nil {
			continue
		}
		if errors.Is(err, errREPLExit) {
			return nil
		}
		r.printFmt("error: %v\n", err)
	}
}

func (r *REPL) exec(ctx context.Context, line string) error {
	cmd, rest := splitWord(line)
	switch cmd {
	case "help":
		r.printHelp()
		return nil
	case "exit", "quit":
		return errREPLExit
	case "initialize":
		res, err := r.client.Initialize(ctx)
		if err != nil {
			return err
		}
		r.printFmt("initialized: %s %s\n", res.ServerInfo.Name, res.ServerInfo.Version)
		return nil
	case "ping":
		if err := r.client.Ping(ctx); err != nil {
			return err
		}
		r.printLine("pong")
		return nil
	case "list-tools":
		res, err := r.client.ListTools(ctx, finemcp.ListParams{})
		if err != nil {
			return err
		}
		if len(res.Tools) == 0 {
			r.printLine("(no tools)")
			return nil
		}
		for i, t := range res.Tools {
			r.printFmt("%d. %s", i+1, t.Name)
			if t.Description != "" {
				r.printFmt(" - %s", t.Description)
			}
			r.printLine()
		}
		return nil
	case "call-tool":
		name, argText := splitWord(rest)
		if name == "" {
			return errors.New("usage: call-tool <name> [json-arguments]")
		}
		var raw json.RawMessage
		if strings.TrimSpace(argText) != "" {
			if !json.Valid([]byte(argText)) {
				return errors.New("invalid JSON arguments")
			}
			raw = json.RawMessage(argText)
		}
		res, err := r.client.CallTool(ctx, finemcp.CallToolParams{Name: name, Arguments: raw})
		if err != nil {
			return err
		}
		return prettyPrint(r.out, res)
	case "list-resources":
		res, err := r.client.ListResources(ctx, finemcp.ListParams{})
		if err != nil {
			return err
		}
		if len(res.Resources) == 0 {
			r.printLine("(no resources)")
			return nil
		}
		for i, rr := range res.Resources {
			r.printFmt("%d. %s (%s)\n", i+1, rr.Name, rr.URI)
		}
		return nil
	case "read-resource":
		uri := strings.TrimSpace(rest)
		if uri == "" {
			return errors.New("usage: read-resource <uri>")
		}
		res, err := r.client.ReadResource(ctx, finemcp.ReadResourceParams{URI: uri})
		if err != nil {
			return err
		}
		return prettyPrint(r.out, res)
	case "list-prompts":
		res, err := r.client.ListPrompts(ctx, finemcp.ListParams{})
		if err != nil {
			return err
		}
		if len(res.Prompts) == 0 {
			r.printLine("(no prompts)")
			return nil
		}
		for i, p := range res.Prompts {
			r.printFmt("%d. %s", i+1, p.Name)
			if p.Description != "" {
				r.printFmt(" - %s", p.Description)
			}
			r.printLine()
		}
		return nil
	case "get-prompt":
		name, argsText := splitWord(rest)
		if name == "" {
			return errors.New("usage: get-prompt <name> [json-args]")
		}
		var args map[string]string
		if strings.TrimSpace(argsText) != "" {
			if err := json.Unmarshal([]byte(argsText), &args); err != nil {
				return errors.New("invalid JSON args; expected object of string values")
			}
		}
		res, err := r.client.GetPrompt(ctx, finemcp.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			return err
		}
		return prettyPrint(r.out, res)
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func splitWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	idx := strings.IndexAny(s, " \t")
	if idx < 0 {
		return s, ""
	}
	return s[:idx], strings.TrimSpace(s[idx+1:])
}

func prettyPrint(out io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(b))
	return err
}
