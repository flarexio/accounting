package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/stoa/llm"

	"github.com/flarexio/accounting/bookkeeping"
)

type viewState int

const (
	stateSelect viewState = iota // choosing an agent + scenario
	stateChat                    // conversing with a chosen session
)

type lineKind int

const (
	lineUser lineKind = iota
	lineModel
	lineValidation
	lineExecution
	lineObservation
	lineSystem
	lineError
	lineTool
	linePreview
)

type line struct {
	kind lineKind
	text string
}

type sessionReadyMsg struct {
	label   string
	session Session
	err     error
}

type model struct {
	ctx     context.Context
	options []Option

	state  viewState
	cursor int // selection index on the start screen

	session Session
	label   string

	viewport viewport.Model
	input    textinput.Model
	spinner  spinner.Model
	md       *glamour.TermRenderer // Markdown renderer for lineModel output
	mdWidth  int                   // wrap width md was built for

	lines   []line
	running bool
	cancel  context.CancelFunc
	events  chan llm.CycleEvent
	done    chan turnDoneMsg
	turn    int

	width  int
	height int
	ready  bool
	err    error
}

func newModel(ctx context.Context, options []Option) model {
	ti := textinput.New()
	ti.Placeholder = "Type a request and press Enter"
	ti.Prompt = "> "

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		ctx:      ctx,
		options:  options,
		state:    stateSelect,
		input:    ti,
		spinner:  sp,
		viewport: viewport.New(),
	}
}

func (m model) Init() tea.Cmd {
	if len(m.options) == 1 {
		return tea.Batch(textinput.Blink, m.startSession(m.options[0]))
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.layout()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case sessionReadyMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.session = msg.session
		m.label = msg.label
		m.state = stateChat
		m.lines = nil
		m.turn = 0
		m.input.Focus()
		m.layout()
		return m, textinput.Blink

	case eventMsg:
		m.appendEvent(llm.CycleEvent(msg))
		return m, waitForTurn(m.events, m.done)

	case turnDoneMsg:
		return m.finishTurn(msg)

	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	if m.state == stateChat && !m.running {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.running && m.cancel != nil {
			m.cancel() // cancel the in-flight turn, but keep the program open
			return m, nil
		}
		return m, tea.Quit
	}

	switch m.state {
	case stateSelect:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter":
			return m, m.startSession(m.options[m.cursor])
		case "q", "esc":
			return m, tea.Quit
		}
		return m, nil

	case stateChat:
		switch msg.String() {
		case "esc":
			if m.running {
				return m, nil
			}
			if m.session != nil {
				_ = m.session.Close()
				m.session = nil
			}
			m.state = stateSelect
			return m, nil
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "enter":
			if m.running {
				return m, nil
			}
			request := strings.TrimSpace(m.input.Value())
			if request == "" {
				return m, nil
			}
			m.input.Reset()
			return m.startTurn(request)
		}
		if !m.running {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}

// startSession composes the session off the update loop; Start may do I/O.
func (m model) startSession(opt Option) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		session, err := opt.Start(ctx)
		return sessionReadyMsg{label: opt.Label, session: session, err: err}
	}
}

// startTurn runs the agent in a goroutine; cycle events stream back through chanSink.
func (m model) startTurn(request string) (tea.Model, tea.Cmd) {
	m.turn++
	m.appendLine(line{kind: lineUser, text: request})

	turnCtx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel

	events := make(chan llm.CycleEvent)
	done := make(chan turnDoneMsg, 1)
	m.events = events
	m.done = done
	m.running = true

	session := m.session
	go func() {
		outcome, err := session.Run(turnCtx, request, chanSink{events: events})
		done <- turnDoneMsg{outcome: outcome, err: err}
		close(events)
	}()

	return m, tea.Batch(waitForTurn(events, done), m.spinner.Tick)
}

func (m model) finishTurn(msg turnDoneMsg) (tea.Model, tea.Cmd) {
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.events = nil
	m.done = nil

	switch {
	case msg.err != nil && errors.Is(msg.err, context.Canceled):
		m.appendLine(line{kind: lineSystem, text: "turn cancelled"})
	case msg.err != nil:
		m.appendLine(line{kind: lineError, text: msg.err.Error()})
	case msg.outcome.Summary != "":
		m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("done in %d turn(s) — %s", msg.outcome.Turns, msg.outcome.Summary)})
	default:
		m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("done in %d turn(s)", msg.outcome.Turns)})
	}
	return m, nil
}

func (m *model) appendEvent(ev llm.CycleEvent) {
	m.appendLine(line{kind: eventLineKind(ev.Kind), text: strings.TrimSpace(ev.Content)})
	if ev.Kind != llm.EventModelOutput {
		return
	}
	intent, ok := parseIntent(ev.Content)
	if !ok {
		return
	}
	switch intent.Kind {
	case bookkeeping.IntentPostJournal:
		if intent.Post != nil {
			m.appendLine(line{kind: linePreview, text: renderJournalPreview(intent.Post, m.accountNameResolver())})
		}
	case bookkeeping.IntentReverseJournal:
		if intent.Reverse != nil {
			m.appendReversePreview(*intent.Reverse)
		}
	}
}

