package finemcp

import "errors"

var (
	errToolNameEmpty   = errors.New("tool name must not be empty")
	errToolNameTooLong = errors.New("tool name must be between 1 and 128 characters")
	errToolNameChars   = errors.New("tool name contains invalid characters; allowed: A-Z, a-z, 0-9, _, -, .")
	errToolHandlerNil  = errors.New("tool handler must not be nil")
)

// validToolNameBitmap is a 256-bit bitmap (4 × uint64 = 32 bytes).
// Bit N is set if byte value N is a valid tool name character.
//
// Word 0 (ASCII 0-63):   '-'=45, '.'=46, '0'-'9'=48-57
// Word 1 (ASCII 64-127): 'A'-'Z'=65-90, '_'=95, 'a'-'z'=97-122
// Word 2-3: unused (no valid chars above 127)
var validToolNameBitmap = [4]uint64{
	0x03FF600000000000, // -  .  0-9
	0x07FFFFFE87FFFFFE, // A-Z  _  a-z
	0,
	0,
}

// checks a tool name against MCP spec rules.
func validateToolName(name string) error {
	if name == "" {
		return errToolNameEmpty
	}

	// upper bound per MCP spec.
	if len(name) > 128 {
		return errToolNameTooLong
	}

	for i := 0; i < len(name); i++ {
		c := name[i]
		if validToolNameBitmap[c>>6]>>(c&63)&1 == 0 {
			return errToolNameChars
		}
	}

	return nil
}

// validateHandler returns an error if handler is nil.
func validateHandler(handler ToolHandler) error {
	if handler == nil {
		return errToolHandlerNil
	}
	return nil
}
