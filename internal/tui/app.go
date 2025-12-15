package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bentossell/bentos/internal/config"
	"github.com/bentossell/bentos/internal/events"
	"github.com/bentossell/bentos/internal/query"
	"github.com/bentossell/bentos/internal/runner"
	"github.com/bentossell/bentos/internal/skills"
)

type Model struct {
	posDir string

	homeCfg  config.HomeConfig
	homeBody string

	cmdInput textinput.Model
	cmdMode  bool

	runLines []string
	maxLines int

	proposals       []proposal
	proposalsActive bool
	proposalCursor  int
	lastSurface     string
	lastKind        string

	running  bool
	evCh     chan runner.ScriptEvent
	errCh    chan error
	lastErr  string
	lastInfo string

	eventsWriter events.Writer
}

type proposal struct {
	Selected  bool
	ID        string
	Op        string
	Summary   string
	Reasoning string
	Entities  []events.EventEntity
	Raw       map[string]any
}

type scriptEventMsg struct{ ev runner.ScriptEvent }
type scriptDoneMsg struct{ err error }

func New(posDir string) (Model, error) {
	homePath := filepath.Join(posDir, "HOME.md")
	hc, hb, err := config.ReadHomeConfig(homePath)
	if err != nil {
		return Model{}, err
	}

	ti := textinput.New()
	ti.Prompt = ": "
	ti.Placeholder = "gmail sync"
	ti.CharLimit = 200
	ti.Width = 60

	return Model{
		posDir:          posDir,
		homeCfg:         hc,
		homeBody:        hb,
		cmdInput:        ti,
		maxLines:        200,
		eventsWriter:    events.Writer{Dir: filepath.Join(posDir, "EVENTS")},
		lastInfo:        "Press ':' for commands",
		proposals:       nil,
		proposalsActive: false,
	}, nil
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "q":
			return m, tea.Quit
		case msg.String() == ":":
			m.cmdMode = true
			m.cmdInput.SetValue("")
			m.cmdInput.Focus()
			return m, nil
		case msg.String() == "esc":
			m.cmdMode = false
			m.cmdInput.Blur()
			return m, nil
		}

		if m.cmdMode {
			var cmd tea.Cmd
			m.cmdInput, cmd = m.cmdInput.Update(msg)
			if msg.Type == tea.KeyEnter {
				line := strings.TrimSpace(m.cmdInput.Value())
				m.cmdMode = false
				m.cmdInput.Blur()
				if line != "" {
					return m, m.execCommand(line)
				}
			}
			return m, cmd
		}

		if m.proposalsActive {
			switch msg.String() {
			case "up", "k":
				if m.proposalCursor > 0 {
					m.proposalCursor--
				}
				return m, nil
			case "down", "j":
				if m.proposalCursor < len(m.proposals)-1 {
					m.proposalCursor++
				}
				return m, nil
			case "x":
				if len(m.proposals) > 0 {
					m.proposals[m.proposalCursor].Selected = !m.proposals[m.proposalCursor].Selected
				}
				return m, nil
			case "X":
				for i := range m.proposals {
					m.proposals[i].Selected = true
				}
				return m, nil
			case "u":
				for i := range m.proposals {
					m.proposals[i].Selected = false
				}
				return m, nil
			case "a":
				return m, m.applySelectedProposals()
			}
		}

	case scriptEventMsg:
		m.handleScriptEvent(msg.ev)
		if m.running {
			return m, waitEventCmd(m.evCh)
		}
		return m, nil
	case scriptDoneMsg:
		m.running = false
		m.evCh = nil
		m.errCh = nil
		if msg.err != nil {
			m.lastErr = msg.err.Error()
			m.appendRunLine("error: " + msg.err.Error())
			_ = m.eventsWriter.Append(events.Event{
				Kind:    "skill",
				Surface: m.lastSurface,
				Name:    m.lastSurface + "." + m.lastKind,
				Op:      m.lastKind,
				Actor:   "user",
				Summary: "failed: " + msg.err.Error(),
			})
			return m, nil
		}

		_ = m.eventsWriter.Append(events.Event{
			Kind:    "skill",
			Surface: m.lastSurface,
			Name:    m.lastSurface + "." + m.lastKind,
			Op:      m.lastKind,
			Actor:   "user",
			Summary: "completed",
		})
		if m.lastKind == "apply" && len(m.proposals) > 0 {
			for _, p := range m.proposals {
				if !p.Selected {
					continue
				}
				_ = m.eventsWriter.Append(events.Event{
					Kind:     "skill",
					Surface:  m.lastSurface,
					Name:     m.lastSurface + ".apply",
					Op:       p.Op,
					Actor:    "user",
					Entities: p.Entities,
					Summary:  p.Summary,
					Data:     p.Raw,
				})
			}
		}
		m.lastInfo = fmt.Sprintf("done: %s %s", m.lastSurface, m.lastKind)
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	header := lipgloss.NewStyle().Bold(true).Render("bentos")
	status := ""
	if m.running {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("running")
	} else if m.lastErr != "" {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("error")
	}
	b.WriteString(header)
	if status != "" {
		b.WriteString("  ")
		b.WriteString(status)
	}
	b.WriteString("\n")

	if m.lastErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.lastErr))
		b.WriteString("\n")
	} else if m.lastInfo != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(m.lastInfo))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.renderHome())
	b.WriteString("\n")

	if m.proposalsActive {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render("Proposed Actions"))
		b.WriteString("\n")
		for i, p := range m.proposals {
			cursor := "  "
			if i == m.proposalCursor {
				cursor = "> "
			}
			box := "[ ]"
			if p.Selected {
				box = "[x]"
			}
			b.WriteString(fmt.Sprintf("%s%s %s (%s)\n", cursor, box, p.Summary, p.Op))
			if p.Reasoning != "" {
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("    " + p.Reasoning))
				b.WriteString("\n")
			}
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("j/k move, x toggle, X select all, u unselect all, a apply selected"))
		b.WriteString("\n\n")
	}

	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Run"))
	b.WriteString("\n")
	start := 0
	if len(m.runLines) > 20 {
		start = len(m.runLines) - 20
	}
	for _, l := range m.runLines[start:] {
		b.WriteString(l)
		b.WriteString("\n")
	}

	if m.cmdMode {
		b.WriteString("\n")
		b.WriteString(m.cmdInput.View())
		b.WriteString("\n")
	}

	return b.String()
}

