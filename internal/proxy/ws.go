package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/fsvxavier/accordion-context/internal/obs"
	"github.com/fsvxavier/accordion-context/internal/wire/responses"
)

// WebSocket mode of /v1/responses (spec §19.1, §21 phase 8). The client's
// connection is terminated here and a second one is opened upstream. Each
// response.create frame is compacted like an HTTP request, and the upstream
// events are forwarded as they arrive. Compress and decompress calls stay
// internal, as in the other modes.
//
// A client connection carries many responses one after another. Client frames
// that arrive during a response are queued and run afterwards. Pings are
// answered by the reader goroutines, so they are not held up by a long response.

// defaultWSIdleTimeout closes a connection with no client frame for this long.
const defaultWSIdleTimeout = 10 * time.Minute

// errClientGone means the client closed the connection or failed to read.
var errClientGone = errors.New("websocket: client gone")

// closeErr ends a session with a close frame of this code sent to the client.
type closeErr struct {
	code   websocket.StatusCode
	reason string
}

func (e closeErr) Error() string { return e.reason }

// wsMsg is one message read by a reader goroutine. An error ends the reader.
type wsMsg struct {
	data []byte
	err  error
}

// wsSession serves one client connection.
type wsSession struct {
	srv      *Server
	client   *websocket.Conn
	req      *http.Request // the upgrade request: headers, query and auth
	rt       routed
	codec    *responses.FrameCodec
	clientIn <-chan wsMsg
	up       *wsUpstream
	pending  [][]byte // client frames that arrived during a response
	// pendingBytes is the total size of pending. Both are capped, so a client
	// cannot grow the queue without limit while a response runs.
	pendingBytes int
}

// maxWSPending caps the client frames queued during one response.
const maxWSPending = 16

// wsUpstream is the upstream connection of a session. in is fed by a reader
// goroutine, which stops after sending its one error.
type wsUpstream struct {
	conn *websocket.Conn
	in   <-chan wsMsg
}

// serveResponsesWS handles an Upgrade request on the Responses path. The proxy
// grants no access to browser pages, so any Origin header is refused. The
// upstream is opened before the client is accepted, so that an upstream refusal
// (for example a 401) reaches the client as a plain HTTP status.
func (s *Server) serveResponsesWS(w http.ResponseWriter, r *http.Request, rt routed) {
	if len(r.Header.Values("Origin")) > 0 {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	conn, resp, err := s.dialWS(ctx, r, rt)
	if err != nil {
		s.cfg.Logger.Warn("websocket upstream not opened", "path", rt.path, "err", err)
		relayDialFailure(w, resp)
		return
	}
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		_ = conn.CloseNow()
		return
	}
	client.SetReadLimit(s.cfg.WSMaxFrameBytes)
	ws := &wsSession{
		srv: s, client: client, req: r, rt: rt, codec: responses.NewFrameCodec(),
		clientIn: pumpWS(ctx, client),
	}
	ws.up = &wsUpstream{conn: conn, in: pumpWS(ctx, conn)}
	s.trackWS(ws)
	defer s.untrackWS(ws)
	ws.end(ws.run(ctx))
}

// run serves client frames until the connection ends. It returns the reason, nil
// for a normal close.
func (ws *wsSession) run(ctx context.Context) error {
	idle := time.NewTimer(ws.srv.cfg.WSIdleTimeout)
	defer idle.Stop()
	for {
		if len(ws.pending) > 0 {
			frame := ws.pending[0]
			ws.pending = ws.pending[1:]
			ws.pendingBytes -= len(frame)
			if err := ws.handle(ctx, frame); err != nil {
				return err
			}
			idle.Reset(ws.srv.cfg.WSIdleTimeout)
			continue
		}
		var upIn <-chan wsMsg
		if ws.up != nil {
			upIn = ws.up.in
		}
		select {
		case <-ctx.Done():
			return closeErr{websocket.StatusGoingAway, "server shutting down"}
		case m := <-ws.clientIn:
			if m.err != nil {
				return errClientGone
			}
			if err := ws.handle(ctx, m.data); err != nil {
				return err
			}
			idle.Reset(ws.srv.cfg.WSIdleTimeout)
		case m := <-upIn:
			// Upstream traffic outside a response is forwarded. A closed upstream is
			// reopened when the next response.create needs it.
			if m.err != nil {
				ws.dropUpstream()
				continue
			}
			if err := ws.writeClient(ctx, m.data); err != nil {
				return err
			}
		case <-idle.C:
			return closeErr{websocket.StatusNormalClosure, "idle timeout"}
		}
	}
}

