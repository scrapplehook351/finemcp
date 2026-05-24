// Example: Notes App
//
// A feature-complete in-memory notes store. Demonstrates real-world MCP server
// design: input validation, duplicate-title prevention, update, and full-text search.
//
// Notes live in memory only -- they reset on restart (by design for this example).
//
// Runs over stdio by default (Claude Desktop). Pass --sse [addr] to use SSE instead.
//
// Run (stdio -- Claude Desktop):
//
//	go run ./18-example-apps/notes/
//
// Run (SSE):
//
//	go run ./18-example-apps/notes/ --sse :8081
//
// Tools:
//   - note_create  -- create a note (title + body required, title must be unique)
//   - note_list    -- list all notes sorted by creation time (oldest first)
//   - note_get     -- fetch a single note by ID
//   - note_update  -- update the title and/or body of a note
//   - note_delete  -- delete a note by ID
//   - note_search  -- search notes by keyword (case-insensitive, title or body)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

// -- in-memory store -------------------------------------------------------

type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (n *Note) format() string {
	b, _ := json.MarshalIndent(n, "", "  ")
	return string(b)
}

var (
	mu      sync.RWMutex
	notes   = map[string]*Note{}
	counter int
)

func nextID() string {
	counter++
	return fmt.Sprintf("note_%d", counter)
}

// sortedNotes returns all notes ordered by CreatedAt ascending (caller must hold mu).
func sortedNotes() []*Note {
	list := make([]*Note, 0, len(notes))
	for _, n := range notes {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	return list
}

// -- tool handlers ---------------------------------------------------------

func handleCreate(_ context.Context, in struct {
	Title string `json:"title" description:"Short title (required, must be unique)"`
	Body  string `json:"body"  description:"Note content (required)"`
}) (string, error) {
	if in.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if in.Body == "" {
		return "", fmt.Errorf("body is required")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, n := range notes {
		if strings.EqualFold(n.Title, in.Title) {
			return "", fmt.Errorf("a note titled %q already exists (id: %s); use note_update to edit it", n.Title, n.ID)
		}
	}
	now := time.Now()
	id := nextID()
	notes[id] = &Note{ID: id, Title: in.Title, Body: in.Body, CreatedAt: now, UpdatedAt: now}
	return fmt.Sprintf("Created note %s: %q", id, in.Title), nil
}

func handleList(_ context.Context, _ struct{}) (string, error) {
	mu.RLock()
	defer mu.RUnlock()
	if len(notes) == 0 {
		return "No notes yet.", nil
	}
	out := fmt.Sprintf("%d note(s):\n", len(notes))
	for _, n := range sortedNotes() {
		out += fmt.Sprintf("  [%s] %s  (created %s)\n", n.ID, n.Title, n.CreatedAt.Format(time.RFC3339))
	}
	return out, nil
}

func handleGet(_ context.Context, in struct {
	ID string `json:"id" description:"Note ID (e.g. note_1)"`
}) (string, error) {
	mu.RLock()
	n, ok := notes[in.ID]
	mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("note %q not found", in.ID)
	}
	return n.format(), nil
}

func handleUpdate(_ context.Context, in struct {
	ID    string `json:"id"    description:"Note ID to update"`
	Title string `json:"title" description:"New title (omit to keep current)"`
	Body  string `json:"body"  description:"New body (omit to keep current)"`
}) (string, error) {
	if in.Title == "" && in.Body == "" {
		return "", fmt.Errorf("provide at least one of: title, body")
	}
	mu.Lock()
	defer mu.Unlock()
	n, ok := notes[in.ID]
	if !ok {
		return "", fmt.Errorf("note %q not found", in.ID)
	}
	if in.Title != "" {
		for _, other := range notes {
			if other.ID != in.ID && strings.EqualFold(other.Title, in.Title) {
				return "", fmt.Errorf("a note titled %q already exists (id: %s)", other.Title, other.ID)
			}
		}
		n.Title = in.Title
	}
	if in.Body != "" {
		n.Body = in.Body
	}
	n.UpdatedAt = time.Now()
	return fmt.Sprintf("Updated note %s.", n.ID), nil
}

func handleDelete(_ context.Context, in struct {
	ID string `json:"id" description:"Note ID to delete"`
}) (string, error) {
	mu.Lock()
	_, ok := notes[in.ID]
	if ok {
		delete(notes, in.ID)
	}
	mu.Unlock()
	if !ok {
		return "", fmt.Errorf("note %q not found", in.ID)
	}
	return fmt.Sprintf("Deleted note %s.", in.ID), nil
}

func handleSearch(_ context.Context, in struct {
	Query string `json:"query" description:"Keyword (case-insensitive, matches title or body)"`
}) (string, error) {
	if in.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	q := strings.ToLower(in.Query)
	mu.RLock()
	defer mu.RUnlock()
	var matched []*Note
	for _, n := range notes {
		if strings.Contains(strings.ToLower(n.Title), q) || strings.Contains(strings.ToLower(n.Body), q) {
			matched = append(matched, n)
		}
	}
	if len(matched) == 0 {
		return fmt.Sprintf("No notes matched %q.", in.Query), nil
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})
	out := fmt.Sprintf("%d note(s) matched %q:\n", len(matched), in.Query)
	for _, n := range matched {
		out += fmt.Sprintf("  [%s] %s  (created %s)\n", n.ID, n.Title, n.CreatedAt.Format(time.RFC3339))
	}
	return out, nil
}

func main() {
	s := finemcp.NewServer("notes-server", "1.0.0")

	createTool, err := finemcp.NewTypedTool("note_create", handleCreate,
		finemcp.WithDescription("Create a note. Title and body are required; titles must be unique."))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(createTool)

	listTool, err := finemcp.NewTypedTool("note_list", handleList,
		finemcp.WithDescription("List all notes sorted by creation time (oldest first)."))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(listTool)

	getTool, err := finemcp.NewTypedTool("note_get", handleGet,
		finemcp.WithDescription("Retrieve a single note by ID."))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(getTool)

	updateTool, err := finemcp.NewTypedTool("note_update", handleUpdate,
		finemcp.WithDescription("Update the title and/or body of an existing note."))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(updateTool)

	deleteTool, err := finemcp.NewTypedTool("note_delete", handleDelete,
		finemcp.WithDescription("Delete a note by ID."))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(deleteTool)

	searchTool, err := finemcp.NewTypedTool("note_search", handleSearch,
		finemcp.WithDescription("Search notes by keyword (case-insensitive, title or body)."))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(searchTool)

	if len(os.Args) >= 2 && os.Args[1] == "--sse" {
		addr := ":8081"
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		log.Printf("notes-server v1.0.0 | SSE listening on %s/sse", addr)
		log.Fatal(transport.StartSSE(s, addr))
	} else {
		log.Print("notes-server v1.0.0 | stdio ready (waiting for JSON-RPC on stdin)")
		if err := transport.ServeStdio(context.Background(), s); err != nil {
			log.Fatal(err)
		}
	}
}
