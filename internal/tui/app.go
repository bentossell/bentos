package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

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
	homeVP   viewport.Model

	cmdInput textinput.Model
	cmdMode  bool

	width  int
	height int

	mode         uiMode
	surfaceIdx   int
	listItems    []listItem
	listCursor   int
	listOffset   int
	listSelected map[string]bool

	gmailAccounts   []string
	gmailAccountIdx int

	linearFilterIdx int

	gcalAccounts   []string
	gcalAccountIdx int
	gcalCalendarID string
	gcalView       string
	gcalAnchor     time.Time
	gcalDayIdx     int
	gcalEventIdx   int
	gcalOffset     int

	githubAccounts         []string
	githubAccountIdx       int
	githubFilterIdx        int
	githubNotificationsErr string
	githubItemsErr         string
	githubTrackedRepos     []string
	githubTrackedLocal     []string

	actionOptions []actionOption
	actionCursor  int

	detailItem    *listItem
	detailSurface string
	detailText    string
	detailLoading bool
	detailVP      viewport.Model

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

	pendingOpenSurface  string
	pendingOpenSummary  string
	pendingOpenEntities []events.EventEntity

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

type openURLMsg struct {
	url string
	err error
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
		homeVP:          viewport.New(0, 0),
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
		listOffset:      0,
		listSelected:    map[string]bool{},
		gcalView:        "week",
		gcalAnchor:      time.Now(),
		detailVP:        viewport.New(0, 0),
	}, nil
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeDetailViewport()
		m.resizeHomeViewport()
		if m.activeSurface() == "home" && m.mode == modeHome {
			m.refreshHomeViewport(false)
		}
		return m, nil
	case tea.MouseMsg:
		if m.mode == modeList {
			switch msg.Type {
			case tea.MouseWheelUp:
				return m, m.updateListKeys(tea.KeyMsg{Type: tea.KeyUp})
			case tea.MouseWheelDown:
				return m, m.updateListKeys(tea.KeyMsg{Type: tea.KeyDown})
			}
		}
		if m.mode == modeDetail {
			switch msg.Type {
			case tea.MouseWheelUp:
				return m, m.updateDetailKeys(tea.KeyMsg{Type: tea.KeyUp})
			case tea.MouseWheelDown:
				return m, m.updateDetailKeys(tea.KeyMsg{Type: tea.KeyDown})
			}
		}
		return m, nil
	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "q":
			return m, tea.Quit
		case msg.String() == ":":
			m.cmdMode = true
			m.cmdInput.SetValue("")
			m.cmdInput.Focus()
			m.resizeDetailViewport()
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
			m.resizeDetailViewport()
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
				m.detailVP.SetContent("")
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
				m.resizeDetailViewport()
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
		case modeHome:
			return m, m.updateHomeKeys(msg)
		case modeList:
			return m, m.updateListKeys(msg)
		case modeDetail:
			return m, m.updateDetailKeys(msg)
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
			m.refreshDetailViewport(true)
			return m, nil
		}
		m.detailText = msg.text
		m.refreshDetailViewport(true)
		return m, nil
	case openURLMsg:
		if msg.err != nil {
			m.lastErr = msg.err.Error()
			return m, nil
		}
		if msg.url != "" {
			m.lastInfo = "opened: " + msg.url
			_ = m.eventsWriter.Append(events.Event{
				Kind:     "open",
				Surface:  firstNonEmpty(m.pendingOpenSurface, m.activeSurface()),
				Name:     firstNonEmpty(m.pendingOpenSurface, m.activeSurface()) + ".open",
				Op:       "open",
				Actor:    "user",
				Entities: m.pendingOpenEntities,
				Summary:  firstNonEmpty(m.pendingOpenSummary, msg.url),
				Data:     map[string]any{"url": msg.url},
			})
			m.pendingOpenSurface = ""
			m.pendingOpenSummary = ""
			m.pendingOpenEntities = nil
		}
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

	b.WriteString(m.renderDivider())
	b.WriteString("\n")

	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Run"))
	b.WriteString("\n")
	runN := m.runPanelLines()
	start := 0
	if len(m.runLines) > runN {
		start = len(m.runLines) - runN
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
	m.listOffset = 0
	m.listSelected = map[string]bool{}
	m.homeVP.SetContent("")
	m.actionOptions = nil
	m.actionCursor = 0
	m.detailItem = nil
	m.detailSurface = ""
	m.detailText = ""
	m.detailLoading = false
	m.detailVP.SetContent("")
	m.proposalsActive = false
	m.proposals = nil
	m.proposalCursor = 0
	m.proposalsSurface = ""
	m.reloadListFromState()
	if m.activeSurface() == "home" {
		m.refreshHomeViewport(true)
	}
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
		"Home:",
		"  j/k or ↑/↓      scroll",
		"",
		"List view:",
		"  j/k or ↑/↓      move cursor",
		"  Enter           open detail",
		"  o               open selected in browser",
		"  Space or x      toggle selection for current row",
		"  X               select all visible",
		"  u               unselect all",
		"  a               actions for selected (where supported)",
		"  Esc             back",
		"",
		"Detail view:",
		"  j/k or ↑/↓      scroll",
		"  o               open in browser",
		"",
		"Commands:",
		"  :               command palette (e.g. 'gmail sync', 'linear sync')",
		"",
		"Gmail tips:",
		"  '[' / ']' or '/' cycle account filter (All / per-account)",
		"",
		"Linear tips:",
		"  f               cycle status filters (Focus/All/Done/Canceled)",
		"",
		"Calendar tips:",
		"  '[' / ']' or '/' cycle account filter (All / per-account)",
		"  w/d             week/day view",
		"  ←/→             prev/next week/day",
		"  h/l             prev/next day (week view)",
		"",
		"GitHub tips:",
		"  '[' / ']' or '/' cycle account filter (All / per-account)",
		"  f               cycle view filters (All/PRs/Issues/Tracked)",
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(strings.Join(lines, "\n"))
}

func (m Model) renderDivider() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	if w < 10 {
		w = 10
	}
	line := strings.Repeat("─", w)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(line)
}

