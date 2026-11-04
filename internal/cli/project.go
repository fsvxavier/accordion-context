package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fsvxavier/accordion-context/internal/store"
)

func runProject(args []string, s IO) int {
	if len(args) == 0 {
		return usageError(s, "project needs a subcommand")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "init":
		fset := newFlagSet("project init", s)
		local := fset.Bool("local-storage", false, "keep this project's data in .acd-ctx/acd-ctx.db")
		pos, err := parseFlags(fset, rest)
		if err != nil || len(pos) > 0 {
			return usageError(s, "usage: project init [--local-storage]")
		}
		return projectInit(s, *local)
	case "show":
		if len(rest) > 0 {
			return usageError(s, "usage: project show")
		}
		return projectShow(s)
	case "list":
		if len(rest) > 0 {
			return usageError(s, "usage: project list")
		}
		return projectList(s)
	case "move":
		if len(rest) != 2 {
			return usageError(s, "usage: project move <old> <new>")
		}
		return projectMove(s, rest[0], rest[1])
	case "migrate":
		return projectMigrate(s, rest)
	case "purge":
		return projectPurge(s, rest)
	}
	return usageError(s, fmt.Sprintf("unknown project subcommand %q", sub))
}

// projectInit creates .acd-ctx/ at the project root (spec §18.3). The project
// ID is the global one (hash of the root), so sessions already recorded in the
// global database stay attached. With local set, the database is created now
// and the config says storage: local.
func projectInit(s IO, local bool) int {
	res, err := resolveProject(s)
	if err != nil {
		return fail(s, err)
	}
	dir := filepath.Join(res.Root, localDirName)
	if fileExists(dir) {
		return fail(s, fmt.Errorf("%s already exists; the project is initialized", dir))
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fail(s, err)
	}
	storage := "global"
	if local {
		storage = "local"
	}
	id := res.ID
	if err := os.WriteFile(filepath.Join(dir, projectIDName), []byte(id+"\n"), projectIDMode); err != nil {
		return fail(s, err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte(configTemplate(storage)), configFileMode); err != nil {
		return fail(s, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignoreFileText), ignoreFileMode); err != nil {
		return fail(s, err)
	}
	if local {
		st, err := openStore(localDBPath(res.Root))
		if err != nil {
			return fail(s, err)
		}
		err = st.EnsureProject(context.Background(), id, res.Root, "local", time.Now().Unix())
		if cerr := st.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fail(s, err)
		}
	}
	fmt.Fprintf(s.Stdout, "initialized %s (project %s, storage %s)\n", dir, id, storage)
	if !local {
		fmt.Fprintln(s.Stdout, "data stays in the global database; use project migrate --to local to move it")
	}
	return ExitOK
}

// configTemplate is the .acd-ctx/config.yaml written by init (spec §18.3).
func configTemplate(storage string) string {
	return "# acd-ctx project configuration (spec §18.3).\n" +
		"# Only compression parameters are honoured here (§18.6).\n" +
		"storage: " + storage + "\n"
}

func projectShow(s IO) int {
	sc, err := resolveScope(s)
	if err != nil {
		return fail(s, err)
	}
	w := tabwriter.NewWriter(s.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "project\t%s\n", sc.res.ID)
	fmt.Fprintf(w, "root\t%s\n", sc.res.Root)
	fmt.Fprintf(w, "detected by\t%s\n", sc.res.Source)
	fmt.Fprintf(w, "storage\t%s\n", sc.cfg.Config.Storage)
	fmt.Fprintf(w, "database\t%s\n", sc.dbPath)
	return flushOK(s, w)
}

func flushOK(s IO, w *tabwriter.Writer) int {
	if err := w.Flush(); err != nil {
		return fail(s, err)
	}
	return ExitOK
}

func projectList(s IO) int {
	path, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}
	if !fileExists(path) {
		fmt.Fprintln(s.Stdout, "no projects")
		return ExitOK
	}
	st, err := store.Open(path)
	if err != nil {
		return fail(s, err)
	}
	defer st.Close()
	list, err := st.ListProjects(context.Background())
	if err != nil {
		return fail(s, err)
	}
	if len(list) == 0 {
		fmt.Fprintln(s.Stdout, "no projects")
		return ExitOK
	}
	w := tabwriter.NewWriter(s.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTORAGE\tSESSIONS\tSIZE\tLAST ACTIVITY\tROOT")
	for _, p := range list {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", p.ID, p.Storage, p.Sessions,
			humanBytes(p.Bytes), time.Unix(p.LastSeenAt, 0).Format("2006-01-02 15:04"), p.RootPath)
	}
	return flushOK(s, w)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// projectMove re-points a project at a new root path (spec §18.7). <old> may
