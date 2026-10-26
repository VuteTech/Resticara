// Package config loads the resticara INI configuration.
package config

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

type Config struct {
	General  General
	SMTP     SMTP
	Matrix   Matrix
	Telegram Telegram
	// Jobs are sorted by Key.
	Jobs []Job
	// Warnings are problems that do not stop resticara, such as unknown keys.
	Warnings []string
}

type General struct {
	// HostID is shown in reports; the value "hostname" means the machine's hostname.
	HostID string
	// RetentionPrune is the default prune interval in days.
	RetentionPrune int
	// Exclude applies to every dir job.
	Exclude Exclude
	// Notify says when reports are sent.
	Notify Notify
}

type Notify string

const (
	// NotifyAlways sends a report after every run.
	NotifyAlways Notify = "always"
	// NotifyProblems only sends reports for runs with warnings or failures.
	NotifyProblems Notify = "problems"
)

// Exclude selects files that dir jobs leave out of the backup.
type Exclude struct {
	// Patterns are passed as --exclude, one per "exclude" line.
	Patterns []string
	// Files are passed as --exclude-file, one per "exclude_file" line.
	Files []string
	// Caches excludes directories marked with a CACHEDIR.TAG file (--exclude-caches).
	Caches bool
	// IfPresent are file names passed as --exclude-if-present: directories that
	// contain such a file are left out.
	IfPresent []string
}

type SMTP struct {
	Enabled  bool
	From     string
	Username string
	Pass     string
	To       string
	Server   string
	Port     string
}

type Matrix struct {
	Enabled     bool
	Server      string
	Username    string
	Pass        string
	RoomID      string
	AccessToken string
}

type Telegram struct {
	Enabled  bool
	BotToken string
	ChatID   int64
}

type JobType string

const (
	JobDir   JobType = "dir"
	JobMySQL JobType = "mysql"
)

// Job is a backup configured in a [type:name] section.
type Job struct {
	// Key is the section name, for example "dir:website".
	Key        string
	Type       JobType
	Name       string
	Repository string // INI key "bucket"
	// Directories are the paths backed up by dir jobs, one per "directory" line.
	Directories []string
	// Database is what mysql jobs dump: a database name or mysqldump selection
	// options such as "--all-databases". It is split on whitespace for mysqldump.
	Database string
	// MysqldumpArgs are extra mysqldump options for mysql jobs.
	MysqldumpArgs []string
	// Exclude holds the [general] excludes followed by the job's own, for dir jobs.
	Exclude   Exclude
	Retention Retention
	// PruneDays is the prune interval, from the job or the [general] default.
	PruneDays int
}

type Retention struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Error lists every problem that makes the configuration unusable.
type Error struct {
	Problems []string
	// Warnings found in the same file, which often explain the problems (a
	// misspelled key shows up as an unknown key and as a missing one).
	Warnings []string
}

func (e *Error) Error() string {
	return strings.Join(e.Problems, "\n")
}

var excludeKeys = []string{"exclude", "exclude_file", "exclude_caches", "exclude_if_present"}

var knownKeys = map[string][]string{
	"general":  append([]string{"hostID", "retention_prune", "notify"}, excludeKeys...),
	"smtp":     {"enabled", "from", "username", "pass", "to", "server", "port"},
	"matrix":   {"enabled", "server", "username", "pass", "room_id", "access_token"},
	"telegram": {"enabled", "bot_token", "chat_id"},
	"dir":      append([]string{"bucket", "directory", "retention_daily", "retention_weekly", "retention_monthly", "retention_prune"}, excludeKeys...),
	"mysql":    {"bucket", "database", "mysqldump_args", "retention_daily", "retention_weekly", "retention_monthly", "retention_prune"},
}

// repeatableKeys may be set more than once in a section, each line adding a value.
var repeatableKeys = map[string]bool{"directory": true, "exclude": true, "exclude_file": true, "exclude_if_present": true}

