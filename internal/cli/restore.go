package cli

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/backup"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// maxRestoreEnv overrides the default restore size cap when --max-size is not given.
const maxRestoreEnv = "ACD_CTX_MAX_RESTORE_BYTES"

const restoreUsage = "usage: restore <file.db> [--project <id>] [--mode merge|replace] [--yes] [--to local|global] [--dry-run] [--max-size <size>] [--docker <container>]\n       restore --stdin [same flags]\n  <size> is bytes or a number with KiB, MiB or GiB (default 2GiB; env " + maxRestoreEnv + ")"

// runRestore implements `acd-ctx restore` (spec §18.7). The backup is validated
// and imported in one transaction on the destination. It is never copied over
// the live database file.
func runRestore(args []string, s IO) int {
	fset := newFlagSet("restore", s)
	project := fset.String("project", "", "restore only this project of the backup")
	mode := fset.String("mode", backup.ModeMerge, "merge (refuse existing IDs) or replace (purge the target project first)")
	yes := fset.Bool("yes", false, "confirm a replace")
	to := fset.String("to", backup.StorageGlobal, "destination: the global database, or the current project's local .acd-ctx database")
	dryRun := fset.Bool("dry-run", false, "print the plan and write nothing")
	stdin := fset.Bool("stdin", false, "read the backup from stdin instead of a file")
	docker := fset.String("docker", "", "restore into this container; the file is read on this machine")
	maxSize := fset.String("max-size", "", "refuse a backup larger than this (default 2GiB, or $"+maxRestoreEnv+")")
	pos, err := parseFlags(fset, args)
	if err != nil || (*stdin && len(pos) > 0) || (!*stdin && len(pos) != 1) ||
		(*mode != backup.ModeMerge && *mode != backup.ModeReplace) ||
		(*to != backup.StorageGlobal && *to != backup.StorageLocal) {
		return usageError(s, restoreUsage)
	}
	maxBytes, err := restoreCap(s, *maxSize)
	if err != nil {
		fmt.Fprintf(s.Stderr, "acd-ctx: %v\n%s\n", err, restoreUsage)
		return ExitUsage
	}

	in := s.Stdin
	if !*stdin {
		f, err := os.Open(pos[0])
		if err != nil {
			return fail(s, err)
		}
		defer f.Close()
		in = f
	}

	if *docker != "" {
		err := backup.DockerRestore(s.Ctx, dockerRunner, *docker, in, backup.DockerRestoreOptions{
			Project:  *project,
			Mode:     *mode,
			To:       *to,
			Yes:      *yes,
			DryRun:   *dryRun,
			MaxBytes: maxBytes,
		}, s.Stdout, s.Stderr)
		if err != nil {
			return fail(s, err)
		}
		return ExitOK
	}

	dest, err := restoreDestination(s, *to)
	if err != nil {
		return fail(s, err)
	}
	res, err := backup.Restore(s.Ctx, backup.RestoreRequest{
		Source:    in,
		ProjectID: *project,
		Mode:      *mode,
		Yes:       *yes,
		DryRun:    *dryRun,
		Dest:      dest,
		MaxBytes:  maxBytes,
	})
	if errors.Is(err, backup.ErrRestoreTooLarge) {
		return fail(s, fmt.Errorf("%w; raise --max-size or %s", err, maxRestoreEnv))
	}
	if errors.Is(err, store.ErrRestoreConflict) {
		return fail(s, fmt.Errorf("restore refused, nothing written: %w", err))
	}
	if err != nil {
		return fail(s, err)
	}
	printRestore(s, res)
	return ExitOK
}

// restoreCap resolves the restore size cap: --max-size, else the environment,
// else backup.DefaultMaxRestoreBytes.
func restoreCap(s IO, flagVal string) (int64, error) {
	v, source := flagVal, "--max-size"
	if v == "" {
		env, ok := s.LookupEnv(maxRestoreEnv)
		if !ok || env == "" {
			return backup.DefaultMaxRestoreBytes, nil
		}
		v, source = env, maxRestoreEnv
	}
	n, err := parseByteSize(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", source, v, err)
	}
	return n, nil
}

// parseByteSize reads a positive byte count: plain digits, or digits with a
// binary suffix (KiB, MiB, GiB, or K, M, G).
func parseByteSize(v string) (int64, error) {
	num, unit := strings.TrimSpace(v), int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}} {
		if strings.HasSuffix(num, u.suffix) {
			num, unit = strings.TrimSuffix(num, u.suffix), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("want a positive size such as 2GiB or 1048576")
	}
	if n > math.MaxInt64/unit-1 {
		return 0, errors.New("size is too large")
	}
	return n * unit, nil
}

// restoreDestination picks the database for --to. Local means the current
// project's .acd-ctx/acd-ctx.db, and the project must already be initialised.
func restoreDestination(s IO, to string) (backup.Destination, error) {
	if to == backup.StorageGlobal {
		gp, err := globalDBPath(s.LookupEnv)
		if err != nil {
			return backup.Destination{}, err
		}
		return backup.Destination{DBPath: gp, Storage: backup.StorageGlobal}, nil
	}
	sc, err := resolveScope(s)
	if err != nil {
		return backup.Destination{}, err
	}
	root := sc.res.Root
	if !fileExists(filepath.Join(root, localDirName)) {
		return backup.Destination{}, errNoLocalProject
	}
	id, err := readLocalProjectID(root)
	if err != nil {
		return backup.Destination{}, err
	}
	return backup.Destination{
		DBPath:    localDBPath(root),
		Storage:   backup.StorageLocal,
		ProjectID: id,
		RootPath:  root,
	}, nil
}

// printRestore reports what was restored, or what a dry run would restore.
func printRestore(s IO, res backup.RestoreResult) {
	w := io.Writer(s.Stdout)
	for _, p := range res.Projects {
		target := p.ID
		if res.Dest.Storage == backup.StorageLocal {
			target = res.Dest.ProjectID
		}
		verb := "restored"
		if res.DryRun {
			verb = "would restore"
		}
		fmt.Fprintf(w, "%s project %s (%s) into %s database %s\n",
			verb, target, p.RootPath, res.Dest.Storage, res.Dest.DBPath)
	}
	rep := res.Report
	fmt.Fprintf(w, "sessions %d, messages %d, blocks %d, events %d\n",
		res.Sessions, rep.Written["messages"], rep.Written["blocks"], rep.Written["events"])
	if res.Mode == backup.ModeReplace {
		fmt.Fprintf(w, "replaced: purged %d message(s), %d session(s)\n",
			rep.Purged["messages"], rep.Purged["sessions"])
	}
	if res.DryRun {
		fmt.Fprintln(w, "dry run: nothing written")
	}
}