func (m *Model) reloadListFromState() {
	surface := m.activeSurface()
	if surface == "home" {
		m.listItems = nil
		m.refreshHomeViewport(false)
		return
	}
	items, err := m.loadListItems(surface)
	if err != nil {
		m.lastErr = err.Error()
		m.listItems = nil
		return
	}
	m.listOffset = 0
	m.listItems = items
	if m.listCursor >= len(m.listItems) {
		m.listCursor = max(0, len(m.listItems)-1)
	}
	m.ensureCursorVisible()
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
		return m.listItemsFromLinear(obj), nil
	case "gcal":
		v, err := m.readJSONRel("STATE/gcal.json")
		if err != nil {
			return nil, err
		}
		obj, _ := v.(map[string]any)
		return m.listItemsFromGcal(obj), nil
	case "github":
		v, err := m.readJSONRel("STATE/github.json")
		if err != nil {
			return nil, err
		}
		obj, _ := v.(map[string]any)
		return m.listItemsFromGithub(obj), nil
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

func (m *Model) listItemsFromLinear(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	issues := deriveMapList(obj["issues"])
	filter := m.linearFilterName()
	var out []listItem
	for _, it := range issues {
		status, _ := it["status"].(string)
		statusLabel := linearStatusLabel(status)
		if !linearStatusMatchesFilter(status, filter) {
			continue
		}
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
		team, _ := it["team"].(string)
		sub := statusLabel
		if team != "" {
			sub = team + " · " + statusLabel
		}
		shown := title
		if identifier != "" {
			shown = identifier + " — " + title
		}
		out = append(out, listItem{Key: key, Title: shown, Subtitle: sub, Meta: "", Raw: it})
	}
	return out
}

func (m *Model) listItemsFromGcal(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	if calID, ok := obj["calendar_id"].(string); ok {
		m.gcalCalendarID = calID
	}
	accounts := deriveStringList(obj["accounts"])
	m.gcalAccounts = append([]string{"All"}, accounts...)
	if m.gcalAccountIdx < 0 || m.gcalAccountIdx >= len(m.gcalAccounts) {
		m.gcalAccountIdx = 0
	}
	accountFilter := ""
	if m.gcalAccountIdx > 0 && m.gcalAccountIdx < len(m.gcalAccounts) {
		accountFilter = m.gcalAccounts[m.gcalAccountIdx]
	}

	events := deriveMapList(obj["events"])
	var out []listItem
	for _, it := range events {
		id, _ := it["id"].(string)
		if id == "" {
			continue
		}
		acct, _ := it["account"].(string)
		if accountFilter != "" && acct != accountFilter {
			continue
		}
		summary, _ := it["summary"].(string)
		start, _ := it["start"].(string)
		when := formatEventWhen(start)
		delta := humanDeltaFromISO(start)
		meta := strings.TrimSpace(strings.Trim(strings.Join([]string{acct, delta}, " · "), " ·"))
		if acct != "" {
			// keep acct in meta
		}
		key := id
		if acct != "" {
			key = acct + ":" + id
		}
		sub := when
		if sub == "" {
			sub = start
		}
		out = append(out, listItem{Key: key, Title: summary, Subtitle: sub, Meta: meta, Raw: it})
	}
	return out
}

func (m *Model) linearFilterName() string {
	filters := []string{"Focus", "All", "Done", "Canceled"}
	if m.linearFilterIdx < 0 || m.linearFilterIdx >= len(filters) {
		m.linearFilterIdx = 0
	}
	return filters[m.linearFilterIdx]
}

func (m *Model) githubFilterName() string {
	filters := []string{"All", "PRs", "Issues", "Tracked"}
	if m.githubFilterIdx < 0 || m.githubFilterIdx >= len(filters) {
		m.githubFilterIdx = 0
	}
	return filters[m.githubFilterIdx]
}

func linearStatusMatchesFilter(status string, filter string) bool {
	s := strings.ToLower(status)
	switch filter {
	case "All":
		return true
	case "Done":
		return strings.Contains(s, "done") || strings.Contains(s, "completed")
	case "Canceled":
		return strings.Contains(s, "canceled") || strings.Contains(s, "cancelled")
	case "Focus":
		// Intent: show items you likely need to act on.
		return strings.Contains(s, "todo") || strings.Contains(s, "triage")
	default:
		return true
	}
}

func linearStatusLabel(status string) string {
	// Linear outputs "Name (type)"; the type is redundant in list rows.
	if status == "" {
		return ""
	}
	if i := strings.Index(status, " ("); i > 0 {
		return strings.TrimSpace(status[:i])
	}
	return strings.TrimSpace(status)
}

func listItemsFromLinear(obj map[string]any) []listItem {
	return nil
}

func listItemsFromGcal(obj map[string]any) []listItem {
	return nil
}

func (m *Model) listItemsFromGithub(obj map[string]any) []listItem {
	if obj == nil {
		return nil
	}
	if repos, locals, err := readGithubTrackedConfig(filepath.Join(m.posDir, "skills", "github", "SKILL.md")); err == nil {
		m.githubTrackedRepos = repos
		m.githubTrackedLocal = locals
	}
	m.githubNotificationsErr, _ = obj["notifications_error"].(string)
	if itemsErr, ok := obj["items_error"].(map[string]any); ok {
		m.githubItemsErr = prettyJSON(itemsErr)
	} else {
		m.githubItemsErr = ""
	}

	accounts := deriveMapList(obj["accounts"])
	var logins []string
	for _, it := range accounts {
		login, _ := it["login"].(string)
		if login != "" {
			logins = append(logins, login)
		}
	}
	m.githubAccounts = append([]string{"All"}, logins...)
	if m.githubAccountIdx < 0 || m.githubAccountIdx >= len(m.githubAccounts) {
		m.githubAccountIdx = 0
	}
	accountFilter := ""
	if m.githubAccountIdx > 0 && m.githubAccountIdx < len(m.githubAccounts) {
		accountFilter = m.githubAccounts[m.githubAccountIdx]
	}
	filter := m.githubFilterName()

	items := deriveMapList(obj["items"])
	if len(items) == 0 {
		return nil
	}

	var out []listItem
	for _, it := range items {
		kind, _ := it["kind"].(string)
		source, _ := it["source"].(string)
		acct, _ := it["account"].(string)
		if accountFilter != "" && acct != accountFilter && source != "tracked" {
			continue
		}
		switch filter {
		case "PRs":
			if kind != "pr" {
				continue
			}
		case "Issues":
			if kind != "issue" {
				continue
			}
		case "Tracked":
			if source != "tracked" {
				continue
			}
		}

		repo, _ := it["repo"].(string)
		num := intFromAny(it["number"])
		title, _ := it["title"].(string)
		shown := title
		if repo != "" && num > 0 {
			shown = fmt.Sprintf("%s#%d — %s", repo, num, title)
		}
		state, _ := it["state"].(string)
		updated, _ := it["updatedAt"].(string)
		age := humanAgeFromISO(updated)
		meta := strings.Trim(strings.Join([]string{source, strings.ToLower(state), age}, " · "), " ·")
		url, _ := it["url"].(string)
		key := url
		if key == "" {
			key = fmt.Sprintf("%s#%d", repo, num)
		}
		out = append(out, listItem{Key: key, Title: shown, Subtitle: meta, Meta: url, Raw: it})
	}
	return out
}

func (m Model) renderList() string {
	surface := m.activeSurface()
	if surface == "gcal" {
		return m.renderCalendar()
	}
	header := strings.ToUpper(surface[:1]) + surface[1:]
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
	if surface == "linear" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Filter: " + m.linearFilterName() + ")"))
	}
	if surface == "gcal" {
		acct := "All"
		if m.gcalAccountIdx > 0 && m.gcalAccountIdx < len(m.gcalAccounts) {
			acct = m.gcalAccounts[m.gcalAccountIdx]
		}
		calID := m.gcalCalendarID
		if calID == "" {
			calID = "primary"
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Account: " + acct + " · Cal: " + calID + ")"))
	}
	if surface == "github" {
		acct := "All"
		if m.githubAccountIdx > 0 && m.githubAccountIdx < len(m.githubAccounts) {
			acct = m.githubAccounts[m.githubAccountIdx]
		}
		tracked := ""
		if len(m.githubTrackedRepos) > 0 || len(m.githubTrackedLocal) > 0 {
			tracked = fmt.Sprintf(" · Tracked: %d", len(m.githubTrackedRepos))
			if len(m.githubTrackedLocal) > 0 {
				tracked += fmt.Sprintf(" (%d local)", len(m.githubTrackedLocal))
			}
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Account: " + acct + " · View: " + m.githubFilterName() + tracked + ")"))
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(fmt.Sprintf("Selected: %d", m.selectedCount())))
	b.WriteString("\n\n")

	if len(m.listItems) == 0 {
		empty := "(empty)"
		if surface == "gcal" {
			calID := m.gcalCalendarID
			if calID == "" {
				calID = "primary"
			}
			empty = "No calendar events. Try :gcal sync (or increase days_ahead in skills/gcal/SKILL.md).\nCalendar: " + calID
		}
		if surface == "github" {
			empty = "No GitHub items. Try :github sync.\nTo track issues/PRs in specific repos, add tracked_repos in skills/github/SKILL.md."
			if m.githubNotificationsErr != "" {
				empty += "\n\nNotifications: " + truncate(m.githubNotificationsErr, 120)
			}
			if m.githubItemsErr != "" {
				empty += "\n\nItems error: " + truncate(strings.ReplaceAll(m.githubItemsErr, "\n", " "), 160)
			}
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(empty))
		b.WriteString("\n")
		return b.String()
	}

	pageSize := m.listPageSize()
	if pageSize <= 0 {
		pageSize = 8
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
	if m.listOffset > max(0, len(m.listItems)-1) {
		m.listOffset = max(0, len(m.listItems)-1)
	}
	end := m.listOffset + pageSize
	if end > len(m.listItems) {
		end = len(m.listItems)
	}
	maxTitle := 80
	maxMeta := 120
	if m.width > 0 {
		w := m.width - 10
		if w < 20 {
			w = 20
		}
		maxTitle = w
		maxMeta = w
	}
	for i := m.listOffset; i < end; i++ {
		it := m.listItems[i]
		cursor := "  "
		if i == m.listCursor {
			cursor = "> "
		}
		box := "[ ]"
		if m.listSelected[it.Key] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s%s %s", cursor, box, truncate(it.Title, maxTitle))
		b.WriteString(line)
		b.WriteString("\n")
		meta := strings.TrimSpace(strings.Trim(strings.Join([]string{it.Subtitle, it.Meta}, " · "), " ·"))
		if meta != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("    " + truncate(meta, maxMeta)))
			b.WriteString("\n")
		}
	}
	if len(m.listItems) > end {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("…"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	footer := "Space select · Enter detail · o open · a actions · : commands · ? help"
	if surface == "gmail" || surface == "gcal" {
		footer = "Space select · Enter detail · o open · a actions · [ ] or / account · : commands · ? help"
	}
	if surface == "linear" {
		footer = "Space select · Enter detail · o open · f filter · : commands · ? help"
	}
	if surface == "github" {
		footer = "Enter detail · o open · f filter · [ ] or / account · : commands · ? help"
	}
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(footer))
	b.WriteString("\n")
	return b.String()
}