func (m *model) appendReversePreview(intent bookkeeping.ReverseIntent) {
	lookup, ok := m.session.(EntryLookup)
	if !ok || intent.EntryID == "" {
		return
	}
	entry, found, err := lookup.LookupEntry(m.ctx, intent.EntryID)
	if err != nil || !found {
		return
	}
	m.appendLine(line{kind: linePreview, text: renderReversePreview(entry, intent.Reason, m.accountNameResolver())})
}

func (m model) accountNameResolver() accountNameFn {
	lookup, ok := m.session.(AccountLookup)
	if !ok {
		return nil
	}
	ctx := m.ctx
	return func(code string) string {
		acc, found, err := lookup.LookupAccount(ctx, code)
		if err != nil || !found {
			return ""
		}
		return acc.Name
	}
}

func (m *model) appendLine(l line) {
	m.lines = append(m.lines, l)
	m.viewport.SetContent(m.renderTranscript())
	m.viewport.GotoBottom()
}

func (m *model) layout() {
	if !m.ready {
		return
	}
	m.input.SetWidth(max(m.width-6, 10))
	// header (1) + blank (1) + status (1) + input (1) + footer (1) + margins.
	h := max(m.height-7, 3)
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(h)
	m.ensureMarkdown()
	m.viewport.SetContent(m.renderTranscript())
	m.viewport.GotoBottom()
}

// ensureMarkdown rebuilds the Glamour renderer when wrap width changes;
// a build failure leaves md nil and renderBody falls back to plain text.
func (m *model) ensureMarkdown() {
	width := max(m.viewport.Width()-2, 20)
	if m.md != nil && width == m.mdWidth {
		return
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		m.md = nil
		return
	}
	m.md, m.mdWidth = r, width
}

func eventLineKind(k llm.EventKind) lineKind {
	switch k {
	case llm.EventModelOutput:
		return lineModel
	case llm.EventValidationError:
		return lineValidation
	case llm.EventExecutionError:
		return lineExecution
	case llm.EventObservation:
		return lineObservation
	case llm.EventToolResult:
		return lineTool
	default:
		return lineSystem
	}
}

func lineMeta(k lineKind) (string, lipgloss.Style) {
	switch k {
	case lineUser:
		return "you", userStyle
	case lineModel:
		return "model", modelStyle
	case lineValidation:
		return "rejected", validationStyle
	case lineExecution:
		return "exec error", executionStyle
	case lineObservation:
		return "observation", observationStyle
	case lineTool:
		return "tool", toolStyle
	case linePreview:
		return "preview", previewStyle
	case lineError:
		return "error", errorStyle
	default:
		return "·", systemStyle
	}
}

func (m model) renderTranscript() string {
	if len(m.lines) == 0 {
		return hintStyle.Render("No turns yet. Type a request below to start.")
	}
	width := max(m.viewport.Width()-2, 20)
	var b strings.Builder
	for i, l := range m.lines {
		if i > 0 {
			b.WriteString("\n\n")
		}
		label, style := lineMeta(l.kind)
		b.WriteString(style.Render(label))
		b.WriteString("\n")
		b.WriteString(m.renderBody(l, width))
	}
	return b.String()
}

// renderBody renders lineModel through Glamour Markdown; other kinds stay literal.
func (m model) renderBody(l line, width int) string {
	if l.kind == lineModel && m.md != nil {
		if out, err := m.md.Render(l.text); err == nil {
			return strings.Trim(out, "\n")
		}
	}
	if l.kind == linePreview {
		return l.text
	}
	return lipgloss.NewStyle().Width(width).Render(l.text)
}

func (m model) View() tea.View {
	var content string
	switch {
	case !m.ready:
		content = "Starting accounting TUI..."
	case m.err != nil:
		content = errorStyle.Render("error: "+m.err.Error()) + "\n\n" +
			footerStyle.Render("ctrl+c quit")
	case m.state == stateSelect && len(m.options) == 1:
		content = "Connecting to ledger..."
	case m.state == stateSelect:
		content = m.selectView()
	default:
		content = m.chatView()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m model) selectView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Accounting - choose your working branch"))
	b.WriteString("\n\n")
	for i, opt := range m.options {
		cursor := "  "
		label := opt.Label
		if i == m.cursor {
			cursor = cursorStyle.Render("> ")
			label = selectedStyle.Render(label)
		}
		b.WriteString(cursor)
		b.WriteString(label)
		if opt.Hint != "" {
			b.WriteString("  ")
			b.WriteString(hintStyle.Render(opt.Hint))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(footerStyle.Render("↑/↓ move · enter start · q quit"))
	return b.String()
}

func (m model) chatView() string {
	header := headerStyle.Render(m.label)
	if m.turn > 0 {
		header += hintStyle.Render(fmt.Sprintf("  ·  turn %d", m.turn))
	}

	status := " "
	if m.running {
		status = m.spinner.View() + " running… (ctrl+c cancels this turn)"
	}

	footer := "enter send · pgup/pgdn scroll · esc back · ctrl+c quit"
	if m.running {
		footer = "ctrl+c cancel turn"
	}

	return strings.Join([]string{
		header,
		m.viewport.View(),
		systemStyle.Render(status),
		m.input.View(),
		footerStyle.Render(footer),
	}, "\n")
}
