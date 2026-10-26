package restic

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"resticara/internal/config"
)

func TestCommands(t *testing.T) {
	retention := config.Retention{Daily: 2, Weekly: 7, Monthly: 3}
	dir := config.Job{Type: config.JobDir, Repository: "b2:bucket:website/", Directories: []string{"/var/www/site", "/srv/My Files"}, Retention: retention}
	mysql := config.Job{Type: config.JobMySQL, Repository: "b2:bucket:mysql/", Database: "site_db", MysqldumpArgs: []string{"--routines", "--events"}, Retention: retention}
	allDatabases := config.Job{Type: config.JobMySQL, Repository: "/repo", Database: "--all-databases"}
	excluding := config.Job{Type: config.JobDir, Repository: "/repo", Directories: []string{"/var/www"}, Exclude: config.Exclude{
		Patterns:  []string{"*.tmp", "-weird"},
		Files:     []string{"/etc/resticara/site.exclude"},
		Caches:    true,
		IfPresent: []string{".nobackup"},
	}}

	tests := []struct {
		name string
		got  Command
		want Command
	}{
		{"dir backup", BackupCommand(dir, ""), Command{"restic", "-r", "b2:bucket:website/", "--retry-lock", "30m", "backup", "/var/www/site", "/srv/My Files"}},
		{"mysql backup", BackupCommand(mysql, ""), Command{"restic", "-r", "b2:bucket:mysql/", "--retry-lock", "30m", "backup", "--stdin-filename", "site_db.sql", "--stdin-from-command", "--", "mysqldump", "--single-transaction", "--routines", "--events", "site_db"}},
		// the snapshot path stays "<database>.sql", so existing snapshots keep their group for forget
		{"all databases", BackupCommand(allDatabases, ""), Command{"restic", "-r", "/repo", "--retry-lock", "30m", "backup", "--stdin-filename", "--all-databases.sql", "--stdin-from-command", "--", "mysqldump", "--single-transaction", "--all-databases"}},
		{"dir backup with excludes", BackupCommand(excluding, ""), Command{"restic", "-r", "/repo", "--retry-lock", "30m", "backup",
			"--exclude=*.tmp", "--exclude=-weird", "--exclude-file=/etc/resticara/site.exclude", "--exclude-caches", "--exclude-if-present=.nobackup", "/var/www"}},
		// excludes are only for directories, a mysql job never gets them
		{"mysql backup ignores excludes", BackupCommand(config.Job{Type: config.JobMySQL, Repository: "/repo", Database: "db", Exclude: config.Exclude{Caches: true}}, ""),
			Command{"restic", "-r", "/repo", "--retry-lock", "30m", "backup", "--stdin-filename", "db.sql", "--stdin-from-command", "--", "mysqldump", "--single-transaction", "db"}},
		{"forget", ForgetCommand(mysql, ""), Command{"restic", "-r", "b2:bucket:mysql/", "--retry-lock", "30m", "forget", "--keep-daily", "2", "--keep-weekly", "7", "--keep-monthly", "3"}},
		{"prune", PruneCommand(config.Repository{URL: "b2:bucket:mysql/"}), Command{"restic", "-r", "b2:bucket:mysql/", "--retry-lock", "30m", "prune"}},
		{"prune with limits", PruneCommand(config.Repository{URL: "/repo", Prune: config.PruneLimits{MaxUnused: "10%", MaxRepackSize: "2G"}}),
			Command{"restic", "-r", "/repo", "--retry-lock", "30m", "prune", "--max-unused=10%", "--max-repack-size=2G"}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestCommandString(t *testing.T) {
	tests := []struct {
		cmd  Command
		want string
	}{
		// commands without special characters look exactly like before
		{Command{"restic", "-r", "b2:bucket:website/", "--retry-lock", "30m", "backup", "/var/www/example.com"}, "restic -r b2:bucket:website/ --retry-lock 30m backup /var/www/example.com"},
		{Command{"restic", "backup", "/srv/My Files", "it's", ""}, `restic backup '/srv/My Files' 'it'\''s' ''`},
	}
	for _, tt := range tests {
		if got := tt.cmd.String(); got != tt.want {
			t.Errorf("String() = %s, want %s", got, tt.want)
		}
	}
}

func TestExecRunner(t *testing.T) {
	dir := t.TempDir()
	exit3 := filepath.Join(dir, "exit3")
	if err := os.WriteFile(exit3, []byte("#!/bin/sh\necho partial\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		cmd      Command
		exitCode int
		stdout   string
	}{
		{Command{"echo", "hello"}, 0, "hello\n"},
		// arguments are passed as they are, spaces included
		{Command{"echo", "two  spaces"}, 0, "two  spaces\n"},
		{Command{"false"}, 1, ""},
		{Command{exit3}, 3, "partial\n"},
		{Command{"/nonexistent/binary"}, -1, ""},
		{nil, -1, ""},
	}
	for _, tt := range tests {
		res := ExecRunner{}.Run(tt.cmd)
		if res.ExitCode != tt.exitCode || res.Stdout != tt.stdout {
			t.Errorf("Run(%q) = %+v, want exit code %d and stdout %q", tt.cmd, res, tt.exitCode, tt.stdout)
		}
	}
}

func TestHostArgs(t *testing.T) {
	dir := config.Job{Type: config.JobDir, Repository: "/repo", Directories: []string{"/var/www"}, Retention: config.Retention{Daily: 2, Weekly: 7, Monthly: 3}}
	mysql := config.Job{Type: config.JobMySQL, Repository: "/repo", Database: "db"}

	tests := []struct {
		name string
		got  Command
		want Command
	}{
		{"dir backup", BackupCommand(dir, "server-1"), Command{"restic", "-r", "/repo", "--retry-lock", "30m", "backup", "--host", "server-1", "/var/www"}},
		{"mysql backup", BackupCommand(mysql, "server-1"), Command{"restic", "-r", "/repo", "--retry-lock", "30m", "backup", "--host", "server-1", "--stdin-filename", "db.sql", "--stdin-from-command", "--", "mysqldump", "--single-transaction", "db"}},
		{"forget", ForgetCommand(dir, "server-1"), Command{"restic", "-r", "/repo", "--retry-lock", "30m", "forget", "--host", "server-1", "--keep-daily", "2", "--keep-weekly", "7", "--keep-monthly", "3"}},
		{"snapshots", SnapshotsCommand("/repo"), Command{"restic", "-r", "/repo", "--retry-lock", "30m", "snapshots", "--json"}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestSnapshotPaths(t *testing.T) {
	dir := config.Job{Type: config.JobDir, Directories: []string{"/var/www/", "/etc/nginx"}}
	if got := SnapshotPaths(dir); !reflect.DeepEqual(got, []string{"/etc/nginx", "/var/www"}) {
		t.Errorf("dir paths = %q", got)
	}
	mysql := config.Job{Type: config.JobMySQL, Database: "--all-databases"}
	if got := SnapshotPaths(mysql); !reflect.DeepEqual(got, []string{"/--all-databases.sql"}) {
		t.Errorf("mysql paths = %q", got)
	}
}