func (m Model) renderCalendar() string {
	var b strings.Builder
	acct := "All"
	if m.gcalAccountIdx > 0 && m.gcalAccountIdx < len(m.gcalAccounts) {
		acct = m.gcalAccounts[m.gcalAccountIdx]
	}
	calID := m.gcalCalendarID
	if calID == "" {
		calID = "primary"
	}
	view := m.gcalView
	if view == "" {
		view = "week"
	}

	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Calendar"))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (" + strings.Title(view) + " · Account: " + acct + " · Cal: " + calID + ")"))
	b.WriteString("\n\n")

	if len(m.listItems) == 0 {
		empty := "No calendar events. Try :gcal sync (or increase days_ahead in skills/gcal/SKILL.md).\nCalendar: " + calID
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(empty))
		b.WriteString("\n")
		return b.String()
	}

	if view == "day" {
		b.WriteString(m.renderCalendarDay())
	} else {
		b.WriteString(m.renderCalendarWeek())
	}
	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("←/→ nav · j/k move · enter detail · o open · / account · w week · d day · t today"))
	b.WriteString("\n")
	return b.String()
}

func (m Model) renderCalendarWeek() string {
	weekStart := startOfWeek(m.gcalAnchor)
	weekEnd := weekStart.AddDate(0, 0, 6)
	weekLabel := weekStart.Format("Mon 02 Jan") + " – " + weekEnd.Format("Mon 02 Jan")

	width := m.width - 2
	if width < 70 {
		width = 70
	}
	colW := width / 7
	if colW < 10 {
		colW = 10
	}

	days := m.gcalWeekEvents(weekStart)
	maxRows := m.mainBudgetLines() - 7
	if maxRows < 3 {
		maxRows = 3
	}
	if maxRows > 12 {
		maxRows = 12
	}

	selectedStyle := lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230"))
	todayStyle := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(weekLabel))
	b.WriteString("\n")

	// Day headers
	for i := 0; i < 7; i++ {
		d := weekStart.AddDate(0, 0, i)
		label := d.Format("Mon 2")
		if sameDay(time.Now(), d) {
			label = todayStyle.Render(label)
		}
		b.WriteString(truncate(padRight(label, colW), colW))
	}
	b.WriteString("\n")
	for i := 0; i < 7; i++ {
		b.WriteString(dim.Render(strings.Repeat("─", colW-1)))
		b.WriteString(" ")
	}
	b.WriteString("\n")

	for row := 0; row < maxRows; row++ {
		for day := 0; day < 7; day++ {
			cell := ""
			items := days[day]
			if row < len(items) {
				cell = gcalEventCell(*items[row], colW)
			} else if row == maxRows-1 && len(items) > maxRows {
				cell = "… +" + fmt.Sprintf("%d", len(items)-maxRows+1)
			}
			cell = padRight(cell, colW)
			if day == m.gcalDayIdx && row == m.gcalEventIdx {
				cell = selectedStyle.Render(truncate(cell, colW))
			} else {
				cell = truncate(cell, colW)
			}
			b.WriteString(cell)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderCalendarDay() string {
	day := startOfDay(m.gcalAnchor)
	label := day.Format("Monday, 02 Jan 2006")
	items := m.gcalEventsForDay(day)

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(label))
	b.WriteString("\n\n")
	if len(items) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("(no events)"))
		return b.String()
	}

	pageSize := m.listPageSize()
	if pageSize < 3 {
		pageSize = 3
	}
	offset := m.gcalOffset
	if offset < 0 {
		offset = 0
	}
	eventIdx := clampInt(m.gcalEventIdx, 0, max(0, len(items)-1))
	if eventIdx < offset {
		offset = eventIdx
	}
	if eventIdx >= offset+pageSize {
		offset = eventIdx - pageSize + 1
	}

	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	maxTitle := 80
	if m.width > 0 {
		maxTitle = max(20, m.width-10)
	}
	for i := offset; i < end; i++ {
		it := *items[i]
		cursor := "  "
		if i == eventIdx {
			cursor = "> "
		}
		line := cursor + truncate(gcalEventLine(it), maxTitle)
		b.WriteString(line)
		b.WriteString("\n")
		meta := gcalEventMeta(it)
		if meta != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("    " + truncate(meta, maxTitle)))
			b.WriteString("\n")
		}
	}
	if len(items) > end {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("…"))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) updateGcalKeys(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	switch s {
	case "w":
		m.gcalView = "week"
		m.gcalOffset = 0
		m.ensureGcalSelection()
		return nil
	case "d":
		// Move anchor to the selected day when switching to day view.
		weekStart := startOfWeek(m.gcalAnchor)
		m.gcalAnchor = weekStart.AddDate(0, 0, clampInt(m.gcalDayIdx, 0, 6))
		m.gcalView = "day"
		m.gcalOffset = 0
		m.ensureGcalSelection()
		return nil
	case "t":
		m.gcalAnchor = time.Now()
		if m.gcalView == "week" {
			weekStart := startOfWeek(m.gcalAnchor)
			m.gcalDayIdx = clampInt(int(startOfDay(time.Now()).Sub(weekStart).Hours()/24), 0, 6)
			m.gcalEventIdx = 0
		}
		m.gcalOffset = 0
		m.ensureGcalSelection()
		return nil
	case "left":
		if m.gcalView == "day" {
			m.gcalAnchor = m.gcalAnchor.AddDate(0, 0, -1)
		} else {
			m.gcalAnchor = m.gcalAnchor.AddDate(0, 0, -7)
		}
		m.gcalOffset = 0
		m.ensureGcalSelection()
		return nil
	case "right":
		if m.gcalView == "day" {
			m.gcalAnchor = m.gcalAnchor.AddDate(0, 0, 1)
		} else {
			m.gcalAnchor = m.gcalAnchor.AddDate(0, 0, 7)
		}
		m.gcalOffset = 0
		m.ensureGcalSelection()
		return nil
	case "h":
		if m.gcalView == "week" {
			m.gcalDayIdx = clampInt(m.gcalDayIdx-1, 0, 6)
			m.gcalEventIdx = 0
		} else {
			m.gcalAnchor = m.gcalAnchor.AddDate(0, 0, -1)
			m.gcalEventIdx = 0
			m.gcalOffset = 0
		}
		m.ensureGcalSelection()
		return nil
	case "l":
		if m.gcalView == "week" {
			m.gcalDayIdx = clampInt(m.gcalDayIdx+1, 0, 6)
			m.gcalEventIdx = 0
		} else {
			m.gcalAnchor = m.gcalAnchor.AddDate(0, 0, 1)
			m.gcalEventIdx = 0
			m.gcalOffset = 0
		}
		m.ensureGcalSelection()
		return nil
	case "[", "]", "/":
		// Account cycling.
		if len(m.gcalAccounts) == 0 {
			return nil
		}
		if s == "[" {
			m.gcalAccountIdx--
			if m.gcalAccountIdx < 0 {
				m.gcalAccountIdx = len(m.gcalAccounts) - 1
			}
		} else {
			m.gcalAccountIdx = (m.gcalAccountIdx + 1) % len(m.gcalAccounts)
		}
		m.reloadListFromState()
		m.ensureGcalSelection()
		return nil
	case "up", "k":
		if m.gcalView == "week" {
			if m.gcalEventIdx > 0 {
				m.gcalEventIdx--
			}
		} else {
			if m.gcalEventIdx > 0 {
				m.gcalEventIdx--
			}
		}
		m.ensureGcalSelection()
		return nil
	case "down", "j":
		m.gcalEventIdx++
		m.ensureGcalSelection()
		return nil
	case " ", "x":
		if it := m.gcalSelectedItem(); it != nil {
			m.listSelected[it.Key] = !m.listSelected[it.Key]
		}
		return nil
	case "X":
		// Select only what's currently visible.
		if m.gcalView == "day" {
			day := startOfDay(m.gcalAnchor)
			items := m.gcalEventsForDay(day)
			pageSize := m.listPageSize()
			start := m.gcalOffset
			end := start + pageSize
			if end > len(items) {
				end = len(items)
			}
			for i := start; i < end; i++ {
				m.listSelected[items[i].Key] = true
			}
		}
		return nil
	case "u":
		m.listSelected = map[string]bool{}
		return nil
	case "enter":
		it := m.gcalSelectedItem()
		if it == nil {
			return nil
		}
		_ = m.eventsWriter.Append(events.Event{
			Kind:     "view",
			Surface:  "gcal",
			Name:     "gcal.view",
			Op:       "view",
			Actor:    "user",
			Entities: entitiesForItem("gcal", *it),
			Summary:  truncate(it.Title, 120),
			Data:     it.Raw,
		})
		m.mode = modeDetail
		m.detailItem = it
		m.detailSurface = "gcal"
		m.detailText = ""
		m.detailLoading = false
		m.lastErr = ""
		cmd := m.fetchDetailForItem("gcal", *it)
		m.refreshDetailViewport(true)
		return cmd
	case "o":
		it := m.gcalSelectedItem()
		if it == nil {
			return nil
		}
		return m.openItem("gcal", *it)
	default:
		return nil
	}
}

