package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"resticara/internal/backup"
)

var started = time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)

func sampleResult(status backup.Status) backup.Result {
	return backup.Result{
		Status: status,
		Jobs: []backup.JobResult{{
			Key:          "dir:site",
			Status:       status,
			Duration:     65400 * time.Millisecond,
			BackupCmd:    "restic -r /repo backup /var/www",
			BackupOutput: "saved <snapshot> & done\n",
			ForgetCmd:    "restic -r /repo forget",
			ForgetOutput: "kept 2\n",
		}},
	}
}

func TestNew(t *testing.T) {
	for status, message := range map[backup.Status]string{
		backup.Success: StatusSuccess,
		backup.Warning: StatusWarning,
		backup.Failure: StatusFailure,
	} {
		r := New("server-1", started, sampleResult(status))
		if r.StatusMessage != message {
			t.Errorf("status %v: message %q, want %q", status, r.StatusMessage, message)
		}
		if r.HostID != "server-1" || r.Date != "Mon, 26 Oct 2026 03:00:00 UTC" || len(r.Commands) != 1 || r.Commands[0].CommandKey != "dir:site" {
			t.Errorf("unexpected report %+v", r)
		}
	}
}

type fakeLogger struct{ lines []string }

func (l *fakeLogger) Notice(m string) error {
	l.lines = append(l.lines, m)
	return nil
}

func TestLogAndPrint(t *testing.T) {
	r := New("server-1", started, sampleResult(backup.Warning))

	logger := &fakeLogger{}
	Log(logger, r)
	wantLog := []string{
		"Host ID: server-1",
		"Date: Mon, 26 Oct 2026 03:00:00 UTC",
		"Status: Backup completed with warnings",
		"Job dir:site: WARNING (1m5s)",
		"Command Key: dir:site",
		"Backup Command: restic -r /repo backup /var/www",
		"Backup Output: saved <snapshot> & done",
		"Forget Command: restic -r /repo forget",
		"Forget Output: kept 2",
	}
	if strings.Join(logger.lines, "\n") != strings.Join(wantLog, "\n") {
		t.Errorf("log lines:\n%s", strings.Join(logger.lines, "\n"))
	}

	var out strings.Builder
	Print(&out, r)
	if !strings.Contains(out.String(), "Status:"+reset+" "+yellow+StatusWarning+reset) {
		t.Errorf("warning status is not yellow:\n%q", out.String())
	}
	if !strings.Contains(out.String(), "  "+yellow+"WARNING"+reset+" dir:site (1m5s)\n") {
		t.Errorf("no summary line:\n%q", out.String())
	}
	if !strings.Contains(out.String(), "Backup Output:"+reset+" saved <snapshot> & done\n") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

// TestShippedTemplates renders the templates from the repository.
func TestShippedTemplates(t *testing.T) {
	r := New("server-1", started, sampleResult(backup.Failure))

	mail, err := RenderText("../../templates/mail_template.txt", "", r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mail, "Backup Report For server-1") || !strings.Contains(mail, "saved <snapshot> & done") || !strings.Contains(mail, StatusFailure) ||
		!strings.Contains(mail, "[server-1] BACKUP FAILED: dir:site\n") || !strings.Contains(mail, "\nFAILED  dir:site (1m5s)\n") {
		t.Errorf("mail:\n%s", mail)
	}

	for _, name := range []string{"matrix_template.html", "telegram_template.html"} {
		html, err := RenderHTML("../../templates/"+name, "chat", r)
		if err != nil {
			t.Fatal(err)
		}
		// command output is escaped in HTML templates
		if !strings.Contains(html, "❌ [server-1] BACKUP FAILED: dir:site") || !strings.Contains(html, "❌ <b>dir:site</b> FAILED (1m5s)") ||
			!strings.Contains(html, "saved &lt;snapshot&gt; &amp; done") {
			t.Errorf("%s:\n%s", name, html)
		}
	}
}

func TestRenderErrors(t *testing.T) {
	r := New("server-1", started, sampleResult(backup.Success))
	dir := t.TempDir()

	if _, err := RenderHTML(filepath.Join(dir, "missing.html"), "matrix", r); err == nil || !strings.HasPrefix(err.Error(), "parsing matrix template: ") {
		t.Errorf("missing template error = %v", err)
	}

	broken := filepath.Join(dir, "broken.txt")
	if err := os.WriteFile(broken, []byte("{{.NoSuchField}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderText(broken, "", r); err == nil || !strings.HasPrefix(err.Error(), "executing template: ") {
		t.Errorf("broken template error = %v", err)
	}
}

func TestTemplateLocations(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	want := []string{
		"templates/mail_template.txt",
		"/opt/resticara/templates/mail_template.txt",
		"/etc/resticara/templates/mail_template.txt",
		"/home/user/.config/resticara/mail_template.txt",
	}
	if got := TemplateLocations("/opt/resticara/config.ini", "mail_template.txt"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("TemplateLocations = %v", got)
	}
}

func TestSubjectAndStatuses(t *testing.T) {
	result := backup.Result{Status: backup.Failure, Jobs: []backup.JobResult{
		{Key: "dir:a", Status: backup.Success},
		{Key: "dir:b", Status: backup.Warning, Duration: 1500 * time.Millisecond},
		{Key: "mysql:c", Status: backup.Failure},
		{Key: "mysql:d", Status: backup.Failure},
	}}
	r := New("server-1", started, result)
	if r.Subject != "[server-1] BACKUP FAILED: mysql:c, mysql:d" || r.Status != "failure" || r.Kind != "Backup" {
		t.Errorf("Subject %q, Status %q, Kind %q", r.Subject, r.Status, r.Kind)
	}
	var got []string
	for _, cmd := range r.Commands {
		got = append(got, cmd.CommandKey+" "+cmd.Status+" "+cmd.StatusLabel+" "+cmd.Duration)
	}
	want := "dir:a success OK 0s|dir:b warning WARNING 2s|mysql:c failure FAILED 0s|mysql:d failure FAILED 0s"
	if strings.Join(got, "|") != want {
		t.Errorf("commands = %v", got)
	}

	result.Status = backup.Warning
	result.Jobs = result.Jobs[:2]
	if r := New("server-1", started, result); r.Subject != "[server-1] Backup completed with warnings: dir:b" || r.Status != "warning" {
		t.Errorf("warning: Subject %q, Status %q", r.Subject, r.Status)
	}
	result.Status = backup.Success
	result.Jobs = result.Jobs[:1]
	if r := New("server-1", started, result); r.Subject != "[server-1] Backup successful" || r.Status != "success" {
		t.Errorf("success: Subject %q, Status %q", r.Subject, r.Status)
	}
}

// TestOldTemplatesStillWork renders the templates shipped before the per-job fields
// existed, as installations with customized templates still have them.
func TestOldTemplatesStillWork(t *testing.T) {
	old := `{{if eq .StatusMessage "Backup successful"}}OK{{else}}NOT OK{{end}} {{.HostID}} {{.Date}}{{range .Commands}} {{.CommandKey}} {{.BackupCmd}} {{.BackupOutput}} {{.ForgetCmd}} {{.ForgetOutput}}{{end}}`
	path := filepath.Join(t.TempDir(), "old.html")
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := RenderHTML(path, "matrix", New("server-1", started, sampleResult(backup.Success)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(html, "OK server-1 Mon, 26 Oct 2026 03:00:00 UTC dir:site restic -r /repo backup /var/www") {
		t.Errorf("old template output: %s", html)
	}
}
