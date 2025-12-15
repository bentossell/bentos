package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

type uiMode string

const (
	modeHome         uiMode = "home"
	modeList         uiMode = "list"
	modeDetail       uiMode = "detail"
	modeHelp         uiMode = "help"
	modeActionPicker uiMode = "action_picker"
)

var surfaceOrder = []string{"home", "gmail", "linear", "gcal", "github"}

type Model struct {
	posDir string

	homeCfg  config.HomeConfig
	homeBody string

	cmdInput textinput.Model
	cmdMode  bool

	mode         uiMode
	surfaceIdx   int
	listItems    []listItem
	listCursor   int
	listSelected map[string]bool

	gmailAccounts   []string
	gmailAccountIdx int

	actionOptions []actionOption
	actionCursor  int

	detailItem    *listItem
	detailText    string
	detailLoading bool

	runLines []string
	maxLines int

	proposals        []proposal
	proposalsActive  bool
	proposalCursor   int
	proposalsSurface string
	lastSurface      string
	lastKind         string

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

type listItem struct {
	Key      string
	Title    string
	Subtitle string
	Meta     string
	Raw      map[string]any
}

type actionOption struct {
	ID    string
	Title string
}

type scriptEventMsg struct{ ev runner.ScriptEvent }
type scriptDoneMsg struct{ err error }

type detailLoadedMsg struct {
	text string
	err  error
}

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
		lastInfo:        "Tab to switch apps · ? for help · ':' for commands",
		proposals:       nil,
		proposalsActive: false,
		mode:            modeHome,
		surfaceIdx:      0,
		listItems:       nil,
		listCursor:      0,
		listSelected:    map[string]bool{},
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
		case msg.String() == "?":
			if m.mode == modeHelp {
				m.mode = m.defaultModeForSurface()
			} else {
				m.mode = modeHelp
			}
			return m, nil
		case msg.String() == "esc":
			m.cmdMode = false
			m.cmdInput.Blur()
			if m.proposalsActive {
				m.proposalsActive = false
				m.proposals = nil
				m.proposalCursor = 0
				m.lastInfo = "closed proposals"
				return m, nil
			}
			if m.mode == modeHelp {
				m.mode = m.defaultModeForSurface()
				return m, nil
			}
			if m.mode == modeDetail {
				m.mode = modeList
				m.detailItem = nil
				m.detailText = ""
				m.detailLoading = false
				return m, nil
			}
			if m.mode == modeActionPicker {
				m.mode = modeList
				m.actionOptions = nil
				m.actionCursor = 0
				return m, nil
			}
			return m, nil
		case msg.String() == "tab":
			m.nextSurface()
			return m, nil
		case msg.String() == "shift+tab":
			m.prevSurface()
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

		switch m.mode {
		case modeHelp:
			return m, nil
		case modeList:
			return m, m.updateListKeys(msg)
		case modeDetail:
			return m, nil
		case modeActionPicker:
			return m, m.updateActionPickerKeys(msg)
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
	case detailLoadedMsg:
		m.detailLoading = false
		if msg.err != nil {
			m.lastErr = msg.err.Error()
			m.detailText = ""
			return m, nil
		}
		m.detailText = msg.text
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
	b.WriteString(m.renderTabs())
	b.WriteString("\n")

	if m.lastErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.lastErr))
		b.WriteString("\n")
	} else if m.lastInfo != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(m.lastInfo))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	switch {
	case m.mode == modeHelp:
		b.WriteString(m.renderHelp())
	case m.activeSurface() == "home":
		b.WriteString(m.renderHome())
	case m.mode == modeDetail:
		b.WriteString(m.renderDetail())
	case m.mode == modeActionPicker:
		b.WriteString(m.renderActionPicker())
	default:
		b.WriteString(m.renderList())
	}
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
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("j/k move, x toggle, X select all, u unselect all, a apply selected, esc close"))
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

func (m Model) activeSurface() string {
	if m.surfaceIdx < 0 || m.surfaceIdx >= len(surfaceOrder) {
		return "home"
	}
	return surfaceOrder[m.surfaceIdx]
}

