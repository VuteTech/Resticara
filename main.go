/*
***********************************************

	This file is part of Resticara.

Resticara is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License
as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version.

Resticara is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied
warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for more details.
(c)2023 Vute Tech Ltd. <office@vute.tech>
(c)2023 Blagovest Petrov <blagovest@petrovs.info>

You should have received a copy of the GNU General Public License along with Foobar. If not, see <https://www.gnu.org/licenses/>.
*/
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"log/syslog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"gopkg.in/ini.v1"
	htmlTemplate "html/template"
	"resticara/notifiers/email"
	"resticara/notifiers/matrix"
	"resticara/notifiers/telegram"
)

type CommandInfo struct {
	CommandKey   string
	BackupCmd    string
	BackupOutput string
	ForgetCmd    string
	ForgetOutput string
}

type MailData struct {
	HostID        string
	Date          string
	Commands      []CommandInfo
	StatusMessage string
}

type Config struct {
	From            string
	Username        string
	Pass            string
	To              string
	SMTPServer      string
	SMTPPort        string
	HostID          string
	RetentionPrune  int
	SMTPEnabled     bool
	MatrixEnabled   bool
	MatrixServer    string
	MatrixUser      string
	MatrixPass      string
	MatrixRoomID    string
	MatrixToken     string
	TelegramEnabled bool
	TelegramToken   string
	TelegramChatID  int64
	Commands        map[string]map[string]string
}

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Bold   = "\033[1m"
)

const (
	StatusSuccess = "Backup successful"
	StatusWarning = "Backup completed with warnings"
	StatusFailure = "BACKUP FAILED! See output above."
)

const (
	// restic exits with 3 when the snapshot was created but some source files could not be read
	resticExitIncomplete = 3
	resticRetryLock      = "--retry-lock 30m"
)

func readConfig(file string) (Config, error) {
	var config Config

	cfg, err := ini.Load(file)
	if err != nil {
		return Config{}, err
	}

	config.SMTPEnabled = cfg.Section("smtp").Key("enabled").MustBool(true)
	config.From = cfg.Section("smtp").Key("from").String()
	config.Username = cfg.Section("smtp").Key("username").String()
	config.Pass = cfg.Section("smtp").Key("pass").String()
	config.To = cfg.Section("smtp").Key("to").String()
	config.SMTPServer = cfg.Section("smtp").Key("server").String()
	config.SMTPPort = cfg.Section("smtp").Key("port").String()

	config.MatrixEnabled = cfg.Section("matrix").Key("enabled").MustBool(false)
	config.MatrixServer = cfg.Section("matrix").Key("server").String()
	config.MatrixUser = cfg.Section("matrix").Key("username").String()
	config.MatrixPass = cfg.Section("matrix").Key("pass").String()
	config.MatrixRoomID = cfg.Section("matrix").Key("room_id").String()
	config.MatrixToken = cfg.Section("matrix").Key("access_token").String()

	config.TelegramEnabled = cfg.Section("telegram").Key("enabled").MustBool(false)
	config.TelegramToken = cfg.Section("telegram").Key("bot_token").String()
	config.TelegramChatID = cfg.Section("telegram").Key("chat_id").MustInt64(0)

	config.HostID = cfg.Section("general").Key("hostID").String()
	config.RetentionPrune = cfg.Section("general").Key("retention_prune").MustInt(30)

	config.Commands = make(map[string]map[string]string)

	for _, section := range cfg.Sections() {
		if section.Name() == "smtp" || section.Name() == "matrix" || section.Name() == "telegram" {
			continue
		}

		splitName := strings.Split(section.Name(), ":")
		if len(splitName) < 2 {
			continue
		}

		commandType := splitName[0]
		commandName := splitName[1]
		if commandType != "dir" && commandType != "mysql" {
			return Config{}, fmt.Errorf("unsupported backup type %q in section [%s], expected dir or mysql", commandType, section.Name())
		}
		commandSettings := make(map[string]string)
		for key, value := range section.KeysHash() {
			commandSettings[key] = value
		}

		config.Commands[commandType+":"+commandName] = commandSettings
	}

	for commandKey, settings := range config.Commands {
		if _, err := strconv.Atoi(settings["retention_daily"]); err != nil {
			return Config{}, fmt.Errorf("'retention_daily' for %s must be an integer", commandKey)
		}
		if _, err := strconv.Atoi(settings["retention_weekly"]); err != nil {
			return Config{}, fmt.Errorf("'retention_weekly' for %s must be an integer", commandKey)
		}
		if _, err := strconv.Atoi(settings["retention_monthly"]); err != nil {
			return Config{}, fmt.Errorf("'retention_monthly' for %s must be an integer", commandKey)
		}
		if val, ok := settings["retention_prune"]; ok {
			if _, err := strconv.Atoi(val); err != nil {
				return Config{}, fmt.Errorf("'retention_prune' for %s must be an integer", commandKey)
			}
		}
	}

	return config, nil
}

