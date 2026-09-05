package mcpsrv

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/core"
)

// secretFields are the map keys whose values are secret by construction. A
// recipe's manifest type is what makes a param secret, and wire already
// renders those as <set> or <unset>; this middleware is the second layer, so
// a DTO that forgets cannot leak.
var secretFields = []string{"secrets", "console_password", "authkey", "password", "token"}

// redact wraps the receiving handler for tools/call: AddSendingMiddleware
// only sees requests the server itself initiates (sampling, elicitation),
// never a CallToolResult built for an incoming request, so this is receiving
// middleware even though the redaction happens on the way out. It runs
// closest to the handler, after every registered tool has built its result,
// so a DTO built anywhere in this package is covered without every handler
// remembering.
func (s *srv) redact() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			ctr, ok := res.(*mcp.CallToolResult)
			if !ok || ctr == nil {
				return res, err
			}
			if ctr.StructuredContent != nil {
				ctr.StructuredContent = redactValue(ctr.StructuredContent)
			}
			// AddTool's wrapper fills Content with a TextContent fallback
			// holding the full unredacted JSON of an object-shaped Out,
			// before this middleware runs (SDK v1.7.0, mcp/server.go:435-443).
			// StructuredContent alone is not enough: the fallback text must
			// be redacted too.
			for _, c := range ctr.Content {
				tc, ok := c.(*mcp.TextContent)
				if !ok {
					continue
				}
				tc.Text = redactText(tc.Text)
			}
			return ctr, err
		}
	}
}

// redactText re-marshals a JSON text block through redactValue. Text that is
// not JSON, such as an error message, passes through unchanged.
func redactText(text string) string {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return text
	}
	raw, err := json.Marshal(redactValue(v))
	if err != nil {
		return text
	}
	return string(raw)
}

// redactValue walks a value as JSON and replaces every secret field. The
// round trip through JSON is what makes one walk cover every DTO shape,
// including a map a future tool returns.
func redactValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return v
	}
	return walk(generic)
}

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSecretField(k) {
				t[k] = maskSecret(val)
				continue
			}
			t[k] = walk(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = walk(val)
		}
		return t
	}
	return v
}

func isSecretField(k string) bool {
	for _, f := range secretFields {
		if k == f {
			return true
		}
	}
	return false
}

// maskSecret keeps the shape and drops the value, so a caller can still see
// which secrets a recipe declares and which of them are set.
func maskSecret(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = maskSecret(val)
		}
		return out
	case nil:
		return core.SecretUnset
	case string:
		if t == "" {
			return core.SecretUnset
		}
		return core.SecretSet
	}
	return core.SecretSet
}
