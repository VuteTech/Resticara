package matrix

import (
	"fmt"

	"github.com/matrix-org/gomatrix"
)

type MatrixConfig struct {
	Homeserver string
	Username   string
	Password   string
	RoomID     string
	Message    string
}

type MatrixNotifier interface {
	Send(cfg MatrixConfig) error
}

type GomatrixNotifier struct{}

func (s GomatrixNotifier) Send(cfg MatrixConfig) error {
	cli, err := gomatrix.NewClient(cfg.Homeserver, "", "")
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	resp, err := cli.Login(&gomatrix.ReqLogin{
		Type:     "m.login.password",
		User:     cfg.Username,
		Password: cfg.Password,
	})
	if err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}

	cli.SetCredentials(resp.UserID, resp.AccessToken)

	if _, err := cli.JoinRoom(cfg.RoomID, "", nil); err != nil {
		// attempt to continue even if already joined
	}

	if _, err := cli.SendText(cfg.RoomID, cfg.Message); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}