func searchForFile(customPath string, defaultLocations []string) string {
	if customPath != "" {
		return customPath
	}

	for _, loc := range defaultLocations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}
	return ""
}

func templateLocations(configPath, name string) []string {
	return []string{
		filepath.Join("templates", name),
		filepath.Join(filepath.Dir(configPath), "templates", name),
		filepath.Join("/etc/resticara/templates", name),
		filepath.Join(os.Getenv("HOME"), ".config/resticara", name),
	}
}

// warnIfAccessibleByOthers warns when a file holding credentials can be read by other users.
func warnIfAccessibleByOthers(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		fmt.Printf("Warning: %s contains credentials but has mode %04o; restrict it with: chmod 600 %s\n", path, perm, path)
	}
}

func printUsage() {
	fmt.Println("Usage of resticara:")
	fmt.Println("  --config=       : Specify a custom config.ini file path")
	fmt.Println("  --mail_template=: Specify a custom mail template file path")
	fmt.Println("  run [command]   : Run backups (all or specific command)")
	fmt.Println("  prune <all|repository> : Prune restic repositories")
	fmt.Println("  gentimer        : Generate systemd service and timer files")
}

func printSummary(mailData MailData, logwriter *syslog.Writer) {

	// Log to syslog
	logwriter.Notice(fmt.Sprintf("Host ID: %s", mailData.HostID))
	logwriter.Notice(fmt.Sprintf("Date: %s", mailData.Date))
	logwriter.Notice(fmt.Sprintf("Status: %s", mailData.StatusMessage))

	for _, cmdInfo := range mailData.Commands {
		logwriter.Notice(fmt.Sprintf("Command Key: %s", cmdInfo.CommandKey))
		logwriter.Notice(fmt.Sprintf("Backup Command: %s", cmdInfo.BackupCmd))
		logwriter.Notice(fmt.Sprintf("Backup Output: %s", strings.TrimSpace(cmdInfo.BackupOutput)))
		logwriter.Notice(fmt.Sprintf("Forget Command: %s", cmdInfo.ForgetCmd))
		logwriter.Notice(fmt.Sprintf("Forget Output: %s", strings.TrimSpace(cmdInfo.ForgetOutput)))
	}

	fmt.Println(Bold + "Backup Summary:" + Reset)
	fmt.Println("---------------")
	fmt.Printf(Bold+"Host ID:"+Reset+" %s\n", mailData.HostID)
	fmt.Printf(Bold+"Date:"+Reset+" %s\n", mailData.Date)
	statusColor := Red
	switch mailData.StatusMessage {
	case StatusSuccess:
		statusColor = Green
	case StatusWarning:
		statusColor = Yellow
	}
	fmt.Printf(Bold+"Status:"+Reset+" %s%s%s\n", statusColor, mailData.StatusMessage, Reset)
	for _, cmdInfo := range mailData.Commands {
		fmt.Printf(Bold+"Command Key:"+Reset+" %s\n", cmdInfo.CommandKey)
		fmt.Printf("  "+Bold+"Backup Command:"+Reset+" %s\n", cmdInfo.BackupCmd)
		fmt.Printf("  "+Bold+"Backup Output:"+Reset+" %s\n", strings.TrimSpace(cmdInfo.BackupOutput))
		fmt.Printf("  "+Bold+"Forget Command:"+Reset+" %s\n", cmdInfo.ForgetCmd)
		fmt.Printf("  "+Bold+"Forget Output:"+Reset+" %s\n", strings.TrimSpace(cmdInfo.ForgetOutput))
	}
	fmt.Println("---------------")
}

