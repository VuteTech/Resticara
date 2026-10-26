// Package restic builds and runs restic commands.
package restic

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"resticara/internal/config"
)

const (
	// ExitIncomplete is restic's exit code when the snapshot was created but some
	// source files could not be read.
	ExitIncomplete = 3

	retryLock = "--retry-lock 30m"
)

// BackupCommand returns the restic backup command for a job.
func BackupCommand(job config.Job) string {
	switch job.Type {
	case config.JobDir:
		return fmt.Sprintf("restic -r %s %s backup %s", job.Repository, retryLock, job.Directory)
	case config.JobMySQL:
		// restic runs mysqldump itself, so a failed dump fails the backup instead of storing a truncated snapshot
		return fmt.Sprintf("restic -r %s %s backup --stdin-filename %s.sql --stdin-from-command -- mysqldump --single-transaction %s", job.Repository, retryLock, job.Database, job.Database)
	}
	return ""
}

// ForgetCommand returns the restic command that applies the job's retention policy.
func ForgetCommand(job config.Job) string {
	return fmt.Sprintf("restic -r %s %s forget --keep-daily %d --keep-weekly %d --keep-monthly %d", job.Repository, retryLock, job.Retention.Daily, job.Retention.Weekly, job.Retention.Monthly)
}

// PruneCommand returns the restic command that removes unreferenced data from a repository.
func PruneCommand(repository string) string {
	return fmt.Sprintf("restic -r %s %s prune", repository, retryLock)
}

// Result is the outcome of a command. ExitCode is -1 when the command could not be started.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner runs commands.
type Runner interface {
	Run(command string) Result
}

// ExecRunner runs commands as processes. The command is split on whitespace; no shell is involved.
type ExecRunner struct{}

func (ExecRunner) Run(command string) Result {
	var stdoutBuf, stderrBuf bytes.Buffer

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return Result{ExitCode: -1, Stderr: "empty command"}
	}

	cmd := exec.Command(parts[0], parts[1:]...)
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