func (m Model) defaultModeForSurface() uiMode {
	if m.activeSurface() == "home" {
		return modeHome
	}
	return modeList
}

func (m *Model) nextSurface() {
	m.surfaceIdx = (m.surfaceIdx + 1) % len(surfaceOrder)
	m.mode = m.defaultModeForSurface()
	m.resetSurfaceState()
}

func (m *Model) prevSurface() {
	m.surfaceIdx--
	if m.surfaceIdx < 0 {
		m.surfaceIdx = len(surfaceOrder) - 1
	}
	m.mode = m.defaultModeForSurface()
	m.resetSurfaceState()
}

func (m *Model) resetSurfaceState() {
	m.listCursor = 0
	m.listSelected = map[string]bool{}
	m.actionOptions = nil
	m.actionCursor = 0
	m.detailItem = nil
	m.detailText = ""
	m.detailLoading = false
	m.proposalsActive = false
	m.proposals = nil
	m.proposalCursor = 0
	m.proposalsSurface = ""
	m.reloadListFromState()
}

func (m Model) renderTabs() string {
	var parts []string
	for i, s := range surfaceOrder {
		label := strings.ToUpper(s[:1]) + s[1:]
		if s == "gcal" {
			label = "Calendar"
		}
		if i == m.surfaceIdx {
			parts = append(parts, lipgloss.NewStyle().Bold(true).Underline(true).Render("["+label+"]"))
		} else {
			parts = append(parts, " "+label+" ")
		}
	}
	return strings.Join(parts, "|")
}