// handle runs one client frame. A response.create is compacted. Any other frame,
// and any frame that cannot be compacted, goes upstream unchanged.
func (ws *wsSession) handle(ctx context.Context, frame []byte) error {
	s := ws.srv
	p := responsesProtocol{}
	// The client's own compaction request is forwarded unchanged, as over HTTP: no
	// ingest, tools or nudge. Its signal is read from the frame, which has the body's shape.
	if typ, _ := responses.EventType(frame); typ == "response.create" {
		if sig := s.compactionSignal(ws.req, ws.rt, p, frame); sig != "" {
			s.noteClientCompaction(context.WithoutCancel(ctx), ws.req, ws.rt, p, frame, sig)
			return ws.passthrough(ctx, frame)
		}
	}
	req, msgs, err := ws.codec.ReadCreate(frame)
	if errors.Is(err, responses.ErrNotCreate) {
		return ws.send(ctx, frame)
	}
	if err != nil {
		// Stateful requests (previous_response_id, conversation) and undecodable
		// frames pass through untouched, as in HTTP mode.
		s.failOpenWS("decode", err)
		return ws.passthrough(ctx, frame)
	}
	model, _, err := p.Meta(ws.rt.path, "", frame)
	if err != nil {
		s.failOpenWS("meta", err)
		return ws.passthrough(ctx, frame)
	}
	t, stage, err := s.prepareTurn(context.WithoutCancel(ctx), ws.req, ws.rt, p, model, wsRequest(req), msgs)
	if err != nil {
		s.failOpenWS(stage, err)
		return ws.passthrough(ctx, frame)
	}
	if err := ws.turn(ctx, t); err != nil {
		return err
	}
	s.countRequest(p.Name(), obs.OutcomeCompact)
	return nil
}

// turn runs the server-side loop of one compacted response: each round is sent
// upstream and forwarded live, and a round that asked for compress or decompress
// continues with the results. Only the last round's terminal event reaches the
// client.
func (ws *wsSession) turn(ctx context.Context, t *turn) error {
	s := ws.srv
	for round := 0; ; round++ {
		ws.codec.StartRound()
		s.observeCall(t.sid)
		if err := ws.send(ctx, t.body); err != nil {
			return err
		}
		term := responses.NotTerminal
		for term == responses.NotTerminal {
			data, err := ws.nextUpstream(ctx)
			if err != nil {
				return err
			}
			out, tk, err := ws.codec.ServerEvent(data)
			if err != nil {
				return ws.upstreamFailed(ctx, "upstream event could not be read")
			}
			term = tk
			if out != nil && tk != responses.TerminalHeld {
				if err := ws.writeClient(ctx, out); err != nil {
					return err
				}
			}
		}
		res := ws.codec.Result()
		if res.HasUsage {
			s.recordCache(t, usage{input: res.Usage.InputTokens, cached: res.Usage.CachedTokens})
		}
		if term == responses.TerminalError {
			return nil // the error event was forwarded; no terminal frame follows
		}
		if len(res.Calls) == 0 {
			return ws.finish(ctx)
		}
		calls := wsCalls(res)
		results := make([]toolResult, 0, len(calls))
		for _, c := range calls {
			results = append(results, s.runTool(t, c))
		}
		if res.ClientCalls > 0 || round+1 >= s.cfg.MaxLoop {
			s.cfg.Logger.Warn("internal tool_use not continued", "protocol", "responses", "session", t.sid,
				"client_tools", res.ClientCalls, "round", round)
			return ws.finish(ctx)
		}
		next, err := t.proto.Continue(t.body, reply{assistant: wsAssistant(res)}, results)
		if err != nil {
			s.cfg.Logger.Error("append tool round failed", "session", t.sid, "err", err)
			return ws.finish(ctx)
		}
		t.grow(calls, results)
		t.body = next
	}
}

