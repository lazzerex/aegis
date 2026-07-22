package main

import (
	"testing"
	"time"
)

var testTime = time.Date(2026, 7, 21, 14, 30, 0, 0, time.UTC)

func TestDiffBackends_HealthTransition(t *testing.T) {
	prev := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Closed"},
	}
	next := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: false, CircuitState: "Closed"},
	}

	events := diffBackends(prev, next, testTime)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	want := "localhost:3000 health: healthy -> unhealthy"
	if events[0].Text != want {
		t.Errorf("event text: got %q, want %q", events[0].Text, want)
	}
}

func TestDiffBackends_CircuitTransition(t *testing.T) {
	prev := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Closed"},
	}
	next := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Open"},
	}

	events := diffBackends(prev, next, testTime)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	want := "localhost:3000 circuit: Closed -> Open"
	if events[0].Text != want {
		t.Errorf("event text: got %q, want %q", events[0].Text, want)
	}
}

func TestDiffBackends_NoChangeNoEvent(t *testing.T) {
	prev := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Closed"},
	}
	next := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Closed"},
	}

	events := diffBackends(prev, next, testTime)
	if len(events) != 0 {
		t.Errorf("expected no events, got %+v", events)
	}
}

func TestDiffBackends_FirstSeenNoEvent(t *testing.T) {
	prev := map[string]Backend{}
	next := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Closed"},
	}

	events := diffBackends(prev, next, testTime)
	if len(events) != 0 {
		t.Errorf("a backend seen for the first time should not emit a transition event, got %+v", events)
	}
}

func TestDiffBackends_MultipleTransitionsSameBackend(t *testing.T) {
	prev := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: true, CircuitState: "Closed"},
	}
	next := map[string]Backend{
		"localhost:3000": {Address: "localhost:3000", Healthy: false, CircuitState: "Open"},
	}

	events := diffBackends(prev, next, testTime)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (health + circuit), got %d: %+v", len(events), events)
	}
}

func TestReachabilityEvent_TransitionDown(t *testing.T) {
	ev := reachabilityEvent("control plane", true, false, testTime)
	if ev == nil {
		t.Fatal("expected an event for reachable -> unreachable")
	}
	want := "control plane unreachable"
	if ev.Text != want {
		t.Errorf("event text: got %q, want %q", ev.Text, want)
	}
}

func TestReachabilityEvent_TransitionUp(t *testing.T) {
	ev := reachabilityEvent("data plane", false, true, testTime)
	if ev == nil {
		t.Fatal("expected an event for unreachable -> reachable")
	}
	want := "data plane reconnected"
	if ev.Text != want {
		t.Errorf("event text: got %q, want %q", ev.Text, want)
	}
}

func TestReachabilityEvent_NoTransitionNoEvent(t *testing.T) {
	if ev := reachabilityEvent("control plane", true, true, testTime); ev != nil {
		t.Errorf("expected no event when reachability doesn't change, got %+v", ev)
	}
	if ev := reachabilityEvent("control plane", false, false, testTime); ev != nil {
		t.Errorf("expected no event when reachability doesn't change, got %+v", ev)
	}
}
