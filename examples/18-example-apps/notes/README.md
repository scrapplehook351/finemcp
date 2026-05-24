# Notes App

An in-memory notes store — a real-world style MCP server example with validation,
uniqueness enforcement, update, and keyword search.

## Tools

| Tool | Description |
|---|---|
| `note_create` | Create a note. Title and body required; titles must be unique. |
| `note_list` | List all notes sorted by creation time. |
| `note_get` | Fetch a single note by ID. |
| `note_update` | Update the title and/or body of a note. |
| `note_delete` | Delete a note by ID. |
| `note_search` | Search by keyword (case-insensitive, title and body). |

## Run

**stdio (Claude Desktop):**
```bash
go run ./18-example-apps/notes/
```

**SSE:**
```bash
go run ./18-example-apps/notes/ --sse :8081
```

## Claude Desktop

Build once, point the config at the binary:

```bash
go build -o ~/bin/mcp-notes ./18-example-apps/notes/
```

`~/Library/Application Support/Claude/claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "notes": { "command": "/Users/YOUR_USER/bin/mcp-notes" }
  }
}
```

Then restart Claude Desktop.

## Design notes

- Notes are **in-memory only** — they reset when the server restarts.
- `note_create` rejects empty body and duplicate titles (case-insensitive).
- `note_update` accepts partial updates — omit `title` or `body` to keep the current value.
- `note_list` is always sorted oldest-first, regardless of insertion order.
- `note_search` matches anywhere in title or body, case-insensitively.
