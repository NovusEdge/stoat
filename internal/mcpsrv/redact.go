package mcpsrv

import "github.com/modelcontextprotocol/go-sdk/mcp"

// redact is sending middleware over tool results. Task 13's implementer
// fills this in to walk StructuredContent and mask secret fields with
// core.SecretSet or core.SecretUnset.
func (s *srv) redact() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return next
	}
}

// redactValue is a stub: it returns v unchanged. Task 13's implementer
// replaces this with the JSON walk that masks secret fields.
func redactValue(v any) any {
	return v
}
