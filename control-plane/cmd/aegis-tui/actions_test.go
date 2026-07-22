package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestKillArgs(t *testing.T) {
	got := killArgs([]string{"docker", "compose"}, "backend1")
	want := []string{"docker", "compose", "kill", "backend1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("killArgs: got %v, want %v", got, want)
	}
}

func TestStartArgs(t *testing.T) {
	got := startArgs([]string{"docker-compose"}, "backend2")
	want := []string{"docker-compose", "start", "backend2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("startArgs: got %v, want %v", got, want)
	}
}

func TestKillArgs_DoesNotMutateComposeBinInput(t *testing.T) {
	composeBin := []string{"docker", "compose"}
	first := killArgs(composeBin, "backend1")
	second := startArgs(composeBin, "backend2")

	wantFirst := []string{"docker", "compose", "kill", "backend1"}
	if !reflect.DeepEqual(first, wantFirst) {
		t.Errorf("first call corrupted: got %v, want %v", first, wantFirst)
	}
	if len(composeBin) != 2 {
		t.Errorf("composeBin input mutated: got %v", composeBin)
	}
	_ = second
}

func TestDetectComposeBinary_PrefersDockerComposePlugin(t *testing.T) {
	check := func(name string, args ...string) error {
		if name == "docker" && len(args) == 2 && args[0] == "compose" && args[1] == "version" {
			return nil
		}
		return errors.New("unexpected call")
	}
	got := detectComposeBinary(check)
	want := []string{"docker", "compose"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDetectComposeBinary_FallsBackToLegacyBinary(t *testing.T) {
	check := func(name string, args ...string) error {
		return errors.New("docker compose plugin not available")
	}
	got := detectComposeBinary(check)
	want := []string{"docker-compose"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestActionForKey_KnownKeysResolve(t *testing.T) {
	m := NewModel("http://localhost:9090", "http://localhost:9100/metrics", "http://localhost:8080/api/test", "localhost:8081")
	for _, key := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		cmd, ok := actionForKey(key, m)
		if !ok {
			t.Errorf("key %q: expected a resolved action", key)
		}
		if cmd == nil {
			t.Errorf("key %q: expected a non-nil tea.Cmd", key)
		}
	}
}

func TestActionForKey_UnknownKeyReturnsFalse(t *testing.T) {
	m := NewModel("http://localhost:9090", "http://localhost:9100/metrics", "http://localhost:8080/api/test", "localhost:8081")
	_, ok := actionForKey("9", m)
	if ok {
		t.Error("expected unknown key to not resolve to an action")
	}
	_, ok = actionForKey("q", m)
	if ok {
		t.Error("expected 'q' (quit key) to not resolve to an action")
	}
}

func TestActionPendingLabel_KnownKeys(t *testing.T) {
	label, ok := actionPendingLabel("1")
	if !ok || label != "[you] killing backend1" {
		t.Errorf("got (%q, %v), want (%q, true)", label, ok, "[you] killing backend1")
	}
}

func TestActionPendingLabel_UnknownKey(t *testing.T) {
	if _, ok := actionPendingLabel("x"); ok {
		t.Error("expected unknown key to not resolve to a pending label")
	}
}
