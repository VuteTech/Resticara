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
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

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
	Date          string
	Commands      []CommandInfo
	StatusMessage string
}

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

func main() {
	customConfig := flag.String("config", "", "Path to custom config.ini file")
	customTemplate := flag.String("mail_template", "", "Path to custom mail template file")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 || args[0] != "run" {
		fmt.Println("Usage of resticara:")
		fmt.Println("  --config=  : Specify a custom config.ini file path")
		fmt.Println("  --mail_template= : Specify a custom mail template file path")
		fmt.Println("  run        : Run the backup")
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

	templatePath := searchForFile(*customTemplate, []string{
		"./templates/mail_template.txt",
		"/etc/resticara/templates/mail_template.txt",
		filepath.Join(os.Getenv("HOME"), ".config/resticara/mail_template.txt"),
	})
	if templatePath == "" {
		fmt.Println("Error: mail_template.txt not found in any of the expected locations")
		return
	}

	from, username, pass, to, smtpServer, smtpPort, commands, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	mailData := MailData{
		Date: time.Now().Format(time.RFC1123),
	}
	allSuccess := true

	for commandKey, settings := range commands {
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
			backupCmd = fmt.Sprintf("mysqldump %s | restic -r %s backup --stdin --stdin-filename %s.sql", database, bucket, commandKey)
			forgetCmd = fmt.Sprintf("restic -r %s forget --keep-daily %s --keep-weekly %s --keep-monthly %s", bucket, retentionDaily, retentionWeekly, retentionMonthly)
		}

		cmdSuccess := func(command string) (bool, string) {
			parts := strings.Fields(command)
			head := parts[0]
			parts = parts[1:]

			cmd := exec.Command(head, parts...)
			var out bytes.Buffer
			cmd.Stdout = &out
			err := cmd.Run()

			if err != nil {
				fmt.Printf("Error during command: %v\n", err)
			}
			return err == nil, out.String()
		}

		success, output := cmdSuccess(backupCmd)
		commandInfo.BackupCmd = backupCmd
		commandInfo.BackupOutput = output
		allSuccess = allSuccess && success

		success, output = cmdSuccess(forgetCmd)
		commandInfo.ForgetCmd = forgetCmd
		commandInfo.ForgetOutput = output
		allSuccess = allSuccess && success

		mailData.Commands = append(mailData.Commands, commandInfo)
	}

	if allSuccess {
		mailData.StatusMessage = "Backup successful"
	} else {
		mailData.StatusMessage = "BACKUP FAILED! See output above."
	}

	// Open and parse the template file
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		fmt.Println("Error parsing template:", err)
		return
	}

	// Execute the template and write the output to a bytes.Buffer
	var mailMessageBuffer bytes.Buffer
	err = tmpl.Execute(&mailMessageBuffer, mailData)
	if err != nil {
		fmt.Println("Error executing template:", err)
		return
	}

	// Convert the bytes.Buffer to a string
	mailMessage := mailMessageBuffer.String()
	mailSubject := mailData.StatusMessage + "---" + time.Now().Format(time.RFC1123)

	sendEmail(from, username, pass, to, smtpServer, smtpPort, mailSubject, mailMessage)
}
