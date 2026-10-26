package systemd

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"resticara/internal/config"
)

var update = flag.Bool("update", false, "rewrite the golden unit files in testdata")

// TestNewPlanGolden compares the rendered units with the files in testdata.
// Changing a unit means updating them on purpose with go test -update and
// reviewing the diff.
func TestNewPlanGolden(t *testing.T) {
	cfg, err := config.Load("testdata/config.ini")
	if err != nil {
		t.Fatal(err)
	}
	plan := NewPlan(cfg, "/usr/local/bin/resticara", "/etc/resticara/config.ini")

	if *update {
		old, _ := filepath.Glob("testdata/resticara-*")
		for _, f := range old {
			os.Remove(f)
		}
		for _, unit := range plan.Units {
			if err := os.WriteFile(filepath.Join("testdata", unit.Name), []byte(unit.Content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	var names []string
	for _, unit := range plan.Units {
		names = append(names, unit.Name)
		want, err := os.ReadFile(filepath.Join("testdata", unit.Name))
		if err != nil {
			t.Errorf("unexpected unit %s", unit.Name)
			continue
		}
		if unit.Content != string(want) {
			t.Errorf("%s differs from testdata:\n--- got\n%s\n--- want\n%s", unit.Name, unit.Content, want)
		}
	}

	golden, err := filepath.Glob("testdata/resticara-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(golden) != len(plan.Units) {
		t.Errorf("rendered %v, testdata has %v", names, golden)
	}

	wantTimers := []string{"resticara-backup.timer", "resticara-prune-b2-example-bucket-mysql-98315465.timer", "resticara-prune-b2-example-bucket-website-f9d2ac99.timer"}
	if strings.Join(plan.Timers, " ") != strings.Join(wantTimers, " ") {
		t.Errorf("Timers = %v", plan.Timers)
	}
}

type fakeSystemctl struct {
	unitPath string
	calls    []string
	fail     string // a call that returns an error
}

func (f *fakeSystemctl) Output(args ...string) ([]byte, error) {
	return []byte("UnitPath=" + f.unitPath + "\n"), nil
}

func (f *fakeSystemctl) Run(args ...string) error {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if call == f.fail {
		return errors.New("exit status 1")
	}
	return nil
}

func TestUnitDir(t *testing.T) {
	dir := t.TempDir()
	got, err := UnitDir(&fakeSystemctl{unitPath: "/nonexistent/one " + dir + ":/nonexistent/two"})
	if err != nil || got != dir {
		t.Errorf("UnitDir = %q, %v; want %q", got, err, dir)
	}

	if _, err := UnitDir(&fakeSystemctl{unitPath: "/nonexistent"}); err == nil || err.Error() != "could not determine systemd unit directory from UnitPath: /nonexistent" {
		t.Errorf("error = %v", err)
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"resticara-old.service", "resticara-old.timer", "resticara-backup.timer", "resticara-other.conf", "unrelated.service"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := Plan{
		Units:  []Unit{{Name: "resticara-backup.service", Content: "service"}, {Name: "resticara-backup.timer", Content: "timer"}},
		Timers: []string{"resticara-backup.timer"},
	}
	sys := &fakeSystemctl{}

	if err := Apply(sys, dir, plan); err != nil {
		t.Fatal(err)
	}

	wantCalls := []string{
		"disable --now resticara-old.timer",
		"disable --now resticara-old.service",
		"daemon-reload",
		"enable resticara-backup.timer",
		"restart resticara-backup.timer",
	}
	if strings.Join(sys.calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Errorf("calls:\n%s", strings.Join(sys.calls, "\n"))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	if want := "resticara-backup.service resticara-backup.timer resticara-other.conf unrelated.service"; strings.Join(files, " ") != want {
		t.Errorf("files = %v, want %s", files, want)
	}
	if content, _ := os.ReadFile(filepath.Join(dir, "resticara-backup.timer")); string(content) != "timer" {
		t.Errorf("timer content = %q", content)
	}
}

func TestApplyErrors(t *testing.T) {
	plan := Plan{Units: []Unit{{Name: "resticara-backup.timer"}}, Timers: []string{"resticara-backup.timer"}}
	for call, want := range map[string]string{
		"daemon-reload":                  "failed to reload systemd daemon: exit status 1",
		"enable resticara-backup.timer":  "failed to enable resticara-backup.timer: exit status 1",
		"restart resticara-backup.timer": "failed to restart resticara-backup.timer: exit status 1",
	} {
		err := Apply(&fakeSystemctl{fail: call}, t.TempDir(), plan)
		if err == nil || err.Error() != want {
			t.Errorf("failing %q: error = %v, want %q", call, err, want)
		}
	}
}

func TestGenerate(t *testing.T) {
	unitDir := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.ini")
	cfg := config.Config{Jobs: []config.Job{{Key: "dir:a", Repository: "/repo", PruneDays: 7}}}

	var out strings.Builder
	if err := Generate(&fakeSystemctl{unitPath: unitDir}, cfg, "/usr/bin/resticara", configPath, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Systemd timer files written to "+unitDir+" and activated.") ||
		!strings.Contains(out.String(), filepath.Join(configDir, EnvFile)+" not found") {
		t.Errorf("output = %q", out.String())
	}
	content, err := os.ReadFile(filepath.Join(unitDir, pruneUnitBase("/repo")+".service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "ExecStart=/usr/bin/resticara --config="+configPath+" prune /repo\n") {
		t.Errorf("prune service:\n%s", content)
	}

	// an env file readable by other users gets a warning instead
	if err := os.WriteFile(filepath.Join(configDir, EnvFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Generate(&fakeSystemctl{unitPath: unitDir}, cfg, "/usr/bin/resticara", configPath, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "contains credentials but has mode 0644") {
		t.Errorf("output = %q", out.String())
	}
}

func TestPlanSharedRepository(t *testing.T) {
	cfg := config.Config{Jobs: []config.Job{
		{Key: "dir:a", Repository: "/srv/repo", PruneDays: 30},
		{Key: "mysql:b", Repository: "/srv/repo", PruneDays: 7},
	}}
	plan := NewPlan(cfg, "/usr/bin/resticara", "/etc/resticara/config.ini")

	// one prune per repository, as often as the most frequent job asks for
	base := pruneUnitBase("/srv/repo")
	if len(plan.Units) != 4 || strings.Join(plan.Timers, " ") != "resticara-backup.timer "+base+".timer" {
		t.Fatalf("units %d, timers %v", len(plan.Units), plan.Timers)
	}
	if !strings.Contains(plan.Units[3].Content, "OnUnitActiveSec=7d\n") {
		t.Errorf("prune timer:\n%s", plan.Units[3].Content)
	}
}

func TestPruneUnitBase(t *testing.T) {
	tests := map[string]string{
		"b2:example-bucket:website/":        "resticara-prune-b2-example-bucket-website-",
		"/srv/restic repo":                  "resticara-prune-srv-restic-repo-",
		"s3:https://s3.example.com/a":       "resticara-prune-s3-https-s3-example-com-a-",
		"local:/" + strings.Repeat("x", 60): "resticara-prune-local-" + strings.Repeat("x", 34) + "-",
	}
	for url, prefix := range tests {
		got := pruneUnitBase(url)
		if !strings.HasPrefix(got, prefix) || len(got) != len(prefix)+8 {
			t.Errorf("pruneUnitBase(%q) = %q, want %s<hash>", url, got, prefix)
		}
	}
	// URLs that only differ in punctuation still get their own units
	if pruneUnitBase("/srv/repo-a") == pruneUnitBase("/srv/repo/a") {
		t.Error("different repositories share a unit name")
	}
}

func TestExecArg(t *testing.T) {
	tests := map[string]string{
		"b2:example-bucket:website/":         "b2:example-bucket:website/",
		"--config=/etc/resticara/config.ini": "--config=/etc/resticara/config.ini",
		"/srv/restic repo":                   `"/srv/restic repo"`,
		"/srv/50%":                           "/srv/50%%",
		"/srv/$HOME":                         "/srv/$$HOME",
		`/srv/say "hi"\x`:                    `"/srv/say \"hi\"\\x"`,
	}
	for arg, want := range tests {
		if got := execArg(arg); got != want {
			t.Errorf("execArg(%q) = %s, want %s", arg, got, want)
		}
	}
}
