package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorBorder = lipgloss.Color("#262b33")
	colorText   = lipgloss.Color("#e6e9ef")
	colorMuted  = lipgloss.Color("#8a93a3")
	colorGreen  = lipgloss.Color("#3ecf8e")
	colorRed    = lipgloss.Color("#f2545b")
	colorAmber  = lipgloss.Color("#f2b705")

	styleTitle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	styleMuted = lipgloss.NewStyle().Foreground(colorMuted)
	styleGreen = lipgloss.NewStyle().Foreground(colorGreen)
	styleRed   = lipgloss.NewStyle().Foreground(colorRed)
	styleAmber = lipgloss.NewStyle().Foreground(colorAmber)

	styleHeader = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)

	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)
)

const minSidebarWidth = 28

const banner = ` █████╗ ███████╗ ██████╗ ██╗███████╗
██╔══██╗██╔════╝██╔════╝ ██║██╔════╝
███████║█████╗  ██║  ███╗██║███████╗
██╔══██║██╔══╝  ██║   ██║██║╚════██║
██║  ██║███████╗╚██████╔╝██║███████║
╚═╝  ╚═╝╚══════╝ ╚═════╝ ╚═╝╚══════╝`

func panelStyle(width int) lipgloss.Style {
	if width <= 4 {
		return stylePanel
	}
	return stylePanel.Width(width - 4)
}

func renderView(m Model) string {
	if m.quitting {
		return ""
	}
	if m.showHelp {
		return renderHelp()
	}
	if !m.adminEverPolled && !m.dataPlaneEverPolled {
		return renderSplash(m)
	}
	return renderDashboard(m)
}

func renderSplash(m Model) string {
	return "\n" + styleGreen.Render(banner) + "\n\n" +
		styleMuted.Render("connecting to "+m.adminURL+" ...") + "\n"
}

func renderDashboard(m Model) string {
	var b strings.Builder
	b.WriteString(renderHeader(m))
	b.WriteString("\n\n")

	backendCol := lipgloss.JoinVertical(lipgloss.Left,
		renderBackendTable("TCP BACKENDS", m.backends, 0),
		renderBackendTable("UDP BACKENDS", m.udpBackends, 0),
	)
	eventsCol := renderEvents(m, 0)

	sidebarWidth := m.width - lipgloss.Width(backendCol) - 1
	actionsWidth := m.width - lipgloss.Width(eventsCol) - 1

	if sidebarWidth >= minSidebarWidth && actionsWidth >= minSidebarWidth {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, backendCol, " ", renderStatsBlock(m, sidebarWidth)))
		b.WriteString("\n\n")
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, eventsCol, " ", renderActions(actionsWidth)))
	} else {
		b.WriteString(backendCol)
		b.WriteString("\n\n")
		b.WriteString(renderStatsBlock(m, 0))
		b.WriteString("\n\n")
		b.WriteString(eventsCol)
		b.WriteString("\n\n")
		b.WriteString(renderActions(0))
	}

	b.WriteString("\n\n")
	b.WriteString(renderFooter(m))
	return b.String()
}

func renderHeader(m Model) string {
	algo := m.status.Config.Algorithm
	if algo == "" {
		algo = "-"
	}
	affinity := "session affinity: off"
	if m.status.Config.SessionAffinity {
		affinity = "session affinity: on"
	}
	title := styleTitle.Render("Aegis") + styleMuted.Render("  "+algo+"  ·  "+affinity)

	admin := reachabilityDot("control plane", m.adminReachable, m.adminEverPolled)
	data := reachabilityDot("data plane", m.dataPlaneReachable, m.dataPlaneEverPolled)

	return title + "\n" + admin + "   " + data
}

func reachabilityDot(label string, reachable, everPolled bool) string {
	if !everPolled {
		return styleMuted.Render("○ " + label + " connecting...")
	}
	if reachable {
		return styleGreen.Render("● " + label)
	}
	return styleRed.Render("● " + label + " unreachable")
}

func renderBackendTable(title string, backends []Backend, width int) string {
	if len(backends) == 0 {
		return panelStyle(width).Render(styleHeader.Render(title) + "\n" + styleMuted.Render("No backends reported yet."))
	}

	var maxReq int64
	for _, bk := range backends {
		if bk.TotalRequests > maxReq {
			maxReq = bk.TotalRequests
		}
	}

	header := fmt.Sprintf("%-24s %-10s %-10s %6s %9s %10s %8s %9s",
		"ADDRESS", "HEALTH", "CIRCUIT", "WEIGHT", "CONNECTED", "REQUESTS", "FAILURES", "LATENCY")

	rows := make([]string, 0, len(backends)+2)
	rows = append(rows, styleHeader.Render(title))
	rows = append(rows, styleHeader.Render(header))

	for _, bk := range backends {
		health := styleRed.Render("down")
		if bk.Healthy {
			health = styleGreen.Render("up")
		}

		circuit := circuitBadge(bk.CircuitState)
		bar := requestBar(bk.TotalRequests, maxReq)

		row := fmt.Sprintf("%-24s %-19s %-19s %6d %9d %10d %8d %8.2fms  %s",
			bk.Address, health, circuit, bk.Weight, bk.ActiveConnections, bk.TotalRequests, bk.FailedRequests, bk.AvgLatencyMs, bar)
		rows = append(rows, row)
	}

	return panelStyle(width).Render(strings.Join(rows, "\n"))
}

