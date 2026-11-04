package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsvxavier/accordion-context/internal/backup"
)

// dockerRunner runs the docker commands of --docker. Tests replace it with a
// fake that records the argv.
var dockerRunner backup.Runner = backup.ExecRunner{}

const backupUsage = "usage: backup [--project <id|path|.>] [-o file.db] [--stdout] [--force] [--docker <container>]"

// runBackup implements `acd-ctx backup` (spec §18.7).
func runBackup(args []string, s IO) int {
	fset := newFlagSet("backup", s)
	project := fset.String("project", "", "one project: an ID, or a path (. for the current project); empty copies the whole database")
	out := fset.String("o", "", "output file (default acd-ctx-backup-<time>.db in the working directory)")
	toStdout := fset.Bool("stdout", false, "write the backup to stdout (status goes to stderr)")
	force := fset.Bool("force", false, "replace the output file if it exists")
	docker := fset.String("docker", "", "back up inside this container; the file is written on this machine")
	pos, err := parseFlags(fset, args)
	if err != nil || len(pos) > 0 || (*toStdout && *out != "") {
		return usageError(s, backupUsage)
	}

	outPath := *out
	if outPath == "" && !*toStdout {
		outPath = defaultBackupPath(s)
	}
	if *toStdout {
		outPath = ""
	} else if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(s.Cwd, outPath)
	}

	if *docker != "" {
		if err := backup.DockerBackup(s.Ctx, dockerRunner, *docker, *project, outPath, *force, s.Stdout, s.Stderr); err != nil {
			return fail(s, err)
		}
		if outPath != "" {
			fmt.Fprintf(s.Stdout, "backup of container %s written to %s\n", *docker, outPath)
		}
		return ExitOK
	}

	src, err := resolveBackupSource(s, *project)
	if err != nil {
		return fail(s, err)
	}
	req := backup.CreateRequest{
		Source:    src.dbPath,
		ProjectID: src.projectID,
		Out:       outPath,
		Force:     *force,
	}
	status := s.Stdout
	if *toStdout {
		req.Stdout = s.Stdout
		status = s.Stderr
	}
	res, err := backup.Create(s.Ctx, req)
	if err != nil {
		return fail(s, err)
	}
	if res.Path != "" {
		fmt.Fprintf(status, "backup written to %s (%d bytes)\n", res.Path, res.Bytes)
	}
	fmt.Fprintf(status, "%d project(s), %d session(s), schema v%d\n",
		len(res.Info.Projects), len(res.Info.Sessions), res.Info.SchemaVersion)
	return ExitOK
}

// backupSource is the file to read and, when set, the one project to copy from it.
type backupSource struct {
	dbPath    string
	projectID string
}

// resolveBackupSource maps --project to a database. Without it, the whole global
// database is copied. A path names a project by its directory. A local project
// is copied as its own file (§18.3). Any other value is a project ID in the global
// database.
func resolveBackupSource(s IO, project string) (backupSource, error) {
	gp, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return backupSource{}, err
	}
	if project == "" {
		return backupSource{dbPath: gp}, nil
	}
	isDir := func(p string) bool {
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.Cwd, p)
		}
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}
	if !backup.IsPathArg(project, isDir) {
		return backupSource{dbPath: gp, projectID: project}, nil
	}
	dir := project
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.Cwd, dir)
	}
	sub := s
	sub.Cwd = dir
	sc, err := resolveScope(sub)
	if err != nil {
		return backupSource{}, err
	}
	if sc.cfg.Config.Storage == "local" {
		return backupSource{dbPath: sc.dbPath}, nil
	}
	return backupSource{dbPath: gp, projectID: sc.res.ID}, nil
}

// defaultBackupPath names a backup in the working directory after the current
// UTC time, so it never collides with acd-ctx.db.
func defaultBackupPath(s IO) string {
	name := "acd-ctx-backup-" + time.Now().UTC().Format("20060102-150405") + ".db"
	return filepath.Join(s.Cwd, name)
}
