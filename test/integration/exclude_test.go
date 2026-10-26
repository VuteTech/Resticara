//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestExcludes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	site := e.path("site")

	e.writeFile("<?php", "site", "index.php")
	e.writeFile("upload", "site", "wp-content", "uploads", "photo.jpg")
	e.writeFile("cached page", "site", "wp-content", "cache", "page.html")
	e.writeFile("temporary", "site", "build.tmp")
	e.writeFile("keep me", "site", "important.tmp")
	e.writeFile("Signature: 8a477f597d28d172789f06886806bc55\n", "site", "thumbnails", "CACHEDIR.TAG")
	e.writeFile("thumbnail", "site", "thumbnails", "small.jpg")
	e.writeFile("", "site", "node_modules", ".nobackup")
	e.writeFile("module", "site", "node_modules", "lib.js")
	e.writeFile("secret", "site", "secret.txt")
	excludeFile := e.writeFile(site+"/secret.txt\n", "site.exclude")

	config := e.writeConfig(`[general]
hostID = hostname
exclude = *.tmp
exclude = !important.tmp
exclude_caches = true

[smtp]
enabled = false

[dir:site]
bucket = ` + repo + `
directory = ` + site + `
exclude = wp-content/cache
exclude_file = ` + excludeFile + `
exclude_if_present = .nobackup
retention_daily = 2
retention_weekly = 7
retention_monthly = 3
`)

	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful", "'--exclude=*.tmp'", "--exclude=wp-content/cache")

	files := make(map[string]bool)
	for _, line := range strings.Split(e.restic("ls", "-r", repo, "latest"), "\n") {
		if strings.HasPrefix(line, site) {
			files[strings.TrimPrefix(line, site)] = true
		}
	}
	for _, kept := range []string{"/index.php", "/wp-content/uploads/photo.jpg", "/important.tmp"} {
		if !files[kept] {
			t.Errorf("%s is missing from the snapshot", kept)
		}
	}
	for _, excluded := range []string{"/wp-content/cache/page.html", "/build.tmp", "/thumbnails/small.jpg", "/node_modules/lib.js", "/secret.txt"} {
		if files[excluded] {
			t.Errorf("%s should have been excluded", excluded)
		}
	}
	if t.Failed() {
		t.Logf("snapshot contains: %v", files)
	}
}

func TestExcludeFileMissing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	missing := e.path("missing.exclude")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")) + "exclude_file = " + missing + "\n")

	// the warning names the cause before restic fails on the missing file
	e.resticara("--config="+config, "run").expect(t, 1,
		"Warning: exclude file \""+missing+"\" of dir:site does not exist",
		"Status: BACKUP FAILED!",
	)
}
