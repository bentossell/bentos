package posfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type ScaffoldOptions struct {
	Force bool
}

// ScaffoldFromFS copies embedded templates under the "pos/" prefix into posDir.
func ScaffoldFromFS(posDir string, templateFS fs.FS, opts ScaffoldOptions) error {
	return fs.WalkDir(templateFS, "pos", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "pos")
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		if rel == "" {
			return nil
		}
		dst := filepath.Join(posDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}

		if !opts.Force {
			if _, statErr := os.Stat(dst); statErr == nil {
				return nil
			}
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}

		srcFile, err := templateFS.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		data, err := io.ReadAll(srcFile)
		if err != nil {
			return err
		}

		perm := os.FileMode(0o644)
		if filepath.Base(dst) == "CACHE.db" {
			perm = 0o600
		}
		return os.WriteFile(dst, data, perm)
	})
}
