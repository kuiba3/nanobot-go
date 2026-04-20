// Package gitstore is a thin wrapper around `git` CLI to version-control
// memory files under the workspace. We shell out to git rather than embed
// go-git to keep dependencies minimal. Operations degrade to no-ops when
// git is unavailable.
package gitstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Store wraps a workspace-scoped git repo.
type Store struct {
	dir string
}

// New constructs a Store rooted at workspace.
func New(workspace string) *Store { return &Store{dir: workspace} }

// Available reports whether `git` is on PATH.
func (s *Store) Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// EnsureInit initializes the repo with a conservative .gitignore that only
// tracks the given paths.
func (s *Store) EnsureInit(ctx context.Context, tracked []string) error {
	if !s.Available() {
		return errors.New("git unavailable")
	}
	gitDir := filepath.Join(s.dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if out, err := s.run(ctx, "init", "-b", "main"); err != nil {
			return fmt.Errorf("git init: %w: %s", err, out)
		}
	}
	// Force-include only tracked paths.
	lines := []string{"*"}
	for _, p := range tracked {
		lines = append(lines, "!"+p)
	}
	ignore := strings.Join(lines, "\n") + "\n"
	_ = write(filepath.Join(s.dir, ".gitignore"), ignore)
	return nil
}

// AutoCommit adds tracked paths and commits with the given message.
// Returns (committed, err). `committed=false` when there was nothing to commit.
func (s *Store) AutoCommit(ctx context.Context, msg string) (bool, error) {
	if !s.Available() {
		return false, errors.New("git unavailable")
	}
	if _, err := s.run(ctx, "add", "-A"); err != nil {
		return false, err
	}
	if out, err := s.run(ctx, "diff", "--cached", "--quiet"); err == nil {
		_ = out
		return false, nil // nothing staged
	}
	if out, err := s.run(ctx, "commit", "-m", msg); err != nil {
		return false, fmt.Errorf("commit: %w: %s", err, out)
	}
	return true, nil
}

// Log returns the most recent n commits as "sha subject" lines.
func (s *Store) Log(ctx context.Context, n int) (string, error) {
	if !s.Available() {
		return "", errors.New("git unavailable")
	}
	if n <= 0 {
		n = 20
	}
	out, err := s.run(ctx, "log", "--pretty=format:%h %s", fmt.Sprintf("-n%d", n))
	return out, err
}

// Revert rolls the tracked files back to the given commit.
func (s *Store) Revert(ctx context.Context, sha string) error {
	if !s.Available() {
		return errors.New("git unavailable")
	}
	_, err := s.run(ctx, "checkout", sha, "--", ".")
	return err
}

func (s *Store) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = s.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func write(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