func (m *Model) ensureGcalSelection() {
	if m.gcalView == "day" {
		day := startOfDay(m.gcalAnchor)
		items := m.gcalEventsForDay(day)
		if len(items) == 0 {
			m.gcalEventIdx = 0
			m.gcalOffset = 0
			return
		}
		if m.gcalEventIdx < 0 {
			m.gcalEventIdx = 0
		}
		if m.gcalEventIdx >= len(items) {
			m.gcalEventIdx = len(items) - 1
		}
		if m.gcalOffset < 0 {
			m.gcalOffset = 0
		}
		if m.gcalOffset > m.gcalEventIdx {
			m.gcalOffset = m.gcalEventIdx
		}
		pageSize := m.listPageSize()
		if pageSize < 3 {
			pageSize = 3
		}
		if m.gcalEventIdx >= m.gcalOffset+pageSize {
			m.gcalOffset = m.gcalEventIdx - pageSize + 1
		}
		if m.gcalOffset > max(0, len(items)-1) {
			m.gcalOffset = max(0, len(items)-1)
		}
		return
	}

	weekStart := startOfWeek(m.gcalAnchor)
	days := m.gcalWeekEvents(weekStart)
	if m.gcalDayIdx < 0 {
		m.gcalDayIdx = 0
	}
	if m.gcalDayIdx > 6 {
		m.gcalDayIdx = 6
	}
	items := days[m.gcalDayIdx]
	if len(items) == 0 {
		m.gcalEventIdx = 0
		return
	}
	if m.gcalEventIdx < 0 {
		m.gcalEventIdx = 0
	}
	if m.gcalEventIdx >= len(items) {
		m.gcalEventIdx = len(items) - 1
	}
}

func (m *Model) gcalSelectedItem() *listItem {
	if m.gcalView == "day" {
		day := startOfDay(m.gcalAnchor)
		items := m.gcalEventsForDay(day)
		if len(items) == 0 {
			return nil
		}
		idx := clampInt(m.gcalEventIdx, 0, len(items)-1)
		return items[idx]
	}
	weekStart := startOfWeek(m.gcalAnchor)
	days := m.gcalWeekEvents(weekStart)
	day := clampInt(m.gcalDayIdx, 0, 6)
	items := days[day]
	if len(items) == 0 {
		return nil
	}
	idx := clampInt(m.gcalEventIdx, 0, len(items)-1)
	return items[idx]
}

func (m Model) gcalWeekEvents(weekStart time.Time) [7][]*listItem {
	var out [7][]*listItem
	for i := range m.listItems {
		it := &m.listItems[i]
		startStr, _ := it.Raw["start"].(string)
		t, ok := parseISOOrDate(startStr)
		if !ok {
			continue
		}
		local := t.In(time.Local)
		for day := 0; day < 7; day++ {
			d := weekStart.AddDate(0, 0, day)
			if sameDay(local, d) {
				out[day] = append(out[day], it)
				break
			}
		}
	}
	for i := 0; i < 7; i++ {
		sort.SliceStable(out[i], func(a, b int) bool {
			as, _ := out[i][a].Raw["start"].(string)
			bs, _ := out[i][b].Raw["start"].(string)
			at, aok := parseISOOrDate(as)
			bt, bok := parseISOOrDate(bs)
			if aok && bok {
				return at.Before(bt)
			}
			return as < bs
		})
	}
	return out
}

func (m Model) gcalEventsForDay(day time.Time) []*listItem {
	var out []*listItem
	for i := range m.listItems {
		it := &m.listItems[i]
		startStr, _ := it.Raw["start"].(string)
		t, ok := parseISOOrDate(startStr)
		if !ok {
			continue
		}
		if sameDay(t.In(time.Local), day) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		as, _ := out[a].Raw["start"].(string)
		bs, _ := out[b].Raw["start"].(string)
		at, aok := parseISOOrDate(as)
		bt, bok := parseISOOrDate(bs)
		if aok && bok {
			return at.Before(bt)
		}
		return as < bs
	})
	return out
}

func gcalEventCell(it listItem, width int) string {
	startStr, _ := it.Raw["start"].(string)
	start, _ := parseISOOrDate(startStr)
	prefix := "▓"
	if !strings.Contains(startStr, "T") {
		prefix = "░"
	}
	timePart := start.In(time.Local).Format("15:04")
	if prefix == "░" {
		timePart = "all"
	}
	cell := fmt.Sprintf("%s %s %s", prefix, timePart, it.Title)
	return truncate(cell, width)
}

func gcalEventLine(it listItem) string {
	startStr, _ := it.Raw["start"].(string)
	endStr, _ := it.Raw["end"].(string)
	start, _ := parseISOOrDate(startStr)
	end, _ := parseISOOrDate(endStr)
	if strings.Contains(startStr, "T") {
		return start.In(time.Local).Format("15:04") + "–" + end.In(time.Local).Format("15:04") + "  " + it.Title
	}
	return "all-day  " + it.Title
}

func gcalEventMeta(it listItem) string {
	acct, _ := it.Raw["account"].(string)
	return acct
}

func startOfWeek(t time.Time) time.Time {
	local := t.In(time.Local)
	start := startOfDay(local)
	wd := int(start.Weekday())
	// Monday as start of week.
	delta := (wd + 6) % 7
	return start.AddDate(0, 0, -delta)
}