// Load reads and validates the configuration file. Problems that make it unusable
// are returned together as an *Error.
func Load(path string) (Config, error) {
	file, err := ini.ShadowLoad(path)
	if err != nil {
		return Config{}, err
	}

	p := &parser{}
	cfg := p.parse(file)
	if len(p.errors) > 0 {
		return Config{}, &Error{Problems: p.errors, Warnings: p.warnings}
	}
	cfg.Warnings = p.warnings
	return cfg, nil
}

type parser struct {
	errors   []string
	warnings []string
}

func (p *parser) errorf(format string, args ...any) {
	p.errors = append(p.errors, fmt.Sprintf(format, args...))
}

func (p *parser) warnf(format string, args ...any) {
	p.warnings = append(p.warnings, fmt.Sprintf(format, args...))
}

func (p *parser) parse(file *ini.File) Config {
	var cfg Config

	general := file.Section("general")
	p.checkKeys(general, "general", true)
	cfg.General = General{
		HostID:         value(general, "hostID"),
		RetentionPrune: p.pruneDays(general, "[general]", 30),
		Exclude:        p.exclude(general, Exclude{}),
		Notify:         NotifyAlways,
	}
	switch notify := Notify(value(general, "notify")); notify {
	case "":
	case NotifyAlways, NotifyProblems:
		cfg.General.Notify = notify
	default:
		p.errorf("'notify' in [general] must be always or problems, got %q", notify)
	}

	smtp := file.Section("smtp")
	p.checkKeys(smtp, "smtp", true)
	cfg.SMTP = SMTP{
		Enabled:  p.boolean(smtp, "enabled", true),
		From:     value(smtp, "from"),
		Username: value(smtp, "username"),
		Pass:     value(smtp, "pass"),
		To:       value(smtp, "to"),
		Server:   value(smtp, "server"),
		Port:     value(smtp, "port"),
	}

	matrix := file.Section("matrix")
	p.checkKeys(matrix, "matrix", true)
	cfg.Matrix = Matrix{
		Enabled:     p.boolean(matrix, "enabled", false),
		Server:      value(matrix, "server"),
		Username:    value(matrix, "username"),
		Pass:        value(matrix, "pass"),
		RoomID:      value(matrix, "room_id"),
		AccessToken: value(matrix, "access_token"),
	}

	telegram := file.Section("telegram")
	p.checkKeys(telegram, "telegram", true)
	cfg.Telegram = Telegram{
		Enabled:  p.boolean(telegram, "enabled", false),
		BotToken: value(telegram, "bot_token"),
	}
	if raw := value(telegram, "chat_id"); raw != "" {
		chatID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			p.errorf("'chat_id' in [telegram] must be an integer")
		}
		cfg.Telegram.ChatID = chatID
	}

	p.checkChannels(file, cfg)

	for _, key := range file.Section(ini.DefaultSection).KeyStrings() {
		p.warnf("key '%s' is outside of any section and is ignored", key)
	}

	for _, section := range file.Sections() {
		name := section.Name()
		switch name {
		case ini.DefaultSection, "general", "smtp", "matrix", "telegram":
			continue
		}
		jobType, jobName, isJob := strings.Cut(name, ":")
		if !isJob {
			p.warnf("section [%s] is not used", name)
			continue
		}
		if jobType != string(JobDir) && jobType != string(JobMySQL) {
			p.errorf("unsupported backup type %q in section [%s], expected dir or mysql", jobType, name)
			continue
		}
		if jobName == "" || strings.Contains(jobName, ":") {
			p.errorf("section [%s] must be named type:name, for example [%s:website]", name, jobType)
			continue
		}
		cfg.Jobs = append(cfg.Jobs, p.job(section, JobType(jobType), jobName, cfg.General))
	}
	sort.Slice(cfg.Jobs, func(i, j int) bool { return cfg.Jobs[i].Key < cfg.Jobs[j].Key })

	return cfg
}

