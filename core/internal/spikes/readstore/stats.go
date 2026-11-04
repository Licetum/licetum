package main

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// latencies collects samples from several goroutines.
type latencies struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latencies) add(d []time.Duration) {
	l.mu.Lock()
	l.d = append(l.d, d...)
	l.mu.Unlock()
}

type summary struct {
	N                  int
	P50, P95, P99, Max time.Duration
	Took               time.Duration // wall time of the whole run, when known
}

func (l *latencies) summary() summary {
	l.mu.Lock()
	defer l.mu.Unlock()
	return summarize(l.d)
}

func summarize(d []time.Duration) summary {
	if len(d) == 0 {
		return summary{}
	}
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(q float64) time.Duration { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return summary{N: len(s), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: s[len(s)-1]}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d)/float64(time.Millisecond)) }

func (s summary) row() string {
	return fmt.Sprintf("%d | %s | %s | %s | %s", s.N, ms(s.P50), ms(s.P95), ms(s.P99), ms(s.Max))
}

// rate is the operations per second of a run, "" when the wall time is unknown.
func (s summary) rate() string {
	if s.Took <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f", float64(s.N)/s.Took.Seconds())
}