// be a project ID or the old path, which may no longer exist. The ID does not
// change. A local database at <new>, if any, is updated too.
func projectMove(s IO, oldArg, newArg string) int {
	path, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}
	if !fileExists(path) {
		return fail(s, fmt.Errorf("no global database at %s", path))
	}
	newRoot, err := canonicalDir(s.Cwd, newArg)
	if err != nil {
		return fail(s, fmt.Errorf("new path: %w", err))
	}
	st, err := store.Open(path)
	if err != nil {
		return fail(s, err)
	}
	defer st.Close()
	ctx := context.Background()

	id := oldArg
	if _, err := st.GetProject(ctx, oldArg); err != nil {
		if !errors.Is(err, store.ErrProjectNotFound) {
			return fail(s, err)
		}
		oldPath := absPath(s.Cwd, oldArg)
		if byRoot, rerr := st.ProjectIDByRoot(ctx, oldPath); rerr == nil {
			id = byRoot
		} else if errors.Is(rerr, store.ErrProjectNotFound) {
			id = globalProjectID(oldPath)
		} else {
			return fail(s, rerr)
		}
	}
	if err := st.MoveProject(ctx, id, newRoot); err != nil {
		return fail(s, fmt.Errorf("move %s: %w", id, err))
	}
	fmt.Fprintf(s.Stdout, "project %s now at %s\n", id, newRoot)

	if fileExists(localDBPath(newRoot)) {
		if localID, err := readLocalProjectID(newRoot); err == nil {
			lst, err := store.Open(localDBPath(newRoot))
			if err != nil {
				return fail(s, err)
			}
			err = lst.MoveProject(ctx, localID, newRoot)
			_ = lst.Close()
			if err != nil && !errors.Is(err, store.ErrProjectNotFound) {
				return fail(s, err)
			}
		}
	}
	return ExitOK
}

// absPath makes p absolute against base, without requiring it to exist.
func absPath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// canonicalDir resolves p against base and requires an existing directory.
func canonicalDir(base, p string) (string, error) {
	abs, err := filepath.EvalSymlinks(absPath(base, p))
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// projectMigrate copies the current project's sessions between the global and
// the local database, in one transaction, and verifies the invariants at the
// destination (spec §18.7). The source is kept; purge it when no longer needed.
func projectMigrate(s IO, args []string) int {
	fset := newFlagSet("project migrate", s)
	to := fset.String("to", "", "destination storage: local or global")
	pos, err := parseFlags(fset, args)
	if err != nil || len(pos) > 0 || (*to != "local" && *to != "global") {
		return usageError(s, "usage: project migrate --to local|global")
	}
	sc, err := resolveScope(s)
	if err != nil {
		return fail(s, err)
	}
	root := sc.res.Root
	ctx := context.Background()
	localDir := filepath.Join(root, localDirName)
	globalPath, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}

	var srcPath, dstPath, srcID, dstID string
	if *to == "local" {
		if !fileExists(localDir) {
			return fail(s, errNoLocalProject)
		}
		localID, err := readLocalProjectID(root)
		if err != nil {
			return fail(s, err)
		}
		srcPath, srcID = globalPath, globalProjectID(root)
		dstPath, dstID = localDBPath(root), localID
	} else {
		if !fileExists(localDir) {
			return fail(s, errNoLocalProject)
		}
		localID, err := readLocalProjectID(root)
		if err != nil {
			return fail(s, err)
		}
		srcPath, srcID = localDBPath(root), localID
		dstPath, dstID = globalPath, localID
	}
	if srcPath == dstPath {
		return fail(s, errors.New("source and destination are the same database"))
	}
	if !fileExists(srcPath) {
		return fail(s, fmt.Errorf("no database at %s to migrate from", srcPath))
	}

	src, err := openStore(srcPath)
	if err != nil {
		return fail(s, err)
	}
	defer src.Close()
	dump, err := src.ExportProject(ctx, srcID)
	if errors.Is(err, store.ErrProjectNotFound) {
		return fail(s, fmt.Errorf("nothing to migrate: project %s has no data in %s", srcID, srcPath))
	}
	if err != nil {
		return fail(s, err)
	}

	dst, err := openStore(dstPath)
	if err != nil {
		return fail(s, err)
	}
	defer dst.Close()
	counts, err := dst.ImportProject(ctx, dump, store.ImportTarget{ProjectID: dstID, RootPath: root, Storage: *to})
	if err != nil {
		return fail(s, fmt.Errorf("migrate (rolled back, nothing written): %w", err))
	}
	for _, sid := range dump.SessionIDs() {
		if err := dst.CheckInvariants(ctx, sid); err != nil {
			return fail(s, fmt.Errorf("invariants at destination for session %s: %w", sid, err))
		}
	}
	if err := setConfigStorage(filepath.Join(localDir, configFileName), *to); err != nil {
		return fail(s, fmt.Errorf("data copied, but config not updated: %w", err))
	}

	fmt.Fprintf(s.Stdout, "migrated project %s to %s storage: %d sessions, %d messages, %d blocks, %d events\n",
		dstID, *to, len(dump.SessionIDs()), counts["messages"], counts["blocks"], counts["events"])
	fmt.Fprintf(s.Stdout, "source kept at %s (remove with project purge %s)\n", srcPath, srcID)
	if *to == "global" {
		fmt.Fprintln(s.Stdout, "note: the project keeps its local ID while .acd-ctx/ exists")
	}
	return ExitOK
}