func startOfDay(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

func clampInt(v int, lo int, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func padRight(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}

func (m Model) listPageSize() int {
	budget := m.mainBudgetLines()
	// Each item is ~2 lines.
	rows := budget / 2
	if rows < 3 {
		rows = 3
	}
	// UX: keep lists compact by default.
	if rows > 10 {
		rows = 10
	}
	return rows
}

func (m Model) runPanelLines() int {
	if m.height <= 0 {
		return 12
	}
	lines := m.height / 4
	if lines < 6 {
		lines = 6
	}
	if lines > 12 {
		lines = 12
	}
	// Ensure we leave enough space for the main panel.
	minMain := 10
	fixed := 1 + 1 + 1 + 1 + 1 + 1 // header, tabs, status, blank, main/run blank, run header
	cmdLines := 0
	if m.cmdMode {
		cmdLines = 2
	}
	if m.height-(fixed+cmdLines+lines) < minMain {
		lines = m.height - (fixed + cmdLines + minMain)
	}
	if lines < 4 {
		lines = 4
	}
	return lines
}

func (m Model) mainBudgetLines() int {
	if m.height <= 0 {
		return 20
	}
	runLines := m.runPanelLines()
	// Header + tabs + status + blank + blank + run header.
	used := 1 + 1 + 1 + 1 + 1 + 1 + runLines
	if m.cmdMode {
		used += 2
	}
	budget := m.height - used
	if budget < 6 {
		budget = 6
	}
	return budget
}

func (m Model) renderDetail() string {
	if m.detailVP.Width > 0 && m.detailVP.Height > 0 {
		content := m.detailVP.View()
		surface := m.detailSurface
		if surface == "" {
			surface = m.activeSurface()
		}
		footer := m.renderDetailFooter(surface)
		if footer != "" {
			return content + "\n" + footer
		}
		return content
	}
	return m.renderDetailContent()
}

func (m Model) renderDetailFooter(surface string) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	switch surface {
	case "gmail":
		return style.Render("r reply  R reply-all  f forward  l label  a archive  o open")
	default:
		return style.Render("j/k scroll  o open  esc back")
	}
}

func (m Model) renderDetailContent() string {
	var b strings.Builder
	surface := m.detailSurface
	if surface == "" {
		surface = m.activeSurface()
	}
	name := strings.ToUpper(surface[:1]) + surface[1:]
	if surface == "gcal" {
		name = "Calendar"
	}
	if surface == "github" {
		name = "GitHub"
	}

	b.WriteString(lipgloss.NewStyle().Bold(true).Render(name))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  (Esc back · o open)"))
	b.WriteString("\n\n")

	if m.detailItem != nil {
		if surface == "gmail" {
			from, _ := m.detailItem.Raw["from"].(string)
			subject := m.detailItem.Title
			date, _ := m.detailItem.Raw["date"].(string)
			acct, _ := m.detailItem.Raw["account"].(string)

			b.WriteString(lipgloss.NewStyle().Bold(true).Render(subject))
			b.WriteString("\n")
			if from != "" {
				b.WriteString(renderLabelValue("From", from))
				b.WriteString("\n")
			}
			if acct != "" {
				b.WriteString(renderLabelValue("Account", acct))
				b.WriteString("\n")
			}
			if date != "" {
				b.WriteString(renderLabelValue("Date", date))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		} else {
			b.WriteString(lipgloss.NewStyle().Bold(true).Render(m.detailItem.Title))
			b.WriteString("\n")
			meta := strings.TrimSpace(strings.Trim(strings.Join([]string{m.detailItem.Subtitle, m.detailItem.Meta}, " · "), " ·"))
			if meta != "" {
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(meta))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	if m.detailLoading {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Loading…"))
		b.WriteString("\n")
		return b.String()
	}

	text := strings.TrimSpace(m.detailText)
	if text == "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("(no detail)"))
		b.WriteString("\n")
		return b.String()
	}

	w := m.width - 2
	if w < 20 {
		w = 20
	}

	switch surface {
	case "gmail":
		body := stripGmailThreadHeaders(text)
		body = normalizeNewlines(body)
		body = stripEmailSignature(body)
		body = normalizeBlankLines(body, 1)
		body = linkifyBareDomains(body)
		body = wrapPreserveURLs(body, w)
		b.WriteString(body)
		b.WriteString("\n")
		return b.String()
	case "linear":
		body := normalizeNewlines(text)
		body = normalizeBlankLines(body, 1)
		body = linkifyBareDomains(body)
		body = wrapPreserveURLs(body, w)
		b.WriteString(body)
		b.WriteString("\n")
		return b.String()
	case "gcal":
		body := normalizeNewlines(text)
		body = normalizeBlankLines(body, 1)
		body = formatGcalDetail(body, w)
		b.WriteString(body)
		b.WriteString("\n")
		return b.String()
	case "github":
		body := normalizeNewlines(text)
		body = normalizeBlankLines(body, 1)
		body = formatGithubDetail(body, w, m.detailItem)
		b.WriteString(body)
		b.WriteString("\n")
		return b.String()
	default:
		body := normalizeNewlines(text)
		body = normalizeBlankLines(body, 1)
		body = wrapPreserveURLs(body, w)
		b.WriteString(body)
		b.WriteString("\n")
		return b.String()
	}
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
	if surface == "gcal" {
		return m.updateGcalKeys(msg)
	}
	switch s {
	case "up", "k":
		if m.listCursor > 0 {
			m.listCursor--
		}
		m.ensureCursorVisible()
		return nil
	case "down", "j":
		if m.listCursor < len(m.listItems)-1 {
			m.listCursor++
		}
		m.ensureCursorVisible()
		return nil
	case " ", "x":
		if len(m.listItems) == 0 {
			return nil
		}
		key := m.listItems[m.listCursor].Key
		m.listSelected[key] = !m.listSelected[key]
		return nil
	case "X":
		pageSize := m.listPageSize()
		start := m.listOffset
		end := start + pageSize
		if start < 0 {
			start = 0
		}
		if end > len(m.listItems) {
			end = len(m.listItems)
		}
		for i := start; i < end; i++ {
			m.listSelected[m.listItems[i].Key] = true
		}
		return nil
	case "u":
		m.listSelected = map[string]bool{}
		return nil
	case "f":
		if surface == "linear" {
			m.linearFilterIdx = (m.linearFilterIdx + 1) % 4
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		if surface == "github" {
			m.githubFilterIdx = (m.githubFilterIdx + 1) % 4
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		return nil
	case "[":
		if surface == "gmail" && len(m.gmailAccounts) > 0 {
			m.gmailAccountIdx--
			if m.gmailAccountIdx < 0 {
				m.gmailAccountIdx = len(m.gmailAccounts) - 1
			}
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		if surface == "gcal" && len(m.gcalAccounts) > 0 {
			m.gcalAccountIdx--
			if m.gcalAccountIdx < 0 {
				m.gcalAccountIdx = len(m.gcalAccounts) - 1
			}
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		if surface == "github" && len(m.githubAccounts) > 0 {
			m.githubAccountIdx--
			if m.githubAccountIdx < 0 {
				m.githubAccountIdx = len(m.githubAccounts) - 1
			}
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		return nil
	case "]":
		if surface == "gmail" && len(m.gmailAccounts) > 0 {
			m.gmailAccountIdx = (m.gmailAccountIdx + 1) % len(m.gmailAccounts)
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		if surface == "gcal" && len(m.gcalAccounts) > 0 {
			m.gcalAccountIdx = (m.gcalAccountIdx + 1) % len(m.gcalAccounts)
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		if surface == "github" && len(m.githubAccounts) > 0 {
			m.githubAccountIdx = (m.githubAccountIdx + 1) % len(m.githubAccounts)
			m.reloadListFromState()
			m.ensureCursorVisible()
		}
		return nil
	case "/":
		// Quick UX: allow '/' to cycle the Gmail/Calendar account filter.
		if surface == "gmail" && len(m.gmailAccounts) > 0 {
			m.gmailAccountIdx = (m.gmailAccountIdx + 1) % len(m.gmailAccounts)
			m.reloadListFromState()
			m.ensureCursorVisible()
			return nil
		}
		if surface == "gcal" && len(m.gcalAccounts) > 0 {
			m.gcalAccountIdx = (m.gcalAccountIdx + 1) % len(m.gcalAccounts)
			m.reloadListFromState()
			m.ensureCursorVisible()
			return nil
		}
		if surface == "github" && len(m.githubAccounts) > 0 {
			m.githubAccountIdx = (m.githubAccountIdx + 1) % len(m.githubAccounts)
			m.reloadListFromState()
			m.ensureCursorVisible()
			return nil
		}
		return nil
	case "enter":
		if len(m.listItems) == 0 {
			return nil
		}
		it := m.listItems[m.listCursor]
		_ = m.eventsWriter.Append(events.Event{
			Kind:     "view",
			Surface:  surface,
			Name:     surface + ".view",
			Op:       "view",
			Actor:    "user",
			Entities: entitiesForItem(surface, it),
			Summary:  truncate(it.Title, 120),
			Data:     it.Raw,
		})
		m.mode = modeDetail
		m.detailItem = &it
		m.detailSurface = surface
		m.detailText = ""
		m.detailLoading = false
		m.lastErr = ""
		cmd := m.fetchDetailForItem(surface, it)
		m.refreshDetailViewport(true)
		return cmd
	case "o":
		if len(m.listItems) == 0 {
			return nil
		}
		it := m.listItems[m.listCursor]
		return m.openItem(surface, it)
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

func (m *Model) updateDetailKeys(msg tea.KeyMsg) tea.Cmd {
	surface := m.detailSurface
	if surface == "" {
		surface = m.activeSurface()
	}

	s := msg.String()
	switch s {
	case "o":
		if m.detailItem == nil {
			m.lastErr = "no item selected"
			return nil
		}
		return m.openItem(surface, *m.detailItem)
	case "a":
		if surface == "gmail" {
			if m.detailItem == nil {
				m.lastErr = "no item selected"
				return nil
			}
			m.listSelected = map[string]bool{m.detailItem.Key: true}
			opts := m.actionOptionsForSurface("gmail")
			if len(opts) == 0 {
				m.lastErr = "no actions"
				return nil
			}
			m.actionOptions = opts
			m.actionCursor = 0
			m.mode = modeActionPicker
			return nil
		}
		return nil
	case "r", "R", "f", "l":
		if surface == "gmail" {
			m.lastInfo = "not implemented yet"
			return nil
		}
	case "g":
		m.detailVP.GotoTop()
		return nil
	case "G":
		m.detailVP.GotoBottom()
		return nil
	}
	// Vim-style scrolling.
	if s == "j" {
		msg = tea.KeyMsg{Type: tea.KeyDown}
	}
	if s == "k" {
		msg = tea.KeyMsg{Type: tea.KeyUp}
	}

	var cmd tea.Cmd
	m.detailVP, cmd = m.detailVP.Update(msg)
	return cmd
}

func (m *Model) resizeDetailViewport() {
	if m.mode != modeDetail {
		return
	}
	w := m.width - 2
	if w < 20 {
		w = 20
	}
	h := m.mainBudgetLines()
	// Leave room for the fixed footer bar.
	if h > 1 {
		h = h - 1
	}
	if h < 6 {
		h = 6
	}
	m.detailVP.Width = w
	m.detailVP.Height = h
}

func (m *Model) refreshDetailViewport(resetTop bool) {
	if m.mode != modeDetail {
		return
	}
	m.resizeDetailViewport()
	m.detailVP.SetContent(m.renderDetailContent())
	if resetTop {
		m.detailVP.GotoTop()
	}
}

func (m *Model) openItem(surface string, it listItem) tea.Cmd {
	m.pendingOpenSurface = surface
	m.pendingOpenSummary = truncate(it.Title, 120)
	m.pendingOpenEntities = entitiesForItem(surface, it)
	switch surface {
	case "gmail":
		threadID, _ := it.Raw["id"].(string)
		acct, _ := it.Raw["account"].(string)
		if acct == "" || threadID == "" {
			m.pendingOpenSurface = ""
			m.pendingOpenSummary = ""
			m.pendingOpenEntities = nil
			m.lastErr = "missing gmail thread"
			return nil
		}
		return openGmailThreadURLCmd(acct, threadID)
	case "linear":
		identifier, _ := it.Raw["identifier"].(string)
		if identifier == "" {
			return openURLCmd("https://linear.app")
		}
		u := "https://linear.app/search?query=" + url.QueryEscape(identifier)
		return openURLCmd(u)
	case "gcal":
		eventID, _ := it.Raw["id"].(string)
		acct, _ := it.Raw["account"].(string)
		calID, _ := it.Raw["calendar_id"].(string)
		if calID == "" {
			calID = m.gcalCalendarID
		}
		if calID == "" {
			calID = "primary"
		}
		if acct == "" || eventID == "" {
			m.pendingOpenSurface = ""
			m.pendingOpenSummary = ""
			m.pendingOpenEntities = nil
			m.lastErr = "missing calendar event"
			return nil
		}
		return openGcalEventURLCmd(acct, calID, eventID)
	case "github":
		if u, ok := it.Raw["url"].(string); ok && u != "" {
			return openURLCmd(u)
		}
		if it.Meta != "" {
			return openURLCmd(it.Meta)
		}
		m.pendingOpenSurface = ""
		m.pendingOpenSummary = ""
		m.pendingOpenEntities = nil
		m.lastErr = "no url"
		return nil
	default:
		m.pendingOpenSurface = ""
		m.pendingOpenSummary = ""
		m.pendingOpenEntities = nil
		return nil
	}
}

func openURLCmd(u string) tea.Cmd {
	return func() tea.Msg {
		u = strings.TrimSpace(u)
		if u == "" {
			return openURLMsg{err: errors.New("no url")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.CommandContext(ctx, "open", u)
		default:
			cmd = exec.CommandContext(ctx, "xdg-open", u)
		}
		err := cmd.Run()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		return openURLMsg{url: u, err: err}
	}
}

func openGmailThreadURLCmd(account string, threadID string) tea.Cmd {
	return func() tea.Msg {
		if account == "" || threadID == "" {
			return openURLMsg{err: errors.New("missing gmail account or thread id")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gmcli", account, "url", threadID)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return openURLMsg{err: fmt.Errorf("gmcli url: %w (%s)", err, strings.TrimSpace(string(out)))}
		}
		line := strings.TrimSpace(string(out))
		parts := strings.Split(line, "\t")
		u := ""
		if len(parts) >= 2 {
			u = strings.TrimSpace(parts[len(parts)-1])
		}
		if u == "" {
			return openURLMsg{err: errors.New("could not parse gmail url")}
		}
		return openURLCmd(u)()
	}
}

func openGcalEventURLCmd(account string, calendarID string, eventID string) tea.Cmd {
	return func() tea.Msg {
		if account == "" || eventID == "" {
			return openURLMsg{err: errors.New("missing calendar account or event id")}
		}
		if calendarID == "" {
			calendarID = "primary"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gccli", account, "event", calendarID, eventID)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return openURLMsg{err: fmt.Errorf("gccli event: %w (%s)", err, strings.TrimSpace(string(out)))}
		}
		link := extractFirstKVValue(string(out), "Link")
		if link == "" {
			return openURLMsg{err: errors.New("no Link found in event")}
		}
		return openURLCmd(link)()
	}
}

func entitiesForItem(surface string, it listItem) []events.EventEntity {
	switch surface {
	case "gmail":
		threadID, _ := it.Raw["id"].(string)
		acct, _ := it.Raw["account"].(string)
		id := threadID
		if acct != "" {
			id = acct + ":" + threadID
		}
		if id == "" {
			return nil
		}
		return []events.EventEntity{{Type: "email_thread", ID: id}}
	case "linear":
		identifier, _ := it.Raw["identifier"].(string)
		if identifier == "" {
			identifier, _ = it.Raw["id"].(string)
		}
		if identifier == "" {
			return nil
		}
		return []events.EventEntity{{Type: "linear_issue", ID: identifier}}
	case "gcal":
		id, _ := it.Raw["id"].(string)
		acct, _ := it.Raw["account"].(string)
		key := id
		if acct != "" {
			key = acct + ":" + id
		}
		if key == "" {
			return nil
		}
		return []events.EventEntity{{Type: "calendar_event", ID: key}}
	case "github":
		kind, _ := it.Raw["kind"].(string)
		repo, _ := it.Raw["repo"].(string)
		num := intFromAny(it.Raw["number"])
		if repo == "" || num <= 0 {
			return nil
		}
		t := "github_item"
		if kind == "pr" {
			t = "github_pr"
		}
		if kind == "issue" {
			t = "github_issue"
		}
		return []events.EventEntity{{Type: t, ID: fmt.Sprintf("%s#%d", repo, num)}}
	default:
		return nil
	}
}

func firstNonEmpty(a string, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
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
	case "gcal":
		eventID, _ := it.Raw["id"].(string)
		acct, _ := it.Raw["account"].(string)
		calID, _ := it.Raw["calendar_id"].(string)
		if calID == "" {
			calID = m.gcalCalendarID
		}
		if calID == "" {
			calID = "primary"
		}
		if eventID == "" || acct == "" {
			m.detailText = prettyJSON(it.Raw)
			return nil
		}
		m.detailLoading = true
		return fetchGcalEventCmd(acct, calID, eventID)
	case "github":
		kind, _ := it.Raw["kind"].(string)
		repo, _ := it.Raw["repo"].(string)
		num := intFromAny(it.Raw["number"])
		if kind == "" || repo == "" || num <= 0 {
			m.detailText = prettyJSON(it.Raw)
			return nil
		}
		m.detailLoading = true
		return fetchGithubDetailCmd(kind, repo, num)
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

func fetchGcalEventCmd(account string, calendarID string, eventID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gccli", account, "event", calendarID, eventID)
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		return detailLoadedMsg{text: string(out), err: err}
	}
}

func fetchGithubDetailCmd(kind string, repo string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var cmd *exec.Cmd
		if kind == "issue" {
			cmd = exec.CommandContext(ctx, "gh", "issue", "view", fmt.Sprintf("%d", number), "-R", repo, "--json", "title,body,state,url,author,updatedAt")
		} else {
			cmd = exec.CommandContext(ctx, "gh", "pr", "view", fmt.Sprintf("%d", number), "-R", repo, "--json", "title,body,state,url,author,updatedAt")
		}
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

func (m *Model) ensureCursorVisible() {
	pageSize := m.listPageSize()
	if pageSize <= 0 {
		pageSize = 8
	}
	if m.listCursor < m.listOffset {
		m.listOffset = m.listCursor
		return
	}
	if m.listCursor >= m.listOffset+pageSize {
		m.listOffset = m.listCursor - pageSize + 1
		return
	}
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

func readGithubTrackedConfig(skillPath string) ([]string, []string, error) {
	front, _, err := readFrontmatter(skillPath)
	if err != nil {
		return nil, nil, err
	}
	repos := anyToStringSlice(front["tracked_repos"])
	locals := anyToStringSlice(front["tracked_local"])
	sort.Strings(repos)
	sort.Strings(locals)
	return uniqueStrings(repos), uniqueStrings(locals), nil
}

func updateGithubTrackedConfig(skillPath string, addRepos []string, removeRepos []string, addLocal []string, removeLocal []string) error {
	front, body, err := readFrontmatter(skillPath)
	if err != nil {
		return err
	}

	repos := stringSliceToSet(anyToStringSlice(front["tracked_repos"]))
	locals := stringSliceToSet(anyToStringSlice(front["tracked_local"]))
	for _, r := range addRepos {
		r = strings.TrimSpace(r)
		if r != "" {
			repos[r] = true
		}
	}
	for _, r := range removeRepos {
		r = strings.TrimSpace(r)
		if r != "" {
			delete(repos, r)
		}
	}
	for _, p := range addLocal {
		p = strings.TrimSpace(p)
		if p != "" {
			locals[p] = true
		}
	}
	for _, p := range removeLocal {
		p = strings.TrimSpace(p)
		if p != "" {
			delete(locals, p)
		}
	}

	front["tracked_repos"] = setToSortedSlice(repos)
	if len(locals) > 0 {
		front["tracked_local"] = setToSortedSlice(locals)
	} else {
		delete(front, "tracked_local")
	}

	frontYAML, err := yaml.Marshal(front)
	if err != nil {
		return err
	}

	newContent := "---\n" + string(frontYAML) + "---\n" + body
	if err := os.WriteFile(skillPath, []byte(newContent), 0o644); err != nil {
		return err
	}
	return nil
}

func readFrontmatter(path string) (map[string]any, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	content := string(b)
	lines := strings.Split(normalizeNewlines(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, content, errors.New("missing frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, content, errors.New("unterminated frontmatter")
	}
	fmText := strings.Join(lines[1:end], "\n")
	body := "\n" + strings.Join(lines[end+1:], "\n")
	front := map[string]any{}
	if err := yaml.Unmarshal([]byte(fmText), &front); err != nil {
		return nil, content, err
	}
	return front, body, nil
}

func anyToStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string{}, t...)
	case []any:
		var out []string
		for _, it := range t {
			s, ok := it.(string)
			if ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case nil:
		return nil
	default:
		return nil
	}
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func stringSliceToSet(in []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out[s] = true
		}
	}
	return out
}

func setToSortedSlice(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func resolveGithubRepoFromLocalPath(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "remote", "get-url", "origin")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	remote := strings.TrimSpace(string(out))
	remote = strings.TrimSuffix(remote, ".git")
	if strings.HasPrefix(remote, "git@github.com:") {
		return strings.TrimPrefix(remote, "git@github.com:"), nil
	}
	if strings.HasPrefix(remote, "https://github.com/") {
		return strings.TrimPrefix(remote, "https://github.com/"), nil
	}
	if strings.HasPrefix(remote, "ssh://git@github.com/") {
		return strings.TrimPrefix(remote, "ssh://git@github.com/"), nil
	}
	return "", fmt.Errorf("unsupported remote url: %s", remote)
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
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

func parseISOOrDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func humanDeltaFromISO(s string) string {
	t, ok := parseISOOrDate(s)
	if !ok {
		return "-"
	}
	d := time.Until(t)
	if d < 0 {
		return humanAge(-d) + " ago"
	}
	return "in " + humanAge(d)
}

func formatEventWhen(s string) string {
	t, ok := parseISOOrDate(s)
	if !ok {
		return ""
	}
	local := t.In(time.Local)
	now := time.Now().In(time.Local)
	// Heuristic: date-only strings are all-day.
	if len(s) == 10 {
		return local.Format("Mon 02 Jan") + " (all-day)"
	}
	if sameDay(now, local) {
		return "Today " + local.Format("15:04")
	}
	if sameDay(now.Add(24*time.Hour), local) {
		return "Tomorrow " + local.Format("15:04")
	}
	return local.Format("Mon 02 Jan 15:04")
}

func sameDay(a, b time.Time) bool {
	ay, ad := a.Year(), a.YearDay()
	by, bd := b.Year(), b.YearDay()
	return ay == by && ad == bd
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func normalizeNewlines(s string) string {
	// gmcli/gccli output can include CRLF; normalize so wrapping and blank-line collapsing is stable.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func normalizeBlankLines(s string, maxBlank int) string {
	if maxBlank < 0 {
		maxBlank = 0
	}
	lines := strings.Split(normalizeNewlines(s), "\n")
	var out []string
	blankRun := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun <= maxBlank {
				out = append(out, "")
			}
			continue
		}
		blankRun = 0
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripEmailSignature(s string) string {
	lines := strings.Split(normalizeNewlines(s), "\n")
	cut := -1
	barRE := regexp.MustCompile(`^\|{4,}$`)
	underscoreRE := regexp.MustCompile(`^_{4,}$`)
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if l == "--" || l == "-- " || l == "—" || l == "—-" {
			cut = i
			break
		}
		if strings.HasPrefix(strings.ToLower(l), "sent from my") {
			cut = i
			break
		}
		if strings.Contains(strings.ToLower(l), "unsubscribe") {
			cut = i
			break
		}
		if barRE.MatchString(l) || underscoreRE.MatchString(l) {
			cut = i
			break
		}
		if strings.Count(l, "|") >= 6 {
			cut = i
			break
		}
	}
	if cut > 0 {
		lines = lines[:cut]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func linkifyBareDomains(s string) string {
	// Lightweight linkify so macOS terminals make common domains clickable.
	// We intentionally avoid touching emails, and we don't rewrite http(s) URLs.
	s = normalizeNewlines(s)
	// Match domains that are preceded by start/whitespace/open-paren and not part of an email.
	re := regexp.MustCompile(`(?i)(^|[\s(\[])([a-z0-9][a-z0-9.-]+\.[a-z]{2,}(?:/[^\s)\]]*)?)`)
	return re.ReplaceAllString(s, `${1}https://${2}`)
}

func wrapPreserveURLs(text string, width int) string {
	if width <= 0 {
		return text
	}
	text = normalizeNewlines(text)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		indent := leadingWhitespace(line)
		words := strings.Fields(strings.TrimSpace(line))
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur := indent
		curLen := runeLen(cur)
		limit := width
		if limit < curLen+10 {
			limit = curLen + 10
		}
		for _, w := range words {
			wLen := runeLen(w)
			if curLen == runeLen(indent) {
				cur += w
				curLen += wLen
				continue
			}
			if curLen+1+wLen > limit {
				out = append(out, cur)
				cur = indent + w
				curLen = runeLen(indent) + wLen
				continue
			}
			cur += " " + w
			curLen += 1 + wLen
		}
		out = append(out, cur)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func leadingWhitespace(s string) string {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return s[:i]
		}
	}
	return s
}

func runeLen(s string) int {
	return len([]rune(s))
}

func extractFirstKVValue(text string, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(normalizeNewlines(text), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(l, prefix))
		}
	}
	return ""
}

func formatGcalDetail(text string, width int) string {
	// Prefer a clean key/value view with the Link on its own line for cmd+click.
	lines := strings.Split(normalizeNewlines(text), "\n")
	var out []string
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	joined := strings.Join(out, "\n")
	joined = normalizeBlankLines(joined, 1)
	joined = linkifyBareDomains(joined)
	return wrapPreserveURLs(joined, width)
}

func formatGithubDetail(text string, width int, it *listItem) string {
	var v map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &v); err != nil {
		return wrapPreserveURLs(text, width)
	}

	title, _ := v["title"].(string)
	body, _ := v["body"].(string)
	state, _ := v["state"].(string)
	urlStr, _ := v["url"].(string)
	updatedAt, _ := v["updatedAt"].(string)
	author := ""
	if am, ok := v["author"].(map[string]any); ok {
		author, _ = am["login"].(string)
	}
	repo := ""
	if it != nil {
		repo, _ = it.Raw["repo"].(string)
	}

	var b strings.Builder
	if title != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
		b.WriteString("\n")
	}
	meta := strings.TrimSpace(strings.Trim(strings.Join([]string{repo, state, author}, " · "), " ·"))
	if updatedAt != "" {
		meta = strings.TrimSpace(strings.Trim(strings.Join([]string{meta, "updated " + humanAgeFromISO(updatedAt)}, " · "), " ·"))
	}
	if meta != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(meta))
		b.WriteString("\n")
	}
	if urlStr != "" {
		b.WriteString(renderLabelValue("URL", urlStr))
		b.WriteString("\n")
	}

	if strings.TrimSpace(body) != "" {
		b.WriteString("\n")
		body = normalizeNewlines(body)
		body = normalizeBlankLines(body, 1)
		body = linkifyBareDomains(body)
		b.WriteString(wrapPreserveURLs(body, width))
		b.WriteString("\n")
	}

	// Raw JSON (code-like)
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Raw"))
	b.WriteString("\n")
	raw := prettyJSON(v)
	b.WriteString(raw)
	return strings.TrimSpace(b.String())
}

func renderLabelValue(label string, value string) string {
	labelStyle := lipgloss.NewStyle().Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	return labelStyle.Render(label+":") + " " + valueStyle.Render(value)
}

func stripGmailThreadHeaders(text string) string {
	lines := strings.Split(text, "\n")
	// Drop a leading header block emitted by gmcli thread.
	// We keep everything after the first blank line that follows known header fields.
	seen := false
	start := 0
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "Message-ID") || strings.HasPrefix(l, "From:") || strings.HasPrefix(l, "To:") || strings.HasPrefix(l, "Date:") || strings.HasPrefix(l, "Subject:") {
			seen = true
			continue
		}
		if seen && l == "" {
			start = i + 1
			break
		}
		if i > 30 {
			break
		}
	}
	if start > 0 && start < len(lines) {
		return strings.TrimSpace(strings.Join(lines[start:], "\n"))
	}
	return strings.TrimSpace(text)
}

func hardWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		r := []rune(line)
		if len(r) <= width {
			out = append(out, line)
			continue
		}
		for len(r) > width {
			out = append(out, string(r[:width]))
			r = r[width:]
		}
		if len(r) > 0 {
			out = append(out, string(r))
		}
	}
	return strings.Join(out, "\n")
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
	if m.homeVP.Width > 0 && m.homeVP.Height > 0 {
		return m.homeVP.View()
	}
	return m.renderHomeContent()
}

func (m *Model) renderHomeContent() string {
	var out strings.Builder
	out.WriteString(lipgloss.NewStyle().Bold(true).Render("Home"))
	out.WriteString("\n")
	out.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("j/k scroll · Tab switch apps · ':' commands"))
	out.WriteString("\n\n")
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
	return strings.TrimRight(out.String(), "\n")
}

func (m *Model) resizeHomeViewport() {
	if m.mode != modeHome {
		return
	}
	w := m.width - 2
	if w < 20 {
		w = 20
	}
	h := m.mainBudgetLines()
	if h < 6 {
		h = 6
	}
	m.homeVP.Width = w
	m.homeVP.Height = h
}

func (m *Model) refreshHomeViewport(resetTop bool) {
	if m.mode != modeHome {
		return
	}
	m.resizeHomeViewport()
	m.homeVP.SetContent(m.renderHomeContent())
	if resetTop {
		m.homeVP.GotoTop()
	}
}

func (m *Model) updateHomeKeys(msg tea.KeyMsg) tea.Cmd {
	s := msg.String()
	switch s {
	case "g":
		m.homeVP.GotoTop()
		return nil
	case "G":
		m.homeVP.GotoBottom()
		return nil
	}
	if s == "j" {
		msg = tea.KeyMsg{Type: tea.KeyDown}
	}
	if s == "k" {
		msg = tea.KeyMsg{Type: tea.KeyUp}
	}
	var cmd tea.Cmd
	m.homeVP, cmd = m.homeVP.Update(msg)
	return cmd
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
		// Prefer actionable items (PRs/issues) over auth metadata.
		if arr, ok := obj["items"].([]any); ok {
			return arr
		}
		if arr, ok := obj["notifications"].([]any); ok {
			return arr
		}
		if arr, ok := obj["accounts"].([]any); ok {
			return arr
		}
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
	if len(fields) == 0 {
		m.lastErr = "empty command"
		return nil
	}
	if fields[0] == "github" && len(fields) >= 2 {
		skillPath := filepath.Join(m.posDir, "skills", "github", "SKILL.md")
		sub := fields[1]
		switch sub {
		case "track":
			if len(fields) < 3 {
				m.lastErr = "usage: github track <owner/repo>"
				return nil
			}
			repo := fields[2]
			if err := updateGithubTrackedConfig(skillPath, []string{repo}, nil, nil, nil); err != nil {
				m.lastErr = err.Error()
				return nil
			}
			m.lastInfo = "tracked: " + repo + " (run :github sync)"
			return nil
		case "untrack":
			if len(fields) < 3 {
				m.lastErr = "usage: github untrack <owner/repo>"
				return nil
			}
			repo := fields[2]
			if err := updateGithubTrackedConfig(skillPath, nil, []string{repo}, nil, nil); err != nil {
				m.lastErr = err.Error()
				return nil
			}
			m.lastInfo = "untracked: " + repo + " (run :github sync)"
			return nil
		case "track-local":
			if len(fields) < 3 {
				m.lastErr = "usage: github track-local <path>"
				return nil
			}
			path := fields[2]
			repo, err := resolveGithubRepoFromLocalPath(path)
			if err != nil {
				m.lastErr = err.Error()
				return nil
			}
			if err := updateGithubTrackedConfig(skillPath, []string{repo}, nil, []string{path}, nil); err != nil {
				m.lastErr = err.Error()
				return nil
			}
			m.lastInfo = "tracked local: " + path + " → " + repo + " (run :github sync)"
			return nil
		case "untrack-local":
			if len(fields) < 3 {
				m.lastErr = "usage: github untrack-local <path>"
				return nil
			}
			path := fields[2]
			if err := updateGithubTrackedConfig(skillPath, nil, nil, nil, []string{path}); err != nil {
				m.lastErr = err.Error()
				return nil
			}
			m.lastInfo = "untracked local: " + path + " (run :github sync)"
			return nil
		case "tracked":
			repos, locals, err := readGithubTrackedConfig(skillPath)
			if err != nil {
				m.lastErr = err.Error()
				return nil
			}
			m.lastInfo = fmt.Sprintf("tracked repos: %d · local: %d", len(repos), len(locals))
			return nil
		}
	}
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
