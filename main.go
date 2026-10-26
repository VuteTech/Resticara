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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/syslog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resticara/internal/backup"
	"resticara/internal/config"
	"resticara/internal/notify"
	"resticara/internal/report"
	"resticara/internal/restic"
	"resticara/internal/systemd"
	"resticara/notifiers/healthcheck"
)

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

	configPath := config.Find(*customConfig, []string{
		"./config.ini",
		"/etc/resticara/config.ini",
		filepath.Join(os.Getenv("HOME"), ".config/resticara/config.ini"),
	})
	if configPath == "" {
		fmt.Println("Error: config.ini not found in any of the expected locations")
		return 1
	}

	if warning := config.PermissionWarning(configPath); warning != "" {
		fmt.Println(warning)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		printConfigError(err)
		return 1
	}
	for _, warning := range cfg.Warnings {
		fmt.Println("Warning: " + warning)
	}

	switch args[0] {
	case "run":
		return runBackup(cfg, configPath, *customTemplate, args[1:], logwriter)
	case "validate":
		return runValidate(cfg, configPath)
	case "prune":
		return runPrune(cfg, args[1:])
	case "gentimer":
		return runGentimer(cfg, configPath)
	default:
		printUsage()
		return 2
	}
}

func printUsage() {
	fmt.Println("Usage of resticara:")
	fmt.Println("  --config=       : Specify a custom config.ini file path")
	fmt.Println("  --mail_template=: Specify a custom mail template file path")
	fmt.Println("  run [command]   : Run backups (all or specific command)")
	fmt.Println("  prune <all|repository> : Prune restic repositories")
	fmt.Println("  gentimer        : Generate systemd service and timer files")
	fmt.Println("  validate        : Check the configuration and the backed up directories")
}

// printConfigError prints why the configuration could not be loaded, one problem per line.
func printConfigError(err error) {
	var cfgErr *config.Error
	if errors.As(err, &cfgErr) {
		for _, warning := range cfgErr.Warnings {
			fmt.Println("Warning: " + warning)
		}
	}
	if cfgErr == nil || len(cfgErr.Problems) == 1 {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}
	fmt.Println("Error reading config:")
	for _, problem := range cfgErr.Problems {
		fmt.Println("  " + problem)
	}
}

// runValidate reports problems that only show up on this machine. Configuration errors
// and warnings were already printed while loading.
func runValidate(cfg config.Config, configPath string) int {
	for _, warning := range cfg.PathWarnings() {
		fmt.Println("Warning: " + warning)
	}
	fmt.Printf("Configuration %s is valid: %d backup jobs.\n", configPath, len(cfg.Jobs))
	return 0
}

// pinged runs fn between the start and end pings of the Healthchecks check at url,
// sending fn's exit code and report. A failed ping is reported and makes the exit
// code 1. Without a url, fn just runs.
func pinged(url string, fn func() (int, string)) int {
	if url == "" {
		code, _ := fn()
		return code
	}
	pinger := healthcheck.New(url)
	startErr := pinger.Start()
	if startErr != nil {
		fmt.Println("Warning: " + startErr.Error())
	}
	code, body := fn()
	if err := pinger.Finish(code, body); err != nil {
		fmt.Println("Warning: " + err.Error())
		return 1
	}
	if startErr != nil {
		return 1
	}
	return code
}

// runBackup runs all jobs, or the one named in args, and sends the report. Only
// full runs ping the healthcheck, so a manual run of one job does not hide a
// missing scheduled run.
func runBackup(cfg config.Config, configPath, customTemplate string, args []string, logwriter *syslog.Writer) int {
	url := cfg.Healthcheck.URL
	if len(args) > 0 {
		url = ""
	}
	return pinged(url, func() (int, string) {
		return backupAndReport(cfg, configPath, customTemplate, args, logwriter)
	})
}

// backupAndReport returns the exit code and the report text for the healthcheck.
func backupAndReport(cfg config.Config, configPath, customTemplate string, args []string, logwriter *syslog.Writer) (int, string) {
	templatePath := config.Find(customTemplate, report.TemplateLocations(configPath, "mail_template.txt"))
	if templatePath == "" {
		return fail("Error: mail_template.txt not found in any of the expected locations")
	}

	host := hostID(cfg)

	started := time.Now()
	jobs := cfg.Jobs
	if len(args) > 0 {
		job, ok := cfg.Job(args[0])
		if !ok {
			return fail(fmt.Sprintf("Command %s not found in config", args[0]))
		}
		jobs = []config.Job{job}
	}
	for _, warning := range (config.Config{Jobs: jobs}).PathWarnings() {
		fmt.Println("Warning: " + warning)
	}

	// restic records the machine's hostname unless hostID names the host explicitly
	resticHost := ""
	if cfg.General.HostID != "hostname" {
		resticHost = cfg.General.HostID
	}
	result := backup.Run(restic.ExecRunner{}, jobs, resticHost, os.Stdout)
	rep := report.New(host, started, result)
	exitCode := 0
	if result.Status == backup.Failure {
		exitCode = 1
	}

	messages, err := renderMessages(cfg, configPath, templatePath, rep)
	if err != nil {
		return fail(err.Error())
	}

	report.Log(logwriter, rep)
	report.Print(os.Stdout, rep)

	if cfg.General.Notify == config.NotifyProblems && result.Status == backup.Success {
		fmt.Println("Backup successful, not sending notifications (notify = problems).")
		return exitCode, messages.Mail
	}
	if !notify.Default().Send(cfg, rep, messages, os.Stdout) {
		exitCode = 1
	}
	return exitCode, messages.Mail
}