func (m Model) renderHelp() string {
	lines := []string{
		"Navigation:",
		"  Tab / Shift+Tab  switch app (Home/Gmail/Linear/Calendar/GitHub)",
		"  q               quit",
		"  ?               toggle help",
		"",
		"List view:",
		"  j/k or ↑/↓      move cursor",
		"  Enter           open detail",
		"  Space or x      toggle selection for current row",
		"  X               select all visible",
		"  u               unselect all",
		"  a               actions for selected (where supported)",
		"  Esc             back",
		"",
		"Commands:",
		"  :               command palette (e.g. 'gmail sync', 'linear sync')",
		"",
		"Gmail tips:",
		"  '[' / ']'       cycle account filter (All / per-account)",
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(strings.Join(lines, "\n"))
}

func (m *Model) reloadListFromState() {
	surface := m.activeSurface()
	if surface == "home" {
		m.listItems = nil
		return
	}
	items, err := m.loadListItems(surface)
	if err != nil {
		m.lastErr = err.Error()
		m.listItems = nil
		return
	}
	m.listItems = items
	if m.listCursor >= len(m.listItems) {
		m.listCursor = max(0, len(m.listItems)-1)
	}
}

func (m *Model) loadListItems(surface string) ([]listItem, error) {
	switch surface {
	case "gmail":
		v, err := m.readJSONRel("STATE/gmail.json")
		if err != nil {
			return nil, err
		}
		obj, _ := v.(map[string]any)
		return m.listItemsFromGmail(obj), nil
	case "linear":
		v, err := m.readJSONRel("STATE/linear.json")
		if err != nil {
			return nil, err
		}
		obj, _ := v.(map[string]any)
		return listItemsFromLinear(obj), nil
	case "gcal":
		v, err := m.readJSONRel("STATE/gcal.json")
		if err != nil {
			return nil, err
		}
		obj, _ := v.(map[string]any)
		return listItemsFromGcal(obj), nil
	case "github":
		v, err := m.readJSONRel("STATE/github.json")
		if err != nil {
			return nil, err
		}
		obj, _ := v.(map[string]any)
		return listItemsFromGithub(obj), nil
	default:
		return nil, fmt.Errorf("unknown surface: %s", surface)
	}
}

func (m *Model) listItemsFromGmail(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	accounts := deriveStringList(obj["accounts"])
	if len(accounts) == 0 {
		threads := deriveMapList(obj["threads"])
		seen := map[string]bool{}
		for _, t := range threads {
			acct, _ := t["account"].(string)
			if acct == "" {
				continue
			}
			seen[acct] = true
		}
		for acct := range seen {
			accounts = append(accounts, acct)
		}
		sort.Strings(accounts)
	}

	m.gmailAccounts = append([]string{"All"}, accounts...)
	if m.gmailAccountIdx < 0 || m.gmailAccountIdx >= len(m.gmailAccounts) {
		m.gmailAccountIdx = 0
	}
	accountFilter := ""
	if m.gmailAccountIdx > 0 {
		accountFilter = m.gmailAccounts[m.gmailAccountIdx]
	}

	threads := deriveMapList(obj["threads"])
	var out []listItem
	for _, t := range threads {
		id, _ := t["id"].(string)
		if id == "" {
			continue
		}
		acct, _ := t["account"].(string)
		if accountFilter != "" && acct != accountFilter {
			continue
		}
		subject, _ := t["subject"].(string)
		from, _ := t["from"].(string)
		date, _ := t["date"].(string)
		age := humanAgeFromISO(date)
		meta := age
		if acct != "" {
			meta = acct + " · " + age
		}
		key := id
		if acct != "" {
			key = acct + ":" + id
		}
		out = append(
			out,
			listItem{
				Key:      key,
				Title:    subject,
				Subtitle: from,
				Meta:     meta,
				Raw:      t,
			},
		)
	}
	return out
}

func listItemsFromLinear(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	issues := deriveMapList(obj["issues"])
	var out []listItem
	for _, it := range issues {
		identifier, _ := it["identifier"].(string)
		id, _ := it["id"].(string)
		key := identifier
		if key == "" {
			key = id
		}
		if key == "" {
			continue
		}
		title, _ := it["title"].(string)
		status, _ := it["status"].(string)
		team, _ := it["team"].(string)
		sub := status
		if team != "" {
			sub = team + " · " + status
		}
		shown := title
		if identifier != "" {
			shown = identifier + " — " + title
		}
		out = append(out, listItem{Key: key, Title: shown, Subtitle: sub, Meta: "", Raw: it})
	}
	return out
}

func listItemsFromGcal(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	events := deriveMapList(obj["events"])
	var out []listItem
	for _, it := range events {
		id, _ := it["id"].(string)
		if id == "" {
			continue
		}
		summary, _ := it["summary"].(string)
		start, _ := it["start"].(string)
		meta := ""
		if start != "" {
			meta = start
		}
		out = append(out, listItem{Key: id, Title: summary, Subtitle: meta, Meta: "", Raw: it})
	}
	return out
}

func listItemsFromGithub(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	accounts := deriveMapList(obj["accounts"])
	var out []listItem
	for _, it := range accounts {
		login, _ := it["login"].(string)
		if login == "" {
			continue
		}
		active, _ := it["active"].(bool)
		scopes, _ := it["scopes"].(string)
		meta := scopes
		if active {
			meta = "active · " + scopes
		}
		out = append(out, listItem{Key: login, Title: login, Subtitle: meta, Meta: "", Raw: it})
	}
	return out
}

func (m Model) renderList() string {
	surface := m.activeSurface()
	header := strings.ToUpper(surface[:1]) + surface[1:]
	if surface == "gcal" {
		header = "Calendar"
	}
	if surface == "github" {
		header = "GitHub"
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(header))
	if surface == "gmail" {
		acct := "All"
		if m.gmailAccountIdx > 0 && m.gmailAccountIdx < len(m.gmailAccounts) {
			acct = m.gmailAccounts[m.gmailAccountIdx]
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Account: " + acct + ")"))
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(fmt.Sprintf("Selected: %d", m.selectedCount())))
	b.WriteString("\n\n")

	if len(m.listItems) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("(empty)"))
		b.WriteString("\n")
		return b.String()
	}

	for i, it := range m.listItems {
		cursor := "  "
		if i == m.listCursor {
			cursor = "> "
		}
		box := "[ ]"
		if m.listSelected[it.Key] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s%s %s", cursor, box, it.Title)
		b.WriteString(line)
		b.WriteString("\n")
		if it.Subtitle != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("    " + it.Subtitle))
			b.WriteString("\n")
		}
		if it.Meta != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("    " + it.Meta))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(
		lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(
			"Space select · Enter detail · a actions · : commands · ? help",
		),
	)
	b.WriteString("\n")
	return b.String()
}

