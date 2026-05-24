package finemcp

import (
	"context"
	"encoding/json"
)

// LogLevel represents the logging level for the server.
// Values follow RFC 5424 (syslog) severity levels as required by the MCP spec.
type LogLevel string

// LogLevel constants follow the RFC 5424 syslog severity levels as required by the MCP spec,
// ordered from least severe (debug) to most severe (emergency).
const (
	LogLevelDebug     LogLevel = "debug"     // RFC 5424 severity 7
	LogLevelInfo      LogLevel = "info"      // RFC 5424 severity 6
	LogLevelNotice    LogLevel = "notice"    // RFC 5424 severity 5
	LogLevelWarning   LogLevel = "warning"   // RFC 5424 severity 4
	LogLevelError     LogLevel = "error"     // RFC 5424 severity 3
	LogLevelCritical  LogLevel = "critical"  // RFC 5424 severity 2
	LogLevelAlert     LogLevel = "alert"     // RFC 5424 severity 1
	LogLevelEmergency LogLevel = "emergency" // RFC 5424 severity 0
)

// validLogLevels is the set of valid log levels per RFC 5424.
// Defined at package level to avoid allocation on every logging/setLevel request.
var validLogLevels = map[LogLevel]bool{
	LogLevelDebug:     true,
	LogLevelInfo:      true,
	LogLevelNotice:    true,
	LogLevelWarning:   true,
	LogLevelError:     true,
	LogLevelCritical:  true,
	LogLevelAlert:     true,
	LogLevelEmergency: true,
}

const (
	methodLoggingSetLevel = "logging/setLevel"
	methodLoggingMessage  = "notifications/message"
)

// SetLevelParams is the client's payload for the "logging/setLevel" request.
type SetLevelParams struct {
	Level LogLevel `json:"level"`
}

// SetLevelResult is the server's response to a "logging/setLevel" request.
type SetLevelResult struct{}

// LogMessageParams is the server's payload for the "notifications/message" notification.
type LogMessageParams struct {
	Level  LogLevel `json:"level"`
	Logger string   `json:"logger,omitempty"`
	Data   any      `json:"data"`
}

// LogHandler is called when the client sends a logging/setLevel request.
// The handler receives the requested log level and can adjust logging behavior.
type LogHandler func(ctx context.Context, level LogLevel) error

// SendLogMessage sends a notifications/message notification to the client
// with the provided log level and message data.
// Returns errNotInitialized if the handshake hasn't completed, since the
// protocol version and client capabilities are unknown until then.
func (s *Server) SendLogMessage(ctx context.Context, level LogLevel, logger string, data any) error {
	if !s.initialized.Load() {
		return errNotInitialized
	}

	sender := NotificationSenderFromCtx(ctx)
	if sender == nil {
		return errNoNotificationSender
	}

	notification := &JSONRPCNotification{
		JSONRPC: jsonrpcVersion,
		Method:  methodLoggingMessage,
		Params: LogMessageParams{
			Level:  level,
			Logger: logger,
			Data:   data,
		},
	}

	sender(notification)
	return nil
}

// SetLogHandler registers a handler for logging/setLevel requests.
// The handler is called when clients send logging/setLevel requests.
func (s *Server) SetLogHandler(handler LogHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logHandler = handler
}

// handleLoggingSetLevel processes a logging/setLevel request.
func (s *Server) handleLoggingSetLevel(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	var p SetLevelParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid params"), nil
	}

	// Validate log level against the package-level set.
	if !validLogLevels[p.Level] {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid log level"), nil
	}

	s.mu.RLock()
	handler := s.logHandler
	s.mu.RUnlock()

	if handler != nil {
		if err := handler(ctx, p.Level); err != nil {
			return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
		}
	}

	return NewResponse(req.ID, SetLevelResult{}), nil
}
