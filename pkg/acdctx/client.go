package acdctx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/proxy"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const (
	defaultContextLimit = 200_000
	defaultMaxLoop      = 4
	defaultProjectID    = "default"
	libraryMode         = "library"
)

// defaultUpstreams are the origins the middleware compacts when Options.Upstreams
// is empty.
var defaultUpstreams = []string{
	"https://api.anthropic.com",
	"https://api.openai.com",
	"https://generativelanguage.googleapis.com",
}

// Options configures Open, NewTransport and Handler. Zero fields take defaults.
type Options struct {
	// DBPath is the SQLite database file. Required. Its directory is created.
	DBPath string
	// Cwd is the working directory of the agent. It selects the project (spec
	// §18.2). Empty means the process working directory.
	Cwd string
	// ConfigPath is the global configuration file. Empty means the user config path.
	ConfigPath string
	// Model is the model name recorded on the library session.
	Model string
	// ContextLimit is the context window in tokens. Zero means 200000.
	ContextLimit int
	// ConversationID, when set, names the library conversation explicitly. Otherwise
	// the first user message identifies it, as the proxy does.
	ConversationID string

	// Upstreams lists the origins (scheme://host[:port]) whose compaction-eligible
	// requests the middleware and handler compact. Other origins pass through
	// untouched. Empty means the Anthropic, OpenAI and Gemini origins.
	Upstreams []string
	// MaxLoop bounds the server-side tool rounds per request. Zero means 4.
	MaxLoop int
	// ListenHost is an extra Host header name accepted by Handler, for embedding
	// in a server that is not reached through localhost. Empty for none.
	ListenHost string
	// Logger receives diagnostics. Nil discards them.
	Logger *slog.Logger
}

// Client is the library handle: one store, one project and the active conversation.
// It is safe for concurrent use; Turn, Compress, Decompress and Recall are serialized
// per client.
type Client struct {
	mu        sync.Mutex
	closed    bool
	st        *store.Store
	projectID string
	root      string
	cfg       config.Config
	opts      Options
	sessions  map[string]*session.Session
	active    string // store session id of the last Turn
}

// ErrClosed is returned by a Client after Close.
var ErrClosed = errors.New("acdctx: client is closed")

// ErrNoConversation is returned by Compress, Decompress and Recall before the first Turn.
var ErrNoConversation = errors.New("acdctx: no conversation yet; call Turn first")

// Open opens the database, resolves the project of opts.Cwd and loads the
// configuration. The caller must Close the client.
func Open(ctx context.Context, opts Options) (*Client, error) {
	opts = opts.withDefaults()
	if opts.DBPath == "" {
		return nil, errors.New("acdctx: Options.DBPath is required")
	}
	st, err := openStore(opts)
	if err != nil {
		return nil, err
	}
	c, err := openWith(ctx, st, opts)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return c, nil
}

// openStore opens the database of opts, creating its directory.
func openStore(opts Options) (*store.Store, error) {
	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o700); err != nil {
		return nil, fmt.Errorf("acdctx: database directory: %w", err)
	}
	st, err := store.Open(opts.DBPath)
	if err != nil {
		return nil, fmt.Errorf("acdctx: open store: %w", err)
	}
	return st, nil
}

func openWith(ctx context.Context, st *store.Store, opts Options) (*Client, error) {
	cwd := opts.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("acdctx: working directory: %w", err)
		}
		cwd = wd
	}
	res, err := project.ResolveWith(cwd, proxy.ProjectMarkers(), project.Options{
		ReadOnly: true,
		IDByRoot: func(root string) (string, bool) {
			id, err := st.ProjectIDByRoot(ctx, root)
			return id, err == nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("acdctx: project: %w", err)
	}
	if err := st.EnsureProject(ctx, res.ID, res.Root, res.Storage, time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("acdctx: project row: %w", err)
	}
	loaded, err := config.Load(config.Options{GlobalPath: opts.ConfigPath, Root: res.Root})
	if err != nil {
		return nil, fmt.Errorf("acdctx: configuration: %w", err)
	}
	for _, w := range loaded.Warnings {
		opts.Logger.Warn("acdctx: configuration", "detail", w)
	}
	return &Client{
		st: st, projectID: res.ID, root: res.Root, cfg: loaded.Config, opts: opts,
		sessions: map[string]*session.Session{},
	}, nil
}

// Close releases the database. Later calls on the client return ErrClosed.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.st.Close()
}

// ProjectID returns the project id the client uses.
func (c *Client) ProjectID() string { return c.projectID }

// ensureSession returns the wrapped store session for sid, creating its row on first use.
// The caller holds c.mu.
func (c *Client) ensureSession(ctx context.Context, sid string) (*session.Session, error) {
	if s, ok := c.sessions[sid]; ok {
		return s, nil
	}
	if _, err := c.st.Snapshot(ctx, sid); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("acdctx: session: %w", err)
		}
		if err := c.st.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: c.projectID, Model: c.opts.Model, ContextLimit: c.opts.ContextLimit,
			Mode: libraryMode, Integration: libraryMode,
		}); err != nil {
			return nil, fmt.Errorf("acdctx: create session: %w", err)
		}
	}
	s := session.NewWithOptions(c.st, sid, session.Options{})
	c.sessions[sid] = s
	return s, nil
}

