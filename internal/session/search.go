package session

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// Limits of the read-side tools search_context and retrieve (spec §13.1, §16).
// They sit here, below the proxy, so that no caller can let one tool result fill
// the window.
const (
	DefaultSearchHits = 10
	MaxSearchHits     = 20
	MaxSnippetRunes   = 300
	MaxRetrieveTokens = 32 << 10
)

// refPattern is the shape of a message ref, with or without its m prefix.
var refPattern = regexp.MustCompile(`^[mM]?0*([0-9]{1,18})$`)

// Search runs search_context over this conversation. At most MaxSearchHits hits
// come back, each snippet is at most MaxSnippetRunes runes, and an empty limit
// means DefaultSearchHits.
func (s *Session) Search(ctx context.Context, query string, limit int) ([]store.SearchHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits, err := s.st.Search(ctx, s.sid, query, clampHits(limit))
	return clipHits(hits), err
}

// SearchProject runs search_context over every conversation of projectID. The
// caller passes the project of the request, so a scope never crosses projects.
func (s *Session) SearchProject(ctx context.Context, projectID, query string, limit int) ([]store.SearchHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hits, err := s.st.SearchProject(ctx, projectID, query, clampHits(limit))
	return clipHits(hits), err
}

// ErrNothingToRetrieve reports a retrieve whose target has no message in its span.
var ErrNothingToRetrieve = errors.New("retrieve: no message in the requested span")

// Retrieval is the original text returned by retrieve.
type Retrieval struct {
	Target    string // the block id or message ref asked for
	Messages  int    // messages in the returned text
	Text      string // one "[ref] text" entry per message, capped at MaxRetrieveTokens
	Truncated bool   // the text was cut to fit the cap
}

// Retrieve returns the original messages behind target, which is a message ref
// (m00042) or a block id (b12). Messages folded into blocks are returned as they
// were recorded. rng, for example m00340..m00412, narrows a block to that span,
// and for a ref it selects the span instead. This session only.
func (s *Session) Retrieve(ctx context.Context, target, rng string) (Retrieval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return Retrieval{}, err
	}
	lo, hi, err := retrieveSpan(snap, target, rng)
	if err != nil {
		return Retrieval{}, err
	}
	var b strings.Builder
	n := 0
	for _, m := range snap.Messages {
		if m.Seq < lo || m.Seq > hi {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\n\n", m.Ref, m.Text)
		n++
	}
	if n == 0 {
		return Retrieval{}, ErrNothingToRetrieve
	}
	text, truncated := capRunes(b.String(), MaxRetrieveTokens*charsPerToken)
	return Retrieval{Target: target, Messages: n, Text: text, Truncated: truncated}, nil
}

// retrieveSpan resolves target and rng to an inclusive ledger span.
func retrieveSpan(snap kernel.Snapshot, target, rng string) (lo, hi int64, err error) {
	if blockIDPattern.MatchString(target) {
		blk := findBlock(snap.Blocks, target)
		if blk == nil {
			return 0, 0, fmt.Errorf("retrieve: unknown block %q", target)
		}
		lo, hi = blk.StartSeq, blk.EndSeq
	} else {
		seq, perr := parseRef(target)
		if perr != nil {
			return 0, 0, fmt.Errorf("retrieve: %q is neither a message ref nor a block id", target)
		}
		lo, hi = seq, seq
	}
	if rng == "" {
		return lo, hi, nil
	}
	a, b, ok := strings.Cut(rng, "..")
	if !ok {
		return 0, 0, fmt.Errorf("retrieve: range %q must look like m00340..m00412", rng)
	}
	from, err1 := parseRef(a)
	to, err2 := parseRef(b)
	if err1 != nil || err2 != nil || from > to {
		return 0, 0, fmt.Errorf("retrieve: range %q must look like m00340..m00412", rng)
	}
	if !blockIDPattern.MatchString(target) {
		return from, to, nil
	}
	// A block: the range narrows its span and must leave something of it.
	if from < lo {
		from = lo
	}
	if to > hi {
		to = hi
	}
	if from > to {
		return 0, 0, fmt.Errorf("retrieve: range %q is outside block %s", rng, target)
	}
	return from, to, nil
}

func findBlock(blocks []kernel.Block, id string) *kernel.Block {
	for i := range blocks {
		if blocks[i].ID == id {
			return &blocks[i]
		}
	}
	return nil
}

func parseRef(s string) (int64, error) {
	m := refPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid ref %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid ref %q", s)
	}
	return n, nil
}

func clampHits(limit int) int {
	switch {
	case limit <= 0:
		return DefaultSearchHits
	case limit > MaxSearchHits:
		return MaxSearchHits
	}
	return limit
}

// clipHits shortens every snippet to MaxSnippetRunes and folds its whitespace, so
// that a hit is one line.
func clipHits(hits []store.SearchHit) []store.SearchHit {
	for i := range hits {
		hits[i].Snippet = clipSnippet(hits[i].Snippet)
	}
	return hits
}

func clipSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= MaxSnippetRunes {
		return s
	}
	cut := 0
	for i := range s {
		if cut == MaxSnippetRunes {
			return s[:i] + "…"
		}
		cut++
	}
	return s
}

// capRunes keeps at most max runes of s and reports whether it cut anything.
func capRunes(s string, max int) (string, bool) {
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i], true
		}
		n++
	}
	return s, false
}
