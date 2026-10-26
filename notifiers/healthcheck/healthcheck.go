// Package healthcheck pings a Healthchecks (healthchecks.io or self-hosted) check
// when a run starts and when it ends, so missing or failed runs raise an alert.
package healthcheck

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxBody is how much of the request body Healthchecks stores.
const MaxBody = 100_000

// Pinger signals the start and the end of one run.
type Pinger struct {
	// URL is the check's ping URL, for example https://hc-ping.com/<uuid>.
	URL string
	// RunID ties the start and end pings together, so the duration is measured.
	RunID      string
	HTTPClient *http.Client
	// Attempts is how often a ping is tried; RetryDelay is the wait after the first
	// failed attempt and doubles after each further one.
	Attempts   int
	RetryDelay time.Duration
}

// New returns a pinger for the check URL with a fresh run ID.
func New(checkURL string) *Pinger {
	return &Pinger{
		URL:        checkURL,
		RunID:      newUUID(),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Attempts:   3,
		RetryDelay: 2 * time.Second,
	}
}

// Start signals that the run started.
func (p *Pinger) Start() error {
	return p.ping(http.MethodGet, "start", "")
}

// Finish signals the end of the run with its exit code (0 is success) and attaches
// the report, cut to what Healthchecks stores.
func (p *Pinger) Finish(exitCode int, report string) error {
	return p.ping(http.MethodPost, strconv.Itoa(exitCode), truncate(report, MaxBody))
}

func (p *Pinger) ping(method, signal, body string) error {
	target, err := p.endpoint(signal)
	if err != nil {
		return err
	}

	delay := p.RetryDelay
	for attempt := 1; ; attempt++ {
		retry, err := p.send(method, target, body)
		if err == nil {
			return nil
		}
		if !retry || attempt >= p.Attempts {
			return fmt.Errorf("healthcheck %s ping failed: %w", signal, err)
		}
		time.Sleep(delay)
		delay *= 2
	}
}

// endpoint appends the signal to the URL path and adds the run ID, keeping any
// query the URL already has (such as create=1 for slug URLs).
func (p *Pinger) endpoint(signal string) (string, error) {
	u, err := url.Parse(p.URL)
	if err != nil {
		return "", fmt.Errorf("invalid healthcheck URL: %w", err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + signal
	q := u.Query()
	q.Set("rid", p.RunID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// send makes one request and says whether a failure is worth retrying.
func (p *Pinger) send(method, target, body string) (retry bool, err error) {
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		return false, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	answer := strings.TrimSpace(string(text))

	switch {
	case resp.StatusCode >= 500:
		return true, fmt.Errorf("HTTP %d %s", resp.StatusCode, answer)
	case resp.StatusCode >= 300:
		return false, fmt.Errorf("HTTP %d %s", resp.StatusCode, answer)
	// Healthchecks answers unknown checks and rate limiting with 200 and a note
	case strings.Contains(answer, "not found"):
		return false, fmt.Errorf("no check found for this URL (%s)", answer)
	case strings.Contains(answer, "rate limited"):
		return false, fmt.Errorf("rate limited (%s)", answer)
	}
	return false, nil
}

// truncate cuts s to at most limit bytes without splitting a UTF-8 character.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	// drop the last character if the cut went through it
	for i := len(s) - 1; i >= 0 && i >= len(s)-utf8.UTFMax; i-- {
		if utf8.RuneStart(s[i]) {
			if !utf8.FullRuneInString(s[i:]) {
				s = s[:i]
			}
			break
		}
	}
	return s
}

// newUUID returns a random (version 4) UUID.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
