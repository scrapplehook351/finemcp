package finemcp

import "encoding/json"

// ProtocolVersion is the latest MCP protocol version this server supports.
// Format is YYYY-MM-DD (e.g., "2025-11-25").
// This is the preferred version used in negotiation when the client's
// requested version is not in the server's supported set.
// Must always equal defaultSupportedVersions[0]; the init() guard below
// panics at program start if they drift.
const ProtocolVersion = "2025-11-25"

// defaultSupportedVersions is the canonical list of supported protocol
// versions, ordered from latest (preferred) to oldest.
var defaultSupportedVersions = []string{
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

func init() {
	if len(defaultSupportedVersions) == 0 ||
		ProtocolVersion != defaultSupportedVersions[0] {
		panic("finemcp: ProtocolVersion constant must equal defaultSupportedVersions[0]")
	}
}

// DefaultSupportedVersions returns all MCP protocol versions the server
// supports by default, ordered from latest to oldest. The first entry is
// the preferred version. A fresh copy is returned each time.
func DefaultSupportedVersions() []string {
	cp := make([]string, len(defaultSupportedVersions))
	copy(cp, defaultSupportedVersions)
	return cp
}

// MCP method constants.
const (
	methodInitialize             = "initialize"
	methodInitialized            = "notifications/initialized"
	methodToolsList              = "tools/list"
	methodToolsCall              = "tools/call"
	methodResourcesList          = "resources/list"
	methodResourcesRead          = "resources/read"
	methodResourcesTemplatesList = "resources/templates/list"
	methodRootsList              = "roots/list"
	methodPromptsList            = "prompts/list"
	methodPromptsGet             = "prompts/get"
	methodPing                   = "ping"
	methodProgress               = "notifications/progress"
	methodCancelled              = "notifications/cancelled"

	// Resource subscription methods.
	methodResourcesSubscribe   = "resources/subscribe"
	methodResourcesUnsubscribe = "resources/unsubscribe"

	// Completion method.
	methodCompletionComplete = "completion/complete"

	// Task methods.
	methodTasksGet    = "tasks/get"
	methodTasksResult = "tasks/result"
	methodTasksCancel = "tasks/cancel"
	methodTasksList   = "tasks/list"

	// Server-to-client list-changed notifications.
	methodResourcesUpdated     = "notifications/resources/updated"
	methodToolsListChanged     = "notifications/tools/list_changed"
	methodResourcesListChanged = "notifications/resources/list_changed"
	methodPromptsListChanged   = "notifications/prompts/list_changed"
	methodRootsListChanged     = "notifications/roots/list_changed"
)

// Exported MCP method constants for use by the client package and external consumers.
const (
	MethodInitialize             = methodInitialize
	MethodInitialized            = methodInitialized
	MethodToolsList              = methodToolsList
	MethodToolsCall              = methodToolsCall
	MethodResourcesList          = methodResourcesList
	MethodResourcesRead          = methodResourcesRead
	MethodResourcesTemplatesList = methodResourcesTemplatesList
	MethodRootsList              = methodRootsList
	MethodPromptsList            = methodPromptsList
	MethodPromptsGet             = methodPromptsGet
	MethodPing                   = methodPing
	MethodProgress               = methodProgress
	MethodCancelled              = methodCancelled
	MethodResourcesSubscribe     = methodResourcesSubscribe
	MethodResourcesUnsubscribe   = methodResourcesUnsubscribe
	MethodCompletionComplete     = methodCompletionComplete
	MethodTasksGet               = methodTasksGet
	MethodTasksResult            = methodTasksResult
	MethodTasksCancel            = methodTasksCancel
	MethodTasksList              = methodTasksList
	MethodResourcesUpdated       = methodResourcesUpdated
	MethodToolsListChanged       = methodToolsListChanged
	MethodResourcesListChanged   = methodResourcesListChanged
	MethodPromptsListChanged     = methodPromptsListChanged
	MethodRootsListChanged       = methodRootsListChanged
	MethodSamplingCreateMessage  = methodSamplingCreateMessage
	MethodElicitationCreate      = methodElicitationCreate
	MethodLoggingSetLevel        = methodLoggingSetLevel
	MethodLoggingMessage         = methodLoggingMessage
)

// InitializeParams is the client's payload for the "initialize" request.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    ClientCaps     `json:"capabilities"`
	ClientInfo      ProcessInfo    `json:"clientInfo"`
	Meta            map[string]any `json:"_meta,omitempty"`
}

// ClientCaps describes the client's declared capabilities.
type ClientCaps struct {
	// Sampling indicates the client supports sampling/createMessage requests.
	// When non-nil, the server may send LLM sampling requests to the client.
	Sampling *SamplingCapability `json:"sampling,omitempty"`

	// Elicitation indicates the client supports elicitation/create requests.
	// When non-nil, the server may send user prompt requests to the client.
	Elicitation *ElicitationCapability `json:"elicitation,omitempty"`
}

// SamplingCapability signals that the client supports sampling/createMessage.
// Per the MCP spec, the capability is an empty object when present.
type SamplingCapability struct{}

// ElicitationCapability signals that the client supports elicitation/create.
// Per the MCP spec, the capability is an empty object when present.
type ElicitationCapability struct{}

