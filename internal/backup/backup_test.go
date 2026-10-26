package backup

import (
	"reflect"
	"strings"
	"testing"

	"resticara/internal/config"
	"resticara/internal/restic"
)

// fakeRunner returns the result configured for a command, matched by substring, and
// records the commands it ran.
type fakeRunner struct {
	results map[string]restic.Result
	ran     []string
}

func (f *fakeRunner) Run(cmd restic.Command) restic.Result {
	command := cmd.String()
	f.ran = append(f.ran, command)
	for match, res := range f.results {
		if strings.Contains(command, match) {
			return res
		}
	}
	return restic.Result{Stdout: "ok"}
}

var jobs = []config.Job{
	{Key: "dir:a", Type: config.JobDir, Repository: "/repo-a", Directories: []string{"/a"}},
	{Key: "dir:b", Type: config.JobDir, Repository: "/repo-b", Directories: []string{"/b"}},
}

func TestRunStatus(t *testing.T) {
	tests := []struct {
		name    string
		results map[string]restic.Result
		want    Status
	}{
		{"success", nil, Success},
		{"unreadable files", map[string]restic.Result{"backup /a": {ExitCode: restic.ExitIncomplete}}, Warning},
		{"backup fails", map[string]restic.Result{"backup /b": {ExitCode: 1}}, Failure},
		{"forget fails", map[string]restic.Result{"/repo-a --retry-lock 30m forget": {ExitCode: 1}}, Failure},
		{"failure beats warning", map[string]restic.Result{"backup /a": {ExitCode: restic.ExitIncomplete}, "backup /b": {ExitCode: -1}}, Failure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{results: tt.results}
			var out strings.Builder
			if got := Run(runner, jobs, "", &out).Status; got != tt.want {
				t.Errorf("status = %v, want %v", got, tt.want)
			}
			// a failing job never stops the others
			if len(runner.ran) != 4 {
				t.Errorf("ran %d commands, want 4: %v", len(runner.ran), runner.ran)
			}
		})
	}
}

func TestRunResults(t *testing.T) {
	runner := &fakeRunner{results: map[string]restic.Result{
		"backup /a": {Stdout: "snapshot saved\n", Stderr: "warning\n", ExitCode: restic.ExitIncomplete},
	}}
	var out strings.Builder
	result := Run(runner, jobs[:1], "", &out)

	if out.String() != "Executing command dir:a\n" {
		t.Errorf("progress output = %q", out.String())
	}
	want := []JobResult{{
		Key:          "dir:a",
		Status:       Warning,
		BackupCmd:    "restic -r /repo-a --retry-lock 30m backup /a",
		BackupOutput: "snapshot saved\n\nStderr: warning\n",
		ForgetCmd:    "restic -r /repo-a --retry-lock 30m forget --keep-daily 0 --keep-weekly 0 --keep-monthly 0",
		ForgetOutput: "ok\nStderr: ",
	}}
	if result.Jobs[0].Duration <= 0 {
		t.Errorf("Duration = %v", result.Jobs[0].Duration)
	}
	result.Jobs[0].Duration = 0
	if !reflect.DeepEqual(result.Jobs, want) {
		t.Errorf("jobs =\n%+v\nwant\n%+v", result.Jobs, want)
	}
	if !reflect.DeepEqual(runner.ran, []string{want[0].BackupCmd, want[0].ForgetCmd}) {
		t.Errorf("ran %v", runner.ran)
	}
}

func TestOtherHostsNote(t *testing.T) {
	snapshots := `[
		{"hostname": "server-1", "paths": ["/a"]},
		{"hostname": "old-name", "paths": ["/a"]},
		{"hostname": "old-name", "paths": ["/a"]},
		{"hostname": "old-name", "paths": ["/a", "/other"]},
		{"hostname": "elsewhere", "paths": ["/srv"]}
	]`
	runner := &fakeRunner{results: map[string]restic.Result{"snapshots --json": {Stdout: snapshots}}}
	var out strings.Builder
	result := Run(runner, jobs[:1], "server-1", &out)

	if result.Status != Success {
		t.Errorf("status = %v", result.Status)
	}
	// only snapshots with exactly the job's paths count; others are a different backup
	want := "ok\nStderr: \nNote: 2 snapshots of these paths belong to host \"old-name\", so the retention policy for \"server-1\" does not remove them. " +
		"If they are this machine's snapshots from before hostID was set, assign them to \"server-1\" once with:\n" +
		"  restic -r /repo-a rewrite --new-host server-1 --forget --host old-name --path /a"
	if got := result.Jobs[0].ForgetOutput; got != want {
		t.Errorf("ForgetOutput:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(runner.ran[0], "backup --host server-1") || !strings.Contains(runner.ran[1], "forget --host server-1") {
		t.Errorf("ran %v", runner.ran)
	}
}

func TestOtherHostsNoteSkipped(t *testing.T) {
	for name, tt := range map[string]struct {
		host    string
		results map[string]restic.Result
		ran     int
	}{
		// without hostID restic uses the hostname, so there is nothing to adopt
		"no host":                {"", map[string]restic.Result{"snapshots": {Stdout: `[{"hostname": "x", "paths": ["/a"]}]`}}, 2},
		"forget failed":          {"server-1", map[string]restic.Result{"forget": {ExitCode: 1}}, 2},
		"listing failed":         {"server-1", map[string]restic.Result{"snapshots": {ExitCode: 1}}, 3},
		"same host only":         {"server-1", map[string]restic.Result{"snapshots": {Stdout: `[{"hostname": "server-1", "paths": ["/a"]}]`}}, 3},
		"unexpected json output": {"server-1", map[string]restic.Result{"snapshots": {Stdout: `not json`}}, 3},
	} {
		runner := &fakeRunner{results: tt.results}
		var out strings.Builder
		result := Run(runner, jobs[:1], tt.host, &out)
		if strings.Contains(result.Jobs[0].ForgetOutput, "Note:") || len(runner.ran) != tt.ran {
			t.Errorf("%s: ForgetOutput %q, ran %v", name, result.Jobs[0].ForgetOutput, runner.ran)
		}
	}
}

func TestRunJobStatus(t *testing.T) {
	runner := &fakeRunner{results: map[string]restic.Result{
		"backup /a":                       {ExitCode: restic.ExitIncomplete},
		"/repo-b --retry-lock 30m forget": {ExitCode: 1},
	}}
	jobs := append(jobs, config.Job{Key: "dir:c", Type: config.JobDir, Repository: "/repo-c", Directories: []string{"/c"}})
	var out strings.Builder
	result := Run(runner, jobs, "", &out)

	var got []string
	for _, job := range result.Jobs {
		got = append(got, job.Key+"="+job.Status.String())
	}
	// a forget failure fails the job, and one job's status never leaks into another's
	if strings.Join(got, " ") != "dir:a=warning dir:b=failure dir:c=success" || result.Status != Failure {
		t.Errorf("statuses %v, overall %v", got, result.Status)
	}
}