// passthrough forwards a response without compaction: the upstream events go to
// the client as they arrive, up to the end of the response.
func (ws *wsSession) passthrough(ctx context.Context, frame []byte) error {
	if err := ws.send(ctx, frame); err != nil {
		return err
	}
	for {
		data, err := ws.nextUpstream(ctx)
		if err != nil {
			return err
		}
		if err := ws.writeClient(ctx, data); err != nil {
			return err
		}
		typ, _ := responses.EventType(data)
		if responsesWSEnd(typ) {
			return nil
		}
	}
}

// finish sends the held terminal frame of the response.
func (ws *wsSession) finish(ctx context.Context) error {
	frame, err := ws.codec.FinishRound()
	if err != nil {
		return ws.upstreamFailed(ctx, "response could not be finished")
	}
	if frame == nil {
		return nil
	}
	return ws.writeClient(ctx, frame)
}

// send writes one frame upstream, opening the upstream connection if it is gone.
func (ws *wsSession) send(ctx context.Context, frame []byte) error {
	if ws.up == nil {
		conn, _, err := ws.srv.dialWS(ctx, ws.req, ws.rt)
		if err != nil {
			s := ws.srv
			s.cfg.Logger.Error("websocket upstream reopen failed", "path", ws.rt.path, "err", err)
			return ws.upstreamFailed(ctx, "upstream unavailable")
		}
		ws.up = &wsUpstream{conn: conn, in: pumpWS(ctx, conn)}
	}
	if err := ws.up.conn.Write(ctx, websocket.MessageText, frame); err != nil {
		ws.srv.cfg.Logger.Warn("websocket upstream write failed", "path", ws.rt.path, "err", err)
		ws.dropUpstream()
		return ws.upstreamFailed(ctx, "upstream unavailable")
	}
	return nil
}

// nextUpstream returns the next upstream message of the response in progress.
// Client frames that arrive meanwhile are queued. A failed upstream ends the
// session with close code 1011, after an error event for the client.
func (ws *wsSession) nextUpstream(ctx context.Context) ([]byte, error) {
	if ws.up == nil {
		return nil, ws.upstreamFailed(ctx, "upstream closed before the response completed")
	}
	up := ws.up
	// An upstream that goes quiet in the middle of a response must not hold the
	// connection forever: the idle timeout also bounds the wait for each event.
	stall := time.NewTimer(ws.srv.cfg.WSIdleTimeout)
	defer stall.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, errClientGone
		case <-stall.C:
			ws.srv.cfg.Logger.Warn("websocket upstream stalled", "path", ws.rt.path)
			ws.dropUpstream()
			return nil, ws.upstreamFailed(ctx, "upstream stalled before the response completed")
		case m := <-up.in:
			if m.err != nil {
				ws.srv.cfg.Logger.Warn("websocket upstream read failed", "path", ws.rt.path, "err", m.err)
				ws.dropUpstream()
				return nil, ws.upstreamFailed(ctx, "upstream closed before the response completed")
			}
			return m.data, nil
		case m := <-ws.clientIn:
			if m.err != nil {
				return nil, errClientGone
			}
			ws.pendingBytes += len(m.data)
			if len(ws.pending) >= maxWSPending || int64(ws.pendingBytes) > 2*ws.srv.cfg.WSMaxFrameBytes {
				return nil, closeErr{websocket.StatusPolicyViolation, "too many frames during a response"}
			}
			ws.pending = append(ws.pending, m.data)
		}
	}
}

// upstreamFailed sends an error event and returns the close code that ends the session.
func (ws *wsSession) upstreamFailed(ctx context.Context, msg string) error {
	data, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": msg}})
	if err := ws.writeClient(ctx, data); err != nil {
		return err
	}
	return closeErr{websocket.StatusInternalError, "upstream failed"}
}

// writeClient sends one frame to the client.
func (ws *wsSession) writeClient(ctx context.Context, data []byte) error {
	if err := ws.client.Write(ctx, websocket.MessageText, data); err != nil {
		return errClientGone
	}
	return nil
}

// dropUpstream forgets a broken upstream connection. The next response reopens it.
func (ws *wsSession) dropUpstream() {
	if ws.up != nil {
		_ = ws.up.conn.CloseNow()
		ws.up = nil
	}
}

