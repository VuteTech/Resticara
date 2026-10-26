package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"resticara/notifiers/htmltext"
)

type MatrixConfig struct {
	Homeserver  string
	Username    string
	Password    string
	AccessToken string
	RoomID      string
	Message     string
}

type MatrixNotifier interface {
	Send(cfg MatrixConfig) error
}

// ClientAPINotifier sends messages through the Matrix client-server API.
type ClientAPINotifier struct {
	HTTPClient *http.Client
}

type client struct {
	http  *http.Client
	base  string
	token string
}

func (s ClientAPINotifier) Send(cfg MatrixConfig) error {
	c := client{
		http:  s.HTTPClient,
		base:  strings.TrimRight(cfg.Homeserver, "/") + "/_matrix/client/v3",
		token: cfg.AccessToken,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}

	if c.token == "" {
		login := map[string]any{
			"type":       "m.login.password",
			"identifier": map[string]string{"type": "m.id.user", "user": cfg.Username},
			"password":   cfg.Password,
		}
		var resp struct {
			AccessToken string `json:"access_token"`
		}
		if err := c.do(http.MethodPost, "/login", login, &resp); err != nil {
			return fmt.Errorf("failed to login: %w", err)
		}
		c.token = resp.AccessToken
		// log out again, otherwise every run leaves a new device on the account; the
		// message is already sent or failed, so a failed logout changes nothing
		defer func() { _ = c.do(http.MethodPost, "/logout", struct{}{}, nil) }()
	}

	room := url.PathEscape(cfg.RoomID)
	// joining fails harmlessly when the bot is already a member; a real problem shows up when sending
	joinErr := c.do(http.MethodPost, "/join/"+room, struct{}{}, nil)

	msg := map[string]string{
		"msgtype":        "m.text",
		"body":           htmltext.PlainText(cfg.Message),
		"format":         "org.matrix.custom.html",
		"formatted_body": cfg.Message,
	}
	txnID := fmt.Sprintf("resticara-%d", time.Now().UnixNano())
	if err := c.do(http.MethodPut, "/rooms/"+room+"/send/m.room.message/"+txnID, msg, nil); err != nil {
		if joinErr != nil {
			return fmt.Errorf("failed to send message: %w (joining the room failed too: %v)", err, joinErr)
		}
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}

func (c client) do(method, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var apiErr struct {
			ErrCode string `json:"errcode"`
			Error   string `json:"error"`
		}
		// best effort: proxies may answer errors with something other than JSON
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return fmt.Errorf("%s %s: HTTP %d %s %s", method, path, resp.StatusCode, apiErr.ErrCode, apiErr.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
