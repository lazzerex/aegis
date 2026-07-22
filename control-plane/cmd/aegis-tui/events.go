package main

import (
	"fmt"
	"time"
)

type Event struct {
	Time time.Time
	Text string
}

func diffBackends(prev, next map[string]Backend, now time.Time) []Event {
	var events []Event

	for addr, n := range next {
		p, seen := prev[addr]
		if !seen {
			continue
		}
		if p.Healthy != n.Healthy {
			events = append(events, Event{
				Time: now,
				Text: fmt.Sprintf("%s health: %s -> %s", addr, healthLabel(p.Healthy), healthLabel(n.Healthy)),
			})
		}
		if p.CircuitState != n.CircuitState && n.CircuitState != "" {
			events = append(events, Event{
				Time: now,
				Text: fmt.Sprintf("%s circuit: %s -> %s", addr, circuitLabel(p.CircuitState), n.CircuitState),
			})
		}
	}

	return events
}

func healthLabel(healthy bool) string {
	if healthy {
		return "healthy"
	}
	return "unhealthy"
}

func circuitLabel(state string) string {
	if state == "" {
		return "unknown"
	}
	return state
}

func reachabilityEvent(source string, wasReachable, isReachable bool, now time.Time) *Event {
	if wasReachable == isReachable {
		return nil
	}
	if isReachable {
		return &Event{Time: now, Text: fmt.Sprintf("%s reconnected", source)}
	}
	return &Event{Time: now, Text: fmt.Sprintf("%s unreachable", source)}
}