func (p *parser) job(section *ini.Section, jobType JobType, name string, general General) Job {
	key := section.Name()
	p.checkKeys(section, string(jobType), false)

	job := Job{
		Key:        key,
		Type:       jobType,
		Name:       name,
		Repository: value(section, "bucket"),
		PruneDays:  p.pruneDays(section, key, general.RetentionPrune),
	}
	if job.Repository == "" {
		p.errorf("'bucket' is required for %s", key)
	}

	switch jobType {
	case JobDir:
		for _, dir := range values(section, "directory") {
			if dir != "" {
				job.Directories = append(job.Directories, dir)
			}
		}
		if len(job.Directories) == 0 {
			p.errorf("'directory' is required for %s", key)
		}
		job.Exclude = p.exclude(section, general.Exclude)
	case JobMySQL:
		job.Database = value(section, "database")
		if job.Database == "" {
			p.errorf("'database' is required for %s", key)
		}
		if args := strings.Fields(value(section, "mysqldump_args")); len(args) > 0 {
			job.MysqldumpArgs = args
		}
	}

	job.Retention.Daily = p.retention(section, "retention_daily")
	job.Retention.Weekly = p.retention(section, "retention_weekly")
	job.Retention.Monthly = p.retention(section, "retention_monthly")
	return job
}

// checkKeys reports unknown keys, and keys set more than once that are not repeatable.
// Duplicates are errors in job sections and warnings elsewhere, where the last value wins.
func (p *parser) checkKeys(section *ini.Section, kind string, lastWins bool) {
	known := make(map[string]bool)
	for _, key := range knownKeys[kind] {
		known[key] = true
	}
	for _, key := range section.Keys() {
		name := key.Name()
		if !known[name] {
			p.warnf("unknown key '%s' in [%s] is ignored", name, section.Name())
			continue
		}
		if len(key.ValueWithShadows()) > 1 && !repeatableKeys[name] {
			if lastWins {
				p.warnf("'%s' is set more than once in [%s]; the last value is used", name, section.Name())
			} else {
				p.errorf("'%s' is set more than once in [%s]", name, section.Name())
			}
		}
	}
}

func (p *parser) retention(section *ini.Section, key string) int {
	n, err := strconv.Atoi(value(section, key))
	if err != nil {
		p.errorf("'%s' for %s must be an integer", key, section.Name())
	}
	return n
}

func (p *parser) pruneDays(section *ini.Section, where string, defaultDays int) int {
	raw := value(section, "retention_prune")
	if raw == "" {
		return defaultDays
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		p.errorf("'retention_prune' for %s must be an integer", where)
		return defaultDays
	}
	if days < 1 {
		p.errorf("'retention_prune' for %s must be at least 1", where)
	}
	return days
}

// exclude reads the exclude keys of a section and adds them to base.
func (p *parser) exclude(section *ini.Section, base Exclude) Exclude {
	ex := Exclude{
		Patterns:  append(append([]string(nil), base.Patterns...), nonEmpty(values(section, "exclude"))...),
		Files:     append(append([]string(nil), base.Files...), nonEmpty(values(section, "exclude_file"))...),
		Caches:    p.boolean(section, "exclude_caches", base.Caches),
		IfPresent: append(append([]string(nil), base.IfPresent...), nonEmpty(values(section, "exclude_if_present"))...),
	}
	for _, pattern := range nonEmpty(values(section, "exclude")) {
		if err := checkPattern(pattern); err != nil {
			p.errorf("'exclude' pattern %q in [%s] is not valid: %v", pattern, section.Name(), err)
		}
	}
	return ex
}

// checkPattern rejects patterns restic cannot use, such as an unclosed "[".
// A leading "!" negates a pattern and "**" matches any number of directories.
func checkPattern(pattern string) error {
	for _, part := range strings.Split(strings.TrimPrefix(pattern, "!"), "/") {
		if part == "**" {
			continue
		}
		if _, err := path.Match(part, ""); err != nil {
			return err
		}
	}
	return nil
}

func nonEmpty(vals []string) []string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (p *parser) boolean(section *ini.Section, key string, defaultValue bool) bool {
	raw := value(section, key)
	if raw == "" {
		return defaultValue
	}
	switch strings.ToLower(raw) {
	case "true", "t", "yes", "y", "on", "1":
		return true
	case "false", "f", "no", "n", "off", "0":
		return false
	}
	p.errorf("'%s' in [%s] must be true or false, got %q", key, section.Name(), raw)
	return defaultValue
}

