package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func main() {
	baseURL := getenv("AEGIS_URL", "http://localhost:9090")
	token := os.Getenv("AEGIS_API_TOKEN")

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "status":
		cmdStatus(baseURL, token)
	case "backends":
		if len(os.Args) < 3 {
			die("usage: aegis-ctl backends <add|remove> ...")
		}
		switch os.Args[2] {
		case "add":
			cmdBackendsAdd(baseURL, token, os.Args[3:])
		case "remove":
			cmdBackendsRemove(baseURL, token, os.Args[3:])
		default:
			die("unknown backends subcommand: %s", os.Args[2])
		}
	case "drain":
		cmdDrain(baseURL, token, os.Args[2:])
	case "reload":
		cmdReload(baseURL, token)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: aegis-ctl <command> [args]

Commands:
  status                         List all backends with health state
  backends add <addr> [-w N]     Add backend (weight default 100)
  backends remove <addr>         Remove backend
  drain [--timeout 30]           Drain all connections
  reload                         Reload config from disk

Env:
  AEGIS_URL         Admin API base URL (default: http://localhost:9090)
  AEGIS_API_TOKEN   Bearer token for auth`)
}

func cmdStatus(baseURL, token string) {
	data, code := request("GET", baseURL+"/backends", token, nil)
	if code != 200 {
		die("server returned %d: %s", code, data)
	}

	var resp struct {
		Backends []struct {
			Address string `json:"address"`
			Weight  int    `json:"weight"`
			Healthy bool   `json:"healthy"`
		} `json:"backends"`
	}
	must(json.Unmarshal(data, &resp))

	fmt.Printf("%-32s  %-8s  %s\n", "ADDRESS", "WEIGHT", "HEALTH")
	fmt.Println(strings.Repeat("-", 52))
	for _, b := range resp.Backends {
		health := "healthy"
		if !b.Healthy {
			health = "unhealthy"
		}
		fmt.Printf("%-32s  %-8d  %s\n", b.Address, b.Weight, health)
	}
}

func cmdBackendsAdd(baseURL, token string, args []string) {
	if len(args) < 1 {
		die("usage: aegis-ctl backends add <address> [-w weight]")
	}
	addr := args[0]
	weight := 100
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "-w" || args[i] == "--weight" {
			w, err := strconv.Atoi(args[i+1])
			if err != nil || w <= 0 {
				die("invalid weight: %s", args[i+1])
			}
			weight = w
		}
	}

	body := map[string]interface{}{"address": addr, "weight": weight}
	data, code := request("POST", baseURL+"/backends", token, body)
	switch code {
	case 201:
		fmt.Printf("added %s (weight %d)\n", addr, weight)
	case 401:
		die("unauthorized: set AEGIS_API_TOKEN")
	case 409:
		die("backend already exists: %s", addr)
	default:
		die("server returned %d: %s", code, data)
	}
}

func cmdBackendsRemove(baseURL, token string, args []string) {
	if len(args) < 1 {
		die("usage: aegis-ctl backends remove <address>")
	}
	addr := args[0]
	data, code := request("DELETE", baseURL+"/backends/"+url.PathEscape(addr), token, nil)
	switch code {
	case 200:
		fmt.Printf("removed %s\n", addr)
	case 401:
		die("unauthorized: set AEGIS_API_TOKEN")
	case 404:
		die("backend not found: %s", addr)
	default:
		die("server returned %d: %s", code, data)
	}
}

func cmdDrain(baseURL, token string, args []string) {
	// --timeout parsed but API uses server-side default (30s); wired up when API supports it
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--timeout" || args[i] == "-timeout" {
			t, err := strconv.Atoi(strings.TrimSuffix(args[i+1], "s"))
			if err != nil || t <= 0 {
				die("invalid timeout: %s", args[i+1])
			}
		}
	}

	data, code := request("POST", baseURL+"/drain", token, nil)
	switch code {
	case 200:
		fmt.Println("connections drained")
	case 401:
		die("unauthorized: set AEGIS_API_TOKEN")
	default:
		die("server returned %d: %s", code, data)
	}
}

func cmdReload(baseURL, token string) {
	data, code := request("POST", baseURL+"/reload", token, nil)
	switch code {
	case 200:
		fmt.Println("config reloaded")
	case 401:
		die("unauthorized: set AEGIS_API_TOKEN")
	default:
		die("server returned %d: %s", code, data)
	}
}

func request(method, rawURL, token string, body interface{}) ([]byte, int) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		must(err)
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	must(err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("connection failed: %v\n  is aegis running at %s?", err, rawURL)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	must(err)
	return data, resp.StatusCode
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		die("%v", err)
	}
}
