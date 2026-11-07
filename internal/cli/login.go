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
	override, err := regionOverride(getenv)
	if err != nil {
		return err
	}
	email, password, err := credentials(ctx, getenv, a)
	if err != nil {
		return err
	}
	client, err := newClient(getenv)
	if err != nil {
		return err
	}
	p := loadPaths(getenv)

	loginRegion := coros.LoginRegion
	if override != 0 {
		loginRegion = override
	}
	result, err := client.Login(ctx, loginRegion, email.value, password.value)
	if errors.Is(err, coros.ErrBadCredentials) {
		return fmt.Errorf("COROS rejected the email or password (result %s), check %s and %s", coros.ResultBadCredentials, email.source, password.source)
	}
	if err != nil {
		return err
	}
	a.secrets.Add(result.Token)
	regionID := result.RegionID
	if override != 0 {
		regionID = override
	}
	host, err := client.HomeHost(regionID)
	if err != nil {
		if override != 0 {
			return fmt.Errorf("COROS_REGION=%s: %w; nothing was saved", getenv("COROS_REGION"), err)
		}
		return fmt.Errorf("COROS reported region %d, which this connector does not know: set COROS_REGION to us, eu or cn; nothing was saved", result.RegionID)
	}
	if err := client.Session(host, result.Token).Verify(ctx); err != nil {
		if errors.Is(err, coros.ErrInvalidToken) && override != 0 {
			return fmt.Errorf("%s rejected the new token (result %s): COROS_REGION=%s is not the account's region; nothing was saved", host, coros.ResultInvalidToken, getenv("COROS_REGION"))
		}
		if errors.Is(err, coros.ErrInvalidToken) {
			return fmt.Errorf("COROS reported region %d but %s rejected the new token (result %s): the account lives in another region, set COROS_REGION to it; nothing was saved", result.RegionID, host, coros.ResultInvalidToken)
		}
		return fmt.Errorf("verify token on %s: %w; nothing was saved", host, err)
	}

	tok := storedToken{Token: result.Token, Host: host, RegionID: regionID, IssuedAt: time.Now().UTC().Truncate(time.Second)}
	if err := saveToken(p.tokens, a.umask.apply(0o600), tok); err != nil {
		return err
	}
	a.log.Info("logged in to COROS, token saved", "region", regionID, "host", host, "file", filepath.Join(p.tokens, tokenFileName))
	a.log.Info("this login signed out every other COROS session of the account, including Training Hub")
	return nil
}

func regionOverride(getenv Env) (int, error) {
	name := getenv("COROS_REGION")
	if name == "" {
		return 0, nil
	}
	id, err := coros.ParseRegion(name)
	if err != nil {
		return 0, fmt.Errorf("COROS_REGION: %w", err)
	}
	return id, nil
}

func credentials(ctx context.Context, getenv Env, a *app) (email, password credential, err error) {
	if email, err = credentialFromEnv(getenv, "COROS_EMAIL"); err != nil {
		return email, password, err
	}
	password, err = credentialFromEnv(getenv, "COROS_PASSWORD")
	a.addPassword(password.value)
	if err != nil {
		return email, password, err
	}
	if a.prompt == nil {
		var missing []string
		if email.value == "" {
			missing = append(missing, "COROS_EMAIL")
		}
		if password.value == "" {
			missing = append(missing, "COROS_PASSWORD")
		}
		if len(missing) > 0 {
			return email, password, missingCredentials(missing...)
		}
		return email, password, nil
	}
	if email.value == "" {
		if email, err = a.prompt.ask(ctx, "Email", false); err != nil {
			return email, password, err
		}
	}
	if password.value == "" {
		password, err = a.prompt.ask(ctx, "Password", true)
		a.addPassword(password.value)
	}
	return email, password, err
}

func (a *app) addPassword(password string) {
	if password == "" {
		return
	}
	a.secrets.Add(password)
	a.secrets.Add(coros.PasswordHash(password))
}
