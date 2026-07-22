package main

import (
	"context"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	pollInterval = time.Second
	pollTimeout  = 2 * time.Second
	maxEvents    = 50
	eventsShown  = 10
	maxHistory   = 30
)

type tickMsg time.Time

type adminPollMsg struct {
	status      Status
	backends    []Backend
	udpBackends []Backend
	err         error
}

type dataPlanePollMsg struct {
	stats DataPlaneStats
	err   error
}

type Model struct {
	adminURL     string
	dataPlaneURL string
	proxyURL     string
	udpAddr      string
	client       *http.Client

	status          Status
	backends        []Backend
	prevBackends    map[string]Backend
	udpBackends     []Backend
	prevUDPBackends map[string]Backend

	dataStats DataPlaneStats

	adminReachable      bool
	adminEverPolled     bool
	dataPlaneReachable  bool
	dataPlaneEverPolled bool

	reqRate         float64
	lastAdminPollAt time.Time

	reqRateHistory []float64
	latencyHistory []float64

	events []Event

	lastActionStatus string

	showHelp        bool
	paused          bool
	splashDismissed bool

	startedAt time.Time

	width, height int
	quitting      bool
}

func NewModel(adminURL, dataPlaneURL, proxyURL, udpAddr string) Model {
	return Model{
		adminURL:        adminURL,
		dataPlaneURL:    dataPlaneURL,
		proxyURL:        proxyURL,
		udpAddr:         udpAddr,
		client:          &http.Client{Timeout: pollTimeout},
		prevBackends:    make(map[string]Backend),
		prevUDPBackends: make(map[string]Backend),
		startedAt:       time.Now(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(pollAdminCmd(m), pollDataPlaneCmd(m), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func pollAdminCmd(m Model) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()

		status, err := fetchStatus(ctx, m.client, m.adminURL)
		if err != nil {
			return adminPollMsg{err: err}
		}
		backends, udpBackends, err := fetchBackends(ctx, m.client, m.adminURL)
		if err != nil {
			return adminPollMsg{err: err}
		}
		return adminPollMsg{status: status, backends: backends, udpBackends: udpBackends}
	}
}

func pollDataPlaneCmd(m Model) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()

		stats, err := fetchDataPlaneMetrics(ctx, m.client, m.dataPlaneURL)
		if err != nil {
			return dataPlanePollMsg{err: err}
		}
		return dataPlanePollMsg{stats: stats}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		key := msg.String()
		switch key {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		if !m.splashDismissed {
			m.splashDismissed = true
			return m, nil
		}
		switch key {
		case "esc":
			if m.showHelp {
				m.showHelp = false
			}
			return m, nil
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "p":
			m.paused = !m.paused
			return m, nil
		}
		if m.showHelp {
			return m, nil
		}
		if cmd, ok := actionForKey(key, m); ok {
			if label, ok := actionPendingLabel(key); ok {
				m.pushEvent(Event{Time: time.Now(), Text: label})
			}
			return m, cmd
		}
		return m, nil

	case tickMsg:
		if m.paused {
			return m, tickCmd()
		}
		return m, tea.Batch(pollAdminCmd(m), pollDataPlaneCmd(m), tickCmd())

	case adminPollMsg:
		return m.handleAdminPoll(msg), nil

	case dataPlanePollMsg:
		return m.handleDataPlanePoll(msg), nil

	case actionResultMsg:
		return m.handleActionResult(msg), nil
	}

	return m, nil
}

func (m Model) handleAdminPoll(msg adminPollMsg) Model {
	now := time.Now()
	wasReachable := m.adminReachable
	m.adminReachable = msg.err == nil

	if !m.adminEverPolled {
		m.adminEverPolled = true
		wasReachable = m.adminReachable
	}
	if ev := reachabilityEvent("control plane", wasReachable, m.adminReachable, now); ev != nil {
		m.pushEvent(*ev)
	}

	if msg.err != nil {
		return m
	}

	nextBackends := make(map[string]Backend, len(msg.backends))
	for _, b := range msg.backends {
		nextBackends[b.Address] = b
	}
	nextUDPBackends := make(map[string]Backend, len(msg.udpBackends))
	for _, b := range msg.udpBackends {
		nextUDPBackends[b.Address] = b
	}

	if !m.lastAdminPollAt.IsZero() {
		elapsed := now.Sub(m.lastAdminPollAt).Seconds()
		if elapsed > 0 {
			delta := sumTotalRequests(msg.backends) - sumTotalRequestsFromMap(m.prevBackends)
			if delta >= 0 {
				m.reqRate = float64(delta) / elapsed
			}
		}
	}
	m.reqRateHistory = pushHistory(m.reqRateHistory, m.reqRate)

	for _, ev := range diffBackends(m.prevBackends, nextBackends, now) {
		m.pushEvent(ev)
	}
	for _, ev := range diffBackends(m.prevUDPBackends, nextUDPBackends, now) {
		m.pushEvent(ev)
	}

	m.status = msg.status
	m.backends = msg.backends
	m.prevBackends = nextBackends
	m.udpBackends = msg.udpBackends
	m.prevUDPBackends = nextUDPBackends
	m.lastAdminPollAt = now
	return m
}

func (m Model) handleDataPlanePoll(msg dataPlanePollMsg) Model {
	now := time.Now()
	wasReachable := m.dataPlaneReachable
	m.dataPlaneReachable = msg.err == nil

	if !m.dataPlaneEverPolled {
		m.dataPlaneEverPolled = true
		wasReachable = m.dataPlaneReachable
	}
	if ev := reachabilityEvent("data plane", wasReachable, m.dataPlaneReachable, now); ev != nil {
		m.pushEvent(*ev)
	}

	if msg.err != nil {
		return m
	}

	m.dataStats = msg.stats
	m.latencyHistory = pushHistory(m.latencyHistory, msg.stats.LatencyAvgMs)
	return m
}

func pushHistory(history []float64, value float64) []float64 {
	history = append(history, value)
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	return history
}

func (m Model) handleActionResult(msg actionResultMsg) Model {
	now := time.Now()
	if msg.err != nil {
		m.lastActionStatus = msg.label + " failed: " + msg.err.Error()
		m.pushEvent(Event{Time: now, Text: "[result] " + msg.label + " failed: " + msg.err.Error()})
		return m
	}
	m.lastActionStatus = msg.label
	m.pushEvent(Event{Time: now, Text: "[result] " + msg.label})
	return m
}

func (m *Model) pushEvent(ev Event) {
	m.events = append(m.events, ev)
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}
}

func sumTotalRequests(backends []Backend) int64 {
	var total int64
	for _, b := range backends {
		total += b.TotalRequests
	}
	return total
}

func sumTotalRequestsFromMap(backends map[string]Backend) int64 {
	var total int64
	for _, b := range backends {
		total += b.TotalRequests
	}
	return total
}

func (m Model) View() string {
	return renderView(m)
}