// ProgressParams is the server's payload for the "notifications/progress" notification.
type ProgressParams struct {
	// ProgressToken correlates the notification with the originating request.
	// When the client sends _meta.progressToken in the request, the server
	// uses that value here; otherwise it defaults to the JSON-RPC request ID.
	ProgressToken any `json:"progressToken"`

	// Progress is the current progress value (e.g. number of items processed).
	Progress float64 `json:"progress"`

	// Total is the total expected value. When > 0, clients can show a percentage.
	// Zero means indeterminate progress.
	Total float64 `json:"total,omitempty"`

	// Content holds optional content blocks embedded in this progress notification.
	// Streaming-capable servers populate this field to allow clients to surface
	// partial tool-result content before the final tools/call response arrives.
	// This field is omitted by servers that do not support streaming; existing
	// progress consumers are unaffected by its presence or absence.
	Content []json.RawMessage `json:"content,omitempty"`
}

// JSONRPCNotification is an outgoing server-to-client JSON-RPC 2.0 notification.
// Notifications have no "id" field and expect no response.
type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// CancelledParams is the client's payload for the "notifications/cancelled" notification.
type CancelledParams struct {
	RequestID any            `json:"requestId"` // can be string or number
	Meta      map[string]any `json:"_meta,omitempty"`
}

// InitializeResult is the server's response to an "initialize" request.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ProcessInfo        `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
	Meta            map[string]any     `json:"_meta,omitempty"`
}

// ServerCapabilities advertises what the server supports.
type ServerCapabilities struct {
	Tools       *ToolCapability       `json:"tools,omitempty"`
	Resources   *ResourceCapability   `json:"resources,omitempty"`
	Prompts     *PromptCapability     `json:"prompts,omitempty"`
	Tasks       *TaskCapability       `json:"tasks,omitempty"`
	Completions *CompletionCapability `json:"completions,omitempty"`
	Logging     *LoggingCapability    `json:"logging,omitempty"`
}

// ToolCapability describes the server's tool-related capabilities.
type ToolCapability struct {
	// ListChanged indicates whether the server will emit tools/list_changed notifications.
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourceCapability describes the server's resource-related capabilities.
type ResourceCapability struct {
	// Subscribe indicates whether the server supports resource subscriptions.
	Subscribe bool `json:"subscribe,omitempty"`
	// ListChanged indicates whether the server will emit resources/list_changed notifications.
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptCapability describes the server's prompt-related capabilities.
type PromptCapability struct {
	// ListChanged indicates whether the server will emit prompts/list_changed notifications.
	ListChanged bool `json:"listChanged,omitempty"`
}

// CompletionCapability signals that the server supports completion/complete.
// Per the MCP spec, the capability is an empty object when present.
type CompletionCapability struct{}

// LoggingCapability signals that the server supports logging/setLevel and
// may emit notifications/message notifications.
// Per the MCP spec, the capability is an empty object when present.
type LoggingCapability struct{}

// Icon represents an icon associated with an MCP server or client.
// Per the MCP spec, icons can be used by clients (e.g. Claude Desktop)
// to display a visual representation of the server.
type Icon struct {
	Src      string   `json:"src"`
	MimeType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
}

// ProcessInfo identifies an MCP endpoint (client or server).
type ProcessInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	WebsiteURL  string `json:"websiteUrl,omitempty"`
	Icons       []Icon `json:"icons,omitempty"`
}

// ToolInfo is the wire representation of a tool in a tools/list response.
type ToolInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	InputSchema any              `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ListParams contains pagination parameters for any list request
// (tools/list, resources/list, prompts/list, resources/templates/list).
//
// Cursor is an opaque server-defined token identifying the starting position
// for the page. Limit is the maximum number of items to return per page
// (default 50; clamped to 1000).
//
// Backward compatibility: when both Cursor and Limit are omitted or zero-valued
// (e.g., {} or null params), the server returns the full unpaginated list.
// To request the first page with the default size, send {"limit": 50}
// explicitly, or any non-zero limit without a cursor.
//
// When pagination is active, the server returns a window of items starting at
// the given cursor, limited to at most Limit items, along with a nextCursor
// in the response when more items are available.
type ListParams struct {
	Cursor string         `json:"cursor,omitempty"`
	Limit  int            `json:"limit,omitempty"`
	Meta   map[string]any `json:"_meta,omitempty"`
}

// ListToolsResult is the server's response to a "tools/list" request.
type ListToolsResult struct {
	Tools      []ToolInfo     `json:"tools"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// RootInfo is the wire representation of a root in a roots/list response.
type RootInfo struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// ListRootsResult is the server's response to a "roots/list" request.
type ListRootsResult struct {
	Roots      []RootInfo     `json:"roots"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// CallToolParams is the client's payload for a "tools/call" request.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Task      *TaskMetadata   `json:"task,omitempty"`
	Meta      map[string]any  `json:"_meta,omitempty"`
}

// SubscribeParams is the client's payload for "resources/subscribe" and
// "resources/unsubscribe" requests.
type SubscribeParams struct {
	URI  string         `json:"uri"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// ResourceUpdatedParams is the server's payload for the
// "notifications/resources/updated" notification.
type ResourceUpdatedParams struct {
	URI string `json:"uri"`
}
