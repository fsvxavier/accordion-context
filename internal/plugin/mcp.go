package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// protocolVersions lists the MCP revisions the server speaks, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// mcpToolNames are the tools the MCP companion exposes (spec §19.3, mode 5).
var mcpToolNames = map[string]bool{
	"decompress": true, "retrieve": true, "search_context": true, ctxStatusToolName: true,
}

const mcpInstructions = "Accordion Context: recover folded history with retrieve, decompress or " +
	"search_context, and check usage with ctx_status. The session is the most recent one of this project."

// MCPConfig wires the stdio MCP companion.
type MCPConfig struct {
	Store   *store.Store // required; the caller owns it
	DBPath  string       // required; the same database, opened read-only to find the latest session
	Cwd     string       // project directory; default: the working directory
	Markers []string     // project root markers; default: go.mod, package.json, ...
	Version string
	Logger  *slog.Logger // default: discard. Never stdout: stdout carries the protocol.
}

// errNoSession is returned when the project has no session yet.
var errNoSession = errors.New("no session for this project yet; send a request through Accordion Context first")

var defaultMarkers = []string{"go.mod", "package.json", "pyproject.toml", "composer.json", "Cargo.toml"}

// MCP is a stdio MCP server bound to the project of its working directory.
type MCP struct {
	cfg       MCPConfig
	reader    *reader
	projectID string
	// backend runs one tool call. NewMCP binds it to the database; tests and the
	// fuzz target substitute a stub.
	backend func(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// NewMCP resolves the project (read-only) and opens the read-only handle.
func NewMCP(cfg MCPConfig) (*MCP, error) {
	if cfg.Store == nil {
		return nil, errors.New("plugin: mcp store is required")
	}
	if cfg.DBPath == "" {
		return nil, errors.New("plugin: mcp db path is required")
	}
	if cfg.Cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cfg.Cwd = wd
	}
	if len(cfg.Markers) == 0 {
		cfg.Markers = defaultMarkers
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	opts := project.Options{ReadOnly: true}
	if cfg.Store != nil {
		opts.IDByRoot = func(root string) (string, bool) {
			id, err := cfg.Store.ProjectIDByRoot(context.Background(), root)
			return id, err == nil
		}
	}
	res, err := project.ResolveWith(cfg.Cwd, cfg.Markers, opts)
	if err != nil {
		return nil, fmt.Errorf("plugin: resolve project: %w", err)
	}
	rd, err := openReader(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	m := &MCP{cfg: cfg, reader: rd, projectID: res.ID}
	t := tools{st: cfg.Store}
	m.backend = func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		sid, err := rd.latestSession(ctx, m.projectID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNoSession
		}
		if err != nil {
			return nil, err
		}
		return t.call(ctx, session.New(cfg.Store, sid), sid, name, args)
	}
	return m, nil
}

// Close releases the read-only database handle. The store is not closed.
func (m *MCP) Close() error { return m.reader.Close() }

// ProjectID is the project the server is bound to.
func (m *MCP) ProjectID() string { return m.projectID }

// Serve runs the protocol on in/out until in ends or ctx is done.
func (m *MCP) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return serveStdio(ctx, in, out, m.route)
}

// route implements the MCP methods on top of the JSON-RPC layer.
func (m *MCP) route(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, err.Error()}
		}
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if p.ProtocolVersion == v {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "acd-ctx", "version": m.cfg.Version},
			"instructions":    mcpInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]toolSpec, 0, len(mcpToolNames))
		for _, s := range toolSpecs {
			if mcpToolNames[s.Name] {
				list = append(list, s)
			}
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := unmarshalParams(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, err.Error()}
		}
		if p.Name == "" {
			return nil, &rpcError{codeInvalidParams, "name is required"}
		}
		if !mcpToolNames[p.Name] {
			return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
		}
		res, err := m.backend(ctx, p.Name, p.Arguments)
		if err != nil {
			// Tool failures are results with isError, not protocol errors (MCP spec).
			return toolResult(err.Error(), true), nil
		}
		b, err := json.Marshal(res)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(string(b), false), nil
	default:
		if isNotificationMethod(req.Method) {
			return nil, nil
		}
		return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// unmarshalParams decodes params when present. Absent or null params leave v zero.
func unmarshalParams(raw json.RawMessage, v any) error {
	if firstByte(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid params: %v", err)
	}
	return nil
}
