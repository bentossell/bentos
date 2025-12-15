package skills

import (
	"fmt"
	"path/filepath"
)

type Manager struct {
	PosDir string
}

func (m Manager) ScriptPath(surface string, kind string) (string, error) {
	var name string
	switch kind {
	case "sync", "propose", "apply":
		name = kind + ".py"
	default:
		return "", fmt.Errorf("unknown script kind: %s", kind)
	}
	return filepath.Join(m.PosDir, "skills", surface, "scripts", name), nil
}
