//go:build integration

package integration

import (
	"os"
	"testing"
)

func TestUsage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	config := e.writeConfig(baseConfig)

	e.resticara().expect(t, 2, "Usage of resticara:")
	e.resticara("--config="+config, "unknown").expect(t, 2, "Usage of resticara:")
}

func TestConfigErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	e.resticara("--config="+e.path("missing.ini"), "run").expect(t, 1, "Error reading config")

	config := e.writeConfig(baseConfig + `[pgsql:main]
bucket = /tmp/repo
retention_daily = 1
retention_weekly = 1
retention_monthly = 1
`)
	e.resticara("--config="+config, "run").expect(t, 1, `unsupported backup type "pgsql"`)

	config = e.writeConfig(baseConfig + `[dir:site]
bucket = /tmp/repo
directory = /tmp
retention_daily = two
retention_weekly = 1
retention_monthly = 1
`)
	e.resticara("--config="+config, "run").expect(t, 1, "'retention_daily' for dir:site must be an integer")
}

func TestWarnsAboutReadableConfig(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	config := e.writeConfig(baseConfig)
	if err := os.Chmod(config, 0o644); err != nil {
		t.Fatal(err)
	}

	e.resticara("--config="+config, "prune", "all").expect(t, 0, "contains credentials but has mode 0644")
}

// TestVersion needs neither a config nor syslog.
func TestVersion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, args := range [][]string{{"--version"}, {"version"}, {"--config=" + e.path("missing.ini"), "--version"}} {
		res := e.resticara(args...)
		res.expect(t, 0, "resticara dev")
	}
}
