package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fsvxavier/accordion-context/internal/backup"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// cleanStdinIsTerminal reports whether a confirmation can be typed. A
// non-terminal stdin without --yes is refused, so a script never deletes by
// accident. Tests replace it.
var cleanStdinIsTerminal = stdinIsTerminal

const cleanUsage = `usage: clean (--project <id|path|.> | --session <id> | --older-than <dur> [--project <id|path|.>] | --all)
             [--dry-run] [--yes] [--backup <file.db>] [--vacuum] [--exports] [--keep-config] [--docker <container>]
  <dur> is 30d, 12h, 90m or any Go duration. The confirmation word is the project ID for
  --project, the session ID for --session, and the word delete for --all and --older-than.`

// cleanFlags are the parsed flags of clean.
type cleanFlags struct {
	project, session, olderThan string
	all, dryRun, yes            bool
	backup                      string
	vacuum, exports             bool
	keepConfig                  bool
	docker                      string
}

// cleanMode says which sessions a clean removes.
type cleanMode int

const (
	modeProject cleanMode = iota
	modeSession
	modeOlder
	modeAll
)

// cleanPlan is the resolved request: what to remove, and the word that
// confirms it.
type cleanPlan struct {
	mode      cleanMode
	projectID string
	sessionID string
	cutoff    time.Time
	label     string
}

// cleanTarget is one database with the data a clean would remove in it.
type cleanTarget struct {
	path    string
	report  store.Report
	exports []string
}

// run applies the plan to st. A dry run counts without deleting.
func (p cleanPlan) run(ctx context.Context, st *store.Store, dry bool) (store.Report, error) {
	o := store.PurgeOpts{DryRun: dry}
	switch p.mode {
	case modeProject:
		return st.PurgeProjectReport(ctx, p.projectID, o)
	case modeSession:
		return st.PurgeSession(ctx, p.sessionID, o)
	case modeOlder:
		return st.PurgeOlderThan(ctx, p.projectID, p.cutoff, o)
	default:
		return st.PurgeAll(ctx, o)
	}
}

// sessions lists the sessions the plan covers in st, for the export cleanup.
func (p cleanPlan) sessions(ctx context.Context, st *store.Store) ([]string, error) {
	switch p.mode {
	case modeProject:
		return st.ScopeSessionIDs(ctx, p.projectID, time.Time{})
	case modeSession:
		return []string{p.sessionID}, nil
	case modeOlder:
		return st.ScopeSessionIDs(ctx, p.projectID, p.cutoff)
	default:
		return st.ScopeSessionIDs(ctx, "", time.Time{})
	}
}

// runClean implements `acd-ctx clean` (spec §18.7). It counts the data in
// every database that holds it, shows the Report, asks for the typed
// confirmation, takes the backup and only then deletes.
func runClean(args []string, s IO) int {
	f, err := parseCleanFlags(args)
	if err != nil {
		fmt.Fprintf(s.Stderr, "acd-ctx: %v\n%s\n", err, cleanUsage)
		return ExitUsage
	}
	if f.docker != "" {
		return cleanDocker(s, f)
	}
	plan, paths, err := resolveClean(s, f)
	if err != nil {
		return fail(s, err)
	}

	ctx := s.Ctx
	var found []cleanTarget
	for _, path := range paths {
		t, ok, err := dryRunTarget(ctx, s, plan, path, f.exports)
		if err != nil {
			return fail(s, err)
		}
		if ok {
			found = append(found, t)
		}
	}
	if len(found) == 0 {
		return fail(s, fmt.Errorf("nothing to clean: no matching data in the known databases"))
	}
	var total store.Report
	var totalExports int
	for _, t := range found {
		fmt.Fprintf(s.Stdout, "would delete from %s: %s\n", t.path, formatReport(t.report))
		if f.exports {
			fmt.Fprintf(s.Stdout, "would remove %d exported file(s)\n", len(t.exports))
		}
		total = addReport(total, t.report)
		totalExports += len(t.exports)
	}
	if len(found) > 1 {
		fmt.Fprintf(s.Stdout, "total: %s\n", formatReport(total))
	}
	if f.dryRun {
		fmt.Fprintln(s.Stdout, "dry run: nothing deleted")
		return ExitOK
	}

	if !f.yes {
		if !cleanStdinIsTerminal(s.Stdin) {
			fmt.Fprintln(s.Stderr, "acd-ctx: clean needs --yes when stdin is not a terminal; nothing deleted")
			return ExitUsage
		}
		fmt.Fprintf(s.Stderr, "type %q to delete the data listed above (cannot be undone): ", plan.label)
		if !typedEquals(s.Stdin, plan.label) {
			fmt.Fprintln(s.Stderr, "\naborted; nothing deleted")
			return ExitFail
		}
	}

	// The backup comes first: if it fails, nothing is deleted.
	if f.backup != "" {
		for i, t := range found {
			out := backupPath(s, f.backup, i, len(found))
			res, err := backup.Create(ctx, backup.CreateRequest{Source: t.path, Out: out})
			if err != nil {
				return fail(s, fmt.Errorf("backup of %s failed, nothing deleted: %w", t.path, err))
			}
			fmt.Fprintf(s.Stdout, "backup written to %s (%d bytes)\n", res.Path, res.Bytes)
		}
	}

	for _, t := range found {
		st, err := store.Open(t.path)
		if err != nil {
			return fail(s, err)
		}
		rep, err := plan.run(ctx, st, false)
		if err == nil && f.vacuum {
			err = st.Vacuum(ctx)
		}
		_ = st.Close()
		if err != nil {
			return fail(s, fmt.Errorf("clean %s: %w", t.path, err))
		}
		fmt.Fprintf(s.Stdout, "deleted from %s: %s\n", t.path, formatReport(rep))
		if f.exports {
			removed := removeExports(t.exports)
			fmt.Fprintf(s.Stdout, "removed %d exported file(s)\n", removed)
		}
		if f.vacuum {
			fmt.Fprintf(s.Stdout, "vacuumed %s\n", t.path)
		}
	}
	return ExitOK
}

