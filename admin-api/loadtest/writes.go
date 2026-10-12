package loadtest

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	adminsvc "github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/model"
)

// WriteEvent is one write the admin API made during a measured window. Creating and deleting a rule each bump the cache
// version of the service, so every write drops the caches of the check API (see bumpVersion in the admin service).
type WriteEvent struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Duration float64   `json:"duration_ms"`
	Error    string    `json:"error,omitempty"`
}

// writer makes the writes of a window: it creates a rule for a subject that no request asks about, and deletes it on the
// next write, so the policy the measured subjects see does not change while the caches are invalidated.
type writer struct {
	admin    *adminsvc.App
	docType  uuid.UUID
	ruleID   *uuid.UUID
	recorded []WriteEvent
}

// writerSubject is the subject of the probe rule. The traffic names only user-N subjects, so no request is affected by
// the rule itself.
const writerSubject = "write-probe"

// tick makes one write: a create when no probe rule exists, a delete when one does.
func (w *writer) tick(ctx context.Context) {
	start := time.Now()
	ev := WriteEvent{At: start}
	if w.ruleID == nil {
		ev.Kind = "create"
		in := &model.AssignmentRuleInput{
			Subject:        ptr(writerSubject),
			TypeResourceID: &w.docType,
			ResourceID:     ptr("d-0"),
			Access:         []string{"read"},
		}
		if err := in.Validate(); err != nil {
			ev.Error = err.Error()
		} else if rule, err := w.admin.CreateAssignmentRule(ctx, in); err != nil {
			ev.Error = err.Error()
		} else {
			id := rule.ID
			w.ruleID = &id
		}
	} else {
		ev.Kind = "delete"
		// The probe rule is forgotten only when it is gone: a failed delete leaves it in the policy, and the next tick
		// tries again instead of creating another one.
		if _, err := w.admin.DeleteAssignmentRule(ctx, *w.ruleID); err != nil {
			ev.Error = err.Error()
		} else {
			w.ruleID = nil
		}
	}
	ev.Duration = ms(time.Since(start))
	w.recorded = append(w.recorded, ev)
}

// WriteEffect is what one write did to the latency of the requests around it, from the raw points of k6: the baseline
// is the median of the per-second p99 before the writes, and the recovery is the time until a second's p99 is back
// within 1.5 times the baseline (and stays there for three seconds).
type WriteEffect struct {
	Writes         int     `json:"writes"`
	BaselineP99MS  float64 `json:"baseline_p99_ms"`
	PeakP99MS      float64 `json:"peak_p99_ms_after_writes"`
	RecoveryMedian float64 `json:"recovery_median_s"`
	RecoveryMax    float64 `json:"recovery_max_s"`
	Unrecovered    int     `json:"unrecovered"`
}

// point is one measured request of the raw k6 output: its time and its duration.
type point struct {
	t      time.Time
	dur    float64
	status int
}

// readRequestPoints reads the http_req_duration points of a k6 output file (gzip when its name ends in .gz).
func readRequestPoints(path string) ([]point, error) {
	f, err := os.Open(path) //nolint:gosec // a file of the report directory of this run
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	var out []point
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"metric":"http_req_duration"`) {
			continue
		}
		var p struct {
			Type string `json:"type"`
			Data struct {
				Time  string            `json:"time"`
				Value float64           `json:"value"`
				Tags  map[string]string `json:"tags"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &p); err != nil || p.Type != "Point" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, p.Data.Time)
		if err != nil {
			continue
		}
		status, _ := strconv.Atoi(p.Data.Tags["status"])
		out = append(out, point{t: t, dur: p.Data.Value, status: status})
	}
	return out, sc.Err()
}

// writeEffects measures the writes of a window against its requests.
func writeEffects(points []point, writes []WriteEvent) WriteEffect {
	var eff WriteEffect
	if len(points) == 0 || len(writes) == 0 {
		return eff
	}
	eff.Writes = len(writes)
	start := points[0].t
	for _, p := range points {
		start = minTime(start, p.t)
	}
	// The per-second p99 of the window, indexed by the second since the start.
	perSecond := map[int][]float64{}
	for _, p := range points {
		perSecond[int(p.t.Sub(start).Seconds())] = append(perSecond[int(p.t.Sub(start).Seconds())], p.dur)
	}
	last := 0
	for s := range perSecond {
		last = max(last, s)
	}
	p99 := make([]float64, last+1)
	for s := range p99 {
		p99[s] = quantileOf(perSecond[s], 0.99)
	}

	first := int(writes[0].At.Sub(start).Seconds())
	before := []float64{}
	for s := max(0, first-10); s < first && s < len(p99); s++ {
		before = append(before, p99[s])
	}
	if len(before) == 0 {
		return eff
	}
	eff.BaselineP99MS = quantileOf(before, 0.5)

	var recoveries []float64
	for _, w := range writes {
		ws := int(w.At.Sub(start).Seconds())
		peak := 0.0
		recovered := -1
		for s := ws; s < len(p99); s++ {
			if s < ws+10 {
				peak = max(peak, p99[s])
			}
			if recovered < 0 && isStable(p99, s, 1.5*eff.BaselineP99MS) {
				recovered = s
			}
		}
		eff.PeakP99MS = max(eff.PeakP99MS, peak)
		if recovered < 0 {
			eff.Unrecovered++
			continue
		}
		recoveries = append(recoveries, float64(recovered-ws))
	}
	if len(recoveries) > 0 {
		eff.RecoveryMedian = quantileOf(recoveries, 0.5)
		eff.RecoveryMax = slices.Max(recoveries)
	}
	return eff
}

// isStable is whether the next three seconds, from second s on, all stay within limit.
func isStable(p99 []float64, s int, limit float64) bool {
	if s+3 > len(p99) {
		return false
	}
	for k := s; k < s+3; k++ {
		if p99[k] > limit {
			return false
		}
	}
	return true
}

// quantileOf is the q-quantile of values (nearest rank), or 0 for no values.
func quantileOf(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	i := min(len(sorted)-1, max(0, int(float64(len(sorted))*q+0.999999)-1))
	return sorted[i]
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// writesFile is the name of the write log of a window, next to its time series.
func writesFile(outDir, window string) string {
	return fmt.Sprintf("%s/%s.writes.json", outDir, window)
}
