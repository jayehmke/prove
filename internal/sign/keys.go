package sign

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jayehmke/prove/internal/gitx"
)

// DefaultKeyPath is the key prove makes for this machine when none is
// configured: $XDG_CONFIG_HOME/prove/signing_key, or ~/.config/prove/.
func DefaultKeyPath(getenv func(string) string) (string, error) {
	if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "prove", "signing_key"), nil
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "prove", "signing_key"), nil
	}
	return "", errors.New("can't find a home for the signing key: set HOME, XDG_CONFIG_HOME or PROVE_SIGNING_KEY")
}

// KeySpec finds the signing key to use, in order: the --key flag,
// $PROVE_SIGNING_KEY, git's SSH signing key (gpg.format=ssh and
// user.signingkey), and the default key. It reports whether the result is
// the default key.
func KeySpec(getenv func(string) string, repo *gitx.Repo, flag string) (spec string, isDefault bool, err error) {
	if flag != "" {
		return expandHome(getenv, flag), false, nil
	}
	if k := getenv("PROVE_SIGNING_KEY"); k != "" {
		return expandHome(getenv, k), false, nil
	}
	if repo != nil {
		if format, _ := repo.Run("config", "gpg.format"); format == "ssh" {
			if k, _ := repo.Run("config", "user.signingkey"); k != "" {
				return expandHome(getenv, k), false, nil
			}
		}
	}
	p, err := DefaultKeyPath(getenv)
	return p, true, err
}

func expandHome(getenv func(string) string, p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(getenv("HOME"), rest)
	}
	return p
}

// LoadOrCreate loads the signing key KeySpec finds, making the default key
// on first use. created is the path of a key it just made, or "".
func LoadOrCreate(getenv func(string) string, repo *gitx.Repo, flag string) (s *SSH, created string, err error) {
	spec, isDefault, err := KeySpec(getenv, repo, flag)
	if err != nil {
		return nil, "", err
	}
	if isDefault {
		if _, err := os.Stat(spec); errors.Is(err, os.ErrNotExist) {
			if err := GenerateKey(spec); err != nil {
				return nil, "", fmt.Errorf("making a signing key at %s: %v", spec, err)
			}
			created = spec
		}
	}
	s, err = LoadSSH(spec)
	return s, created, err
}