// projectPurge deletes all data of one project (spec §18.7). It asks for a
// typed confirmation on stdin unless --yes is given.
func projectPurge(s IO, args []string) int {
	fset := newFlagSet("project purge", s)
	yes := fset.Bool("yes", false, "skip the confirmation prompt")
	pos, err := parseFlags(fset, args)
	if err != nil || len(pos) != 1 {
		return usageError(s, "usage: project purge <id> [--yes]")
	}
	id := pos[0]
	ctx := context.Background()

	// A project can exist in the global database, in a local one, or both.
	type target struct {
		path string
		info store.ProjectInfo
	}
	var targets []target
	candidates, err := purgeCandidates(s)
	if err != nil {
		return fail(s, err)
	}
	for _, path := range candidates {
		st, err := store.Open(path)
		if err != nil {
			return fail(s, err)
		}
		info, err := st.GetProject(ctx, id)
		_ = st.Close()
		if errors.Is(err, store.ErrProjectNotFound) {
			continue
		}
		if err != nil {
			return fail(s, err)
		}
		targets = append(targets, target{path, info})
	}
	if len(targets) == 0 {
		return fail(s, fmt.Errorf("no project %s in the known databases", id))
	}

	if !*yes {
		for _, t := range targets {
			fmt.Fprintf(s.Stderr, "purge project %s (%s) in %s: %d sessions\n", id, t.info.RootPath, t.path, t.info.Sessions)
		}
		fmt.Fprint(s.Stderr, "This deletes all sessions, messages, blocks and events of the project and cannot be undone. Type 'yes' to continue: ")
		if !confirmed(s.Stdin) {
			fmt.Fprintln(s.Stderr, "\naborted; nothing deleted")
			return ExitFail
		}
	}

	for _, t := range targets {
		st, err := store.Open(t.path)
		if err != nil {
			return fail(s, err)
		}
		counts, err := st.PurgeProject(ctx, id)
		_ = st.Close()
		if err != nil {
			return fail(s, fmt.Errorf("purge %s: %w", t.path, err))
		}
		fmt.Fprintf(s.Stdout, "purged project %s from %s: sessions=%d messages=%d blocks=%d events=%d contents=%d\n",
			id, t.path, counts["sessions"], counts["messages"], counts["blocks"], counts["events"], counts["contents"])
	}
	return ExitOK
}

// confirmed reads one line and reports whether it is yes or y.
func confirmed(in io.Reader) bool {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "yes" || ans == "y"
}

// purgeCandidates lists the existing databases that may hold a project.
func purgeCandidates(s IO) ([]string, error) {
	globalPath, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return nil, err
	}
	var out []string
	if fileExists(globalPath) {
		out = append(out, globalPath)
	}
	res, err := resolveProject(s)
	if err != nil {
		return nil, err
	}
	if local := localDBPath(res.Root); fileExists(local) && local != globalPath {
		out = append(out, local)
	}
	return out, nil
}