// dryRunTarget counts the plan in one database. It reports ok false when the
// database holds nothing the plan names (an unknown project or session).
func dryRunTarget(ctx context.Context, s IO, plan cleanPlan, path string, withExports bool) (cleanTarget, bool, error) {
	st, err := store.Open(path)
	if err != nil {
		return cleanTarget{}, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer st.Close()
	rep, err := plan.run(ctx, st, true)
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, store.ErrSessionNotFound) {
		return cleanTarget{}, false, nil
	}
	if err != nil {
		return cleanTarget{}, false, fmt.Errorf("count in %s: %w", path, err)
	}
	t := cleanTarget{path: path, report: rep}
	if withExports {
		sids, err := plan.sessions(ctx, st)
		if err != nil {
			return cleanTarget{}, false, err
		}
		projects, err := st.ListProjects(ctx)
		if err != nil {
			return cleanTarget{}, false, err
		}
		t.exports, err = exportFiles(s, projects, sids)
		if err != nil {
			return cleanTarget{}, false, err
		}
	}
	return t, true, nil
}

// parseCleanFlags parses the flags and checks that exactly one scope is given.
// --project may narrow --older-than; it cannot combine with --session or --all.
func parseCleanFlags(args []string) (cleanFlags, error) {
	var f cleanFlags
	fset := flag.NewFlagSet("clean", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	fset.StringVar(&f.project, "project", "", "one project: an ID, or a path (. for the current project)")
	fset.StringVar(&f.session, "session", "", "one session ID")
	fset.StringVar(&f.olderThan, "older-than", "", "remove sessions whose last activity is older than this age (30d, 12h)")
	fset.BoolVar(&f.all, "all", false, "every project and all derived data")
	fset.BoolVar(&f.dryRun, "dry-run", false, "print what would be deleted, delete nothing")
	fset.BoolVar(&f.yes, "yes", false, "skip the typed confirmation")
	fset.StringVar(&f.backup, "backup", "", "write a verified backup of each database to this file before deleting")
	fset.BoolVar(&f.vacuum, "vacuum", false, "compact the database files after deleting")
	fset.BoolVar(&f.exports, "exports", false, "also remove exported block files this tool created for the removed sessions")
	fset.BoolVar(&f.keepConfig, "keep-config", true, "never delete configuration, the CA or the plugin token (always true)")
	fset.StringVar(&f.docker, "docker", "", "run the clean inside this container")
	pos, err := parseFlags(fset, args)
	if err != nil {
		return f, err
	}
	if len(pos) > 0 {
		return f, fmt.Errorf("unexpected argument %q", pos[0])
	}
	if !f.keepConfig {
		return f, errors.New("--keep-config=false is not supported: clean never deletes configuration, the CA or the plugin token")
	}
	switch {
	case f.all && (f.project != "" || f.session != "" || f.olderThan != ""):
		return f, errors.New("--all cannot be combined with --project, --session or --older-than")
	case f.session != "" && (f.project != "" || f.olderThan != ""):
		return f, errors.New("--session cannot be combined with --project or --older-than")
	case f.project == "" && f.session == "" && f.olderThan == "" && !f.all:
		return f, errors.New("choose a scope: --project, --session, --older-than or --all")
	}
	if f.olderThan != "" {
		if _, err := parseAge(f.olderThan); err != nil {
			return f, err
		}
	}
	return f, nil
}

// parseAge reads 30d (days) or any Go duration such as 12h.
func parseAge(s string) (time.Duration, error) {
	var d time.Duration
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --older-than %q (want 30d, 12h, ...)", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		d, err = time.ParseDuration(s)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("invalid --older-than %q (want 30d, 12h, ...)", s)
		}
	}
	return d, nil
}

