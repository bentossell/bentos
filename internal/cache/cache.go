package cache

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Init(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			if err := os.WriteFile(dbPath, nil, 0o600); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	_ = os.Chmod(dbPath, 0o600)

	schema := `
CREATE TABLE IF NOT EXISTS gmail_messages (
	message_id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL,
	body_text TEXT,
	body_html TEXT,
	headers TEXT,
	attachments TEXT,
	fetched_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS github_prs (
	pr_id TEXT PRIMARY KEY,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	body TEXT,
	diff TEXT,
	comments TEXT,
	reviews TEXT,
	fetched_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS github_issues (
	issue_id TEXT PRIMARY KEY,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	body TEXT,
	comments TEXT,
	fetched_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS linear_issues (
	issue_id TEXT PRIMARY KEY,
	identifier TEXT NOT NULL,
	description TEXT,
	comments TEXT,
	history TEXT,
	fetched_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS calendar_events (
	event_id TEXT PRIMARY KEY,
	description TEXT,
	attachments TEXT,
	conferencing TEXT,
	fetched_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cache (
	key TEXT PRIMARY KEY,
	surface TEXT NOT NULL,
	data TEXT,
	fetched_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_gmail_thread ON gmail_messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_github_pr_repo ON github_prs(repo);
CREATE INDEX IF NOT EXISTS idx_github_issue_repo ON github_issues(repo);
CREATE INDEX IF NOT EXISTS idx_linear_identifier ON linear_issues(identifier);
CREATE INDEX IF NOT EXISTS idx_cache_surface ON cache(surface);
`

	py := `import sqlite3, sys
db_path = sys.argv[1]
schema = r'''` + schema + `'''
conn = sqlite3.connect(db_path)
conn.executescript(schema)
conn.commit()
conn.close()
`

	cmd := exec.Command("python3", "-c", py, dbPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("cache init failed: %s", msg)
	}

	return nil
}
