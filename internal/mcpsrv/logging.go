package mcpsrv

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/novusedge/stoat/internal/logx"
)

// logCalls records every tools/call in the stoat log. It is the outermost
// middleware, so it times a rate-limit refusal too and it reads the result
// after redact has masked it.
//
// It logs the tool name and the vm, never the arguments: update carries
// recipe secrets and useradd carries a password, and neither belongs in an
// append-only file.
func (s *srv) logCalls() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			name, vm := "unknown", ""
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
				name = p.Name
				vm = vmArg(p.Arguments)
			}
			start := time.Now()
			res, err := next(ctx, method, req)

			fields := []any{"tool", name}
			if vm != "" {
				fields = append(fields, "vm", vm)
			}
			fields = append(fields, "ms", time.Since(start).Milliseconds())
			switch {
			case err != nil:
				logx.L().Error("mcp call failed", append(fields, "err", err)...)
			case toolFailed(res):
				logx.L().Warn("mcp call refused", append(fields, "err", toolErrorText(res))...)
			default:
				logx.L().Info("mcp call", fields...)
			}
			return res, err
		}
	}
}

// vmArg pulls the vm name out of raw tool arguments. Most tools take one and
// it is the field that makes a log line searchable. Arguments that do not
// parse give "", because a log line must not turn a bad request into a
// failure of its own.
func vmArg(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var args struct {
		VM string `json:"vm"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	return args.VM
}

// toolFailed reports a tool that refused. A handler returns its failure as a
// CallToolResult with IsError set, not as a Go error, so the outcome is on
// the result.
func toolFailed(res mcp.Result) bool {
	ctr, ok := res.(*mcp.CallToolResult)
	return ok && ctr != nil && ctr.IsError
}

// toolErrorText returns the refusal message toolError put in the first
// content block.
func toolErrorText(res mcp.Result) string {
	ctr, ok := res.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		return ""
	}
	for _, c := range ctr.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
