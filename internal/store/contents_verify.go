package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// ErrContentMismatch reports a contents row whose stored bytes do not match
// its key: the raw data, decoded as AppendMessage stores it, must hash to
// hash and have exactly size_bytes bytes. A backup or dump that breaks this
// is refused before any row is written.
var ErrContentMismatch = errors.New("store: content does not match its hash")

// maxContentWindow bounds the zstd window a stored content may declare. The
// writer (zstd.NewWriter with defaults) uses windows of at most 8 MiB, so the
// limit only refuses frames that would make the decoder allocate more than
// that for one row.
const maxContentWindow = 16 << 20

// contentChecker verifies stored contents one row at a time. The decoder is
// reused across rows; each row is decoded as a stream into a SHA-256 hasher
// and stops one byte past its declared size, so the work per row is linear
// in its size and its memory is bounded by the decoder window.
type contentChecker struct {
	dec *zstd.Decoder
}

func newContentChecker() (*contentChecker, error) {
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(maxContentWindow))
	if err != nil {
		return nil, fmt.Errorf("zstd decoder: %w", err)
	}
	return &contentChecker{dec: dec}, nil
}

func (c *contentChecker) close() { c.dec.Close() }

// verify decodes data exactly as appendMessage stored it and checks the raw
// bytes against hash and size.
func (c *contentChecker) verify(hash string, size int64, data []byte) error {
	if size < 0 || len(data) == 0 {
		return contentMismatch(hash, "malformed size or empty data")
	}
	// A plain io.Reader keeps the decoder on its streaming path, which honours
	// the size limit below; a *bytes.Buffer would take the unbounded one-shot path.
	if err := c.dec.Reset(struct{ io.Reader }{bytes.NewReader(data)}); err != nil {
		return contentMismatch(hash, fmt.Sprintf("decoder: %v", err))
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(c.dec, size+1))
	if err != nil {
		return contentMismatch(hash, fmt.Sprintf("decode: %v", err))
	}
	if n != size {
		return contentMismatch(hash, fmt.Sprintf("size %d, stored size_bytes %d", n, size))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != hash {
		return contentMismatch(hash, "sha256 of the decoded data is "+got)
	}
	return nil
}

func contentMismatch(hash, why string) error {
	return fmt.Errorf("%w: first bad hash %s (%s)", ErrContentMismatch, hash, why)
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// contentFields reads a contents row of a dump.
func contentFields(r Row) (hash string, size int64, data []byte, err error) {
	hash, okH := r["hash"].(string)
	size, okS := r["size_bytes"].(int64)
	data, okD := r["data"].([]byte)
	if !okH || !okS || !okD {
		return "", 0, nil, fmt.Errorf("%w: malformed contents row", ErrContentMismatch)
	}
	return hash, size, data, nil
}

// checkDumpContents verifies every contents row of dumps before anything is
// written. A row whose hash is already in the destination must match it: the
// destination row is verified the same way, so INSERT OR IGNORE that follows
// can only skip identical bytes, never mask a difference.
func checkDumpContents(ctx context.Context, q rowQuerier, c *contentChecker, dumps ...ProjectDump) error {
	for _, d := range dumps {
		for _, t := range d.Tables {
			if t.Table != "contents" {
				continue
			}
			for _, r := range t.Rows {
				hash, size, data, err := contentFields(r)
				if err != nil {
					return err
				}
				if err := c.verify(hash, size, data); err != nil {
					return err
				}
				if err := checkDestContent(ctx, q, c, hash, size); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkDestContent verifies the destination row for hash, if there is one,
// and that it has the size the source claims.
func checkDestContent(ctx context.Context, q rowQuerier, c *contentChecker, hash string, size int64) error {
	var dsize int64
	var data []byte
	err := q.QueryRowContext(ctx, `SELECT size_bytes, data FROM contents WHERE hash = ?1`, hash).Scan(&dsize, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read destination content %s: %w", hash, err)
	}
	if dsize != size {
		return fmt.Errorf("%w: first bad hash %s (destination has size %d, backup has %d)",
			ErrContentMismatch, hash, dsize, size)
	}
	if err := c.verify(hash, dsize, data); err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	return nil
}

// checkAllContents verifies every contents row of a database file, in hash
// order, so the first bad hash it names is deterministic.
func checkAllContents(ctx context.Context, q rowQuerier, c *contentChecker) error {
	rows, err := q.QueryContext(ctx, `SELECT hash, size_bytes, data FROM contents ORDER BY hash`)
	if err != nil {
		return fmt.Errorf("read contents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var size int64
		var data []byte
		if err := rows.Scan(&hash, &size, &data); err != nil {
			return fmt.Errorf("%w: malformed contents row: %v", ErrContentMismatch, err)
		}
		if err := c.verify(hash, size, data); err != nil {
			return err
		}
	}
	return rows.Err()
}