// runCommand executes the command and returns its exit code (-1 if it could not be started)
// together with the captured stdout and stderr.
func runCommand(command string) (int, string, string) {
	var stdoutBuf, stderrBuf bytes.Buffer

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return -1, "", "empty command"
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf // Capture stderr as well
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), stdoutBuf.String(), stderrBuf.String()
	}
	if err != nil {
		return -1, stdoutBuf.String(), stderrBuf.String() + err.Error()
	}
	return 0, stdoutBuf.String(), stderrBuf.String()
}

// buildCommands returns the restic backup and forget commands for a configured backup.
func buildCommands(commandKey string, settings map[string]string) (string, string) {
	bucket := settings["bucket"]
	var backupCmd string
	if strings.HasPrefix(commandKey, "dir:") {
		backupCmd = fmt.Sprintf("restic -r %s %s backup %s", bucket, resticRetryLock, settings["directory"])
	} else if strings.HasPrefix(commandKey, "mysql:") {
		// restic runs mysqldump itself, so a failed dump fails the backup instead of storing a truncated snapshot
		database := settings["database"]
		backupCmd = fmt.Sprintf("restic -r %s %s backup --stdin-filename %s.sql --stdin-from-command -- mysqldump --single-transaction %s", bucket, resticRetryLock, database, database)
	}
	forgetCmd := fmt.Sprintf("restic -r %s %s forget --keep-daily %s --keep-weekly %s --keep-monthly %s", bucket, resticRetryLock, settings["retention_daily"], settings["retention_weekly"], settings["retention_monthly"])
	return backupCmd, forgetCmd
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer(":", "-", "/", "-", " ", "-")
	return replacer.Replace(name)
}

