package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// containerName follows Docker's own rule: alphanumerics, '_', '.' and '-',
// starting with an alphanumeric, so a name cannot be read as a flag.
var containerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Runner runs a program with an argument vector. Implementations must not
// route the arguments through a shell.
type Runner interface {
	Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// ExecRunner runs real processes with exec.CommandContext.
type ExecRunner struct{}

// Run starts name with args and waits for it.
func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// ValidContainer reports an error unless name is a safe container name.
func ValidContainer(name string) error {
	if !containerName.MatchString(name) {
		return fmt.Errorf("invalid container name %q (want [a-zA-Z0-9_.-]+)", name)
	}
	return nil
}

// dockerArgs returns the argv that runs "acd-ctx <acdArgs>" inside container.
func dockerArgs(container string, acdArgs ...string) ([]string, error) {
	if err := ValidContainer(container); err != nil {
		return nil, err
	}
	return append([]string{"exec", "-i", container, "acd-ctx"}, acdArgs...), nil
}

// DockerBackup runs "acd-ctx backup --stdout" inside container. With out set,
// the stream is verified and installed at out on this machine, under Adopt's
// rules. With out empty, the stream goes to stdout.
func DockerBackup(ctx context.Context, r Runner, container, project, out string, force bool, stdout, stderr io.Writer) error {
	acdArgs := []string{"backup", "--stdout"}
	if project != "" {
		acdArgs = append(acdArgs, "--project", project)
	}
	args, err := dockerArgs(container, acdArgs...)
	if err != nil {
		return err
	}
	if out == "" {
		return r.Run(ctx, "docker", args, nil, stdout, stderr)
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil && !force {
		return fmt.Errorf("%w: %s", ErrExists, abs)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".acd-ctx-backup-*.db")
	if err != nil {
		return fmt.Errorf("backup: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := r.Run(ctx, "docker", args, nil, tmp, stderr); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("docker backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	_, err = Adopt(ctx, tmpPath, abs, force)
	return err
}

// DockerRestoreOptions are the restore flags forwarded to the container.
type DockerRestoreOptions struct {
	Project string
	Mode    string
	To      string
	Yes     bool
	DryRun  bool
}

// DockerRestore pipes the backup stream into "acd-ctx restore --stdin" inside
// container. The container validates and imports it, and its report goes to
// stdout and stderr.
func DockerRestore(ctx context.Context, r Runner, container string, in io.Reader, opts DockerRestoreOptions, stdout, stderr io.Writer) error {
	acdArgs := []string{"restore", "--stdin", "--mode", opts.Mode, "--to", opts.To}
	if opts.Project != "" {
		acdArgs = append(acdArgs, "--project", opts.Project)
	}
	if opts.Yes {
		acdArgs = append(acdArgs, "--yes")
	}
	if opts.DryRun {
		acdArgs = append(acdArgs, "--dry-run")
	}
	args, err := dockerArgs(container, acdArgs...)
	if err != nil {
		return err
	}
	if err := r.Run(ctx, "docker", args, in, stdout, stderr); err != nil {
		return fmt.Errorf("docker restore: %w", err)
	}
	return nil
}
