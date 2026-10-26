// Package backup runs the configured backup jobs.
package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

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
// A non-empty host is passed to restic as --host. Progress is written to out.
func Run(runner restic.Runner, jobs []config.Job, host string, out io.Writer) Result {
	var result Result
	failed, warned := false, false

	for _, job := range jobs {
		fmt.Fprintf(out, "Executing command %s\n", job.Key)
		backupCmd, forgetCmd := restic.BackupCommand(job, host), restic.ForgetCommand(job, host)
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
		} else if host != "" {
			if note := otherHostsNote(runner, job, host); note != "" {
				jobResult.ForgetOutput += "\n" + note
			}
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

// otherHostsNote explains how to adopt snapshots of the job's paths that belong to
// other host names. forget with --host leaves them alone, so after setting hostID
// the snapshots made under the machine's hostname would be kept forever.
// It returns "" when there are none or the snapshots cannot be listed.
func otherHostsNote(runner restic.Runner, job config.Job, host string) string {
	res := runner.Run(restic.SnapshotsCommand(job.Repository))
	if res.ExitCode != 0 {
		return ""
	}
	var snapshots []struct {
		Hostname string   `json:"hostname"`
		Paths    []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &snapshots); err != nil {
		return ""
	}

	paths := restic.SnapshotPaths(job)
	counts := make(map[string]int)
	for _, s := range snapshots {
		sort.Strings(s.Paths)
		if s.Hostname != host && strings.Join(s.Paths, "\x00") == strings.Join(paths, "\x00") {
			counts[s.Hostname]++
		}
	}
	if len(counts) == 0 {
		return ""
	}

	hosts := make([]string, 0, len(counts))
	for h := range counts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	var b strings.Builder
	for _, h := range hosts {
		rewrite := restic.Command{"restic", "-r", job.Repository, "rewrite", "--new-host", host, "--forget", "--host", h}
		for _, p := range paths {
			rewrite = append(rewrite, "--path", p)
		}
		fmt.Fprintf(&b, "Note: %d snapshots of these paths belong to host %q, so the retention policy for %q does not remove them. "+
			"If they are this machine's snapshots from before hostID was set, assign them to %q once with:\n  %s\n",
			counts[h], h, host, host, rewrite)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