// checkChannels warns about enabled notification channels that cannot work. These
// are warnings, so a broken notification setup never stops the backups.
func (p *parser) checkChannels(file *ini.File, cfg Config) {
	missing := func(pairs ...string) []string {
		var names []string
		for i := 0; i < len(pairs); i += 2 {
			if pairs[i+1] == "" {
				names = append(names, "'"+pairs[i]+"'")
			}
		}
		return names
	}

	if cfg.SMTP.Enabled {
		m := missing("server", cfg.SMTP.Server, "port", cfg.SMTP.Port, "from", cfg.SMTP.From, "to", cfg.SMTP.To)
		switch {
		case value(file.Section("smtp"), "enabled") == "" && cfg.SMTP.Server == "":
			p.warnf("email is enabled by default but [smtp] has no server; set 'enabled = false' in [smtp] or configure it")
		case len(m) > 0:
			p.warnf("[smtp] is enabled but %s not set, so email cannot be sent", strings.Join(m, ", "))
		}
	}
	if cfg.Matrix.Enabled {
		m := missing("server", cfg.Matrix.Server, "room_id", cfg.Matrix.RoomID)
		if cfg.Matrix.AccessToken == "" && (cfg.Matrix.Username == "" || cfg.Matrix.Pass == "") {
			m = append(m, "'access_token' (or 'username' and 'pass')")
		}
		if len(m) > 0 {
			p.warnf("[matrix] is enabled but %s not set, so Matrix messages cannot be sent", strings.Join(m, ", "))
		}
	}
	if cfg.Telegram.Enabled {
		m := missing("bot_token", cfg.Telegram.BotToken)
		if cfg.Telegram.ChatID == 0 {
			m = append(m, "'chat_id'")
		}
		if len(m) > 0 {
			p.warnf("[telegram] is enabled but %s not set, so Telegram messages cannot be sent", strings.Join(m, ", "))
		}
	}
}

// value returns the last value of a key, like a plain INI parser would, or "".
func value(section *ini.Section, key string) string {
	vals := values(section, key)
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1]
}

// values returns every value of a key that may be repeated.
func values(section *ini.Section, key string) []string {
	if !section.HasKey(key) {
		return nil
	}
	var vals []string
	for _, v := range section.Key(key).ValueWithShadows() {
		vals = append(vals, strings.TrimSpace(v))
	}
	return vals
}

// PathWarnings reports directories of dir jobs that do not exist on this machine.
func (c Config) PathWarnings() []string {
	var warnings []string
	for _, job := range c.Jobs {
		for _, dir := range job.Directories {
			if _, err := os.Stat(dir); err == nil {
				continue
			}
			warning := fmt.Sprintf("directory %q of %s does not exist", dir, job.Key)
			if fields := strings.Fields(dir); len(fields) > 1 && allExist(fields) {
				warning += "; to back up several directories, put each on its own 'directory =' line"
			}
			warnings = append(warnings, warning)
		}
		for _, file := range job.Exclude.Files {
			if _, err := os.Stat(file); err != nil {
				warnings = append(warnings, fmt.Sprintf("exclude file %q of %s does not exist", file, job.Key))
			}
		}
	}
	return warnings
}

func allExist(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

// Job returns the job with the given key.
func (c Config) Job(key string) (Job, bool) {
	for _, job := range c.Jobs {
		if job.Key == key {
			return job, true
		}
	}
	return Job{}, false
}

// Repositories returns the distinct repositories of all jobs, sorted.
func (c Config) Repositories() []string {
	seen := make(map[string]bool)
	var repos []string
	for _, job := range c.Jobs {
		if !seen[job.Repository] {
			seen[job.Repository] = true
			repos = append(repos, job.Repository)
		}
	}
	sort.Strings(repos)
	return repos
}

// Find returns the first of the locations that exists, or custom when it is set.
func Find(custom string, locations []string) string {
	if custom != "" {
		return custom
	}
	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}
	return ""
}

// PermissionWarning returns a warning when a file holding credentials can be read by
// other users, or "" when its permissions are fine or it does not exist.
func PermissionWarning(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Sprintf("Warning: %s contains credentials but has mode %04o; restrict it with: chmod 600 %s", path, perm, path)
	}
	return ""
}
