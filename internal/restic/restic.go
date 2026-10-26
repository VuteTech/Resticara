// Package restic builds and runs restic commands.
package restic

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"resticara/internal/config"
)

const (
	// ExitIncomplete is restic's exit code when the snapshot was created but some
	// source files could not be read.
	ExitIncomplete = 3

	retryLock = "30m"
)

// Command is a program and its arguments. Arguments are passed as they are; no shell
// is involved, so they may contain spaces.
type Command []string

// String returns the command as it would be typed in a shell, quoting arguments
// that contain spaces or other special characters.
func (c Command) String() string {
	quoted := make([]string, len(c))
	for i, arg := range c {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	for _, r := range arg {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./_-", r)) {
			return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
	}
	return arg
}

func base(repository string) Command {
	return Command{"restic", "-r", repository, "--retry-lock", retryLock}
}

// BackupCommand returns the restic backup command for a job. A non-empty host is
// stored in the snapshot instead of the machine's hostname.
func BackupCommand(job config.Job, host string) Command {
	cmd := append(base(job.Repository), "backup")
	cmd = append(cmd, hostArgs(host)...)
	switch job.Type {
	case config.JobDir:
		cmd = append(cmd, excludeArgs(job.Exclude)...)
		cmd = append(cmd, job.Directories...)
	case config.JobMySQL:
		// restic runs mysqldump itself, so a failed dump fails the backup instead of storing a truncated snapshot
		cmd = append(cmd, "--stdin-filename", job.Database+".sql", "--stdin-from-command", "--", "mysqldump", "--single-transaction")
		cmd = append(cmd, job.MysqldumpArgs...)
		cmd = append(cmd, strings.Fields(job.Database)...)
	}
	return cmd
}

// excludeArgs returns the restic backup options for the excludes. Values are joined to
// their option with "=", so a pattern starting with "-" is not read as an option.
func excludeArgs(ex config.Exclude) []string {
	var args []string
	for _, pattern := range ex.Patterns {
		args = append(args, "--exclude="+pattern)
	}
	for _, file := range ex.Files {
		args = append(args, "--exclude-file="+file)
	}
	if ex.Caches {
		args = append(args, "--exclude-caches")
	}
	for _, name := range ex.IfPresent {
		args = append(args, "--exclude-if-present="+name)
	}
	return args
}

func hostArgs(host string) []string {
	if host == "" {
		return nil
	}
	return []string{"--host", host}
}

// ForgetCommand returns the restic command that applies the job's retention policy.
// A non-empty host limits it to that host's snapshots, so other machines sharing
// the repository keep theirs.
func ForgetCommand(job config.Job, host string) Command {
	cmd := append(base(job.Repository), "forget")
	cmd = append(cmd, hostArgs(host)...)
	return append(cmd,
		"--keep-daily", strconv.Itoa(job.Retention.Daily),
		"--keep-weekly", strconv.Itoa(job.Retention.Weekly),
		"--keep-monthly", strconv.Itoa(job.Retention.Monthly))
}

// CheckCommand returns the restic command that verifies a repository, reading the
// repository's check subset of the data.
func CheckCommand(repo config.Repository) Command {
	cmd := append(base(repo.URL), "check")
	if repo.CheckSubset != "" && repo.CheckSubset != "none" {
		cmd = append(cmd, "--read-data-subset="+repo.CheckSubset)
	}
	return cmd
}

// SnapshotsCommand lists the snapshots of a repository as JSON.
func SnapshotsCommand(repository string) Command {
	return append(base(repository), "snapshots", "--json")
}

// SnapshotPaths returns the paths restic records in the job's snapshots: the
// absolute directories, or "/<database>.sql" for a dump.
func SnapshotPaths(job config.Job) []string {
	if job.Type == config.JobMySQL {
		return []string{"/" + job.Database + ".sql"}
	}
	var paths []string
	for _, dir := range job.Directories {
		if abs, err := filepath.Abs(dir); err == nil {
			paths = append(paths, abs)
		}
	}
	sort.Strings(paths)
	return paths
}

// PruneCommand returns the restic command that removes unreferenced data from a
// repository, within the repository's prune limits.
func PruneCommand(repo config.Repository) Command {
	cmd := append(base(repo.URL), "prune")
	if repo.Prune.MaxUnused != "" {
		cmd = append(cmd, "--max-unused="+repo.Prune.MaxUnused)
	}
	if repo.Prune.MaxRepackSize != "" {
		cmd = append(cmd, "--max-repack-size="+repo.Prune.MaxRepackSize)
	}
	return cmd
}

// Result is the outcome of a command. ExitCode is -1 when the command could not be started.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner runs commands.
type Runner interface {
	Run(cmd Command) Result
}

// ExecRunner runs commands as processes.
type ExecRunner struct{}

func (ExecRunner) Run(c Command) Result {
	var stdoutBuf, stderrBuf bytes.Buffer

	if len(c) == 0 {
		return Result{ExitCode: -1, Stderr: "empty command"}
	}

	cmd := exec.Command(c[0], c[1:]...) //nolint:gosec // G204: running restic with arguments from the config is the purpose of resticara
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		return Result{ExitCode: exitErr.ExitCode(), Stdout: stdoutBuf.String(), Stderr: stderrBuf.String()}
	}
	if err != nil {
		return Result{ExitCode: -1, Stdout: stdoutBuf.String(), Stderr: stderrBuf.String() + err.Error()}
	}
	return Result{Stdout: stdoutBuf.String(), Stderr: stderrBuf.String()}
}
