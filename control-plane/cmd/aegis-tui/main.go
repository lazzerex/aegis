package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	adminURL := getenv("AEGIS_URL", "http://localhost:9090")
	dataPlaneURL := getenv("AEGIS_DATA_METRICS_URL", "http://localhost:9100/metrics")
	proxyURL := getenv("AEGIS_PROXY_URL", "http://localhost:8080/api/test")
	udpAddr := getenv("AEGIS_UDP_ADDR", "localhost:8081")

	m := NewModel(adminURL, dataPlaneURL, proxyURL, udpAddr)

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "aegis-tui: %v\n", err)
		os.Exit(1)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