// resolveClean turns the flags into a plan and the databases it applies to.
// A project given by path resolves to one database and one project ID. A
// project ID, or no project, applies to every database this command knows.
func resolveClean(s IO, f cleanFlags) (cleanPlan, []string, error) {
	var plan cleanPlan
	switch {
	case f.all:
		plan.mode = modeAll
		plan.label = "delete"
	case f.session != "":
		plan.mode = modeSession
		plan.sessionID = f.session
		plan.label = f.session
	case f.olderThan != "":
		d, err := parseAge(f.olderThan)
		if err != nil {
			return plan, nil, err
		}
		plan.mode = modeOlder
		plan.cutoff = time.Now().Add(-d)
		plan.label = "delete"
	default:
		plan.mode = modeProject
	}

	var paths []string
	if f.project != "" {
		isDir := func(p string) bool {
			if !filepath.IsAbs(p) {
				p = filepath.Join(s.Cwd, p)
			}
			fi, err := os.Stat(p)
			return err == nil && fi.IsDir()
		}
		if backup.IsPathArg(f.project, isDir) {
			dir := f.project
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(s.Cwd, dir)
			}
			sub := s
			sub.Cwd = dir
			sc, err := resolveScope(sub)
			if err != nil {
				return plan, nil, err
			}
			if !fileExists(sc.dbPath) {
				return plan, nil, fmt.Errorf("no database for the project at %s", dir)
			}
			plan.projectID = sc.res.ID
			paths = []string{sc.dbPath}
		} else {
			plan.projectID = f.project
		}
		if plan.mode == modeProject {
			plan.label = plan.projectID
		}
	}
	if plan.mode == modeProject && plan.projectID == "" {
		return plan, nil, errors.New("clean --project needs a project")
	}
	if paths == nil {
		var err error
		paths, err = purgeCandidates(s)
		if err != nil {
			return plan, nil, err
		}
	}
	if len(paths) == 0 {
		return plan, nil, errors.New("no database to clean")
	}
	return plan, paths, nil
}

// exportTag is the part of an export file name that identifies its session.
// It is the first 12 hex digits of sha256(session), as ExportBlock writes it.
func exportTag(sid string) string {
	sum := sha256.Sum256([]byte(sid))
	return hex.EncodeToString(sum[:6])
}

// exportNameRE is the name ExportBlock gives every file: block id, session
// tag, .txt.
var exportNameRE = regexp.MustCompile(`^b[0-9]{1,12}-([0-9a-f]{12})\.txt$`)

// exportFiles lists the export files of the given sessions in the export
// directory of every project in the database. Only regular files whose name
// matches what ExportBlock writes, and whose tag belongs to one of sids, are
// listed. Symbolic links and other files are never touched.
func exportFiles(s IO, projects []store.ProjectInfo, sids []string) ([]string, error) {
	if len(sids) == 0 {
		return nil, nil
	}
	want := make(map[string]bool, len(sids))
	for _, sid := range sids {
		want[exportTag(sid)] = true
	}
	var out []string
	for _, p := range projects {
		dir, err := exportDir(s, p)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read exports %s: %w", dir, err)
		}
		for _, e := range entries {
			m := exportNameRE.FindStringSubmatch(e.Name())
			if m == nil || !want[m[1]] {
				continue
			}
			path := filepath.Join(dir, e.Name())
			fi, err := os.Lstat(path)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			out = append(out, path)
		}
	}
	return out, nil
}

// exportDir is where decompress mode writes a project's exports (see
// plugin.exportsDir): .acd-ctx/exports for a local project, else the data
// home's acd-ctx/exports/<id>.
func exportDir(s IO, p store.ProjectInfo) (string, error) {
	if p.Storage == "local" {
		return filepath.Join(p.RootPath, localDirName, "exports"), nil
	}
	base, ok := s.LookupEnv("XDG_DATA_HOME")
	if !ok || base == "" {
		home, err := homeDir(s.LookupEnv)
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "acd-ctx", "exports", p.ID), nil
}

