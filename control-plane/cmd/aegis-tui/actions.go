package main

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	tcpBurstCount = 300
	udpBurstCount = 20

	grafanaURL    = "http://localhost:3030"
	prometheusURL = "http://localhost:9092"
	dashboardURL  = "http://localhost:9090/dashboard"
)

type actionResultMsg struct {
	label string
	err   error
}

type commandChecker func(name string, args ...string) error

func realCommandChecker(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func detectComposeBinary(check commandChecker) []string {
	if check("docker", "compose", "version") == nil {
		return []string{"docker", "compose"}
	}
	return []string{"docker-compose"}
}

var (
	composeBinOnce   sync.Once
	composeBinCached []string
)

func composeBinary() []string {
	composeBinOnce.Do(func() {
		composeBinCached = detectComposeBinary(realCommandChecker)
	})
	return composeBinCached
}

func killArgs(composeBin []string, service string) []string {
	return append(append([]string{}, composeBin...), "kill", service)
}

func startArgs(composeBin []string, service string) []string {
	return append(append([]string{}, composeBin...), "start", service)
}

func runComposeCmd(label string, args []string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return actionResultMsg{
				label: label,
				err:   fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))),
			}
		}
		return actionResultMsg{label: label}
	}
}

func runKillCmd(service string) tea.Cmd {
	return runComposeCmd(fmt.Sprintf("killed %s", service), killArgs(composeBinary(), service))
}

func runStartCmd(service string) tea.Cmd {
	return runComposeCmd(fmt.Sprintf("restarted %s", service), startArgs(composeBinary(), service))
}

func runTCPBurstCmd(proxyURL string) tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 3 * time.Second}
		var wg sync.WaitGroup
		var okCount, errCount int64

		for i := 0; i < tcpBurstCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := client.Get(proxyURL)
				if err != nil {
					atomic.AddInt64(&errCount, 1)
					return
				}
				resp.Body.Close()
				atomic.AddInt64(&okCount, 1)
			}()
		}
		wg.Wait()

		return actionResultMsg{
			label: fmt.Sprintf("TCP burst: %d ok, %d failed/rejected", okCount, errCount),
		}
	}
}

func runUDPBurstCmd(udpAddr string) tea.Cmd {
	return func() tea.Msg {
		addr, err := net.ResolveUDPAddr("udp", udpAddr)
		if err != nil {
			return actionResultMsg{label: "UDP burst", err: err}
		}
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			return actionResultMsg{label: "UDP burst", err: err}
		}
		defer conn.Close()

		sent := 0
		for i := 0; i < udpBurstCount; i++ {
			if _, err := conn.Write([]byte(fmt.Sprintf("aegis-tui burst packet %d", i))); err == nil {
				sent++
			}
		}
		return actionResultMsg{label: fmt.Sprintf("UDP burst: sent %d/%d packets", sent, udpBurstCount)}
	}
}

func browserOpenArgs(goos, url string) []string {
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}
	default:
		return []string{"xdg-open", url}
	}
}

func openBrowser(url string) error {
	args := browserOpenArgs(runtime.GOOS, url)
	return exec.Command(args[0], args[1:]...).Start()
}

func runOpenCmd(label, url string) tea.Cmd {
	return func() tea.Msg {
		if err := openBrowser(url); err != nil {
			return actionResultMsg{label: label, err: err}
		}
		return actionResultMsg{label: label}
	}
}

func actionForKey(key string, m Model) (tea.Cmd, bool) {
	switch key {
	case "1":
		return runKillCmd("backend1"), true
	case "2":
		return runKillCmd("backend2"), true
	case "3":
		return runKillCmd("backend3"), true
	case "4":
		return runStartCmd("backend1"), true
	case "5":
		return runStartCmd("backend2"), true
	case "6":
		return runStartCmd("backend3"), true
	case "7":
		return runTCPBurstCmd(m.proxyURL), true
	case "8":
		return runUDPBurstCmd(m.udpAddr), true
	case "g":
		return runOpenCmd("opened Grafana", grafanaURL), true
	case "m":
		return runOpenCmd("opened Prometheus", prometheusURL), true
	case "d":
		return runOpenCmd("opened admin dashboard", dashboardURL), true
	}
	return nil, false
}

func actionPendingLabel(key string) (string, bool) {
	switch key {
	case "1":
		return "[you] killing backend1", true
	case "2":
		return "[you] killing backend2", true
	case "3":
		return "[you] killing backend3", true
	case "4":
		return "[you] restarting backend1", true
	case "5":
		return "[you] restarting backend2", true
	case "6":
		return "[you] restarting backend3", true
	case "7":
		return "[you] firing TCP burst", true
	case "8":
		return "[you] firing UDP burst", true
	case "g":
		return "[you] opening Grafana", true
	case "m":
		return "[you] opening Prometheus", true
	case "d":
		return "[you] opening admin dashboard", true
	}
	return "", false
}