func (m *Model) renderHome() string {
	var out strings.Builder
	for _, w := range m.homeCfg.Widgets {
		out.WriteString(lipgloss.NewStyle().Bold(true).Render(w.Title))
		out.WriteString("\n")
		lines, err := m.renderWidget(w)
		if err != nil {
			out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("  " + err.Error()))
			out.WriteString("\n\n")
			continue
		}
		for _, l := range lines {
			out.WriteString("  ")
			out.WriteString(l)
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	return out.String()
}

func (m *Model) renderWidget(w config.WidgetConfig) ([]string, error) {
	maxRows := w.MaxRows
	if maxRows <= 0 {
		maxRows = 10
	}

	switch w.Source {
	case "state":
		data, err := m.readJSONRel(w.Path)
		if err != nil {
			return nil, err
		}
		arr := extractSurfaceArray(w.Surface, data)
		filtered, err := query.Filter(arr, w.Query)
		if err != nil {
			return nil, err
		}
		if len(filtered) > maxRows {
			filtered = filtered[:maxRows]
		}
		return renderItems(w, filtered), nil
	case "log":
		events, err := m.readRecentEvents()
		if err != nil {
			return nil, err
		}
		filtered, err := query.Filter(events, w.Query)
		if err != nil {
			return nil, err
		}
		if len(filtered) > maxRows {
			filtered = filtered[len(filtered)-maxRows:]
		}
		return renderItems(w, filtered), nil
	default:
		return nil, fmt.Errorf("unsupported source: %s", w.Source)
	}
}

func (m *Model) readJSONRel(rel string) (any, error) {
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.posDir, rel)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func extractSurfaceArray(surface string, v any) []any {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	key := ""
	switch surface {
	case "gmail":
		key = "threads"
	case "gcal":
		key = "events"
	case "linear":
		key = "issues"
	}
	if key != "" {
		if arr, ok := obj[key].([]any); ok {
			return arr
		}
	}
	for _, k := range []string{"items", "nodes", "data"} {
		if arr, ok := obj[k].([]any); ok {
			return arr
		}
	}
	return nil
}

func renderItems(w config.WidgetConfig, items []any) []string {
	var lines []string
	switch w.Type {
	case "table":
		cols := w.Columns
		if len(cols) == 0 {
			cols = []string{"id"}
		}
		lines = append(lines, strings.Join(cols, " | "))
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			var row []string
			for _, c := range cols {
				row = append(row, formatCell(c, m))
			}
			lines = append(lines, strings.Join(row, " | "))
		}
	case "list":
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			lines = append(lines, applyFormat(w.Format, m))
		}
	default:
		lines = append(lines, fmt.Sprintf("(unsupported widget type: %s)", w.Type))
	}
	if len(lines) == 0 {
		lines = []string{"(empty)"}
	}
	return lines
}

func formatCell(col string, m map[string]any) string {
	if col == "age" {
		for _, k := range []string{"date", "start", "updatedAt", "updated_at", "createdAt", "created_at", "ts"} {
			if s, ok := m[k].(string); ok {
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					return humanAge(time.Since(t))
				}
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					return humanAge(time.Since(t))
				}
			}
		}
		return "-"
	}
	if v, ok := m[col]; ok {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return s
	}
	return ""
}

func applyFormat(format string, m map[string]any) string {
	if format == "" {
		if v, ok := m["summary"]; ok {
			return fmt.Sprintf("%v", v)
		}
		return fmt.Sprintf("%v", m)
	}
	out := format
	for k, v := range m {
		out = strings.ReplaceAll(out, "{"+k+"}", fmt.Sprintf("%v", v))
	}
	if strings.Contains(out, "{time}") {
		if s, ok := m["start"].(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				out = strings.ReplaceAll(out, "{time}", t.Format("15:04"))
			}
		}
	}
	return out
}

func humanAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func (m *Model) readRecentEvents() ([]any, error) {
	dir := filepath.Join(m.posDir, "EVENTS")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".jsonl") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, nil
	}
	path := files[len(files)-1]

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Model) execCommand(line string) tea.Cmd {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		m.lastErr = "command must be: <surface> <sync|propose|apply>"
		return nil
	}
	surface := fields[0]
	kind := fields[1]

	stdin := []byte(nil)
	if kind == "apply" && m.proposalsActive {
		stdin = m.selectedProposalsJSON()
		if stdin == nil {
			m.lastErr = "no proposals selected"
			return nil
		}
	}

	return m.startScript(surface, kind, stdin)
}

func (m *Model) applySelectedProposals() tea.Cmd {
	if !m.proposalsActive {
		return nil
	}
	stdin := m.selectedProposalsJSON()
	if stdin == nil {
		m.lastErr = "no proposals selected"
		return nil
	}
	return m.startScript(m.lastSurface, "apply", stdin)
}

func (m *Model) selectedProposalsJSON() []byte {
	var selected []map[string]any
	for _, p := range m.proposals {
		if !p.Selected {
			continue
		}
		selected = append(selected, p.Raw)
	}
	if len(selected) == 0 {
		return nil
	}
	payload := map[string]any{"proposed_actions": selected}
	b, _ := json.Marshal(payload)
	return b
}

func (m *Model) startScript(surface, kind string, stdin []byte) tea.Cmd {
	if m.running {
		m.lastErr = "already running"
		return nil
	}
	m.lastErr = ""
	m.lastInfo = fmt.Sprintf("running: %s %s", surface, kind)
	m.proposalsActive = false
	m.proposals = nil
	m.proposalCursor = 0
	m.lastSurface = surface
	m.lastKind = kind

	sm := skills.Manager{PosDir: m.posDir}
	scriptPath, err := sm.ScriptPath(surface, kind)
	if err != nil {
		m.lastErr = err.Error()
		return nil
	}

	evCh := make(chan runner.ScriptEvent)
	errCh := make(chan error, 1)
	m.evCh = evCh
	m.errCh = errCh
	m.running = true

	m.appendRunLine(fmt.Sprintf("$ %s %s", surface, kind))
	_ = m.eventsWriter.Append(events.Event{
		Kind:    "skill",
		Surface: surface,
		Name:    surface + "." + kind,
		Op:      kind,
		Actor:   "user",
		Summary: "started",
	})

	go func() {
		err := runner.Run(context.Background(), scriptPath, runner.RunOptions{PosDir: m.posDir, Stdin: stdin}, func(ev runner.ScriptEvent) {
			evCh <- ev
		})
		errCh <- err
		close(evCh)
	}()

	return tea.Batch(waitEventCmd(evCh), waitDoneCmd(errCh))
}

func waitEventCmd(ch <-chan runner.ScriptEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return scriptEventMsg{ev: ev}
	}
}

func waitDoneCmd(ch <-chan error) tea.Cmd {
	return func() tea.Msg {
		err := <-ch
		return scriptDoneMsg{err: err}
	}
}

func (m *Model) handleScriptEvent(ev runner.ScriptEvent) {
	switch ev.Type {
	case "progress":
		if ev.Message != "" {
			m.appendRunLine("• " + ev.Message)
		}
	case "artifact":
		m.appendRunLine(fmt.Sprintf("artifact: %s", ev.Path))
	case "error":
		if ev.Message != "" {
			m.appendRunLine("error: " + ev.Message)
		}
	case "result":
		m.appendRunLine("result")
		m.maybeExtractProposals(ev)
	}
}

func (m *Model) maybeExtractProposals(ev runner.ScriptEvent) {
	if ev.Data == nil {
		return
	}
	pa, ok := ev.Data["proposed_actions"].([]any)
	if !ok {
		return
	}
	var props []proposal
	for _, raw := range pa {
		mraw, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		p := proposal{Selected: true, Raw: mraw}
		if v, ok := mraw["id"].(string); ok {
			p.ID = v
		}
		if v, ok := mraw["op"].(string); ok {
			p.Op = v
		}
		if v, ok := mraw["summary"].(string); ok {
			p.Summary = v
		}
		if v, ok := mraw["reasoning"].(string); ok {
			p.Reasoning = v
		}
		if ents, ok := mraw["entities"].([]any); ok {
			for _, e := range ents {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				et, _ := em["type"].(string)
				ei, _ := em["id"].(string)
				if et != "" && ei != "" {
					p.Entities = append(p.Entities, events.EventEntity{Type: et, ID: ei})
				}
			}
		}
		props = append(props, p)
	}
	if len(props) == 0 {
		return
	}
	m.proposals = props
	m.proposalsActive = true
	m.proposalCursor = 0
}

func (m *Model) appendRunLine(s string) {
	m.runLines = append(m.runLines, s)
	if len(m.runLines) > m.maxLines {
		m.runLines = m.runLines[len(m.runLines)-m.maxLines:]
	}
}