// generateTimers writes one backup timer that runs all backups sequentially (one report per run)
// and a prune timer per configured backup.
func generateTimers(config Config, configPath string) error {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if binary, err = filepath.EvalSymlinks(binary); err != nil {
		return err
	}
	// restic credentials (RESTIC_PASSWORD, B2_ACCOUNT_ID, B2_ACCOUNT_KEY, ...) are read from this file
	envFile := filepath.Join(filepath.Dir(configPath), "restic.env")
	serviceEnv := fmt.Sprintf(`EnvironmentFile=-%s
Environment=RESTIC_CACHE_DIR=/var/cache/resticara
CacheDirectory=resticara`, envFile)

	unitOut, err := exec.Command("systemctl", "show", "--property=UnitPath").Output()
	if err != nil {
		return fmt.Errorf("failed to retrieve systemd unit path: %v", err)
	}
	unitLine := strings.TrimSpace(string(unitOut))
	unitLine = strings.TrimPrefix(unitLine, "UnitPath=")
	paths := strings.FieldsFunc(unitLine, func(r rune) bool { return r == ':' || r == ' ' })
	unitDir := ""
	for _, p := range paths {
		if p == "/etc/systemd/system" {
			if _, err := os.Stat(p); err == nil {
				unitDir = p
				break
			}
		}
	}
	if unitDir == "" {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				unitDir = p
				break
			}
		}
	}
	if unitDir == "" {
		return fmt.Errorf("could not determine systemd unit directory from UnitPath: %s", unitLine)
	}

	type timerUnit struct {
		commandKey string
		sanitized  string
		bucket     string
		pruneDays  int
	}
	var units []timerUnit
	expected := map[string]struct{}{"resticara-backup": {}}
	for commandKey, settings := range config.Commands {
		sanitized := sanitizeName(commandKey)
		pruneDays := config.RetentionPrune
		if val, ok := settings["retention_prune"]; ok {
			if d, err := strconv.Atoi(val); err == nil {
				pruneDays = d
			}
		}
		bucket := settings["bucket"]
		units = append(units, timerUnit{
			commandKey: commandKey,
			sanitized:  sanitized,
			bucket:     bucket,
			pruneDays:  pruneDays,
		})
		expected["resticara-"+sanitized+"-prune"] = struct{}{}
	}

	entries, err := os.ReadDir(unitDir)
	if err != nil {
		return err
	}
	existing := make(map[string]struct{})
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "resticara-") {
			continue
		}
		if !(strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer")) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".service"), ".timer")
		existing[base] = struct{}{}
	}
	for base := range existing {
		if _, ok := expected[base]; !ok {
			exec.Command("systemctl", "disable", "--now", base+".timer").Run()
			exec.Command("systemctl", "disable", "--now", base+".service").Run()
			os.Remove(filepath.Join(unitDir, base+".timer"))
			os.Remove(filepath.Join(unitDir, base+".service"))
		}
	}

	backupService := fmt.Sprintf(`[Unit]
Description=Resticara backup
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s --config=%s run
%s

[Install]
WantedBy=multi-user.target
`, binary, configPath, serviceEnv)

	backupTimer := `[Unit]
Description=Resticara backup timer

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
`

	if err := os.WriteFile(filepath.Join(unitDir, "resticara-backup.service"), []byte(backupService), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(unitDir, "resticara-backup.timer"), []byte(backupTimer), 0644); err != nil {
		return err
	}
	timers := []string{"resticara-backup.timer"}

	for _, u := range units {
		pruneService := fmt.Sprintf(`[Unit]
Description=Resticara prune for %s
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s --config=%s prune %s
%s

[Install]
WantedBy=multi-user.target
`, u.commandKey, binary, configPath, u.bucket, serviceEnv)

		// OnUnitActiveSec alone never fires because it counts from the last run of the service;
		// OnActiveSec schedules the first run after the timer is (re)started.
		pruneTimer := fmt.Sprintf(`[Unit]
Description=Resticara prune timer for %s

[Timer]
OnActiveSec=1h
OnUnitActiveSec=%dd
RandomizedDelaySec=1h

[Install]
WantedBy=timers.target
`, u.commandKey, u.pruneDays)

		if err := os.WriteFile(filepath.Join(unitDir, fmt.Sprintf("resticara-%s-prune.service", u.sanitized)), []byte(pruneService), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(unitDir, fmt.Sprintf("resticara-%s-prune.timer", u.sanitized)), []byte(pruneTimer), 0644); err != nil {
			return err
		}
		timers = append(timers, fmt.Sprintf("resticara-%s-prune.timer", u.sanitized))
	}

	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %v", err)
	}
	for _, t := range timers {
		if err := exec.Command("systemctl", "enable", t).Run(); err != nil {
			return fmt.Errorf("failed to enable %s: %v", t, err)
		}
		if err := exec.Command("systemctl", "restart", t).Run(); err != nil {
			return fmt.Errorf("failed to restart %s: %v", t, err)
		}
	}

	fmt.Printf("Systemd timer files written to %s and activated.\n", unitDir)
	if _, err := os.Stat(envFile); err != nil {
		fmt.Printf("Warning: %s not found; restic credentials (RESTIC_PASSWORD, B2_ACCOUNT_ID, B2_ACCOUNT_KEY) must be set there for the timers to work.\n", envFile)
	} else {
		warnIfAccessibleByOthers(envFile)
	}
	return nil
}

type DefaultCommandRunner struct{}

func (runner DefaultCommandRunner) Run(cmd string) (int, string, string) {
	return runCommand(cmd)
}

func main() {
	os.Exit(realMain())
}

