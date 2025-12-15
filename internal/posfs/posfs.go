package posfs

import (
	"errors"
	"os"
	"path/filepath"
)

const DefaultDirName = ".pos"

func DefaultPosDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultDirName), nil
}

func PosDirFromEnv() (string, error) {
	if v := os.Getenv("POS_DIR"); v != "" {
		return v, nil
	}
	return DefaultPosDir()
}

func EnsureDir(path string, perm os.FileMode) error {
	if path == "" {
		return errors.New("empty path")
	}
	return os.MkdirAll(path, perm)
}
