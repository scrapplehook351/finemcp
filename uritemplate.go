package finemcp

import (
	"strings"
	"unicode"
)

// maxBacktrackSteps limits the number of recursive match attempts in the
// template matcher to prevent exponential blow-up on pathological inputs
// (e.g., many consecutive placeholders with a non-matching trailing literal).
const maxBacktrackSteps = 10_000

// uriTemplateMatches reports whether a concrete URI matches an RFC 6570-style
// URI template. It implements a conservative subset of RFC 6570 suitable for
// the resource templates used by FineMCP:
//
//   - {var} and {+var} placeholders are treated as wildcards that match one or
//     more non-"/" characters within a path segment.
//   - Literal characters must match exactly (no case folding).
//   - Templates with malformed braces never match.
//   - Percent-encoding is not handled: URIs and templates are compared as
//     literal rune sequences. Handlers that receive expanded URIs may need
//     to percent-decode values themselves.
//
// Supported operators:
//   - {var}  — simple string expansion
//   - {+var} — reserved expansion (treated identically to {var} for matching)
//
// NOT supported (rejected at validation time):
//   - Query expansion:   {?var,list}
//   - Fragment:          {#section}
//   - Path segment:      {/var,x}
//   - Explode:           {var*}
//   - Prefix:            {var:3}
//
// Examples of supported patterns:
//
//	"file:///logs/{date}.log"
//	"https://api.example.com/repos/{owner}/{repo}/issues/{id}"
func uriTemplateMatches(tmpl, uri string) bool {
	// Empty template matches only an empty URI.
	if tmpl == "" {
		return uri == ""
	}
	// Pre-parse the template into literal and placeholder segments so the
	// recursive matcher never needs to re-scan brace positions or allocate
	// strings from the template.
	segs := parseTemplate(tmpl)
	if segs == nil {
		return false // malformed template
	}
	budget := maxBacktrackSteps
	return matchSegs(segs, []rune(uri), 0, 0, &budget)
}

// matchesParsedTemplate reports whether uri matches the given pre-parsed
// template segments. Used by findTemplateForURI to avoid re-parsing the
// template on every match attempt.
func matchesParsedTemplate(segs []segment, uri string) bool {
	if segs == nil {
		return false
	}
	budget := maxBacktrackSteps
	return matchSegs(segs, []rune(uri), 0, 0, &budget)
}

// segment represents either a literal run or a placeholder in a parsed
// URI template. Exactly one of literal or isPlaceholder is meaningful.
type segment struct {
	literal       []rune // non-empty for literal segments
	isPlaceholder bool   // true for {var} / {+var} segments
}

// parseTemplate splits a URI template string into alternating literal and
// placeholder segments. Returns nil if the template contains malformed braces
// or empty placeholders. This parse is done once per match call, so the
// recursive matcher doesn't need to re-scan for braces.
func parseTemplate(tmpl string) []segment {
	runes := []rune(tmpl)
	var segs []segment
	i := 0
	for i < len(runes) {
		if runes[i] == '{' {
			end := i + 1
			for end < len(runes) && runes[end] != '}' {
				end++
			}
			if end >= len(runes) {
				return nil // unclosed brace
			}
			inner := string(runes[i+1 : end])
			if inner == "" {
				return nil
			}
			if inner[0] == '+' {
				inner = inner[1:]
			}
			if strings.TrimSpace(inner) == "" {
				return nil
			}
			segs = append(segs, segment{isPlaceholder: true})
			i = end + 1
		} else {
			start := i
			for i < len(runes) && runes[i] != '{' {
				i++
			}
			segs = append(segs, segment{literal: runes[start:i]})
		}
	}
	return segs
}

