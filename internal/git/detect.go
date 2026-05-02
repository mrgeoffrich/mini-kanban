package git

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrNotARepo = errors.New("not inside a git repository")

type Info struct {
	Root      string // absolute path
	Name      string // basename of root
	RemoteURL string // empty if no origin configured
}

// Detect finds the git repo containing dir (or any of its parents).
func Detect(dir string) (*Info, error) {
	root, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNotARepo
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info := &Info{Root: abs, Name: filepath.Base(abs)}
	if remote, err := run(dir, "remote", "get-url", "origin"); err == nil {
		info.RemoteURL = remote
	}
	return info, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
