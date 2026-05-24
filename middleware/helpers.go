package middleware

// shouldProcess returns whether a tool should be processed based on
// inclusion/exclusion filters.
//
// Rules:
//   - If include is non-empty, only listed tools match (exclude ignored).
//   - If exclude is non-empty, listed tools are skipped.
//   - If both are empty, all tools match.
func shouldProcess(toolName string, include, exclude map[string]struct{}) bool {
	if len(include) > 0 {
		_, ok := include[toolName]
		return ok
	}
	if len(exclude) > 0 {
		_, ok := exclude[toolName]
		return !ok
	}
	return true
}

// formatPanicValue formats a recovered panic value as a string.
func formatPanicValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case error:
		return val.Error()
	default:
		return "unknown panic"
	}
}