// matchSegs is the recursive core of uriTemplateMatches. It operates on
// pre-parsed segments (avoiding repeated brace scanning) and uses a shared
// budget counter to cap the total number of recursive calls at
// maxBacktrackSteps, preventing exponential blow-up on pathological inputs.
func matchSegs(segs []segment, uri []rune, si, uj int, budget *int) bool {
	for si < len(segs) {
		seg := segs[si]

		if !seg.isPlaceholder {
			// Literal segment: every rune must match exactly.
			for _, ch := range seg.literal {
				if uj >= len(uri) || uri[uj] != ch {
					return false
				}
				uj++
			}
			si++
			continue
		}

		// Placeholder segment: consume one or more non-'/' runes.
		si++

		// If this is the last segment, the placeholder must eat all remaining
		// runes and none may be '/'.
		if si >= len(segs) {
			if uj >= len(uri) {
				return false // must consume ≥1
			}
			for k := uj; k < len(uri); k++ {
				if uri[k] == '/' {
					return false
				}
			}
			return true
		}

		// Determine the first rune of the next segment for pruning.
		// If the next segment is another placeholder, nextLit is 0 (try all splits).
		var nextLit rune
		nextSeg := segs[si]
		if !nextSeg.isPlaceholder && len(nextSeg.literal) > 0 {
			nextLit = nextSeg.literal[0]
		}

		// Try every split point: placeholder consumes URI[uj..start),
		// then the remainder must match from start onward.
		for start := uj + 1; start <= len(uri); start++ {
			// Do not allow the wildcard to span path separators.
			if uri[start-1] == '/' {
				break
			}

			*budget -= 1
			if *budget <= 0 {
				return false // backtrack budget exhausted
			}

			// Prune: if the next segment is a literal, only recurse when
			// the URI at this position matches its first rune. For a
			// following placeholder (nextLit==0), always try.
			if nextLit != 0 {
				if start >= len(uri) || uri[start] != nextLit {
					continue
				}
			}
			if matchSegs(segs, uri, si, start, budget) {
				return true
			}
		}
		return false
	}

	return uj == len(uri)
}

// isValidURITemplate checks that a URI template string has balanced braces,
// non-empty placeholder names, no nested braces, and no unsupported RFC 6570
// operators. Used at registration time and in NewResourceTemplate to fail
// fast on malformed or unsupported templates.
//
// Variable names must start with a letter, digit, or underscore and may
// contain letters, digits, underscores, and dots (dots are permitted after
// the first character, e.g., {var.name}). A leading '+' is allowed for
// reserved expansion. Names starting with '#', '/', '.', ';', '?', '&' or
// containing '*', ':' are rejected (unsupported operators).
func isValidURITemplate(tmpl string) bool {
	depth := 0
	for _, ch := range tmpl {
		switch ch {
		case '{':
			if depth > 0 {
				return false // nested braces
			}
			depth++
		case '}':
			if depth == 0 {
				return false // unmatched close
			}
			depth--
		}
	}
	if depth != 0 {
		return false // unclosed brace
	}

	// Verify every placeholder has a valid variable name.
	rest := tmpl
	for rest != "" {
		open := strings.IndexRune(rest, '{')
		if open < 0 {
			break
		}
		close := strings.IndexRune(rest[open:], '}')
		if close < 0 {
			return false
		}
		inner := rest[open+1 : open+close]
		if inner == "" {
			return false
		}
		// Strip leading '+' for reserved expansion operator.
		if inner[0] == '+' {
			inner = inner[1:]
		}
		if !isValidVarName(inner) {
			return false
		}
		rest = rest[open+close+1:]
	}
	return true
}

// isValidVarName checks that s is a non-empty variable name that starts with
// a letter, digit, or underscore and then contains only letters, digits,
// underscores, and dots. This rejects unsupported RFC 6570 operators
// (e.g., '#', '/', '?', '&', ';', '*', ':', and leading '.').
func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			// First character: must be letter, digit, or underscore.
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		} else {
			// Subsequent characters: also allow dots.
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' {
				return false
			}
		}
	}
	return true
}
