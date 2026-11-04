// Package backup writes and restores the context base as SQLite files (spec
// §18.7). A backup is a single database file: the whole database for the
// global store or a local .acd-ctx database, or one project extracted from the
// global store. Restore never overwrites a live database file. It imports
// through one transaction on the destination.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// ErrExists reports an output path that exists and was not given --force.
var ErrExists = errors.New("backup: output exists (use --force to replace it)")

// fileMode is the permission of every file this package writes.
const fileMode = 0o600

// CreateRequest describes one backup.
type CreateRequest struct {
	// Source is the database file to read: the global database, or a local
	// .acd-ctx/acd-ctx.db.
	Source string
	// ProjectID selects one project of Source. Empty copies the whole file.
	ProjectID string
	// Out is the destination file. Empty means write to Stdout.
	Out string
	// Stdout receives the backup when Out is empty.
	Stdout io.Writer
	// Force replaces an existing Out.
	Force bool
}

// CreateResult describes a finished backup.
type CreateResult struct {
	// Path is the written file, or empty when the backup went to Stdout.
	Path  string
	Bytes int64
	Info  store.FileInfo
}

// Create writes a verified backup. The file is built in a private temporary
// directory next to Out, checked with store.InspectFile, and renamed into
// place, so Out is either the old file or the complete new one.
func Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	if req.Out == "" && req.Stdout == nil {
		return CreateResult{}, errors.New("backup: needs an output file or a stdout writer")
	}
	src, err := filepath.Abs(req.Source)
	if err != nil {
		return CreateResult{}, err
	}
	if fi, err := os.Stat(src); err != nil || !fi.Mode().IsRegular() {
		return CreateResult{}, fmt.Errorf("backup: no database at %s", src)
	}

	var out, workDir string
	if req.Out != "" {
		out, err = filepath.Abs(req.Out)
		if err != nil {
			return CreateResult{}, err
		}
		if err := refuseSource(out, src); err != nil {
			return CreateResult{}, err
		}
		if _, err := os.Stat(out); err == nil && !req.Force {
			return CreateResult{}, fmt.Errorf("%w: %s", ErrExists, out)
		}
		workDir, err = os.MkdirTemp(filepath.Dir(out), ".acd-ctx-backup-*")
	} else {
		workDir, err = os.MkdirTemp("", "acd-ctx-backup-*")
	}
	if err != nil {
		return CreateResult{}, fmt.Errorf("backup: temp directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	built := filepath.Join(workDir, "backup.db")
	if err := build(ctx, src, req.ProjectID, workDir, built); err != nil {
		return CreateResult{}, err
	}
	info, err := verifyBuilt(ctx, built)
	if err != nil {
		return CreateResult{}, err
	}
	if err := os.Chmod(built, fileMode); err != nil {
		return CreateResult{}, err
	}
	res := CreateResult{Info: info}
	if out == "" {
		n, err := copyTo(req.Stdout, built)
		res.Bytes = n
		return res, err
	}
	if err := install(built, out, req.Force); err != nil {
		return CreateResult{}, err
	}
	res.Path = out
	if fi, err := os.Stat(out); err == nil {
		res.Bytes = fi.Size()
	}
	return res, nil
}

// Adopt verifies a backup file produced elsewhere (for example by a container)
// and moves it to out with the same rules as Create. tmp is consumed.
func Adopt(ctx context.Context, tmp, out string, force bool) (store.FileInfo, error) {
	abs, err := filepath.Abs(out)
	if err != nil {
		return store.FileInfo{}, err
	}
	info, err := store.InspectFile(ctx, tmp)
	if err != nil {
		os.Remove(tmp)
		return store.FileInfo{}, fmt.Errorf("received backup rejected: %w", err)
	}
	if _, err := os.Stat(abs); err == nil && !force {
		os.Remove(tmp)
		return store.FileInfo{}, fmt.Errorf("%w: %s", ErrExists, abs)
	}
	if err := os.Chmod(tmp, fileMode); err != nil {
		os.Remove(tmp)
		return store.FileInfo{}, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		return store.FileInfo{}, fmt.Errorf("install backup: %w", err)
	}
	return info, nil
}

// build fills built with a copy of the requested data.
func build(ctx context.Context, src, projectID, workDir, built string) error {
	if projectID == "" {
		st, err := store.Open(src)
		if err != nil {
			return fmt.Errorf("open %s: %w", src, err)
		}
		defer st.Close()
		if err := st.VacuumInto(ctx, built); err != nil {
			return err
		}
		return nil
	}

	st, err := store.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	proj, err := st.GetProject(ctx, projectID)
	if err != nil {
		st.Close()
		return fmt.Errorf("project %s: %w", projectID, err)
	}
	dump, err := st.ExportProject(ctx, projectID)
	st.Close()
	if err != nil {
		return fmt.Errorf("export project %s: %w", projectID, err)
	}

	scratch, err := store.Open(filepath.Join(workDir, "scratch.db"))
	if err != nil {
		return fmt.Errorf("scratch database: %w", err)
	}
	defer scratch.Close()
	if _, err := scratch.ImportProject(ctx, dump, store.ImportTarget{
		ProjectID: projectID,
		RootPath:  proj.RootPath,
		Storage:   proj.Storage,
	}); err != nil {
		return fmt.Errorf("copy project %s: %w", projectID, err)
	}
	return scratch.VacuumInto(ctx, built)
}

// verifyBuilt seals the copy so the file alone holds the data, then checks it.
func verifyBuilt(ctx context.Context, built string) (store.FileInfo, error) {
	st, err := store.Open(built)
	if err != nil {
		return store.FileInfo{}, fmt.Errorf("backup: reopen copy: %w", err)
	}
	if err := st.Seal(ctx); err != nil {
		st.Close()
		return store.FileInfo{}, err
	}
	if err := st.Close(); err != nil {
		return store.FileInfo{}, err
	}
	info, err := store.InspectFile(ctx, built)
	if err != nil {
		return store.FileInfo{}, fmt.Errorf("backup failed verification: %w", err)
	}
	return info, nil
}

// refuseSource rejects an output that is the source database or one of its
// journal files. An output with the same name in the same directory is refused
// too, when it is the same file.
func refuseSource(out, src string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if out == src+suffix {
			return fmt.Errorf("backup: output %s is the database itself", out)
		}
	}
	if oi, err := os.Stat(out); err == nil {
		if si, err := os.Stat(src); err == nil && os.SameFile(oi, si) {
			return fmt.Errorf("backup: output %s is the database itself", out)
		}
	}
	return nil
}

// install moves a finished file to out. The rename is atomic within one
// directory, and the temp file sits in that directory.
func install(tmp, out string, force bool) error {
	if _, err := os.Stat(out); err == nil && !force {
		return fmt.Errorf("%w: %s", ErrExists, out)
	}
	if err := os.Rename(tmp, out); err != nil {
		return fmt.Errorf("install backup: %w", err)
	}
	return nil
}

// copyTo streams a file to w and returns the bytes written.
func copyTo(w io.Writer, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(w, f)
}

// IsPathArg reports whether a --project argument names a directory rather than
// a project ID.
func IsPathArg(arg string, isDir func(string) bool) bool {
	return arg == "." || arg == ".." || strings.ContainsAny(arg, `/\`) || isDir(arg)
}