func (m Model) renderDetail() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Detail"))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Esc back)"))
	b.WriteString("\n\n")

	if m.detailItem != nil {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(m.detailItem.Title))
		b.WriteString("\n")
		if m.detailItem.Subtitle != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(m.detailItem.Subtitle))
			b.WriteString("\n")
		}
		if m.detailItem.Meta != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(m.detailItem.Meta))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if m.detailLoading {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Loading…"))
		b.WriteString("\n")
		return b.String()
	}

	if m.detailText != "" {
		b.WriteString(m.detailText)
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("(no detail)"))
	b.WriteString("\n")
	return b.String()
}

func (m Model) renderActionPicker() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Actions"))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Enter select · Esc cancel)"))
	b.WriteString("\n\n")
	for i, opt := range m.actionOptions {
		cursor := "  "
		if i == m.actionCursor {
			cursor = "> "
		}
		b.WriteString(cursor + opt.Title)
		b.WriteString("\n")
	}
	if len(m.actionOptions) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("(no actions)"))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) updateListKeys(msg tea.KeyMsg) tea.Cmd {
	surface := m.activeSurface()
	s := msg.String()
	switch s {
	case "up", "k":
		if m.listCursor > 0 {
			m.listCursor--
		}
		return nil
	case "down", "j":
		if m.listCursor < len(m.listItems)-1 {
			m.listCursor++
		}
		return nil
	case " ", "x":
		if len(m.listItems) == 0 {
			return nil
		}
		key := m.listItems[m.listCursor].Key
		m.listSelected[key] = !m.listSelected[key]
		return nil
	case "X":
		for _, it := range m.listItems {
			m.listSelected[it.Key] = true
		}
		return nil
	case "u":
		m.listSelected = map[string]bool{}
		return nil
	case "[":
		if surface == "gmail" && len(m.gmailAccounts) > 0 {
			m.gmailAccountIdx--
			if m.gmailAccountIdx < 0 {
				m.gmailAccountIdx = len(m.gmailAccounts) - 1
			}
			m.reloadListFromState()
		}
		return nil
	case "]":
		if surface == "gmail" && len(m.gmailAccounts) > 0 {
			m.gmailAccountIdx = (m.gmailAccountIdx + 1) % len(m.gmailAccounts)
			m.reloadListFromState()
		}
		return nil
	case "enter":
		if len(m.listItems) == 0 {
			return nil
		}
		it := m.listItems[m.listCursor]
		m.mode = modeDetail
		m.detailItem = &it
		m.detailText = ""
		m.detailLoading = false
		m.lastErr = ""
		return m.fetchDetailForItem(surface, it)
	case "a":
		if m.selectedCount() == 0 {
			m.lastErr = "select items first (Space)"
			return nil
		}
		opts := m.actionOptionsForSurface(surface)
		if len(opts) == 0 {
			m.lastErr = "no actions for this surface yet"
			return nil
		}
		m.actionOptions = opts
		m.actionCursor = 0
		m.mode = modeActionPicker
		return nil
	default:
		return nil
	}
}

func (m *Model) updateActionPickerKeys(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	switch s {
	case "up", "k":
		if m.actionCursor > 0 {
			m.actionCursor--
		}
		return nil
	case "down", "j":
		if m.actionCursor < len(m.actionOptions)-1 {
			m.actionCursor++
		}
		return nil
	case "enter":
		if len(m.actionOptions) == 0 {
			return nil
		}
		opt := m.actionOptions[m.actionCursor]
		m.mode = modeList
		m.actionOptions = nil
		m.actionCursor = 0
		m.buildProposalsFromSelection(opt)
		return nil
	default:
		return nil
	}
}

func (m *Model) actionOptionsForSurface(surface string) []actionOption {
	switch surface {
	case "gmail":
		return []actionOption{
			{ID: "archive", Title: "Archive selected"},
			{ID: "mark_read", Title: "Mark read selected"},
			{ID: "star", Title: "Star selected"},
		}
	default:
		return nil
	}
}

