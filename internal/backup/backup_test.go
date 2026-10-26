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
			if got := Run(runner, jobs, &out).Status; got != tt.want {
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
	result := Run(runner, jobs[:1], &out)

	if out.String() != "Executing command dir:a\n" {
		t.Errorf("progress output = %q", out.String())
	}
	want := []JobResult{{
		Key:          "dir:a",
		BackupCmd:    "restic -r /repo-a --retry-lock 30m backup /a",
		BackupOutput: "snapshot saved\n\nStderr: warning\n",
		ForgetCmd:    "restic -r /repo-a --retry-lock 30m forget --keep-daily 0 --keep-weekly 0 --keep-monthly 0",
		ForgetOutput: "ok\nStderr: ",
	}}
	if !reflect.DeepEqual(result.Jobs, want) {
		t.Errorf("jobs =\n%+v\nwant\n%+v", result.Jobs, want)
	}
	if !reflect.DeepEqual(runner.ran, []string{want[0].BackupCmd, want[0].ForgetCmd}) {
		t.Errorf("ran %v", runner.ran)
	}
}
