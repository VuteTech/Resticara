// Package restic builds and runs restic commands.
package restic

import (
	"bytes"
	"os/exec"
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

// BackupCommand returns the restic backup command for a job.
func BackupCommand(job config.Job) Command {
	cmd := base(job.Repository)
	switch job.Type {
	case config.JobDir:
		cmd = append(cmd, "backup")
		cmd = append(cmd, job.Directories...)
	case config.JobMySQL:
		// restic runs mysqldump itself, so a failed dump fails the backup instead of storing a truncated snapshot
		cmd = append(cmd, "backup", "--stdin-filename", job.Database+".sql", "--stdin-from-command", "--", "mysqldump", "--single-transaction")
		cmd = append(cmd, job.MysqldumpArgs...)
		cmd = append(cmd, strings.Fields(job.Database)...)
	}
	return cmd
}

// ForgetCommand returns the restic command that applies the job's retention policy.
func ForgetCommand(job config.Job) Command {
	return append(base(job.Repository), "forget",
		"--keep-daily", strconv.Itoa(job.Retention.Daily),
		"--keep-weekly", strconv.Itoa(job.Retention.Weekly),
		"--keep-monthly", strconv.Itoa(job.Retention.Monthly))
}

// PruneCommand returns the restic command that removes unreferenced data from a repository.
func PruneCommand(repository string) Command {
	return append(base(repository), "prune")
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

	cmd := exec.Command(c[0], c[1:]...)
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
