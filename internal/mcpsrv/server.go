package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
)

// class is a tool's annotation class. The four classes are the taxonomy in
// docs/design/mcp-server.md; toolTable in table_test.go assigns one per tool
// and TestAnnotationsMatchTable asserts the mapping.
type class int

const (
	classRead class = iota
	classMutate
	classDestructive
	classExec
)

type toolSpec struct {
	Name   string
	Class  class
	Access Level
}

// Options configures a server. Version is the stoat build version, reported
// in the MCP handshake.
type Options struct {
	Version string
	Limits  Limits
}

const (
	maxLogLines   = 2000
	maxWaitSecs   = 600
	maxReadBytes  = 1 << 20
	maxDirEntries = 2000
	maxPSRows     = 2000
	maxExecSecs   = 600
)

// srv carries the per-process state the handlers need. It is a value on the
// closure each handler captures rather than a global, so a test can build
// two servers with different limits.
type srv struct {
	opts Options
	lim  *limiter
}

// New builds the server with every tool registered. The caller runs it over
// a transport.
func New(opts Options) *mcp.Server {
	if opts.Limits.ToolBurst == 0 {
		opts.Limits = DefaultLimits()
	}
	s := &srv{opts: opts, lim: newLimiter(opts.Limits)}
	server := mcp.NewServer(&mcp.Implementation{Name: "stoat", Version: opts.Version}, nil)
	s.registerRead(server)
	s.registerVM(server)
	s.registerRecipe(server)
	s.registerGuestRead(server)
	s.registerGuestWrite(server)
	s.registerExec(server)
	// redact must be receiving middleware, not sending: a Server's sending
	// method handler only covers requests the server itself initiates
	// (sampling, elicitation), never the CallToolResult built for an
	// incoming tools/call request. See redact.go.
	server.AddReceivingMiddleware(s.rateLimit(), s.redact())
	return server
}

func ptr[T any](v T) *T { return &v }

// annotationsFor renders a class as MCP annotations. DestructiveHint and
// OpenWorldHint are pointers and the MCP default for destructiveHint is
// true, so a non-destructive tool sets an explicit false rather than nil.
func annotationsFor(c class) *mcp.ToolAnnotations {
	switch c {
	case classRead:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
	case classMutate:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
	case classDestructive:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptr(true), OpenWorldHint: ptr(false)}
	case classExec:
		return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}
	}
	panic(fmt.Sprintf("unknown class %d", c))
}

// register adds one tool. In is always a struct so the generated schema sets
// additionalProperties:false, and Out is always a wire DTO so the --json
// contract and the MCP schema are one set of types. c must match the class
// toolTable (in table_test.go, a test fixture the production build cannot
// import) assigns this tool's name; TestAnnotationsMatchTable checks that.
func register[In, Out any](server *mcp.Server, name string, c class, description string, h func(context.Context, In) (Out, error)) {
	tool := &mcp.Tool{Name: name, Description: description, Annotations: annotationsFor(c)}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := h(ctx, in)
		if err != nil {
			return toolError(err), zeroOut[Out](), nil
		}
		return nil, out, nil
	})
}

// zeroOut builds Out's zero value with every nil map and slice field
// replaced by an empty one. The SDK validates a tool's output against its
// generated schema even on the error path, and a field without an
// `omitempty` tag is required as an object or array there; Out's bare zero
// value carries nil for those and fails that check.
func zeroOut[Out any]() Out {
	var out Out
	fillEmpty(reflect.ValueOf(&out).Elem())
	return out
}

func fillEmpty(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillEmpty(v.Field(i))
			}
		}
	case reflect.Map:
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
	case reflect.Slice:
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			fillEmpty(v.Elem())
		}
	}
}

// toolError renders an error the way the CLI prints it. wire.MapError gives
// the same text a --json result line carries, so an agent reading a tool
// failure and a user reading the CLI see one message.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: wire.MapError(err).Message}},
	}
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

// ServeStdio runs the server over stdio, which is how every MCP client
// launches a server as a subprocess.
func ServeStdio(ctx context.Context, opts Options) error {
	return New(opts).Run(ctx, &mcp.StdioTransport{})
}

// CheckLoopback refuses a bind that is not loopback. This server has no
// authentication, so the bind is the boundary.
func CheckLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("address %q binds every interface; use 127.0.0.1", addr)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("address %q is not loopback; this server has no authentication", addr)
	}
	return nil
}

// ServeHTTP runs the server over streamable HTTP for a client that cannot
// launch a subprocess. One server instance serves every request, so the
// rate limiter's buckets are shared across connections.
func ServeHTTP(ctx context.Context, addr string, opts Options) error {
	if err := CheckLoopback(addr); err != nil {
		return err
	}
	server := New(opts)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	hs := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