// realMain returns the process exit code: 0 on success (including backups with warnings),
// 1 on failure and 2 on usage errors, so systemd marks failed runs as failed.
func realMain() int {
	logwriter, err := syslog.New(syslog.LOG_NOTICE, "resticara")
	if err != nil {
		log.Fatal("Failed to initialize syslog writer:", err)
	}
	defer logwriter.Close()

	customConfig := flag.String("config", "", "Path to custom config.ini file")
	customTemplate := flag.String("mail_template", "", "Path to custom mail template file")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		return 2
	}

	configPath := searchForFile(*customConfig, []string{
		"./config.ini",
		"/etc/resticara/config.ini",
		filepath.Join(os.Getenv("HOME"), ".config/resticara/config.ini"),
	})
	if configPath == "" {
		fmt.Println("Error: config.ini not found in any of the expected locations")
		return 1
	}

	warnIfAccessibleByOthers(configPath)

	config, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return 1
	}

	switch args[0] {
	case "run":
		templatePath := searchForFile(*customTemplate, templateLocations(configPath, "mail_template.txt"))
		if templatePath == "" {
			fmt.Println("Error: mail_template.txt not found in any of the expected locations")
			return 1
		}

		hostID := config.HostID
		if hostID == "hostname" {
			host, err := os.Hostname()
			if err != nil {
				fmt.Println("Could not determine hostname, using 'Unknown'")
				hostID = "Unknown"
			} else {
				hostID = host
			}
		}

		mailData := MailData{
			HostID: hostID,
			Date:   time.Now().Format(time.RFC1123),
		}
		allSuccess := true
		hasWarnings := false
		exitCode := 0

		commandRunner := DefaultCommandRunner{}

		var commandKeys []string
		if len(args) > 1 {
			if _, ok := config.Commands[args[1]]; !ok {
				fmt.Printf("Command %s not found in config\n", args[1])
				return 1
			}
			commandKeys = []string{args[1]}
		} else {
			for k := range config.Commands {
				commandKeys = append(commandKeys, k)
			}
			sort.Strings(commandKeys)
		}

		for _, commandKey := range commandKeys {
			settings := config.Commands[commandKey]
			fmt.Printf("Executing command %s\n", commandKey)
			commandInfo := CommandInfo{CommandKey: commandKey}

			backupCmd, forgetCmd := buildCommands(commandKey, settings)

			code, stdout, stderr := commandRunner.Run(backupCmd)
			commandInfo.BackupCmd = backupCmd
			commandInfo.BackupOutput = stdout + "\nStderr: " + stderr
			switch code {
			case 0:
			case resticExitIncomplete:
				hasWarnings = true
			default:
				allSuccess = false
			}

			code, stdout, stderr = commandRunner.Run(forgetCmd)
			commandInfo.ForgetCmd = forgetCmd
			commandInfo.ForgetOutput = stdout + "\nStderr: " + stderr
			allSuccess = allSuccess && code == 0

			mailData.Commands = append(mailData.Commands, commandInfo)
		}

		switch {
		case !allSuccess:
			mailData.StatusMessage = StatusFailure
			exitCode = 1
		case hasWarnings:
			mailData.StatusMessage = StatusWarning
		default:
			mailData.StatusMessage = StatusSuccess
		}

		tmpl, err := template.ParseFiles(templatePath)
		if err != nil {
			fmt.Println("Error parsing template:", err)
			return 1
		}

		var mailMessageBuffer bytes.Buffer
		err = tmpl.Execute(&mailMessageBuffer, mailData)
		if err != nil {
			fmt.Println("Error executing template:", err)
			return 1
		}

		var matrixMessageBuffer bytes.Buffer
		if config.MatrixEnabled {
			matrixTemplatePath := searchForFile("", templateLocations(configPath, "matrix_template.html"))
			if matrixTemplatePath == "" {
				fmt.Println("Error: matrix_template.html not found in any of the expected locations")
				return 1
			}
			matrixTmpl, err := htmlTemplate.ParseFiles(matrixTemplatePath)
			if err != nil {
				fmt.Println("Error parsing matrix template:", err)
				return 1
			}
			if err := matrixTmpl.Execute(&matrixMessageBuffer, mailData); err != nil {
				fmt.Println("Error executing matrix template:", err)
				return 1
			}
		}

		var telegramMessageBuffer bytes.Buffer
		if config.TelegramEnabled {
			telegramTemplatePath := searchForFile("", templateLocations(configPath, "telegram_template.html"))
			if telegramTemplatePath == "" {
				fmt.Println("Error: telegram_template.html not found in any of the expected locations")
				return 1
			}
			telegramTmpl, err := htmlTemplate.ParseFiles(telegramTemplatePath)
			if err != nil {
				fmt.Println("Error parsing telegram template:", err)
				return 1
			}
			if err := telegramTmpl.Execute(&telegramMessageBuffer, mailData); err != nil {
				fmt.Println("Error executing telegram template:", err)
				return 1
			}
		}

		printSummary(mailData, logwriter)

		if config.SMTPEnabled {
			emailNotifier := email.SmtpEmailNotifier{}
			mailMessage := mailMessageBuffer.String()
			mailSubject := mailData.StatusMessage + "---" + time.Now().Format(time.RFC1123)
			emailConfig := email.EmailConfig{
				From:       config.From,
				Username:   config.Username,
				Password:   config.Pass,
				To:         config.To,
				SmtpServer: config.SMTPServer,
				SmtpPort:   config.SMTPPort,
				Subject:    mailSubject,
				Body:       mailMessage,
			}

			if err := emailNotifier.Send(emailConfig); err != nil {
				fmt.Println(err)
				exitCode = 1
			} else {
				fmt.Println("Email sent!")
			}
		} else {
			fmt.Println("SMTP is disabled, not sending email.")
		}

		if config.MatrixEnabled {
			matrixNotifier := matrix.ClientAPINotifier{}
			message := matrixMessageBuffer.String()
			matrixConfig := matrix.MatrixConfig{
				Homeserver:  config.MatrixServer,
				Username:    config.MatrixUser,
				Password:    config.MatrixPass,
				RoomID:      config.MatrixRoomID,
				AccessToken: config.MatrixToken,
				Message:     message,
			}

			if err := matrixNotifier.Send(matrixConfig); err != nil {
				fmt.Println(err)
				exitCode = 1
			} else {
				fmt.Println("Matrix message sent!")
			}
		} else {
			fmt.Println("Matrix is disabled, not sending message.")
		}

		if config.TelegramEnabled {
			telegramNotifier := telegram.BotAPINotifier{}
			message := telegramMessageBuffer.String()
			telegramConfig := telegram.TelegramConfig{
				BotToken: config.TelegramToken,
				ChatID:   config.TelegramChatID,
				Message:  message,
			}

			if err := telegramNotifier.Send(telegramConfig); err != nil {
				fmt.Println(err)
				exitCode = 1
			} else {
				fmt.Println("Telegram message sent!")
			}
		} else {
			fmt.Println("Telegram is disabled, not sending message.")
		}
		return exitCode
	case "prune":
		if len(args) < 2 {
			fmt.Println("Usage: resticara prune <all|repository>")
			return 2
		}
		repoArg := args[1]
		uniqueBuckets := make(map[string]bool)
		for _, settings := range config.Commands {
			bucket := settings["bucket"]
			uniqueBuckets[bucket] = true
		}

		var buckets []string
		if repoArg == "all" {
			for bucket := range uniqueBuckets {
				buckets = append(buckets, bucket)
			}
			sort.Strings(buckets)
		} else {
			if !uniqueBuckets[repoArg] {
				fmt.Printf("Repository %s not found in config\n", repoArg)
				return 1
			}
			buckets = []string{repoArg}
		}

		commandRunner := DefaultCommandRunner{}
		exitCode := 0
		for _, bucket := range buckets {
			fmt.Printf("Pruning repository %s\n", bucket)
			code, stdout, stderr := commandRunner.Run(fmt.Sprintf("restic -r %s %s prune", bucket, resticRetryLock))
			fmt.Print(stdout)
			if stderr != "" {
				fmt.Printf("Stderr: %s\n", stderr)
			}
			if code != 0 {
				fmt.Printf("Prune failed for %s\n", bucket)
				exitCode = 1
			}
		}
		return exitCode
	case "gentimer":
		if err := generateTimers(config, configPath); err != nil {
			fmt.Printf("Error generating timers: %v\n", err)
			return 1
		}
		return 0
	default:
		printUsage()
		return 2
	}
}
