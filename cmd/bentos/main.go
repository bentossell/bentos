package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bentossell/bentos/internal/assets"
	"github.com/bentossell/bentos/internal/cache"
	"github.com/bentossell/bentos/internal/events"
	"github.com/bentossell/bentos/internal/posfs"
	"github.com/bentossell/bentos/internal/runner"
	"github.com/bentossell/bentos/internal/skills"
	"github.com/bentossell/bentos/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bentos:", err)
		os.Exit(1)
	}
}

func run() error {
	posDir, err := posfs.PosDirFromEnv()
	if err != nil {
		return err
	}
	args := os.Args[1:]
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "init":
		return cmdInit(posDir)
	case "run":
		return tui.Run(posDir)
	case "sync", "propose", "apply":
		if len(args) < 2 {
			return fmt.Errorf("%s requires <surface>", args[0])
		}
		surface := args[1]
		kind := args[0]
		stdin, _ := os.ReadFile("/dev/stdin")
		return runScript(posDir, surface, kind, stdin)
	default:
		return usage()
	}
}

func usage() error {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(`
Usage:
  bentos init
  bentos run
  bentos sync <surface>
  bentos propose <surface>
  bentos apply <surface>   # reads JSON from stdin

Env:
  POS_DIR  override ~/.pos
`))
	return nil
}

func cmdInit(posDir string) error {
	if err := posfs.EnsureDir(posDir, 0o755); err != nil {
		return err
	}
	if err := posfs.ScaffoldFromFS(posDir, assets.PosFS, posfs.ScaffoldOptions{Force: false}); err != nil {
		return err
	}
	if err := posfs.EnsureDir(filepath.Join(posDir, "STATE"), 0o755); err != nil {
		return err
	}
	if err := posfs.EnsureDir(filepath.Join(posDir, "EVENTS"), 0o755); err != nil {
		return err
	}
	if err := posfs.EnsureDir(filepath.Join(posDir, "UI"), 0o755); err != nil {
		return err
	}

	if err := cache.Init(filepath.Join(posDir, "CACHE.db")); err != nil {
		return err
	}

	_ = writeIfMissing(filepath.Join(posDir, "STATE", "gmail.json"), `{
  "last_sync": "",
  "threads": [],
  "stats": {"unread": 0, "inbox_total": 0}
}\n`)
	_ = writeIfMissing(filepath.Join(posDir, "STATE", "gcal.json"), `{
  "last_sync": "",
  "events": [],
  "stats": {"count": 0}
}\n`)
	_ = writeIfMissing(filepath.Join(posDir, "STATE", "linear.json"), `{
  "last_sync": "",
  "issues": [],
  "stats": {"count": 0}
}\n`)

	fmt.Println("Initialized", posDir)
	return nil
}

func writeIfMissing(path string, contents string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(contents), 0o644)
}

func runScript(posDir, surface, kind string, stdin []byte) error {
	m := skills.Manager{PosDir: posDir}
	path, err := m.ScriptPath(surface, kind)
	if err != nil {
		return err
	}
	w := events.Writer{Dir: filepath.Join(posDir, "EVENTS")}
	_ = w.Append(events.Event{Kind: "skill", Surface: surface, Name: surface + "." + kind, Op: kind, Actor: "user", Summary: "started"})

	err = runner.Run(context.Background(), path, runner.RunOptions{PosDir: posDir, Stdin: stdin}, func(ev runner.ScriptEvent) {
		b, _ := jsonMarshalLine(ev)
		fmt.Println(b)
	})
	if err != nil {
		_ = w.Append(events.Event{Kind: "skill", Surface: surface, Name: surface + "." + kind, Op: kind, Actor: "user", Summary: "failed: " + err.Error()})
		return err
	}
	_ = w.Append(events.Event{Kind: "skill", Surface: surface, Name: surface + "." + kind, Op: kind, Actor: "user", Summary: "completed"})
	return nil
}

func jsonMarshalLine(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
