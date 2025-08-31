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
	"io"
	"log"
	"log/syslog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/yourusername/resticara/emailsender"
	"gopkg.in/ini.v1"
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
	From           string
	Username       string
	Pass           string
	To             string
	SMTPServer     string
	SMTPPort       string
	HostID         string
	RetentionPrune int
	SMTPEnabled    bool
	Commands       map[string]map[string]string
}

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Bold   = "\033[1m"
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
	config.HostID = cfg.Section("general").Key("hostID").String()
	config.RetentionPrune = cfg.Section("general").Key("retention_prune").MustInt(30)

	config.Commands = make(map[string]map[string]string)

	for _, section := range cfg.Sections() {
		if section.Name() == "smtp" {
			continue
		}

		splitName := strings.Split(section.Name(), ":")
		if len(splitName) < 2 {
			continue
		}

		commandType := splitName[0]
		commandName := splitName[1]
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
	if mailData.StatusMessage == "Backup successful" {
		fmt.Printf(Bold+"Status:"+Reset+" %s%s%s\n", Green, mailData.StatusMessage, Reset)
	} else {
		fmt.Printf(Bold+"Status:"+Reset+" %s%s%s\n", Red, mailData.StatusMessage, Reset)
	}
	for _, cmdInfo := range mailData.Commands {
		fmt.Printf(Bold+"Command Key:"+Reset+" %s\n", cmdInfo.CommandKey)
		fmt.Printf("  "+Bold+"Backup Command:"+Reset+" %s\n", cmdInfo.BackupCmd)
		fmt.Printf("  "+Bold+"Backup Output:"+Reset+" %s\n", strings.TrimSpace(cmdInfo.BackupOutput))
		fmt.Printf("  "+Bold+"Forget Command:"+Reset+" %s\n", cmdInfo.ForgetCmd)
		fmt.Printf("  "+Bold+"Forget Output:"+Reset+" %s\n", strings.TrimSpace(cmdInfo.ForgetOutput))
	}
	fmt.Println("---------------")
}

func cmdSuccess(command string) (bool, string, string) {
	var stdoutBuf, stderrBuf bytes.Buffer

	// Special case: we have a MySQL dump piped to restic
	if strings.HasPrefix(command, "mysqldump") {
		parts := strings.Split(command, "|")
		mysqldumpCmdStr := strings.TrimSpace(parts[0])
		resticCmdStr := strings.TrimSpace(parts[1])

		mysqldumpParts := strings.Fields(mysqldumpCmdStr)
		resticParts := strings.Fields(resticCmdStr)

		c1 := exec.Command(mysqldumpParts[0], mysqldumpParts[1:]...)
		c2 := exec.Command(resticParts[0], resticParts[1:]...)

		pr, pw := io.Pipe()
		c1.Stdout = pw
		c2.Stdin = pr
		c2.Stdout = &stdoutBuf
		c2.Stderr = &stderrBuf // Capture stderr as well

		var err1, err2 error

		c1.Start()
		c2.Start()

		go func() {
			defer pw.Close()
			err1 = c1.Wait()
		}()

		err2 = c2.Wait()
		if err1 != nil || err2 != nil {
			return false, stdoutBuf.String(), stderrBuf.String()
		}

		return true, stdoutBuf.String(), stderrBuf.String()
	}

	// For all other commands
	parts := strings.Fields(command)
	head := parts[0]
	parts = parts[1:]

	cmd := exec.Command(head, parts...)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf // Capture stderr as well
	err := cmd.Run()

	return err == nil, stdoutBuf.String(), stderrBuf.String()
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer(":", "-", "/", "-", " ", "-")
	return replacer.Replace(name)
}

