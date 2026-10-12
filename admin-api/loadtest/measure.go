package loadtest

import (
	"fmt"
	"math"
	"time"
)

// Latency is a distribution of request times in milliseconds, with the number of samples it was computed from. A
// percentile is published only when there are enough samples for it (see MinSamples): a p99 from 100 requests is the
// slowest request, not a p99.
type Latency struct {
	Samples int     `json:"samples"`
	Mean    float64 `json:"mean"`
	P50     float64 `json:"p50"`
	P90     float64 `json:"p90"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	P999    float64 `json:"p999"`
	Max     float64 `json:"max"`
}

// MinSamples is the number of samples a quantile q needs before it is published: 100/(1-q). The formula is written in
// the report too: p99 needs 10,000 requests, p99.9 needs 100,000.
func MinSamples(q float64) int {
	// The small tolerance keeps 100/(1-0.9), which is 1000 with a rounding error, at 1000.
	return int(math.Ceil(100/(1-q) - 1e-9))
}

// Quantile reports a quantile of the distribution, or false when there are too few samples for it.
func (l Latency) Quantile(q float64, value float64) (float64, bool) {
	if l.Samples < MinSamples(q) {
		return 0, false
	}
	return value, true
}

// Operation is what one kind of request did in the measured window.
type Operation struct {
	Name     string  `json:"name"`
	Requests int     `json:"requests"`
	Latency  Latency `json:"latency_ms"`
}

// Measurement is the result of one measured window: a k6 invocation between two readings of the server, with the
// validity checks the report applies before it publishes anything.
type Measurement struct {
	Mode           string      `json:"mode"`
	Seed           int64       `json:"seed"`
	RateAsked      float64     `json:"rate_asked"`
	WindowSeconds  float64     `json:"window_seconds"`
	Requests       int         `json:"requests"`
	Answered2xx    int         `json:"answered_2xx"`
	AnsweredOther  int         `json:"answered_other"`
	ErrorRate      float64     `json:"error_rate"`
	AchievedRPS    float64     `json:"achieved_rps"`
	Dropped        int         `json:"dropped_iterations"`
	Decisions      int         `json:"decisions"`
	AllowedShare   float64     `json:"allowed_share"`
	Operations     []Operation `json:"operations"`
	K6Cores        int         `json:"k6_cores"`
	K6CPUShare     float64     `json:"k6_cpu_share"`
	Server         Server      `json:"server"`
	Oracle         Oracle      `json:"oracle"`
	Overall        Latency     `json:"latency_all_ms"`
	Valid          bool        `json:"valid"`
	InvalidReasons []string    `json:"invalid_reasons,omitempty"`
	// Passed is the verdict of a ramp step: a valid window, the errors within the budget, p99 within the objective, and the
	// answers keeping up with the rate. FailReasons says why a step failed.
	Passed      bool     `json:"passed"`
	FailReasons []string `json:"fail_reasons,omitempty"`
}

// maxGeneratorShare is the share of its cores the load generator may use. Above it, the arrivals are late for the
// generator and not for the server: the measurement is marked invalid.
const maxGeneratorShare = 0.7

// judgeMeasurement sets the validity of a measured window. A window is valid when the generator kept up with the rate
// (no dropped iterations, a CPU share within maxGeneratorShare), when the answers were correct (no oracle mismatch),
// and when the server answered: an error rate over the budget is recorded, and the step is failed by the ramp.
func (m *Measurement) judgeMeasurement() {
	m.InvalidReasons = nil
	if m.Dropped > 0 {
		m.InvalidReasons = append(m.InvalidReasons, fmt.Sprintf("%d iterations dropped: k6 could not send at the rate", m.Dropped))
	}
	if m.K6CPUShare > maxGeneratorShare {
		m.InvalidReasons = append(m.InvalidReasons, fmt.Sprintf("the load generator used %.0f%% of its %d cores (limit %.0f%%)",
			100*m.K6CPUShare, m.K6Cores, 100*maxGeneratorShare))
	}
	if m.Oracle.Mismatches > 0 {
		m.InvalidReasons = append(m.InvalidReasons, fmt.Sprintf("%d of %d sampled answers differ from the expected ones",
			m.Oracle.Mismatches, m.Oracle.Checked))
	}
	m.Valid = len(m.InvalidReasons) == 0
}

// p99 is the 99th percentile of all the requests of the window, in milliseconds.
func (m Measurement) p99() float64 {
	return m.Overall.P99
}

// judgeStep sets the verdict of a ramp step. A step passes when the window is valid, the errors stay in the budget, the p99
// of all the requests stays within the objective, and the answers keep up with the rate asked (95%).
func (m *Measurement) judgeStep(maxErrorRate float64, sloP99 time.Duration) {
	m.FailReasons = nil
	if !m.Valid {
		m.FailReasons = append(m.FailReasons, "the window is not valid")
	}
	if m.ErrorRate > maxErrorRate {
		m.FailReasons = append(m.FailReasons, fmt.Sprintf("errors %.2f%% above %.2f%%", 100*m.ErrorRate, 100*maxErrorRate))
	}
	if m.p99() > ms(sloP99) {
		m.FailReasons = append(m.FailReasons, fmt.Sprintf("p99 %.1f ms above %v", m.p99(), sloP99))
	}
	if m.AchievedRPS < 0.95*m.RateAsked {
		m.FailReasons = append(m.FailReasons, fmt.Sprintf("%.0f answers/s, under 95%% of the %.0f asked", m.AchievedRPS, m.RateAsked))
	}
	m.Passed = len(m.FailReasons) == 0
}
