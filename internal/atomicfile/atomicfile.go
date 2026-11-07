package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Write(dir, tempName, finalName string, mode os.FileMode, r io.Reader) (err error) {
	tempPath := filepath.Join(dir, tempName)
	finalPath := filepath.Join(dir, finalName)

	f, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tempPath, err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tempPath)
		}
	}()

	if _, err = io.Copy(f, r); err != nil {
		return fmt.Errorf("write %s: %w", tempPath, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("fsync %s: %w", tempPath, err)
	}
	if err = f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tempPath, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tempPath, err)
	}
	if err = os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("rename %s: %w", finalPath, err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil && !errors.Is(syncErr, os.ErrInvalid) {
		return fmt.Errorf("fsync %s: %w", dir, syncErr)
	}
	return closeErr
}