func generateTimers(config Config) error {
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
	expected := make(map[string]struct{})
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
		expected["resticara-"+sanitized] = struct{}{}
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

	var timers []string
	for _, u := range units {
		backupService := fmt.Sprintf(`[Unit]
Description=Resticara backup for %s

[Service]
Type=oneshot
ExecStart=/usr/local/bin/resticara run %s

[Install]
WantedBy=multi-user.target
`, u.commandKey, u.commandKey)

		backupTimer := fmt.Sprintf(`[Unit]
Description=Resticara backup timer for %s

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
`, u.commandKey)

		pruneService := fmt.Sprintf(`[Unit]
Description=Resticara prune for %s

[Service]
Type=oneshot
ExecStart=/usr/local/bin/resticara prune %s

[Install]
WantedBy=multi-user.target
`, u.commandKey, u.bucket)

		pruneTimer := fmt.Sprintf(`[Unit]
Description=Resticara prune timer for %s

[Timer]
OnUnitActiveSec=%dd
Persistent=true

[Install]
WantedBy=timers.target
`, u.commandKey, u.pruneDays)

		if err := os.WriteFile(filepath.Join(unitDir, fmt.Sprintf("resticara-%s.service", u.sanitized)), []byte(backupService), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(unitDir, fmt.Sprintf("resticara-%s.timer", u.sanitized)), []byte(backupTimer), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(unitDir, fmt.Sprintf("resticara-%s-prune.service", u.sanitized)), []byte(pruneService), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(unitDir, fmt.Sprintf("resticara-%s-prune.timer", u.sanitized)), []byte(pruneTimer), 0644); err != nil {
			return err
		}
		timers = append(timers, fmt.Sprintf("resticara-%s.timer", u.sanitized))
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
	return nil
}

type DefaultCommandRunner struct{}

func (runner DefaultCommandRunner) Run(cmd string) (bool, string, string) {
	return cmdSuccess(cmd) // Here cmdSuccess is your existing function
}

func main() {
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
		return
	}

	configPath := searchForFile(*customConfig, []string{
		"./config.ini",
		"/etc/resticara/config.ini",
		filepath.Join(os.Getenv("HOME"), ".config/resticara/config.ini"),
	})
	if configPath == "" {
		fmt.Println("Error: config.ini not found in any of the expected locations")
		return
	}

	config, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	switch args[0] {
	case "run":
		templatePath := searchForFile(*customTemplate, []string{
			"./templates/mail_template.txt",
			"/etc/resticara/templates/mail_template.txt",
			filepath.Join(os.Getenv("HOME"), ".config/resticara/mail_template.txt"),
		})
		if templatePath == "" {
			fmt.Println("Error: mail_template.txt not found in any of the expected locations")
			return
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

		commandRunner := DefaultCommandRunner{}

		var commandKeys []string
		if len(args) > 1 {
			if _, ok := config.Commands[args[1]]; !ok {
				fmt.Printf("Command %s not found in config\n", args[1])
				return
			}
			commandKeys = []string{args[1]}
		} else {
			for k := range config.Commands {
				commandKeys = append(commandKeys, k)
			}
		}

		for _, commandKey := range commandKeys {
			settings := config.Commands[commandKey]
			fmt.Printf("Executing command %s\n", commandKey)
			commandInfo := CommandInfo{CommandKey: commandKey}

			var backupCmd, forgetCmd string
			bucket := settings["bucket"]
			retentionDaily := settings["retention_daily"]
			retentionWeekly := settings["retention_weekly"]
			retentionMonthly := settings["retention_monthly"]

			if strings.HasPrefix(commandKey, "dir:") {
				directory := settings["directory"]
				backupCmd = fmt.Sprintf("restic -r %s backup %s", bucket, directory)
				forgetCmd = fmt.Sprintf("restic -r %s forget --keep-daily %s --keep-weekly %s --keep-monthly %s", bucket, retentionDaily, retentionWeekly, retentionMonthly)
			} else if strings.HasPrefix(commandKey, "mysql:") {
				database := settings["database"]
				backupCmd = fmt.Sprintf("mysqldump %s | restic -r %s backup --stdin --stdin-filename %s.sql", database, bucket, database)
				forgetCmd = fmt.Sprintf("restic -r %s forget --keep-daily %s --keep-weekly %s --keep-monthly %s", bucket, retentionDaily, retentionWeekly, retentionMonthly)
			}

			success, stdout, stderr := commandRunner.Run(backupCmd)
			commandInfo.BackupCmd = backupCmd
			commandInfo.BackupOutput = stdout + "\nStderr: " + stderr
			allSuccess = allSuccess && success

			success, stdout, stderr = commandRunner.Run(forgetCmd)
			commandInfo.ForgetCmd = forgetCmd
			commandInfo.ForgetOutput = stdout + "\nStderr: " + stderr
			allSuccess = allSuccess && success

			mailData.Commands = append(mailData.Commands, commandInfo)
		}

		if allSuccess {
			mailData.StatusMessage = "Backup successful"
		} else {
			mailData.StatusMessage = "BACKUP FAILED! See output above."
		}

		tmpl, err := template.ParseFiles(templatePath)
		if err != nil {
			fmt.Println("Error parsing template:", err)
			return
		}

		var mailMessageBuffer bytes.Buffer
		err = tmpl.Execute(&mailMessageBuffer, mailData)
		if err != nil {
			fmt.Println("Error executing template:", err)
			return
		}

		printSummary(mailData, logwriter)

		if config.SMTPEnabled {
			emailSender := emailsender.SmtpEmailSender{}
			mailMessage := mailMessageBuffer.String()
			mailSubject := mailData.StatusMessage + "---" + time.Now().Format(time.RFC1123)
			emailConfig := emailsender.EmailConfig{
				From:       config.From,
				Username:   config.Username,
				Password:   config.Pass,
				To:         config.To,
				SmtpServer: config.SMTPServer,
				SmtpPort:   config.SMTPPort,
				Subject:    mailSubject,
				Body:       mailMessage,
			}

			if err := emailSender.Send(emailConfig); err != nil {
				fmt.Println(err)
			} else {
				fmt.Println("Email sent!")
			}
		} else {
			fmt.Println("SMTP is disabled, not sending email.")
		}
	case "prune":
		if len(args) < 2 {
			fmt.Println("Usage: resticara prune <all|repository>")
			return
		}
		repoArg := args[1]
		uniqueBuckets := make(map[string]bool)
		for _, settings := range config.Commands {
			bucket := settings["bucket"]
			uniqueBuckets[bucket] = true
		}

		commandRunner := DefaultCommandRunner{}

		if repoArg == "all" {
			for bucket := range uniqueBuckets {
				fmt.Printf("Pruning repository %s\n", bucket)
				success, stdout, stderr := commandRunner.Run(fmt.Sprintf("restic -r %s prune", bucket))
				fmt.Print(stdout)
				if stderr != "" {
					fmt.Printf("Stderr: %s\n", stderr)
				}
				if !success {
					fmt.Printf("Prune failed for %s\n", bucket)
				}
			}
		} else {
			if !uniqueBuckets[repoArg] {
				fmt.Printf("Repository %s not found in config\n", repoArg)
				return
			}
			fmt.Printf("Pruning repository %s\n", repoArg)
			success, stdout, stderr := commandRunner.Run(fmt.Sprintf("restic -r %s prune", repoArg))
			fmt.Print(stdout)
			if stderr != "" {
				fmt.Printf("Stderr: %s\n", stderr)
			}
			if !success {
				fmt.Printf("Prune failed for %s\n", repoArg)
			}
		}
	case "gentimer":
		if err := generateTimers(config); err != nil {
			fmt.Printf("Error generating timers: %v\n", err)
		}
	default:
		printUsage()
	}

	defer logwriter.Close()
}
