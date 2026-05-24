# Example Apps

Real-world style MCP servers you can connect to Claude Desktop or any MCP client.
Each app is self-contained, compiles to a single binary, and runs over stdio by default.

## Apps

| App | Port (SSE) | Description |
|---|---|---|
| [notes](./notes/) | 8081 | In-memory notes store with create, list, get, update, delete, and search |

## Claude Desktop setup

**Step 1 — build the binaries** (run from the repo root):

```bash
go build -o ~/bin/mcp-notes ./18-example-apps/notes/
```

**Step 2 — edit your config file**

Location: `~/Library/Application Support/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "notes": {
      "command": "/Users/YOUR_USER/bin/mcp-notes"
    }
  }
}
```

> Replace `YOUR_USER` with your macOS username (run `echo $USER` if unsure).

**Step 3 — restart Claude Desktop.**  
The notes tools will appear automatically in the tool picker.

## Running over SSE

Each app accepts `--sse [addr]` to serve over SSE instead of stdio:

```bash
go run ./18-example-apps/notes/ --sse :8081
```
