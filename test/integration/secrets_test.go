//go:build integration

package integration

import (
	"bufio"
	"encoding/base64"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
)

// environWithout returns the test environment without the named variables.
func (e *env) environWithout(names ...string) []string {
	var out []string
	for _, kv := range e.environ() {
		keep := true
		for _, name := range names {
			keep = keep && !strings.HasPrefix(kv, name+"=")
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

func (e *env) writeEnvFile(content string) {
	e.t.Helper()
	if err := os.WriteFile(e.path("restic.env"), []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// TestEnvFileForManualRuns checks that a manual run finds the restic password in
// restic.env, as the systemd units do.
func TestEnvFileForManualRuns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	config := e.writeConfig(baseConfig + dirJob("site", repo, e.path("data")))
	noPassword := e.environWithout("RESTIC_PASSWORD")

	// without the file, restic has no password
	e.resticaraWithEnv(noPassword, "--config="+config, "run").expect(t, 1, "Status: BACKUP FAILED!")

	e.writeEnvFile("# restic credentials\nRESTIC_PASSWORD=\"" + resticPassword + "\"\n")
	e.resticaraWithEnv(noPassword, "--config="+config, "run").expect(t, 0, "Status: Backup successful")

	// a variable that is already set wins over the file
	e.writeEnvFile("RESTIC_PASSWORD=wrong\n")
	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful")
}

func TestEnvFileWarnings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	config := e.writeConfig(baseConfig)
	e.writeEnvFile("RESTIC_PASSWORD=x\nthis is not a variable\n")
	if err := os.Chmod(e.path("restic.env"), 0o644); err != nil {
		t.Fatal(err)
	}

	e.resticara("--config="+config, "validate").expect(t, 0,
		e.path("restic.env")+" contains credentials but has mode 0644",
		"Warning: line 2 of "+e.path("restic.env")+" is not KEY=VALUE and is ignored",
	)
}

// smtpServer is a minimal SMTP server that accepts AUTH PLAIN and records messages.
type smtpServer struct {
	addr string
	mu   sync.Mutex
	auth []string // "user:password"
	rcpt []string
	data []string
}

func newSMTPServer(t *testing.T) *smtpServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &smtpServer{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *smtpServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(line string) { conn.Write([]byte(line + "\r\n")) }
	reply("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-test")
			reply("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH PLAIN "):
			// base64 of "\x00user\x00password"
			raw, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			parts := strings.Split(string(raw), "\x00")
			s.mu.Lock()
			s.auth = append(s.auth, parts[len(parts)-2]+":"+parts[len(parts)-1])
			s.mu.Unlock()
			reply("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			reply("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			s.mu.Lock()
			s.rcpt = append(s.rcpt, strings.Trim(line[len("RCPT TO:"):], "<> "))
			s.mu.Unlock()
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.data = append(s.data, b.String())
			s.mu.Unlock()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// TestEmailToSeveralRecipients sends a real report email with the password taken
// from restic.env.
func TestEmailToSeveralRecipients(t *testing.T) {
	t.Parallel()
	smtp := newSMTPServer(t)
	host, port, _ := net.SplitHostPort(smtp.addr)
	e := newEnv(t)
	repo := e.initRepo("repo")
	e.writeFile("x", "data", "file.txt")
	e.writeEnvFile("SMTP_PASSWORD=mail-secret\n")
	config := e.writeConfig(`[general]
hostID = server-1

[smtp]
server = ` + host + `
port = ` + port + `
from = backup@example.com
username = backup@example.com
to = admin@example.com, ops@example.com

` + dirJob("site", repo, e.path("data")))

	e.resticara("--config="+config, "run").expect(t, 0, "Status: Backup successful", "Email sent!")

	smtp.mu.Lock()
	defer smtp.mu.Unlock()
	if strings.Join(smtp.auth, " ") != "backup@example.com:mail-secret" {
		t.Errorf("auth = %q", smtp.auth)
	}
	if strings.Join(smtp.rcpt, " ") != "admin@example.com ops@example.com" {
		t.Errorf("recipients = %q", smtp.rcpt)
	}
	if len(smtp.data) != 1 || !strings.Contains(smtp.data[0], "Subject: [server-1] Backup successful\r\n") ||
		!strings.Contains(smtp.data[0], "To: admin@example.com, ops@example.com\r\n") || !strings.Contains(smtp.data[0], "OK      dir:site") {
		t.Errorf("message:\n%s", strings.Join(smtp.data, "\n---\n"))
	}
}
