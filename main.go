package main

import (
	"bytes"
	"fmt"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/ini.v1"
)

func readConfig(file string) (string, string, string, string, string, string, map[string]map[string]string, error) {
	cfg, err := ini.Load(file)
	if err != nil {
		return "", "", "", "", "", "", nil, err
	}

	from := cfg.Section("smtp").Key("from").String()
	username := cfg.Section("smtp").Key("username").String()
	pass := cfg.Section("smtp").Key("pass").String()
	to := cfg.Section("smtp").Key("to").String()
	smtpServer := cfg.Section("smtp").Key("server").String()
	smtpPort := cfg.Section("smtp").Key("port").String()

	commands := make(map[string]map[string]string)
	for _, section := range cfg.Sections() {
		if section.Name() == "smtp" {
			continue
		}

		splitName := strings.Split(section.Name(), ":")
		if len(splitName) < 2 {
			continue // Skip sections with names that don't contain ":"
		}
		commandType := splitName[0]
		commandName := splitName[1]

		commandSettings := make(map[string]string)
		for key, value := range section.KeysHash() {
			commandSettings[key] = value
		}

		commands[commandType+":"+commandName] = commandSettings
	}

	return from, username, pass, to, smtpServer, smtpPort, commands, nil
}

func sendEmail(from, username, pass, to, smtpServer, smtpPort, subject, body string) {
	message := "From: " + from + "\n" +
		"To: " + to + "\n" +
		"Subject: " + subject + "\n\n" +
		body

	err := smtp.SendMail(smtpServer+":"+smtpPort,
		smtp.PlainAuth("", username, pass, smtpServer),
		from, []string{to}, []byte(message))

	if err != nil {
		fmt.Printf("Error sending email: %v\n", err)
		return
	}

	fmt.Println("Email sent!")
}

func main() {
	// Define possible locations for the config file
	configLocations := []string{"./config.ini", "/etc/resticara/config.ini",
		filepath.Join(os.Getenv("HOME"), ".config/resticara/config.ini")}

	var foundConfig string
	for _, loc := range configLocations {
		if _, err := os.Stat(loc); err == nil {
			foundConfig = loc
			break
		}
	}

	if foundConfig == "" {
		fmt.Println("Error: config.ini not found in any of the expected locations")
		return
	}

	from, username, pass, to, smtpServer, smtpPort, commands, err := readConfig(foundConfig)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	mailMessage := fmt.Sprintf("----------Backup Report----------\n%s\n", time.Now().Format(time.RFC1123))
	mailSubject := ""
	allSuccess := true

	for commandKey, settings := range commands {
		fmt.Printf("Executing command %s\n", commandKey)

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
			backupCmd = fmt.Sprintf("mysqldump %s | restic -r %s backup --stdin --stdin-filename %s.sql", database, bucket, commandKey)
			forgetCmd = fmt.Sprintf("restic -r %s forget --keep-daily %s --keep-weekly %s --keep-monthly %s", bucket, retentionDaily, retentionWeekly, retentionMonthly)
		}

		cmdSuccess := func(command string) bool {
			parts := strings.Fields(command)
			head := parts[0]
			parts = parts[1:]

			cmd := exec.Command(head, parts...)
			var out bytes.Buffer
			cmd.Stdout = &out
			err := cmd.Run()

			mailMessage += fmt.Sprintf("\n$ %s\n%s\n", command, out.String())

			if err != nil {
				fmt.Printf("Error during command: %v\n", err)
				return false
			}
			return true
		}

		if !cmdSuccess(backupCmd) || !cmdSuccess(forgetCmd) {
			allSuccess = false
		}
	}

	if allSuccess {
		mailSubject = fmt.Sprintf("Backup successful---%s", time.Now().Format(time.RFC1123))
	} else {
		mailSubject = fmt.Sprintf("Backup FAILED---%s", time.Now().Format(time.RFC1123))
		mailMessage += "\n----------------------------------------\nBACKUP FAILED!! See output above.\n----------------------------------------\n"
	}

	sendEmail(from, username, pass, to, smtpServer, smtpPort, mailSubject, mailMessage)
}
