// Package report turns backup results into the summary printed, logged and sent
// through the notifiers.
package report

import (
	"bytes"
	"fmt"
	htmlTemplate "html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"resticara/internal/backup"
)

// Status messages, also compared in the Matrix and Telegram templates.
const (
	StatusSuccess = "Backup successful"
	StatusWarning = "Backup completed with warnings"
	StatusFailure = "BACKUP FAILED! See output above."
)

const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	bold   = "\033[1m"
)

// Report is the data passed to the templates. Field names are part of the template
// interface: customized templates in /etc/resticara/templates rely on them, so
// fields are only ever added.
type Report struct {
	// Kind names the run, for example "Backup".
	Kind   string
	HostID string
	Date   string
	// Status is "success", "warning" or "failure".
	Status string
	// StatusMessage is StatusSuccess, StatusWarning or StatusFailure.
	StatusMessage string
	// Subject names the host and the jobs that need attention, for example
	// "[server-1] BACKUP FAILED: mysql:website".
	Subject  string
	Commands []CommandInfo
}

type CommandInfo struct {
	CommandKey string
	// Status is "success", "warning" or "failure"; StatusLabel is "OK", "WARNING" or "FAILED".
	Status      string
	StatusLabel string
	// Duration is how long the job took, rounded to seconds, for example "1m23s".
	Duration     string
	BackupCmd    string
	BackupOutput string
	ForgetCmd    string
	ForgetOutput string
}

var statusLabels = map[backup.Status]string{
	backup.Success: "OK",
	backup.Warning: "WARNING",
	backup.Failure: "FAILED",
}

// New builds the report for a backup run that started at date.
func New(hostID string, date time.Time, result backup.Result) Report {
	r := Report{
		Kind:   "Backup",
		HostID: hostID,
		Date:   date.Format(time.RFC1123),
		Status: result.Status.String(),
	}
	var failed, warned []string
	for _, job := range result.Jobs {
		r.Commands = append(r.Commands, CommandInfo{
			CommandKey:   job.Key,
			Status:       job.Status.String(),
			StatusLabel:  statusLabels[job.Status],
			Duration:     job.Duration.Round(time.Second).String(),
			BackupCmd:    job.BackupCmd,
			BackupOutput: job.BackupOutput,
			ForgetCmd:    job.ForgetCmd,
			ForgetOutput: job.ForgetOutput,
		})
		switch job.Status {
		case backup.Failure:
			failed = append(failed, job.Key)
		case backup.Warning:
			warned = append(warned, job.Key)
		}
	}

	switch result.Status {
	case backup.Success:
		r.StatusMessage = StatusSuccess
		r.Subject = fmt.Sprintf("[%s] Backup successful", hostID)
	case backup.Warning:
		r.StatusMessage = StatusWarning
		r.Subject = fmt.Sprintf("[%s] Backup completed with warnings: %s", hostID, strings.Join(warned, ", "))
	default:
		r.StatusMessage = StatusFailure
		r.Subject = fmt.Sprintf("[%s] BACKUP FAILED: %s", hostID, strings.Join(failed, ", "))
	}
	return r
}

// Logger is the part of *syslog.Writer used for the report.
type Logger interface {
	Notice(m string) error
}

// Log writes the report to the logger, one line per field.
func Log(logger Logger, r Report) {
	logger.Notice(fmt.Sprintf("Host ID: %s", r.HostID))
	logger.Notice(fmt.Sprintf("Date: %s", r.Date))
	logger.Notice(fmt.Sprintf("Status: %s", r.StatusMessage))
	for _, cmd := range r.Commands {
		logger.Notice(fmt.Sprintf("Job %s: %s (%s)", cmd.CommandKey, cmd.StatusLabel, cmd.Duration))
	}

	for _, cmd := range r.Commands {
		logger.Notice(fmt.Sprintf("Command Key: %s", cmd.CommandKey))
		logger.Notice(fmt.Sprintf("Backup Command: %s", cmd.BackupCmd))
		logger.Notice(fmt.Sprintf("Backup Output: %s", strings.TrimSpace(cmd.BackupOutput)))
		logger.Notice(fmt.Sprintf("Forget Command: %s", cmd.ForgetCmd))
		logger.Notice(fmt.Sprintf("Forget Output: %s", strings.TrimSpace(cmd.ForgetOutput)))
	}
}

// Print writes the colored summary for the terminal.
func Print(w io.Writer, r Report) {
	fmt.Fprintln(w, bold+"Backup Summary:"+reset)
	fmt.Fprintln(w, "---------------")
	fmt.Fprintf(w, bold+"Host ID:"+reset+" %s\n", r.HostID)
	fmt.Fprintf(w, bold+"Date:"+reset+" %s\n", r.Date)
	statusColor := red
	switch r.StatusMessage {
	case StatusSuccess:
		statusColor = green
	case StatusWarning:
		statusColor = yellow
	}
	fmt.Fprintf(w, bold+"Status:"+reset+" %s%s%s\n", statusColor, r.StatusMessage, reset)
	for _, cmd := range r.Commands {
		fmt.Fprintf(w, "  %s%-7s%s %s (%s)\n", labelColor(cmd.Status), cmd.StatusLabel, reset, cmd.CommandKey, cmd.Duration)
	}
	for _, cmd := range r.Commands {
		fmt.Fprintf(w, bold+"Command Key:"+reset+" %s\n", cmd.CommandKey)
		fmt.Fprintf(w, "  "+bold+"Backup Command:"+reset+" %s\n", cmd.BackupCmd)
		fmt.Fprintf(w, "  "+bold+"Backup Output:"+reset+" %s\n", strings.TrimSpace(cmd.BackupOutput))
		fmt.Fprintf(w, "  "+bold+"Forget Command:"+reset+" %s\n", cmd.ForgetCmd)
		fmt.Fprintf(w, "  "+bold+"Forget Output:"+reset+" %s\n", strings.TrimSpace(cmd.ForgetOutput))
	}
	fmt.Fprintln(w, "---------------")
}

func labelColor(status string) string {
	switch status {
	case "success":
		return green
	case "warning":
		return yellow
	}
	return red
}

// TemplateLocations returns where a template is looked up, in order: relative to the
// working directory, next to the config, in /etc/resticara and in ~/.config/resticara.
func TemplateLocations(configPath, name string) []string {
	return []string{
		filepath.Join("templates", name),
		filepath.Join(filepath.Dir(configPath), "templates", name),
		filepath.Join("/etc/resticara/templates", name),
		filepath.Join(os.Getenv("HOME"), ".config/resticara", name),
	}
}

// RenderText renders a text/template file, used for email. The label names the
// template in errors ("parsing <label> template: ...").
func RenderText(path, label string, r Report) (string, error) {
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", templateName(label), err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, r); err != nil {
		return "", fmt.Errorf("executing %s: %w", templateName(label), err)
	}
	return buf.String(), nil
}

// RenderHTML renders an html/template file, used for Matrix and Telegram.
func RenderHTML(path, label string, r Report) (string, error) {
	tmpl, err := htmlTemplate.ParseFiles(path)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", templateName(label), err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, r); err != nil {
		return "", fmt.Errorf("executing %s: %w", templateName(label), err)
	}
	return buf.String(), nil
}

func templateName(label string) string {
	if label == "" {
		return "template"
	}
	return label + " template"
}
