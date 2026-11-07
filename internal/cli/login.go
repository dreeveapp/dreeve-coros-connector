package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
)

func login(ctx context.Context, getenv Env, a *app) error {
	email, password := getenv("COROS_EMAIL"), getenv("COROS_PASSWORD")
	a.secrets.Add(password)
	a.secrets.Add(coros.PasswordHash(password))
	if email == "" || password == "" {
		return errors.New("set COROS_EMAIL and COROS_PASSWORD")
	}
	client, err := newClient(getenv)
	if err != nil {
		return err
	}
	p := loadPaths(getenv)

	result, err := client.Login(ctx, email, password)
	if errors.Is(err, coros.ErrBadCredentials) {
		return fmt.Errorf("COROS rejected the email or password (result %s), check COROS_EMAIL and COROS_PASSWORD", coros.ResultBadCredentials)
	}
	if err != nil {
		return err
	}
	a.secrets.Add(result.Token)
	host, err := client.HomeHost(result.RegionID)
	if err != nil {
		return fmt.Errorf("COROS reported region %d, which this connector does not know; nothing was saved", result.RegionID)
	}
	if err := client.Session(host, result.Token).Verify(ctx); err != nil {
		if errors.Is(err, coros.ErrInvalidToken) {
			return fmt.Errorf("COROS reported region %d but %s rejected the new token (result %s): the account lives in another region; nothing was saved", result.RegionID, host, coros.ResultInvalidToken)
		}
		return fmt.Errorf("verify token on %s: %w; nothing was saved", host, err)
	}

	tok := storedToken{Token: result.Token, Host: host, RegionID: result.RegionID, IssuedAt: time.Now().UTC().Truncate(time.Second)}
	if err := saveToken(p.tokens, a.umask.apply(0o600), tok); err != nil {
		return err
	}
	a.log.Info("logged in to COROS, token saved", "region", result.RegionID, "host", host, "file", filepath.Join(p.tokens, tokenFileName))
	a.log.Info("this login signed out every other COROS session of the account, including Training Hub")
	return nil
}
