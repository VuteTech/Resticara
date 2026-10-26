// Package backup runs the configured backup jobs.
package backup

import (
	"fmt"
	"io"

	"resticara/internal/config"
	"resticara/internal/restic"
)

type Status int

const (
	Success Status = iota
	// Warning means every snapshot was saved, but restic could not read some files.
	Warning
	Failure
)

// JobResult holds the commands that ran for a job and their output.
type JobResult struct {
	Key          string
	BackupCmd    string
	BackupOutput string
	ForgetCmd    string
	ForgetOutput string
}

type Result struct {
	Jobs   []JobResult
	Status Status
}

// Run backs up every job and applies its retention policy, in the given order.
// Progress is written to out.
func Run(runner restic.Runner, jobs []config.Job, out io.Writer) Result {
	var result Result
	failed, warned := false, false

	for _, job := range jobs {
		fmt.Fprintf(out, "Executing command %s\n", job.Key)
		backupCmd, forgetCmd := restic.BackupCommand(job), restic.ForgetCommand(job)
		jobResult := JobResult{
			Key:       job.Key,
			BackupCmd: backupCmd.String(),
			ForgetCmd: forgetCmd.String(),
		}

		res := runner.Run(backupCmd)
		jobResult.BackupOutput = output(res)
		switch res.ExitCode {
		case 0:
		case restic.ExitIncomplete:
			warned = true
		default:
			failed = true
		}

		res = runner.Run(forgetCmd)
		jobResult.ForgetOutput = output(res)
		if res.ExitCode != 0 {
			failed = true
		}

		result.Jobs = append(result.Jobs, jobResult)
	}

	switch {
	case failed:
		result.Status = Failure
	case warned:
		result.Status = Warning
	default:
		result.Status = Success
	}
	return result
}

func output(res restic.Result) string {
	return res.Stdout + "\nStderr: " + res.Stderr
}