// removeExports deletes the listed files and returns how many it removed.
func removeExports(paths []string) int {
	n := 0
	for _, p := range paths {
		if err := os.Remove(p); err == nil {
			n++
		}
	}
	return n
}

// cleanDocker runs the clean inside a container. The project must be named by
// ID: a path would mean a different directory inside the container. The
// report comes from a dry run inside the container, the confirmation is typed
// here, and the backup is streamed to this machine before the real run.
func cleanDocker(s IO, f cleanFlags) int {
	if f.project != "" && backup.IsPathArg(f.project, func(string) bool { return false }) {
		return usageError(s, "clean --docker needs a project ID, not a path")
	}
	if err := backup.ValidContainer(f.docker); err != nil {
		return fail(s, err)
	}
	ctx := s.Ctx
	dryArgs := cleanArgs(f, true, false)
	if err := backup.DockerClean(ctx, dockerRunner, f.docker, dryArgs, s.Stdout, s.Stderr); err != nil {
		return fail(s, err)
	}
	if f.dryRun {
		fmt.Fprintln(s.Stdout, "dry run: nothing deleted")
		return ExitOK
	}
	if !f.yes {
		if !cleanStdinIsTerminal(s.Stdin) {
			fmt.Fprintln(s.Stderr, "acd-ctx: clean needs --yes when stdin is not a terminal; nothing deleted")
			return ExitUsage
		}
		label := "delete"
		switch {
		case f.project != "":
			label = f.project
		case f.session != "":
			label = f.session
		}
		fmt.Fprintf(s.Stderr, "type %q to delete the data listed above in container %s (cannot be undone): ", label, f.docker)
		if !typedEquals(s.Stdin, label) {
			fmt.Fprintln(s.Stderr, "\naborted; nothing deleted")
			return ExitFail
		}
	}
	if f.backup != "" {
		out := f.backup
		if !filepath.IsAbs(out) {
			out = filepath.Join(s.Cwd, out)
		}
		if err := backup.DockerBackup(ctx, dockerRunner, f.docker, "", out, false, s.Stdout, s.Stderr); err != nil {
			return fail(s, fmt.Errorf("backup of container %s failed, nothing deleted: %w", f.docker, err))
		}
		fmt.Fprintf(s.Stdout, "backup of container %s written to %s\n", f.docker, out)
	}
	if err := backup.DockerClean(ctx, dockerRunner, f.docker, cleanArgs(f, false, true), s.Stdout, s.Stderr); err != nil {
		return fail(s, err)
	}
	return ExitOK
}

// cleanArgs rebuilds the clean command line for the container. --backup and
// --docker stay on this side. dry and yes set the mode of the inner run.
func cleanArgs(f cleanFlags, dry, yes bool) []string {
	var a []string
	if f.project != "" {
		a = append(a, "--project", f.project)
	}
	if f.session != "" {
		a = append(a, "--session", f.session)
	}
	if f.olderThan != "" {
		a = append(a, "--older-than", f.olderThan)
	}
	if f.all {
		a = append(a, "--all")
	}
	if f.vacuum {
		a = append(a, "--vacuum")
	}
	if f.exports {
		a = append(a, "--exports")
	}
	if dry {
		a = append(a, "--dry-run")
	}
	if yes {
		a = append(a, "--yes")
	}
	return a
}

// backupPath names the backup of target i. With one target the name is used
// as given; with several, each gets a -N suffix before the extension.
func backupPath(s IO, name string, i, n int) string {
	if !filepath.IsAbs(name) {
		name = filepath.Join(s.Cwd, name)
	}
	if n == 1 {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(i+1) + ext
}

// typedEquals reads one line and reports whether it is exactly want.
func typedEquals(in io.Reader, want string) bool {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return false
	}
	return strings.TrimSpace(line) == want
}

// stdinIsTerminal reports whether r is a character device, such as a terminal.
func stdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// addReport sums two reports field by field.
func addReport(a, b store.Report) store.Report {
	return store.Report{
		Projects:   a.Projects + b.Projects,
		Sessions:   a.Sessions + b.Sessions,
		Messages:   a.Messages + b.Messages,
		Blocks:     a.Blocks + b.Blocks,
		Events:     a.Events + b.Events,
		Contents:   a.Contents + b.Contents,
		BytesFreed: a.BytesFreed + b.BytesFreed,
	}
}

// formatReport is the one-line form of a Report.
func formatReport(r store.Report) string {
	return fmt.Sprintf("projects=%d sessions=%d messages=%d blocks=%d events=%d contents=%d bytes_freed=%d",
		r.Projects, r.Sessions, r.Messages, r.Blocks, r.Events, r.Contents, r.BytesFreed)
}
