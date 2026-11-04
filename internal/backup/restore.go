package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// Restore modes. Merge refuses any ID that already exists; replace purges the
// target project first.
const (
	ModeMerge   = "merge"
	ModeReplace = "replace"
)

// Storage values of a destination.
const (
	StorageGlobal = "global"
	StorageLocal  = "local"
)

// ErrNeedYes reports a replace without explicit confirmation.
var ErrNeedYes = errors.New("backup: replace deletes the target project's data; pass --yes to confirm")

// Destination is the database a restore writes to.
type Destination struct {
	// DBPath is the database file: the global database, or a local
	// .acd-ctx/acd-ctx.db.
	DBPath string
	// Storage is StorageGlobal or StorageLocal. The projects in the backup keep
	// their own IDs and roots for global. For local, all data lands under
	// ProjectID and RootPath.
	Storage   string
	ProjectID string
	RootPath  string
}

// RestoreRequest describes one restore.
type RestoreRequest struct {
	// Source is the backup file contents. A scratch copy is made, so the source
	// is only read.
	Source io.Reader
	// ProjectID restricts the restore to one project of the backup. Empty means
	// every project in it (local destinations need exactly one).
	ProjectID string
	Mode      string
	Yes       bool
	DryRun    bool
	Dest      Destination
}

// RestoreResult describes a restore, or a dry run of one.
type RestoreResult struct {
	Report   store.RestoreReport
	Projects []store.FileProject
	Sessions int
	Mode     string
	DryRun   bool
	Dest     Destination
}

// Restore imports a backup into the destination in one transaction. The source
// is copied to a private file and must pass store.InspectFile, including the
// invariants of every session, before the destination is opened. An older
// schema is migrated on that copy only. A newer one is refused.
func Restore(ctx context.Context, req RestoreRequest) (RestoreResult, error) {
	if req.Mode != ModeMerge && req.Mode != ModeReplace {
		return RestoreResult{}, fmt.Errorf("backup: mode %q must be merge or replace", req.Mode)
	}
	if req.Dest.Storage != StorageGlobal && req.Dest.Storage != StorageLocal {
		return RestoreResult{}, fmt.Errorf("backup: storage %q must be global or local", req.Dest.Storage)
	}
	if req.Mode == ModeReplace && !req.Yes && !req.DryRun {
		return RestoreResult{}, ErrNeedYes
	}
	if req.Source == nil {
		return RestoreResult{}, errors.New("backup: restore needs a source")
	}

	work, err := os.MkdirTemp("", "acd-ctx-restore-*")
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore: temp directory: %w", err)
	}
	defer os.RemoveAll(work)

	copyPath := filepath.Join(work, "source.db")
	if err := copyStream(copyPath, req.Source); err != nil {
		return RestoreResult{}, err
	}
	info, err := store.InspectFile(ctx, copyPath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup rejected, nothing written: %w", err)
	}
	selected, err := selectProjects(info.Projects, req.ProjectID, req.Dest.Storage)
	if err != nil {
		return RestoreResult{}, err
	}

	src, err := store.Open(copyPath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("open backup copy: %w", err)
	}
	defer src.Close()

	var items []store.RestoreItem
	res := RestoreResult{Mode: req.Mode, DryRun: req.DryRun, Dest: req.Dest, Projects: selected}
	for _, p := range selected {
		dump, err := src.ExportProject(ctx, p.ID)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("read project %s from backup: %w", p.ID, err)
		}
		res.Sessions += len(dump.SessionIDs())
		target := store.ImportTarget{ProjectID: p.ID, RootPath: p.RootPath, Storage: StorageGlobal}
		if req.Dest.Storage == StorageLocal {
			target = store.ImportTarget{ProjectID: req.Dest.ProjectID, RootPath: req.Dest.RootPath, Storage: StorageLocal}
		}
		items = append(items, store.RestoreItem{Dump: dump, Target: target})
	}

	// A dry run plans against a scratch copy of the destination, so it never
	// creates the destination file or its directory.
	dstPath := req.Dest.DBPath
	if req.DryRun {
		if dstPath, err = planCopy(ctx, req.Dest.DBPath, work); err != nil {
			return RestoreResult{}, err
		}
	} else if err := os.MkdirAll(filepath.Dir(req.Dest.DBPath), 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("create database directory: %w", err)
	}
	dst, err := store.Open(dstPath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("open destination %s: %w", req.Dest.DBPath, err)
	}
	defer dst.Close()

	rep, err := dst.RestoreDumps(ctx, items, req.Mode == ModeReplace, req.DryRun)
	res.Report = rep
	if err != nil {
		return res, err
	}
	return res, nil
}

// planCopy returns the path of a scratch database that a dry run plans against.
// It is a snapshot of dbPath when that exists, and an empty database otherwise.
// Opening an existing live database may migrate an older schema, as any other
// acd-ctx command does.
func planCopy(ctx context.Context, dbPath, work string) (string, error) {
	scratch := filepath.Join(work, "plan.db")
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return scratch, nil
	}
	live, err := store.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open destination %s: %w", dbPath, err)
	}
	defer live.Close()
	if err := live.VacuumInto(ctx, scratch); err != nil {
		return "", err
	}
	return scratch, nil
}

// selectProjects returns the projects a restore takes from a backup.
func selectProjects(all []store.FileProject, want, storage string) ([]store.FileProject, error) {
	if len(all) == 0 {
		return nil, errors.New("backup holds no project")
	}
	if want != "" {
		for _, p := range all {
			if p.ID == want {
				return []store.FileProject{p}, nil
			}
		}
		return nil, fmt.Errorf("project %s is not in the backup", want)
	}
	if storage == StorageLocal && len(all) != 1 {
		return nil, fmt.Errorf("backup holds %d projects; select one with --project", len(all))
	}
	return all, nil
}

// copyStream writes r to path with mode 0600.
func copyStream(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("read backup: %w", err)
	}
	return f.Close()
}
