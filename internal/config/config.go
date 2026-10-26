// Package config loads the resticara INI configuration.
package config

import (
	"fmt"
	"os"
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
}

type General struct {
	// HostID is shown in reports; the value "hostname" means the machine's hostname.
	HostID string
	// RetentionPrune is the default prune interval in days.
	RetentionPrune int
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
	Directory  string // dir jobs
	Database   string // mysql jobs
	Retention  Retention
	// PruneDays is the prune interval, from the job or the [general] default.
	PruneDays int
}

type Retention struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Load reads and validates the configuration file.
func Load(path string) (Config, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return Config{}, err
	}

	var config Config

	smtp := cfg.Section("smtp")
	config.SMTP = SMTP{
		Enabled:  smtp.Key("enabled").MustBool(true),
		From:     smtp.Key("from").String(),
		Username: smtp.Key("username").String(),
		Pass:     smtp.Key("pass").String(),
		To:       smtp.Key("to").String(),
		Server:   smtp.Key("server").String(),
		Port:     smtp.Key("port").String(),
	}

	matrix := cfg.Section("matrix")
	config.Matrix = Matrix{
		Enabled:     matrix.Key("enabled").MustBool(false),
		Server:      matrix.Key("server").String(),
		Username:    matrix.Key("username").String(),
		Pass:        matrix.Key("pass").String(),
		RoomID:      matrix.Key("room_id").String(),
		AccessToken: matrix.Key("access_token").String(),
	}

	telegram := cfg.Section("telegram")
	config.Telegram = Telegram{
		Enabled:  telegram.Key("enabled").MustBool(false),
		BotToken: telegram.Key("bot_token").String(),
		ChatID:   telegram.Key("chat_id").MustInt64(0),
	}

	general := cfg.Section("general")
	config.General = General{
		HostID:         general.Key("hostID").String(),
		RetentionPrune: general.Key("retention_prune").MustInt(30),
	}

	jobs := make(map[string]map[string]string)
	for _, section := range cfg.Sections() {
		if section.Name() == "smtp" || section.Name() == "matrix" || section.Name() == "telegram" {
			continue
		}

		splitName := strings.Split(section.Name(), ":")
		if len(splitName) < 2 {
			continue
		}

		jobType := splitName[0]
		if jobType != string(JobDir) && jobType != string(JobMySQL) {
			return Config{}, fmt.Errorf("unsupported backup type %q in section [%s], expected dir or mysql", jobType, section.Name())
		}
		jobs[jobType+":"+splitName[1]] = section.KeysHash()
	}

	keys := make([]string, 0, len(jobs))
	for key := range jobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		job, err := parseJob(key, jobs[key], config.General.RetentionPrune)
		if err != nil {
			return Config{}, err
		}
		config.Jobs = append(config.Jobs, job)
	}

	return config, nil
}

func parseJob(key string, settings map[string]string, defaultPruneDays int) (Job, error) {
	jobType, name, _ := strings.Cut(key, ":")
	job := Job{
		Key:        key,
		Type:       JobType(jobType),
		Name:       name,
		Repository: settings["bucket"],
		Directory:  settings["directory"],
		Database:   settings["database"],
		PruneDays:  defaultPruneDays,
	}

	var err error
	if job.Retention.Daily, err = strconv.Atoi(settings["retention_daily"]); err != nil {
		return Job{}, fmt.Errorf("'retention_daily' for %s must be an integer", key)
	}
	if job.Retention.Weekly, err = strconv.Atoi(settings["retention_weekly"]); err != nil {
		return Job{}, fmt.Errorf("'retention_weekly' for %s must be an integer", key)
	}
	if job.Retention.Monthly, err = strconv.Atoi(settings["retention_monthly"]); err != nil {
		return Job{}, fmt.Errorf("'retention_monthly' for %s must be an integer", key)
	}
	if val, ok := settings["retention_prune"]; ok {
		if job.PruneDays, err = strconv.Atoi(val); err != nil {
			return Job{}, fmt.Errorf("'retention_prune' for %s must be an integer", key)
		}
	}
	return job, nil
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
