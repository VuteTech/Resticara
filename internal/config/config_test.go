package config

import (
	"errors"
	"fmt"
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

	if !reflect.DeepEqual(cfg.General, General{HostID: "server-1", RetentionPrune: 21, Notify: NotifyAlways, CheckDays: 30, CheckSubset: "5%"}) {
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
		{Key: "dir:site", Type: JobDir, Name: "site", Repository: "b2:bucket:website/", Directories: []string{"/var/www/site"}, Retention: Retention{4, 5, 6}, PruneDays: 21, CheckDays: 30, CheckSubset: "5%"},
		{Key: "mysql:site", Type: JobMySQL, Name: "site", Repository: "b2:bucket:mysql/", Database: "site_db", Retention: Retention{2, 7, 3}, PruneDays: 14, CheckDays: 30, CheckSubset: "5%"},
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
	// without a server, email is off, so an empty config does not fail to send mail
	if cfg.SMTP.Enabled || cfg.Matrix.Enabled || cfg.Telegram.Enabled || len(cfg.Warnings) != 0 {
		t.Errorf("enabled: smtp=%v matrix=%v telegram=%v, warnings %q; want all off", cfg.SMTP.Enabled, cfg.Matrix.Enabled, cfg.Telegram.Enabled, cfg.Warnings)
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
	limits := PruneLimits{MaxUnused: "10%"}
	cfg := Config{Jobs: []Job{
		{Key: "dir:b1", Repository: "b", PruneDays: 30, Prune: limits},
		{Key: "dir:a", Repository: "a", PruneDays: 21},
		{Key: "mysql:b2", Repository: "b", PruneDays: 14, Prune: limits},
	}}
	want := []Repository{
		{URL: "a", PruneDays: 21, Jobs: []string{"dir:a"}},
		// the repository is pruned as often as its most frequent job asks for
		{URL: "b", PruneDays: 14, Prune: limits, Jobs: []string{"dir:b1", "mysql:b2"}},
	}
	if got := cfg.Repositories(); !reflect.DeepEqual(got, want) {
		t.Errorf("Repositories() =\n%+v\nwant\n%+v", got, want)
	}
	if repo, ok := cfg.Repository("b"); !ok || repo.PruneDays != 14 {
		t.Errorf("Repository(b) = %+v, %v", repo, ok)
	}
	if _, ok := cfg.Repository("c"); ok {
		t.Error("Repository(c) found a repository")
	}
}

func TestLoadPruneLimits(t *testing.T) {
	job := func(name, repo, extra string) string {
		return "[dir:" + name + "]\nbucket = " + repo + "\ndirectory = /data\nretention_daily = 1\nretention_weekly = 1\nretention_monthly = 1\n" + extra + "\n"
	}
	cfg, err := Load(writeConfig(t, "[general]\nprune_max_unused = 10%\n[smtp]\nenabled = false\n"+
		job("a", "/repo-a", "")+job("b", "/repo-b", "prune_max_repack_size = 2G\nprune_max_unused = unlimited")))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Job("dir:a")
	b, _ := cfg.Job("dir:b")
	if a.Prune != (PruneLimits{MaxUnused: "10%"}) || b.Prune != (PruneLimits{MaxUnused: "unlimited", MaxRepackSize: "2G"}) {
		t.Errorf("limits a %+v, b %+v", a.Prune, b.Prune)
	}

	for _, ok := range []string{"5%", "2.5%", "100", "500M", "10g", "unlimited"} {
		if _, err := Load(writeConfig(t, "[general]\nprune_max_unused = "+ok+"\n")); err != nil {
			t.Errorf("prune_max_unused = %s: %v", ok, err)
		}
	}

	_, err = Load(writeConfig(t, "[general]\nprune_max_unused = 5 %\nprune_max_repack_size = 2GB\n"+
		job("a", "/shared", "prune_max_repack_size = 1G")+job("b", "/shared", "")))
	want := `'prune_max_unused' in [general] must be a percentage, a size or unlimited, for example 5%, 10G or unlimited; got "5 %"
'prune_max_repack_size' in [general] must be a size such as 500M or 2G, got "2GB"
dir:a and dir:b use repository /shared but set different prune_max_unused or prune_max_repack_size`
	if err == nil || err.Error() != want {
		t.Errorf("error:\n%v\nwant:\n%s", err, want)
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

func TestLoadDirectoriesAndMysqldumpArgs(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[dir:site]
bucket = /repo
directory = /var/www/site
directory = /etc/nginx
directory = /srv/My Files
retention_daily = 1
retention_weekly = 1
retention_monthly = 1

[mysql:all]
bucket = /repo-db
database = --all-databases
mysqldump_args = --routines   --events
retention_daily = 1
retention_weekly = 1
retention_monthly = 1
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Jobs[0].Directories; !reflect.DeepEqual(got, []string{"/var/www/site", "/etc/nginx", "/srv/My Files"}) {
		t.Errorf("Directories = %q", got)
	}
	if job := cfg.Jobs[1]; job.Database != "--all-databases" || !reflect.DeepEqual(job.MysqldumpArgs, []string{"--routines", "--events"}) {
		t.Errorf("mysql job = %+v", job)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %q", cfg.Warnings)
	}
}

// TestLoadReportsAllProblems checks that every problem is reported at once, not only the first.
func TestLoadReportsAllProblems(t *testing.T) {
	_, err := Load(writeConfig(t, `
[general]
retention_prune = 0

[smtp]
enabled = maybe

[telegram]
chat_id = @channel

[dir:nothing]
retention_daily = 1
retention_weekly = 1
retention_monthly = 1

[dir:twice]
bucket = /repo-a
bucket = /repo-b
directory = /data
retention_daily = 1
retention_weekly = 1
retention_monthly = 1

[mysql:nodb]
bucket = /repo
retention_daily = 1
retention_weekly = 1
retention_monthly = 1

[dir:]
bucket = /repo
`))
	var cfgErr *Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	want := []string{
		"'retention_prune' for [general] must be at least 1",
		`'enabled' in [smtp] must be true or false, got "maybe"`,
		"'chat_id' in [telegram] must be an integer",
		"'bucket' is required for dir:nothing",
		"'directory' is required for dir:nothing",
		"'bucket' is set more than once in [dir:twice]",
		"'database' is required for mysql:nodb",
		"section [dir:] must be named type:name, for example [dir:website]",
	}
	for _, problem := range want {
		found := false
		for _, got := range cfgErr.Problems {
			found = found || got == problem
		}
		if !found {
			t.Errorf("missing problem %q in:\n%s", problem, err)
		}
	}
	if len(cfgErr.Problems) != len(want) {
		t.Errorf("got %d problems, want %d:\n%s", len(cfgErr.Problems), len(want), err)
	}
}

func TestLoadWarnings(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
stray = 1

[general]
hostID = first
hostID = second

[smtp]
enabled = true
server = mail.example.com

[matrix]
enabled = yes
server = https://matrix.example.com
username = bot

[telegram]
enabled = on

[notes]
text = hello

[dir:site]
bucket = /repo
directory = /data
retention_daily = 1
retention_dialy = 2
retention_weekly = 1
retention_monthly = 1
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"'hostID' is set more than once in [general]; the last value is used",
		"[smtp] is enabled but 'port', 'from', 'to' not set, so email cannot be sent",
		"[matrix] is enabled but 'room_id', 'access_token' (or 'username' and 'pass') not set, so Matrix messages cannot be sent",
		"[telegram] is enabled but 'bot_token', 'chat_id' not set, so Telegram messages cannot be sent",
		"key 'stray' is outside of any section and is ignored",
		"section [notes] is not used",
		"unknown key 'retention_dialy' in [dir:site] is ignored",
	}
	if strings.Join(cfg.Warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(cfg.Warnings, "\n"), strings.Join(want, "\n"))
	}
	// the last value wins outside job sections, like before
	if cfg.General.HostID != "second" || !cfg.Matrix.Enabled || !cfg.Telegram.Enabled {
		t.Errorf("General = %+v, matrix %v, telegram %v", cfg.General, cfg.Matrix.Enabled, cfg.Telegram.Enabled)
	}
}

func TestPathWarnings(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Jobs: []Job{
		{Key: "dir:ok", Type: JobDir, Directories: []string{a, b}},
		{Key: "dir:typo", Type: JobDir, Directories: []string{filepath.Join(dir, "missing")}},
		{Key: "dir:list", Type: JobDir, Directories: []string{a + " " + b}},
	}}
	want := []string{
		fmt.Sprintf("directory %q of dir:typo does not exist", filepath.Join(dir, "missing")),
		fmt.Sprintf("directory %q of dir:list does not exist; to back up several directories, put each on its own 'directory =' line", a+" "+b),
	}
	if got := cfg.PathWarnings(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("PathWarnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoadExcludes(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[general]
exclude = *.tmp
exclude = **/node_modules
exclude_caches = true

[dir:site]
bucket = /repo
directory = /var/www/site
exclude = wp-content/cache
exclude_file = /etc/resticara/site.exclude
exclude_if_present = .nobackup
retention_daily = 1
retention_weekly = 1
retention_monthly = 1

[dir:plain]
bucket = /repo
directory = /srv
exclude_caches = false
retention_daily = 1
retention_weekly = 1
retention_monthly = 1

[mysql:db]
bucket = /repo-db
database = website_db
exclude = *.sql
retention_daily = 1
retention_weekly = 1
retention_monthly = 1
`))
	if err != nil {
		t.Fatal(err)
	}

	general := Exclude{Patterns: []string{"*.tmp", "**/node_modules"}, Caches: true}
	if !reflect.DeepEqual(cfg.General.Exclude, general) {
		t.Errorf("General.Exclude = %+v", cfg.General.Exclude)
	}
	jobs := map[string]Exclude{
		// [general] excludes come first, then the job's own
		"dir:site": {
			Patterns:  []string{"*.tmp", "**/node_modules", "wp-content/cache"},
			Files:     []string{"/etc/resticara/site.exclude"},
			Caches:    true,
			IfPresent: []string{".nobackup"},
		},
		// a job can turn exclude_caches off again
		"dir:plain": {Patterns: []string{"*.tmp", "**/node_modules"}},
		// excludes do not apply to database dumps
		"mysql:db": {},
	}
	for key, want := range jobs {
		job, _ := cfg.Job(key)
		if !reflect.DeepEqual(job.Exclude, want) {
			t.Errorf("%s Exclude =\n%+v\nwant\n%+v", key, job.Exclude, want)
		}
	}
	// the general excludes are copied, not shared between jobs
	site, _ := cfg.Job("dir:site")
	if &site.Exclude.Patterns[0] == &cfg.General.Exclude.Patterns[0] {
		t.Error("job patterns share memory with [general]")
	}

	if len(cfg.Warnings) != 1 || cfg.Warnings[0] != "unknown key 'exclude' in [mysql:db] is ignored" {
		t.Errorf("Warnings = %q", cfg.Warnings)
	}
}

func TestLoadExcludeErrors(t *testing.T) {
	_, err := Load(writeConfig(t, `
[general]
exclude = cache[

[dir:site]
bucket = /repo
directory = /data
exclude = !logs/[a-
exclude = **/ok/*.log
exclude_caches = sometimes
retention_daily = 1
retention_weekly = 1
retention_monthly = 1
`))
	want := `'exclude' pattern "cache[" in [general] is not valid: syntax error in pattern
'exclude_caches' in [dir:site] must be true or false, got "sometimes"
'exclude' pattern "!logs/[a-" in [dir:site] is not valid: syntax error in pattern`
	if err == nil || err.Error() != want {
		t.Errorf("error:\n%v\nwant:\n%s", err, want)
	}
}

func TestPathWarningsExcludeFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.exclude")
	cfg := Config{Jobs: []Job{{Key: "dir:site", Type: JobDir, Directories: []string{dir}, Exclude: Exclude{Files: []string{missing}}}}}
	want := fmt.Sprintf("exclude file %q of dir:site does not exist", missing)
	if got := cfg.PathWarnings(); len(got) != 1 || got[0] != want {
		t.Errorf("PathWarnings = %q", got)
	}
}

func TestLoadNotify(t *testing.T) {
	for content, want := range map[string]Notify{
		"":                               NotifyAlways,
		"[general]\nnotify = always\n":   NotifyAlways,
		"[general]\nnotify = problems\n": NotifyProblems,
	} {
		cfg, err := Load(writeConfig(t, content+"[smtp]\nenabled = false\n"))
		if err != nil || cfg.General.Notify != want {
			t.Errorf("%q: Notify = %q, %v; want %q", content, cfg.General.Notify, err, want)
		}
	}
	_, err := Load(writeConfig(t, "[general]\nnotify = never\n"))
	if err == nil || err.Error() != `'notify' in [general] must be always or problems, got "never"` {
		t.Errorf("error = %v", err)
	}
}

func TestLoadHealthcheck(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[smtp]
enabled = false

[healthcheck]
url = https://hc-ping.com/5bf66975-d4c7-4bf5-bcc8-b8d8a82ea278
prune_url = https://healthchecks.example.com/ping/key/prune?create=1
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Healthcheck{URL: "https://hc-ping.com/5bf66975-d4c7-4bf5-bcc8-b8d8a82ea278", PruneURL: "https://healthchecks.example.com/ping/key/prune?create=1"}
	if cfg.Healthcheck != want || len(cfg.Warnings) != 0 {
		t.Errorf("Healthcheck = %+v, warnings %q", cfg.Healthcheck, cfg.Warnings)
	}

	_, err = Load(writeConfig(t, "[healthcheck]\nurl = ftp://example.com/x\nprune_url = https://\n"))
	want2 := `'url' in [healthcheck] must be an http or https URL, got "ftp://example.com/x"
'prune_url' in [healthcheck] must be an http or https URL, got "https://"`
	if err == nil || err.Error() != want2 {
		t.Errorf("error:\n%v", err)
	}
}

func TestLoadCheck(t *testing.T) {
	job := func(name, repo, extra string) string {
		return "[dir:" + name + "]\nbucket = " + repo + "\ndirectory = /data\nretention_daily = 1\nretention_weekly = 1\nretention_monthly = 1\n" + extra + "\n"
	}
	cfg, err := Load(writeConfig(t, "[general]\ncheck_interval = 14\ncheck_read_data_subset = 10G\n[smtp]\nenabled = false\n[healthcheck]\ncheck_url = https://hc-ping.com/abc\n"+
		job("a", "/repo-a", "")+job("b", "/repo-b", "check_interval = 7\ncheck_read_data_subset = 2.5%")+job("c", "/repo-b", "check_interval = 60\ncheck_read_data_subset = 2.5%")))
	if err != nil {
		t.Fatal(err)
	}
	repos := cfg.Repositories()
	if repos[0].CheckDays != 14 || repos[0].CheckSubset != "10G" || repos[1].CheckDays != 7 || repos[1].CheckSubset != "2.5%" {
		t.Errorf("repositories = %+v", repos)
	}
	if cfg.Healthcheck.CheckURL != "https://hc-ping.com/abc" {
		t.Errorf("CheckURL = %q", cfg.Healthcheck.CheckURL)
	}

	defaults, err := Load(writeConfig(t, "[smtp]\nenabled = false\n"+job("a", "/repo", "")))
	if err != nil || defaults.Jobs[0].CheckDays != 30 || defaults.Jobs[0].CheckSubset != "5%" {
		t.Errorf("defaults = %+v, %v", defaults.Jobs, err)
	}

	for _, ok := range []string{"100%", "0.5%", "500M", "1g", "none"} {
		if _, err := Load(writeConfig(t, "[general]\ncheck_read_data_subset = "+ok+"\n")); err != nil {
			t.Errorf("check_read_data_subset = %s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0%", "101%", "1/5", "0", "half"} {
		_, err := Load(writeConfig(t, "[general]\ncheck_read_data_subset = "+bad+"\n"))
		if err == nil || !strings.Contains(err.Error(), "'check_read_data_subset' in [general] must be") {
			t.Errorf("check_read_data_subset = %s: error %v", bad, err)
		}
	}

	_, err = Load(writeConfig(t, "[general]\ncheck_interval = 0\n"+job("a", "/shared", "check_read_data_subset = 1%")+job("b", "/shared", "")))
	want := `'check_interval' for [general] must be at least 1
dir:a and dir:b use repository /shared but set different check_read_data_subset`
	if err == nil || err.Error() != want {
		t.Errorf("error:\n%v\nwant:\n%s", err, want)
	}
}

func TestLoadSystemd(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[smtp]\nenabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Systemd{Hardening: true, Nice: 10, IOSchedulingClass: "best-effort", BackupTimeout: "20h", MaintenanceTimeout: "12h"}
	if !reflect.DeepEqual(cfg.Systemd, want) {
		t.Errorf("defaults = %+v", cfg.Systemd)
	}

	cfg, err = Load(writeConfig(t, `[smtp]
enabled = false
[systemd]
hardening = false
nice = -5
io_scheduling_class = idle
backup_timeout = 1d 12h
maintenance_timeout = infinity
read_write_paths = /root/.config/rclone
read_write_paths = /srv/extra
`))
	if err != nil {
		t.Fatal(err)
	}
	want = Systemd{Nice: -5, IOSchedulingClass: "idle", BackupTimeout: "1d 12h", MaintenanceTimeout: "infinity", ReadWritePaths: []string{"/root/.config/rclone", "/srv/extra"}}
	if !reflect.DeepEqual(cfg.Systemd, want) {
		t.Errorf("Systemd = %+v", cfg.Systemd)
	}

	_, err = Load(writeConfig(t, "[systemd]\nhardening = sometimes\nnice = 20\nio_scheduling_class = realtime\nbackup_timeout = forever\nmaintenance_timeout = 2 fortnights\nread_write_paths = relative/path\n"))
	wantErr := `'hardening' in [systemd] must be true or false, got "sometimes"
'nice' in [systemd] must be an integer from -20 to 19, got "20"
'io_scheduling_class' in [systemd] must be best-effort or idle, got "realtime"
'backup_timeout' in [systemd] must be a time span such as 20h, 90min or infinity, got "forever"
'maintenance_timeout' in [systemd] must be a time span such as 20h, 90min or infinity, got "2 fortnights"
'read_write_paths' in [systemd] must be absolute paths, got "relative/path"`
	if err == nil || err.Error() != wantErr {
		t.Errorf("error:\n%v\nwant:\n%s", err, wantErr)
	}
}

func TestLoadSMTP(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[smtp]\nserver = mail.example.com\nport = 587\nfrom = bot@example.com\nto = admin@example.com, ops@example.com ,,\n"))
	if err != nil {
		t.Fatal(err)
	}
	// with a server and no enabled line, email is on, as before
	if !cfg.SMTP.Enabled || !reflect.DeepEqual(cfg.SMTP.To, []string{"admin@example.com", "ops@example.com"}) || len(cfg.Warnings) != 0 {
		t.Errorf("SMTP = %+v, warnings %q", cfg.SMTP, cfg.Warnings)
	}

	cfg, err = Load(writeConfig(t, "[smtp]\nenabled = true\n"))
	if err != nil || !cfg.SMTP.Enabled || len(cfg.Warnings) != 1 || cfg.Warnings[0] != "[smtp] is enabled but 'server', 'port', 'from', 'to' not set, so email cannot be sent" {
		t.Errorf("explicitly enabled: %+v, %q, %v", cfg.SMTP, cfg.Warnings, err)
	}
}

func TestLoadSecretsFromEnvironment(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "smtp-secret")
	t.Setenv("MATRIX_PASSWORD", "matrix-secret")
	t.Setenv("MATRIX_ACCESS_TOKEN", "matrix-token")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")

	cfg, err := Load(writeConfig(t, "[smtp]\nenabled = false\n[matrix]\nenabled = true\nserver = https://matrix.example.com\nroom_id = !r:x\n[telegram]\nenabled = true\nchat_id = 42\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP.Pass != "smtp-secret" || cfg.Matrix.Pass != "matrix-secret" || cfg.Matrix.AccessToken != "matrix-token" || cfg.Telegram.BotToken != "123:abc" {
		t.Errorf("secrets: smtp %q, matrix %q %q, telegram %q", cfg.SMTP.Pass, cfg.Matrix.Pass, cfg.Matrix.AccessToken, cfg.Telegram.BotToken)
	}
	// the channels count as configured, so there is no warning
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %q", cfg.Warnings)
	}

	// a value in the config still wins
	cfg, err = Load(writeConfig(t, "[smtp]\nenabled = false\npass = from-config\n"))
	if err != nil || cfg.SMTP.Pass != "from-config" {
		t.Errorf("Pass = %q, %v", cfg.SMTP.Pass, err)
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), EnvFile)
	content := `# restic
RESTIC_PASSWORD=plain value
B2_ACCOUNT_ID = "quoted id"
B2_ACCOUNT_KEY='single quoted'
; comment
RESTICARA_TEST_EMPTY=
RESTICARA_TEST_EQUALS=a=b=c
RESTICARA_TEST_SET=from file
not a variable
2BAD=x
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"RESTIC_PASSWORD", "B2_ACCOUNT_ID", "B2_ACCOUNT_KEY", "RESTICARA_TEST_EMPTY", "RESTICARA_TEST_EQUALS"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	// variables that are already set win over the file
	t.Setenv("RESTICARA_TEST_SET", "from environment")

	warnings, err := LoadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"RESTIC_PASSWORD":       "plain value",
		"B2_ACCOUNT_ID":         "quoted id",
		"B2_ACCOUNT_KEY":        "single quoted",
		"RESTICARA_TEST_EMPTY":  "",
		"RESTICARA_TEST_EQUALS": "a=b=c",
		"RESTICARA_TEST_SET":    "from environment",
	}
	for key, val := range want {
		if got, ok := os.LookupEnv(key); !ok || got != val {
			t.Errorf("%s = %q (set %v), want %q", key, got, ok, val)
		}
	}
	wantWarnings := []string{
		"line 9 of " + path + " is not KEY=VALUE and is ignored",
		"line 10 of " + path + " is not KEY=VALUE and is ignored",
	}
	if !reflect.DeepEqual(warnings, wantWarnings) {
		t.Errorf("warnings = %q", warnings)
	}

	if warnings, err := LoadEnvFile(filepath.Join(t.TempDir(), "missing.env")); err != nil || warnings != nil {
		t.Errorf("missing file: %q, %v", warnings, err)
	}
}
