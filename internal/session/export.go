package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// MaxExportBytes caps the text one export writes. A longer block is cut at a
// rune boundary and reported as truncated.
const MaxExportBytes = 8 << 20

// blockIDPattern is the shape of every block id the kernel mints ("b17"). The
// export file name is built from the stored id after this check, so a crafted
// id from a client can never reach the file system.
var blockIDPattern = regexp.MustCompile(`^b[0-9]{1,12}$`)

// ExportResult describes the file written by ExportBlock.
type ExportResult struct {
	BlockID   string
	Path      string
	Messages  int  // messages in the block's range
	Bytes     int  // bytes written
	Truncated bool // the text was cut at MaxExportBytes
}

// ExportBlock writes the original text of every message covered by block
// blockID into dir (spec §13.1, decompress mode file) and returns the path. The
// file is named <blockID>-<hash of the session>.txt, is created owner-only
// (0600) through a temporary file and an atomic rename, and never follows a
// link already at the target. dir is created with 0700 when missing.
func (s *Session) ExportBlock(ctx context.Context, blockID, dir string) (ExportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !blockIDPattern.MatchString(blockID) {
		return ExportResult{}, fmt.Errorf("export: invalid block id %q", blockID)
	}
	if dir == "" {
		return ExportResult{}, errors.New("export: no export directory")
	}
	snap, err := s.st.Snapshot(ctx, s.sid)
	if err != nil {
		return ExportResult{}, err
	}
	var blk *kernel.Block
	for i := range snap.Blocks {
		if snap.Blocks[i].ID == blockID {
			blk = &snap.Blocks[i]
			break
		}
	}
	if blk == nil {
		return ExportResult{}, fmt.Errorf("export: unknown block %q", blockID)
	}

	var b strings.Builder
	res := ExportResult{BlockID: blockID}
	for _, m := range snap.Messages {
		if m.Seq < blk.StartSeq || m.Seq > blk.EndSeq {
			continue
		}
		res.Messages++
		fmt.Fprintf(&b, "== %s %s %s\n%s\n\n", m.Ref, m.Role, m.Kind, m.Text)
	}
	text := b.String()
	if len(text) > MaxExportBytes {
		cut := MaxExportBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
		res.Truncated = true
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ExportResult{}, fmt.Errorf("export: %w", err)
	}
	sum := sha256.Sum256([]byte(s.sid))
	path := filepath.Join(dir, blockID+"-"+hex.EncodeToString(sum[:6])+".txt")
	if err := writeOwnerFile(dir, path, []byte(text)); err != nil {
		return ExportResult{}, fmt.Errorf("export: %w", err)
	}
	res.Path = path
	res.Bytes = len(text)
	return res, nil
}

// writeOwnerFile writes data to path through a temporary file in dir. The
// temporary file is created with mode 0600, and rename replaces whatever sits
// at path, so a symlink planted there is replaced and not followed.
func writeOwnerFile(dir, path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".export-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