// fail prints the message and returns it as a failed run's report.
func fail(message string) (int, string) {
	fmt.Println(message)
	return 1, message
}

// renderMessages renders the report for email and for the enabled chat channels.
// The error is the complete message to print.
func renderMessages(cfg config.Config, configPath, mailTemplatePath string, rep report.Report) (notify.Messages, error) {
	var messages notify.Messages
	var err error

	if messages.Mail, err = report.RenderText(mailTemplatePath, "", rep); err != nil {
		return messages, errors.New("Error " + err.Error())
	}

	if cfg.Matrix.Enabled {
		path := config.Find("", report.TemplateLocations(configPath, "matrix_template.html"))
		if path == "" {
			return messages, errors.New("Error: matrix_template.html not found in any of the expected locations")
		}
		if messages.Matrix, err = report.RenderHTML(path, "matrix", rep); err != nil {
			return messages, errors.New("Error " + err.Error())
		}
	}

	if cfg.Telegram.Enabled {
		path := config.Find("", report.TemplateLocations(configPath, "telegram_template.html"))
		if path == "" {
			return messages, errors.New("Error: telegram_template.html not found in any of the expected locations")
		}
		if messages.Telegram, err = report.RenderHTML(path, "telegram", rep); err != nil {
			return messages, errors.New("Error " + err.Error())
		}
	}

	return messages, nil
}

// runPrune prunes all repositories, or the one named in args.
func runPrune(cfg config.Config, args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: resticara prune <all|repository>")
		return 2
	}

	repos := cfg.Repositories()
	if args[0] != "all" {
		repo, ok := cfg.Repository(args[0])
		if !ok {
			fmt.Printf("Repository %s not found in config\n", args[0])
			return 1
		}
		repos = []config.Repository{repo}
	}

	return pinged(cfg.Healthcheck.PruneURL, func() (int, string) {
		var output bytes.Buffer
		out := io.MultiWriter(os.Stdout, &output)
		runner := restic.ExecRunner{}
		var failed []string
		for _, repo := range repos {
			fmt.Fprintf(out, "Pruning repository %s\n", repo.URL)
			res := runner.Run(restic.PruneCommand(repo))
			fmt.Fprint(out, res.Stdout)
			if res.Stderr != "" {
				fmt.Fprintf(out, "Stderr: %s\n", res.Stderr)
			}
			if res.ExitCode != 0 {
				fmt.Fprintf(out, "Prune failed for %s\n", repo.URL)
				failed = append(failed, repo.URL)
			}
		}
		if len(failed) == 0 {
			return 0, output.String()
		}

		// successful prunes stay quiet; failures are reported like failed backups
		rep := report.Report{
			Kind:          "Prune",
			HostID:        hostID(cfg),
			Date:          time.Now().Format(time.RFC1123),
			Status:        "failure",
			StatusMessage: "PRUNE FAILED!",
		}
		rep.Subject = fmt.Sprintf("[%s] PRUNE FAILED: %s", rep.HostID, strings.Join(failed, ", "))
		notify.Default().Send(cfg, rep, notify.Plain(rep.Subject, output.String()), os.Stdout)
		return 1, output.String()
	})
}

// hostID returns the name of this machine for reports.
func hostID(cfg config.Config) string {
	if cfg.General.HostID != "hostname" {
		return cfg.General.HostID
	}
	host, err := os.Hostname()
	if err != nil {
		fmt.Println("Could not determine hostname, using 'Unknown'")
		return "Unknown"
	}
	return host
}

// runGentimer installs systemd units that run this binary.
func runGentimer(cfg config.Config, configPath string) int {
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err == nil {
		err = systemd.Generate(systemd.ExecSystemctl{}, cfg, binary, configPath, os.Stdout)
	}
	if err != nil {
		fmt.Printf("Error generating timers: %v\n", err)
		return 1
	}
	return 0
}
