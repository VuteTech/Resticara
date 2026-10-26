package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCommandsMysql(t *testing.T) {
	backup, forget := buildCommands("mysql:site", map[string]string{
		"bucket":            "b2:bucket:mysql/",
		"database":          "site_db",
		"retention_daily":   "2",
		"retention_weekly":  "7",
		"retention_monthly": "3",
	})
	wantBackup := "restic -r b2:bucket:mysql/ --retry-lock 30m backup --stdin-filename site_db.sql --stdin-from-command -- mysqldump --single-transaction site_db"
	if backup != wantBackup {
		t.Errorf("backup command:\n got %q\nwant %q", backup, wantBackup)
	}
	wantForget := "restic -r b2:bucket:mysql/ --retry-lock 30m forget --keep-daily 2 --keep-weekly 7 --keep-monthly 3"
	if forget != wantForget {
		t.Errorf("forget command:\n got %q\nwant %q", forget, wantForget)
	}
}

func TestReadConfigRejectsUnknownType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	content := "[pgsql:main]\nbucket = b2:bucket:pg/\nretention_daily = 1\nretention_weekly = 1\nretention_monthly = 1\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(path); err == nil || !strings.Contains(err.Error(), "unsupported backup type") {
		t.Fatalf("expected unsupported backup type error, got %v", err)
	}
}

func TestRunCommandExitCode(t *testing.T) {
	if code, _, _ := runCommand("sh -c exit"); code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if code, _, _ := runCommand("false"); code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if code, _, _ := runCommand("/nonexistent/binary"); code != -1 {
		t.Errorf("expected exit code -1, got %d", code)
	}
	if code, _, _ := runCommand(""); code != -1 {
		t.Errorf("expected exit code -1 for empty command, got %d", code)
	}
}
