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
	HostID        string
	Date          string
	Commands      []CommandInfo
	StatusMessage string
}

type Config struct {
	From        string
	Username    string
	Pass        string
	To          string
	SMTPServer  string
	SMTPPort    string
	HostID      string
	SMTPEnabled bool
	Commands    map[string]map[string]string
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

	return config, nil
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

func main() {

	logwriter, err := syslog.New(syslog.LOG_NOTICE, "resticara")
	if err != nil {
		log.Fatal("Failed to initialize syslog writer:", err)
	}

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

	config, err := readConfig(configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
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

	for commandKey, settings := range config.Commands {
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

	// Print summary to stfout
	printSummary(mailData, logwriter)

	if config.SMTPEnabled {
		// Convert the bytes.Buffer to a string
		mailMessage := mailMessageBuffer.String()
		mailSubject := mailData.StatusMessage + "---" + time.Now().Format(time.RFC1123)

		sendEmail(config.From, config.Username, config.Pass, config.To, config.SMTPServer, config.SMTPPort, mailSubject, mailMessage)
	} else {
		fmt.Println("SMTP is disabled, not sending email.")
	}

	defer logwriter.Close()
}
