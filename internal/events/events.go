package events

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Event struct {
	TS        string         `json:"ts"`
	Kind      string         `json:"kind"`
	Surface   string         `json:"surface"`
	Name      string         `json:"name"`
	Op        string         `json:"op"`
	Actor     string         `json:"actor"`
	SessionID string         `json:"session_id,omitempty"`
	Entities  []EventEntity  `json:"entities,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type EventEntity struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Writer struct {
	Dir string
}

func (w Writer) Append(e Event) error {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}

	month := time.Now().UTC().Format("2006-01")
	path := filepath.Join(w.Dir, month+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(e)
	if err != nil {
		return err
	}

	bw := bufio.NewWriter(f)
	if _, err := bw.Write(append(b, '\n')); err != nil {
		return err
	}
	return bw.Flush()
}