func (o Options) withDefaults() Options {
	if o.ContextLimit <= 0 {
		o.ContextLimit = defaultContextLimit
	}
	if o.MaxLoop <= 0 {
		o.MaxLoop = defaultMaxLoop
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if len(o.Upstreams) == 0 {
		o.Upstreams = append([]string(nil), defaultUpstreams...)
	}
	return o
}

// toKernel maps public messages to the kernel view. Tokens are estimated here, as
// the proxy does, and the caller's Tokens field is ignored.
func toKernel(msgs []Message) ([]kernel.Message, error) {
	out := make([]kernel.Message, len(msgs))
	for i, m := range msgs {
		role, err := m.Role.kernel()
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		kind, err := m.Kind.kernel()
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out[i] = kernel.Message{
			Role: role, Kind: kind, ToolName: m.ToolName, ToolCallID: m.ToolCallID,
			Text: m.Text, Tokens: kernel.EstimateTokens(m.Text),
		}
	}
	return out, nil
}

// Turn ingests the transcript msgs (the full conversation so far) and plans the
// turn. tokenCount is the current context size in tokens, as the caller measures
// it; it drives the nudge (spec §8). The returned window is what to send to the
// model; Nudge, when not empty, should be added to the next user message.
func (c *Client) Turn(ctx context.Context, msgs []Message, tokenCount int) (TurnResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return TurnResult{}, ErrClosed
	}
	if len(msgs) == 0 {
		return TurnResult{}, errors.New("acdctx: Turn needs at least one message")
	}
	if tokenCount < 0 {
		return TurnResult{}, errors.New("acdctx: tokenCount must not be negative")
	}
	km, err := toKernel(msgs)
	if err != nil {
		return TurnResult{}, err
	}
	sid := proxy.SessionID(c.projectID, c.opts.ConversationID, km)
	sess, err := c.ensureSession(ctx, sid)
	if err != nil {
		return TurnResult{}, err
	}
	if _, err := sess.Ingest(ctx, km); err != nil {
		return TurnResult{}, fmt.Errorf("acdctx: ingest: %w", err)
	}
	view, err := proxy.PlanTurn(ctx, c.st, sess, sid, c.cfg, tokenCount)
	if err != nil {
		return TurnResult{}, fmt.Errorf("acdctx: plan: %w", err)
	}
	c.active = sid
	return TurnResult{
		Visible: fromKernelMessages(view.Visible),
		Nudge:   view.Nudge,
		Hints:   fromKernelHints(view.Hints),
		Row:     view.Row,
	}, nil
}

// activeSession returns the session of the last Turn. The caller holds c.mu.
func (c *Client) activeSession() (*session.Session, error) {
	if c.closed {
		return nil, ErrClosed
	}
	if c.active == "" {
		return nil, ErrNoConversation
	}
	s, ok := c.sessions[c.active]
	if !ok {
		return nil, ErrNoConversation
	}
	return s, nil
}

// Compress records summaries of finished ranges (spec §10). It applies to the
// conversation of the last Turn. Invalid input writes nothing.
func (c *Client) Compress(ctx context.Context, args CompressArgs) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.activeSession()
	if err != nil {
		return Result{}, err
	}
	in := session.CompressArgs{CloseIntents: append([]int64(nil), args.CloseIntents...)}
	for _, r := range args.Ranges {
		in.Ranges = append(in.Ranges, session.CompressRange{
			StartRef: r.StartRef, EndRef: r.EndRef, Summary: r.Summary, Topic: r.Topic,
		})
	}
	for _, f := range args.Folds {
		in.Folds = append(in.Folds, session.FoldRange{
			Tier: f.Tier, StartBlock: f.StartBlock, EndBlock: f.EndBlock, Summary: f.Summary, Topic: f.Topic,
		})
	}
	res, err := s.HandleCompress(ctx, in)
	if err != nil {
		return Result{}, err
	}
	return Result{BlockIDs: res.BlockIDs, TokensAfter: res.TokensAfter}, nil
}

// Decompress expands one level of folding for blockID: the block becomes inactive
// and its direct sources become visible again (spec §13.1). Nothing is deleted.
func (c *Client) Decompress(ctx context.Context, blockID string) (DecompressResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.activeSession()
	if err != nil {
		return DecompressResult{}, err
	}
	res, err := s.Decompress(ctx, blockID)
	if err != nil {
		return DecompressResult{}, err
	}
	return DecompressResult{Target: res.Target, Reactivated: res.Reactivated, Redirected: res.Redirected}, nil
}

// Recall returns the original text of a message ref such as "m00042" (spec §13).
func (c *Client) Recall(ctx context.Context, ref string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.activeSession()
	if err != nil {
		return "", err
	}
	return s.Recall(ctx, strings.TrimSpace(ref))
}