// end closes both sides. A nil reason is a normal close.
func (ws *wsSession) end(err error) {
	if ws.up != nil {
		_ = ws.up.conn.Close(websocket.StatusNormalClosure, "")
		ws.up = nil
	}
	var ce closeErr
	switch {
	case errors.As(err, &ce):
		_ = ws.client.Close(ce.code, ce.reason)
	case errors.Is(err, errClientGone):
		_ = ws.client.CloseNow()
	default:
		_ = ws.client.Close(websocket.StatusNormalClosure, "")
	}
}

// failOpenWS records a response that was forwarded unchanged.
func (s *Server) failOpenWS(stage string, err error) {
	s.cfg.Logger.Warn("compaction bypassed", "protocol", "responses", "transport", "websocket", "stage", stage, "err", err)
	s.countRequest(responsesProtocol{}.Name(), obs.OutcomeFailOpen)
}

// pumpWS reads messages from c into the returned channel until a read fails.
// The reader is the only goroutine that reads c, which lets pings be answered
// while nothing else is reading.
func pumpWS(ctx context.Context, c *websocket.Conn) <-chan wsMsg {
	// One slot of buffer lets the reader hand over its final error and exit even
	// when nobody reads the channel any more (a dropped upstream).
	out := make(chan wsMsg, 1)
	go func() {
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				select {
				case out <- wsMsg{err: err}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- wsMsg{data: data}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// dialWS opens the upstream WebSocket with the client's headers. Headers of the
// client's handshake are not forwarded, since the upstream handshake sets its own.
func (s *Server) dialWS(ctx context.Context, r *http.Request, rt routed) (*websocket.Conn, *http.Response, error) {
	hdr := http.Header{}
	for k, vv := range r.Header {
		if skipWSRequestHeader(k) {
			continue
		}
		for _, v := range vv {
			hdr.Add(k, v)
		}
	}
	conn, resp, err := websocket.Dial(ctx, upstreamWSURL(r, rt), &websocket.DialOptions{
		HTTPClient: s.client, HTTPHeader: hdr, CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		// url.Error embeds the URL, query string included. Keep it out of logs.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = fmt.Errorf("%s upstream %s: %w", ue.Op, rt.up.Host, ue.Err)
		}
		return nil, resp, err
	}
	conn.SetReadLimit(s.cfg.WSMaxFrameBytes)
	return conn, nil, nil
}

// upstreamWSURL maps the upstream base and the request path to a ws(s) URL, as
// upstreamCall maps them to an HTTP URL.
func upstreamWSURL(r *http.Request, rt routed) string {
	u := *rt.up
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(rt.up.Path, "/") + rt.path
	u.RawPath = ""
	if esc := escapedTail(r.URL, rt.path); esc != "" {
		u.RawPath = strings.TrimRight(rt.up.EscapedPath(), "/") + esc
	}
	u.RawQuery = r.URL.RawQuery
	return u.String()
}

// skipWSRequestHeader drops the headers of the client's own WebSocket handshake
// and the hop-by-hop headers.
func skipWSRequestHeader(k string) bool {
	if skipRequestHeader(k) {
		return true
	}
	lk := strings.ToLower(k)
	return strings.HasPrefix(lk, "sec-websocket-") || lk == "origin"
}

// relayDialFailure answers the client with the upstream refusal, as HTTP.
func relayDialFailure(w http.ResponseWriter, resp *http.Response) {
	if resp == nil || resp.StatusCode < 400 || resp.Body == nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// isWebSocketUpgrade reports whether r asks to switch to the WebSocket protocol.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

// trackWS records a live session so that CloseWebSockets can reach it.
func (s *Server) trackWS(ws *wsSession) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.wsSessions == nil {
		s.wsSessions = map[*wsSession]struct{}{}
	}
	s.wsSessions[ws] = struct{}{}
}

func (s *Server) untrackWS(ws *wsSession) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	delete(s.wsSessions, ws)
}

// CloseWebSockets asks every open WebSocket session to close with 1001 (going
// away). serve calls it on shutdown, because http.Server does not track hijacked
// connections.
func (s *Server) CloseWebSockets() {
	s.wsMu.Lock()
	list := make([]*wsSession, 0, len(s.wsSessions))
	for ws := range s.wsSessions {
		list = append(list, ws)
	}
	s.wsMu.Unlock()
	for _, ws := range list {
		go func(c *websocket.Conn) {
			_ = c.Close(websocket.StatusGoingAway, "server shutting down")
		}(ws.client)
	}
}
