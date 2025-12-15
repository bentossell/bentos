package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ScriptEvent struct {
	Type        string         `json:"type"`
	Message     string         `json:"message,omitempty"`
	Pct         float64        `json:"pct,omitempty"`
	OK          *bool          `json:"ok,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
	Path        string         `json:"path,omitempty"`
	Description string         `json:"description,omitempty"`
}

type RunOptions struct {
	PosDir string
	Stdin  []byte
	Cwd    string
}

func Run(ctx context.Context, scriptPath string, opts RunOptions, onEvent func(ScriptEvent)) error {
	if opts.PosDir == "" {
		return errors.New("missing PosDir")
	}

	cmd, err := commandForScript(scriptPath)
	if err != nil {
		return err
	}
	cmd = append(cmd, scriptPath)

	execCmd := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	if opts.Cwd != "" {
		execCmd.Dir = opts.Cwd
	} else {
		execCmd.Dir = filepath.Dir(scriptPath)
	}
	execCmd.Env = append(os.Environ(), "POS_DIR="+opts.PosDir)

	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		return err
	}

	if len(opts.Stdin) > 0 {
		execCmd.Stdin = bytes.NewReader(opts.Stdin)
	}

	if err := execCmd.Start(); err != nil {
		return err
	}

	stderrBuf := &bytes.Buffer{}
	go func() {
		_, _ = io.Copy(stderrBuf, stderr)
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev ScriptEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
			onEvent(ScriptEvent{Type: "progress", Message: line})
			continue
		}
		onEvent(ev)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	if err := execCmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("script failed: %s", msg)
	}
	return nil
}

func commandForScript(path string) ([]string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".py":
		return []string{"python3"}, nil
	case ".js":
		return []string{"node"}, nil
	default:
		return nil, fmt.Errorf("unsupported script type: %s", ext)
	}
}
