package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fsvxavier/accordion-context/internal/backup"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const restoreUsage = "usage: restore <file.db> [--project <id>] [--mode merge|replace] [--yes] [--to local|global] [--dry-run] [--docker <container>]\n       restore --stdin [same flags]"

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
	pos, err := parseFlags(fset, args)
	if err != nil || (*stdin && len(pos) > 0) || (!*stdin && len(pos) != 1) ||
		(*mode != backup.ModeMerge && *mode != backup.ModeReplace) ||
		(*to != backup.StorageGlobal && *to != backup.StorageLocal) {
		return usageError(s, restoreUsage)
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
			Project: *project,
			Mode:    *mode,
			To:      *to,
			Yes:     *yes,
			DryRun:  *dryRun,
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
	})
	if errors.Is(err, store.ErrRestoreConflict) {
		return fail(s, fmt.Errorf("restore refused, nothing written: %w", err))
	}
	if err != nil {
		return fail(s, err)
	}
	printRestore(s, res)
	return ExitOK
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
