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
	options []Option // one per branch; the active one drives the session
	current int      // index into options of the active branch

	picking bool // the /branch picker overlay is open
	cursor  int  // highlighted option while picking

	adding bool   // the /counterparties add form overlay is open
	form   cpForm // field state while adding

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
	ti.Prompt = "❯ "

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		ctx:      ctx,
		options:  options,
		input:    ti,
		spinner:  sp,
		viewport: viewport.New(),
	}
}

// Init starts straight into the first branch's session; there is no select
// screen. Switch branches in-session with /branch.
func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.startSession(m.options[0]))
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

	case cpAddedMsg:
		return m.finishCounterparty(msg)

	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	if m.session != nil && !m.running {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		return m, tea.Quit
	}
	if m.session == nil {
		return m, nil // still connecting; only quit keys act
	}
	if m.picking {
		return m.handlePickerKey(msg)
	}
	if m.adding {
		return m.handleFormKey(msg)
	}

	switch msg.String() {
	case "esc":
		if m.running {
			if m.cancel != nil {
				m.cancel() // cancel the in-flight turn, keep the program open
			}
			return m, nil
		}
		// No select screen to return to; esc starts a fresh session on the
		// current branch (clears the conversation).
		_ = m.session.Close()
		m.session = nil
		return m, m.startSession(m.options[m.current])
	case "up", "down", "pgup", "pgdown":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case "home":
		m.viewport.GotoTop()
		return m, nil
	case "end":
		m.viewport.GotoBottom()
		return m, nil
	case "enter":
		if m.running {
			return m, nil
		}
		request := strings.TrimSpace(m.input.Value())
		if request == "" {
			return m, nil
		}
		m.input.Reset()
		if strings.HasPrefix(request, "/") {
			return m.handleCommand(request)
		}
		return m.startTurn(request)
	}
	if !m.running {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// handlePickerKey drives the /branch picker overlay: move, switch on enter,
// cancel on esc.
func (m model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.options)-1 {
			m.cursor++
		}
	case "esc":
		m.picking = false // cancel, back to chat unchanged
	case "enter":
		m.picking = false
		if m.cursor != m.current {
			m.current = m.cursor
			_ = m.session.Close()
			m.session = nil
			return m, m.startSession(m.options[m.cursor])
		}
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
	m.appendLine(line{kind: linePreview, text: renderReversePreview(entry, intent.Note, m.accountNameResolver())})
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
	// header + divider + status + input + divider + footer + margins.
	h := max(m.height-9, 3)
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

func roleLabel(k lineKind) string {
	switch k {
	case lineUser:
		return "you"
	case lineModel:
		return "model"
	case lineValidation:
		return "rejected"
	case lineExecution:
		return "exec error"
	case lineObservation:
		return "observation"
	case lineTool:
		return "tool"
	case linePreview:
		return "preview"
	case lineError:
		return "error"
	default:
		return "system"
	}
}

// renderLabel is the role tag above a transcript body: a filled badge for the
// meaningful kinds, a subtle marker for system notes.
func renderLabel(k lineKind) string {
	if k == lineSystem {
		return systemStyle.Render("· " + roleLabel(k))
	}
	return badge(roleLabel(k), roleColor[k])
}

func (m model) renderTranscript() string {
	if len(m.lines) == 0 {
		return hintStyle.Render("No turns yet — type a request below to start.")
	}
	width := max(m.viewport.Width()-2, 20)
	var b strings.Builder
	for i, l := range m.lines {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(renderLabel(l.kind))
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
	case m.session == nil:
		content = "Connecting to ledger..."
	case m.picking:
		content = m.branchPickerView()
	case m.adding:
		content = m.counterpartyFormView()
	default:
		content = m.chatView()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m model) branchPickerView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Switch branch"))
	b.WriteString("\n")
	b.WriteString(divider(min(m.width, 60)))
	b.WriteString("\n\n")
	for i, opt := range m.options {
		marker := "  "
		label := opt.Label
		if i == m.cursor {
			marker = keyStyle.Render("❯ ")
			label = headerStyle.Render(label)
		}
		b.WriteString(marker)
		b.WriteString(label)
		if opt.Hint != "" {
			b.WriteString("  ")
			b.WriteString(hintStyle.Render(opt.Hint))
		}
		if i == m.current {
			b.WriteString(systemStyle.Render("  (current)"))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(keyHints(
		[2]string{"↑/↓", "move"},
		[2]string{"enter", "switch"},
		[2]string{"esc", "cancel"},
	))
	return b.String()
}

func (m model) chatView() string {
	header := headerStyle.Render(m.label)
	if m.turn > 0 {
		header += hintStyle.Render(fmt.Sprintf("  ·  turn %d", m.turn))
	}

	status := " "
	if m.running {
		status = systemStyle.Render(m.spinner.View()+" running… ") + hintStyle.Render("(esc cancels this turn)")
	}

	footer := keyHints(
		[2]string{"enter", "send (or /help)"},
		[2]string{"↑/↓ pgup/pgdn", "scroll"},
		[2]string{"esc", "reset"},
		[2]string{"ctrl+c", "quit"},
	)
	if m.running {
		footer = keyHints([2]string{"esc", "cancel turn"}, [2]string{"ctrl+c", "quit"})
	}

	return strings.Join([]string{
		header,
		divider(m.width),
		m.viewport.View(),
		status,
		m.input.View(),
		divider(m.width),
		footer,
	}, "\n")
}
