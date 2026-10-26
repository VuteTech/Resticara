package restic

import (
	"os"
	"path/filepath"
	"testing"

	"resticara/internal/config"
)

func TestCommands(t *testing.T) {
	retention := config.Retention{Daily: 2, Weekly: 7, Monthly: 3}
	dir := config.Job{Type: config.JobDir, Repository: "b2:bucket:website/", Directory: "/var/www/site", Retention: retention}
	mysql := config.Job{Type: config.JobMySQL, Repository: "b2:bucket:mysql/", Database: "site_db", Retention: retention}

	tests := []struct {
		name, got, want string
	}{
		{"dir backup", BackupCommand(dir), "restic -r b2:bucket:website/ --retry-lock 30m backup /var/www/site"},
		{"mysql backup", BackupCommand(mysql), "restic -r b2:bucket:mysql/ --retry-lock 30m backup --stdin-filename site_db.sql --stdin-from-command -- mysqldump --single-transaction site_db"},
		{"forget", ForgetCommand(mysql), "restic -r b2:bucket:mysql/ --retry-lock 30m forget --keep-daily 2 --keep-weekly 7 --keep-monthly 3"},
		{"prune", PruneCommand("b2:bucket:mysql/"), "restic -r b2:bucket:mysql/ --retry-lock 30m prune"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestExecRunner(t *testing.T) {
	exit3 := filepath.Join(t.TempDir(), "exit3")
	if err := os.WriteFile(exit3, []byte("#!/bin/sh\necho partial\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		command  string
		exitCode int
		stdout   string
	}{
		{"echo hello", 0, "hello\n"},
		{"false", 1, ""},
		{exit3, 3, "partial\n"},
		{"/nonexistent/binary", -1, ""},
		{"", -1, ""},
	}
	for _, tt := range tests {
		res := ExecRunner{}.Run(tt.command)
		if res.ExitCode != tt.exitCode || res.Stdout != tt.stdout {
			t.Errorf("Run(%q) = %+v, want exit code %d and stdout %q", tt.command, res, tt.exitCode, tt.stdout)
		}
	}
}
