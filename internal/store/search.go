package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Kinds of search_idx rows. A message row is keyed by its ref, a block row by
// its block id. Both are written in the same transaction as the source row
// (spec §5.2, §16 search_context).
const (
	indexKindMessage = "message"
	indexKindBlock   = "block"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 100
	maxSearchTerms     = 32
	snippetTokens      = 16
)

// ErrEmptyQuery reports a query with no word characters: nothing to match.
var ErrEmptyQuery = errors.New("search: query has no searchable terms")

// SearchHit is one matching message or block.
type SearchHit struct {
	SessionID string
	Kind      string // "message" or "block"
	Key       string // message ref (m00042) or block id (b17)
	Snippet   string // the matching body with the hits in [brackets]
	Score     float64
}

// blockBody is the indexed text of a block: its topic, then its summary.
func blockBody(topic, summary string) string { return topic + "\n" + summary }

// indexTx adds one row to search_idx inside the caller's transaction. Empty
// bodies are not indexed.
func indexTx(ctx context.Context, ex execer, sid, kind, key, body string) error {
	if body == "" {
		return nil
	}
	if _, err := ex.ExecContext(ctx,
		`INSERT INTO search_idx(session_id, kind, key, body) VALUES (?,?,?,?)`,
		sid, kind, key, body); err != nil {
		return fmt.Errorf("index %s %s: %w", kind, key, err)
	}
	return nil
}

// Search runs a full-text query over the messages and block summaries of one
// session. Hits are ordered by bm25 relevance, best first.
func (s *Store) Search(ctx context.Context, sid, query string, limit int) ([]SearchHit, error) {
	return s.search(ctx, `search_idx.session_id = ?`, sid, query, limit)
}

// SearchProject is Search over every session of a project.
func (s *Store) SearchProject(ctx context.Context, projectID, query string, limit int) ([]SearchHit, error) {
	return s.search(ctx, `search_idx.session_id IN (SELECT id FROM sessions WHERE project_id = ?)`,
		projectID, query, limit)
}

func (s *Store) search(ctx context.Context, scope, arg, query string, limit int) ([]SearchHit, error) {
	match, ok := ftsMatch(query)
	if !ok {
		return nil, ErrEmptyQuery
	}
	switch {
	case limit <= 0:
		limit = defaultSearchLimit
	case limit > maxSearchLimit:
		limit = maxSearchLimit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, kind, key, snippet(search_idx, 3, '[', ']', '…', ?), bm25(search_idx)
		FROM search_idx
		WHERE search_idx MATCH ? AND `+scope+`
		ORDER BY bm25(search_idx), kind, key
		LIMIT ?`, snippetTokens, match, arg, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	hits := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SessionID, &h.Kind, &h.Key, &h.Snippet, &h.Score); err != nil {
			return nil, fmt.Errorf("search: scan: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return hits, nil
}

// ftsMatch turns free user text into an FTS5 expression that cannot be read as
// syntax: each whitespace-separated word becomes a quoted phrase, with inner
// quotes doubled, and the phrases are ANDed. Words without any letter or digit
// are dropped. ok is false when nothing is left.
func ftsMatch(q string) (match string, ok bool) {
	var terms []string
	for _, f := range strings.Fields(q) {
		f = strings.ReplaceAll(f, "\x00", "")
		if !strings.ContainsFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
		if len(terms) == maxSearchTerms {
			break
		}
	}
	return strings.Join(terms, " "), len(terms) > 0
}