func (m *Model) buildProposalsFromSelection(opt actionOption) {
	surface := m.activeSurface()
	var props []proposal
	for _, it := range m.listItems {
		if !m.listSelected[it.Key] {
			continue
		}
		var action map[string]any
		switch surface {
		case "gmail":
			threadID, _ := it.Raw["id"].(string)
			acct, _ := it.Raw["account"].(string)
			entities := []any{map[string]any{"type": "email_thread", "id": threadID, "account": acct}}
			summaryPrefix := strings.Title(strings.ReplaceAll(opt.ID, "_", " "))
			action = map[string]any{
				"id":        fmt.Sprintf("%s_%s", opt.ID, threadID),
				"op":        opt.ID,
				"surface":   "gmail",
				"entities":  entities,
				"summary":   fmt.Sprintf("%s: %s", summaryPrefix, truncate(it.Title, 80)),
				"reasoning": "user-selected",
			}
		default:
			continue
		}

		p := proposal{Selected: true, Raw: action}
		if v, ok := action["id"].(string); ok {
			p.ID = v
		}
		if v, ok := action["op"].(string); ok {
			p.Op = v
		}
		if v, ok := action["summary"].(string); ok {
			p.Summary = v
		}
		if v, ok := action["reasoning"].(string); ok {
			p.Reasoning = v
		}
		props = append(props, p)
	}

	if len(props) == 0 {
		m.lastErr = "no actions generated"
		return
	}

	m.proposals = props
	m.proposalsActive = true
	m.proposalsSurface = surface
	m.proposalCursor = 0
	m.lastInfo = fmt.Sprintf("review %d proposed actions (then press 'a' to apply)", len(props))
}

func (m *Model) fetchDetailForItem(surface string, it listItem) tea.Cmd {
	switch surface {
	case "gmail":
		threadID, _ := it.Raw["id"].(string)
		acct, _ := it.Raw["account"].(string)
		if threadID == "" || acct == "" {
			m.detailText = prettyJSON(it.Raw)
			return nil
		}
		m.detailLoading = true
		return fetchGmailThreadCmd(acct, threadID)
	case "linear":
		identifier, _ := it.Raw["identifier"].(string)
		if identifier == "" {
			m.detailText = prettyJSON(it.Raw)
			return nil
		}
		m.detailLoading = true
		return fetchLinearIssueCmd(filepath.Join(m.posDir, "skills", "linear", "vendor", "issues.js"), identifier)
	default:
		m.detailText = prettyJSON(it.Raw)
		return nil
	}
}

func fetchGmailThreadCmd(account string, threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gmcli", account, "thread", threadID)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		return detailLoadedMsg{text: string(out), err: err}
	}
}

func fetchLinearIssueCmd(issuesScriptPath string, identifier string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", issuesScriptPath, "--id", identifier)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		return detailLoadedMsg{text: string(out), err: err}
	}
}

func (m Model) selectedCount() int {
	c := 0
	for _, v := range m.listSelected {
		if v {
			c++
		}
	}
	return c
}

func deriveMapList(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

func deriveStringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range arr {
		s, ok := it.(string)
		if !ok || s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func humanAgeFromISO(s string) string {
	if s == "" {
		return "-"
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return humanAge(time.Since(t))
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return humanAge(time.Since(t))
	}
	return "-"
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
	case "github":
		// Prefer accounts for the "GitHub — Accounts" widget.
		if arr, ok := obj["accounts"].([]any); ok {
			return arr
		}
		key = "notifications"
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
	surface := m.proposalsSurface
	if surface == "" {
		surface = m.activeSurface()
	}
	return m.startScript(surface, "apply", stdin)
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
	m.proposalsSurface = m.lastSurface
}

func (m *Model) appendRunLine(s string) {
	m.runLines = append(m.runLines, s)
	if len(m.runLines) > m.maxLines {
		m.runLines = m.runLines[len(m.runLines)-m.maxLines:]
	}
}
