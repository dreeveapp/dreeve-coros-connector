package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/atomicfile"
)

const tokenFileName = "token.json"

type storedToken struct {
	Token    string    `json:"token"`
	Host     string    `json:"host"`
	RegionID int       `json:"regionId"`
	IssuedAt time.Time `json:"issuedAt"`
}

var errNotLoggedIn = errors.New("not logged in")

func saveToken(dir string, tok storedToken) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(dir, "."+tokenFileName+".tmp", tokenFileName, 0o600, bytes.NewReader(append(data, '\n')))
}

func loadToken(dir string) (storedToken, error) {
	path := filepath.Join(dir, tokenFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return storedToken{}, fmt.Errorf("%w: no COROS token in %s, run `login` first", errNotLoggedIn, dir)
	}
	if err != nil {
		return storedToken{}, fmt.Errorf("read token %s: %w", path, err)
	}
	var tok storedToken
	if err := json.Unmarshal(data, &tok); err != nil || tok.Token == "" || tok.Host == "" {
		return storedToken{}, fmt.Errorf("token file %s is unreadable, run `login` again", path)
	}
	return tok, nil
}
