package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
)

func login(ctx context.Context, getenv Env, a *app) error {
	region, err := chooseRegion(getenv)
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

	result, err := client.Login(ctx, region.loginRegion(), email.value, password.value)
	if errors.Is(err, coros.ErrBadCredentials) {
		return region.badCredentials(email, password)
	}
	if err != nil {
		return err
	}
	a.secrets.Add(result.Token)
	regionID := region.home(result.RegionID)
	host, err := client.HomeHost(regionID)
	if err != nil {
		return region.unknownHost(result.RegionID, err)
	}
	if err := client.Session(host, result.Token).Verify(ctx); err != nil {
		if errors.Is(err, coros.ErrInvalidToken) {
			return region.tokenRejected(result.RegionID, host)
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

type regionChoice struct {
	name string
	id   int
}

func chooseRegion(getenv Env) (regionChoice, error) {
	name := getenv("COROS_REGION")
	if name == "" {
		return regionChoice{}, nil
	}
	id, err := coros.ParseRegion(name)
	if err != nil {
		return regionChoice{}, fmt.Errorf("COROS_REGION: %w", err)
	}
	return regionChoice{name: name, id: id}, nil
}

func (r regionChoice) forced() bool { return r.name != "" }

func (r regionChoice) loginRegion() int {
	if r.forced() {
		return r.id
	}
	return coros.LoginRegion
}

func (r regionChoice) home(reported int) int {
	if r.forced() {
		return r.id
	}
	return reported
}

func (r regionChoice) badCredentials(email, password credential) error {
	err := fmt.Errorf("COROS rejected the email or password (result %s), check %s and %s", coros.ResultBadCredentials, email.source, password.source)
	if r.forced() {
		return fmt.Errorf("%w, and that COROS_REGION=%s is the account's region", err, r.name)
	}
	return err
}

func (r regionChoice) unknownHost(reported int, err error) error {
	if r.forced() {
		return fmt.Errorf("COROS_REGION=%s: %w; nothing was saved", r.name, err)
	}
	return fmt.Errorf("COROS reported region %d, which this connector does not know: set COROS_REGION to one of %s; nothing was saved", reported, strings.Join(coros.RegionNames(), ", "))
}

func (r regionChoice) tokenRejected(reported int, host string) error {
	if r.forced() {
		return fmt.Errorf("%s rejected the new token (result %s): COROS_REGION=%s is not the account's region; nothing was saved", host, coros.ResultInvalidToken, r.name)
	}
	return fmt.Errorf("COROS reported region %d but %s rejected the new token (result %s): the account lives in another region, set COROS_REGION to it; nothing was saved", reported, host, coros.ResultInvalidToken)
}