func circuitBadge(state string) string {
	switch state {
	case "Closed":
		return styleGreen.Render("Closed")
	case "Open":
		return styleRed.Render("Open")
	case "HalfOpen":
		return styleAmber.Render("HalfOpen")
	default:
		return styleMuted.Render("unknown")
	}
}

func requestBar(count, max int64) string {
	const width = 10
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(float64(count) / float64(max) * float64(width))
	if filled > width {
		filled = width
	}
	return styleGreen.Render(strings.Repeat("█", filled)) + styleMuted.Render(strings.Repeat("░", width-filled))
}

var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

func sparkline(samples []float64) string {
	if len(samples) == 0 {
		return ""
	}
	shown := samples
	if len(shown) > 20 {
		shown = shown[len(shown)-20:]
	}

	min, max := shown[0], shown[0]
	for _, v := range shown {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	if max == min {
		return strings.Repeat(string(sparkBlocks[0]), len(shown))
	}

	var b strings.Builder
	for _, v := range shown {
		idx := int((v - min) / (max - min) * float64(len(sparkBlocks)-1))
		b.WriteRune(sparkBlocks[idx])
	}
	return b.String()
}

func renderStatsBlock(m Model, width int) string {
	poolTotal := m.dataStats.PoolHits + m.dataStats.PoolMisses
	poolHitPct := 0.0
	if poolTotal > 0 {
		poolHitPct = m.dataStats.PoolHits / poolTotal * 100
	}

	lines := []string{
		styleMuted.Render("REQ/S") + "  " + styleTitle.Render(fmt.Sprintf("%.1f", m.reqRate)),
		styleGreen.Render(sparkline(m.reqRateHistory)),
		"",
		styleMuted.Render("LATENCY") + "  " + styleTitle.Render(fmt.Sprintf("%.2fms", m.dataStats.LatencyAvgMs)),
		styleGreen.Render(sparkline(m.latencyHistory)),
		"",
		styleMuted.Render("POOL HIT") + "  " + styleTitle.Render(fmt.Sprintf("%.0f%%", poolHitPct)),
		styleMuted.Render("RATE LIMITED") + "  " + styleTitle.Render(fmt.Sprintf("%.0f", m.dataStats.RateLimitDenied)),
		styleMuted.Render("CB TRIPS") + "  " + styleTitle.Render(fmt.Sprintf("%.0f", m.dataStats.CircuitBreakerOpen)),
	}

	return panelStyle(width).Render(strings.Join(lines, "\n"))
}

func renderEvents(m Model, width int) string {
	if len(m.events) == 0 {
		return panelStyle(width).Render(styleMuted.Render("No events yet — waiting for a state change."))
	}

	n := len(m.events)
	start := 0
	if n > eventsShown {
		start = n - eventsShown
	}

	lines := make([]string, 0, eventsShown)
	for i := n - 1; i >= start; i-- {
		ev := m.events[i]
		lines = append(lines, styleMuted.Render(ev.Time.Format(time.TimeOnly))+"  "+ev.Text)
	}

	return panelStyle(width).Render(strings.Join(lines, "\n"))
}

func renderActions(width int) string {
	lines := []string{
		"[1] kill backend1",
		"[2] kill backend2",
		"[3] kill backend3",
		"[4] restart backend1",
		"[5] restart backend2",
		"[6] restart backend3",
		"[7] fire TCP burst",
		"[8] fire UDP burst",
	}
	return panelStyle(width).Render(styleMuted.Render(strings.Join(lines, "\n")))
}

func renderFooter(m Model) string {
	status := "read-only — use aegis-ctl to manage backend config"
	if m.lastActionStatus != "" {
		status = "last: " + m.lastActionStatus
	}

	pausedTag := ""
	if m.paused {
		pausedTag = styleAmber.Render("PAUSED") + "  ·  "
	}

	return pausedTag + styleMuted.Render(status) + "  ·  " + styleMuted.Render("? help  ·  p pause  ·  q quit")
}

func renderHelp() string {
	lines := []string{
		styleTitle.Render("Aegis TUI — Help"),
		"",
		styleHeader.Render("Panels"),
		"TCP/UDP BACKENDS   health, circuit state, weight, connections, requests, failures",
		fmt.Sprintf("REQ/S, LATENCY     rolling sparkline of the last %d polls", maxHistory),
		"EVENTS             diff-generated feed: health/circuit transitions and your own actions",
		"",
		styleHeader.Render("Keys"),
		"1-3   kill backend1/2/3      (docker compose kill)",
		"4-6   restart backend1/2/3   (docker compose start)",
		"7     fire a concurrent TCP burst",
		"8     fire a UDP burst",
		"p     pause/resume polling",
		"?     toggle this help screen",
		"q     quit",
		"",
		styleMuted.Render("esc or ? to return"),
	}
	return stylePanel.Render(strings.Join(lines, "\n"))
}
