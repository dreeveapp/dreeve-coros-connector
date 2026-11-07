package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

func (s *syncer) boot() error {
	if s.dryRun {
		s.log.Info("dry run: nothing will be written to the watch folder or the ledger", "watch", s.paths.watch, "state", s.paths.state)
		return nil
	}
	for _, dir := range []string{s.paths.watch, s.paths.state} {
		if err := probeWritable(dir); err != nil {
			return err
		}
	}
	return s.removeStaleParts()
}

func (s *syncer) removeStaleParts() error {
	entries, err := os.ReadDir(s.paths.watch)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if stale, _ := filepath.Match(".coros_*.fit.part", e.Name()); !stale || e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(s.paths.watch, e.Name())); err != nil {
			return fmt.Errorf("remove stale partial file: %w", err)
		}
		s.log.Info("removed a partial file left by an interrupted delivery", "file", e.Name())
	}
	return nil
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return fmt.Errorf("%s is not writable by uid %d gid %d: %w; give the container's user write access to it", dir, os.Geteuid(), os.Getegid(), err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("%s is not writable: remove write probe: %w", dir, err)
	}
	return nil
}
