package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeConfig(t, `
[general]
hostID = server-1
retention_prune = 21

[smtp]
from = "bot@example.com"
server = mail.example.com
port = 587

[matrix]
enabled = true
server = https://matrix.example.com
access_token = tok
room_id = !room:example.com

[telegram]
enabled = true
bot_token = 123:abc
chat_id = -1001234

[mysql:site]
bucket = b2:bucket:mysql/
database = site_db
retention_daily = 2
retention_weekly = 7
retention_monthly = 3
retention_prune = 14

[dir:site]
bucket = b2:bucket:website/
directory = /var/www/site
retention_daily = 4
retention_weekly = 5
retention_monthly = 6

[notes]
anything = goes
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.General != (General{HostID: "server-1", RetentionPrune: 21}) {
		t.Errorf("General = %+v", cfg.General)
	}
	// SMTP is enabled unless disabled explicitly
	if !cfg.SMTP.Enabled || cfg.SMTP.From != "bot@example.com" || cfg.SMTP.Server != "mail.example.com" || cfg.SMTP.Port != "587" {
		t.Errorf("SMTP = %+v", cfg.SMTP)
	}
	if !cfg.Matrix.Enabled || cfg.Matrix.AccessToken != "tok" || cfg.Matrix.RoomID != "!room:example.com" {
		t.Errorf("Matrix = %+v", cfg.Matrix)
	}
	if cfg.Telegram != (Telegram{Enabled: true, BotToken: "123:abc", ChatID: -1001234}) {
		t.Errorf("Telegram = %+v", cfg.Telegram)
	}

	want := []Job{
		{Key: "dir:site", Type: JobDir, Name: "site", Repository: "b2:bucket:website/", Directory: "/var/www/site", Retention: Retention{4, 5, 6}, PruneDays: 21},
		{Key: "mysql:site", Type: JobMySQL, Name: "site", Repository: "b2:bucket:mysql/", Database: "site_db", Retention: Retention{2, 7, 3}, PruneDays: 14},
	}
	if !reflect.DeepEqual(cfg.Jobs, want) {
		t.Errorf("Jobs =\n%+v\nwant\n%+v", cfg.Jobs, want)
	}

	if job, ok := cfg.Job("mysql:site"); !ok || job.Database != "site_db" {
		t.Errorf("Job(mysql:site) = %+v, %v", job, ok)
	}
	if _, ok := cfg.Job("dir:missing"); ok {
		t.Error("Job(dir:missing) found a job")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SMTP.Enabled || cfg.Matrix.Enabled || cfg.Telegram.Enabled {
		t.Errorf("enabled: smtp=%v matrix=%v telegram=%v, want true false false", cfg.SMTP.Enabled, cfg.Matrix.Enabled, cfg.Telegram.Enabled)
	}
	if cfg.General.RetentionPrune != 30 || len(cfg.Jobs) != 0 {
		t.Errorf("RetentionPrune = %d, jobs = %d", cfg.General.RetentionPrune, len(cfg.Jobs))
	}
}

func TestLoadErrors(t *testing.T) {
	job := func(retention string) string {
		return "[dir:site]\nbucket = /repo\ndirectory = /data\n" + retention
	}
	tests := []struct {
		name, content, err string
	}{
		{"unsupported type", "[pgsql:main]\nbucket = /repo\n", `unsupported backup type "pgsql" in section [pgsql:main], expected dir or mysql`},
		{"daily", job("retention_daily = x\nretention_weekly = 1\nretention_monthly = 1\n"), "'retention_daily' for dir:site must be an integer"},
		{"weekly missing", job("retention_daily = 1\nretention_monthly = 1\n"), "'retention_weekly' for dir:site must be an integer"},
		{"monthly", job("retention_daily = 1\nretention_weekly = 1\nretention_monthly = 1.5\n"), "'retention_monthly' for dir:site must be an integer"},
		{"prune", job("retention_daily = 1\nretention_weekly = 1\nretention_monthly = 1\nretention_prune = soon\n"), "'retention_prune' for dir:site must be an integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil || err.Error() != tt.err {
				t.Errorf("error = %v, want %q", err, tt.err)
			}
		})
	}

	if _, err := Load(filepath.Join(t.TempDir(), "missing.ini")); err == nil {
		t.Error("loading a missing file succeeded")
	}
}

func TestRepositories(t *testing.T) {
	cfg := Config{Jobs: []Job{{Repository: "b"}, {Repository: "a"}, {Repository: "b"}}}
	if got := cfg.Repositories(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Repositories() = %v", got)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "b.ini")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	locations := []string{filepath.Join(dir, "a.ini"), existing}

	if got := Find("", locations); got != existing {
		t.Errorf("Find = %q, want %q", got, existing)
	}
	if got := Find("/custom.ini", locations); got != "/custom.ini" {
		t.Errorf("Find with custom path = %q", got)
	}
	if got := Find("", locations[:1]); got != "" {
		t.Errorf("Find without match = %q", got)
	}
}

func TestPermissionWarning(t *testing.T) {
	path := writeConfig(t, "")
	if w := PermissionWarning(path); w != "" {
		t.Errorf("warning for mode 0600: %q", w)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if w := PermissionWarning(path); !strings.Contains(w, "has mode 0640") || !strings.Contains(w, "chmod 600 "+path) {
		t.Errorf("warning = %q", w)
	}
	if w := PermissionWarning(filepath.Join(t.TempDir(), "missing")); w != "" {
		t.Errorf("warning for a missing file: %q", w)
	}
}
