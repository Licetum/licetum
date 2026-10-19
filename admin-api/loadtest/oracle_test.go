package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A question that gets no decision (an error, a status other than 200) is unanswered; only an answer that differs from the
// expected decision is a wrong answer. The baseline asks for zero wrong answers (F0-7); a server that falls over under load is
// not wrong, it is unavailable, and the error rate says so.
func TestOracleSeparatesWrongAnswersFromUnanswered(t *testing.T) {
	p := profiles["smoke"]

	run := func(handler http.HandlerFunc) Oracle {
		srv := httptest.NewServer(handler)
		defer srv.Close()
		stop := make(chan struct{})
		go func() { time.Sleep(300 * time.Millisecond); close(stop) }()
		return runOracle(context.Background(), srv.URL, "svc", p, "lct_test", 1, 100, stop)
	}

	down := run(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "busy", http.StatusServiceUnavailable) })
	if down.Checked == 0 || down.Unanswered != down.Checked || down.Mismatches != 0 {
		t.Fatalf("a server that answers 503: %+v", down)
	}

	denyAll := run(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"decision":false}`))
	})
	if denyAll.Unanswered != 0 || denyAll.Mismatches != denyAll.Granted || denyAll.Granted == 0 {
		t.Fatalf("a server that denies everything is wrong exactly where access is expected: %+v", denyAll)
	}
}

func TestOracleThatVerifiedNothingMakesTheWindowInvalid(t *testing.T) {
	m := Measurement{Valid: true, Oracle: Oracle{Checked: 20, Unanswered: 20}}
	m.judgeMeasurement()
	if m.Valid {
		t.Fatal("a window whose oracle got no answer to any question is valid")
	}
	m = Measurement{Oracle: Oracle{Checked: 20, Unanswered: 3}}
	m.judgeMeasurement()
	if !m.Valid {
		t.Fatalf("some unanswered questions under load are the error rate's to judge: %v", m.InvalidReasons)
	}
}

// TestContract_LOAD01_TheLoadTestGivesNoWrongAnswer is the unit-level evidence of LOAD01 (ADR 0001, 11.6.1): the oracle
// compares the answers of the AuthZEN API with the decisions of the generated policy, and the verdict of a window follows
// from it. A server that answers as the policy says leaves the window valid; an answer that differs is a wrong answer and
// makes the window invalid; an evaluation the API could not decide (context.error) is unanswered, never a denial.
func TestContract_LOAD01_TheLoadTestGivesNoWrongAnswer(t *testing.T) {
	p := profiles["smoke"]

	// oracleOf asks the oracle of a server that answers reply(expected) to each question, and returns the oracle's counts.
	oracleOf := func(reply func(expected bool) string) Oracle {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q struct {
				Subject struct {
					ID         string
					Properties map[string]any
				} `json:"subject"`
				Resource struct{ ID string } `json:"resource"`
			}
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				http.Error(w, "not an evaluation", http.StatusBadRequest)
				return
			}
			var u, d int
			if _, err := fmt.Sscanf(q.Subject.ID, "user-%d", &u); err != nil {
				http.Error(w, "not a subject of the policy", http.StatusBadRequest)
				return
			}
			// The oracle asks as the PEP client, with the claims of the person it asks about in the properties of the subject.
			if r.Header.Get("Authorization") != "Bearer lct_test" || q.Subject.Properties["department"] != departmentName(u%p.Groups) {
				http.Error(w, "not asked as the PEP client about a person with its claims", http.StatusForbidden)
				return
			}
			if _, err := fmt.Sscanf(q.Resource.ID, "d-%d", &d); err != nil {
				http.Error(w, "not a document of the policy", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(reply(p.expectedAllowed(u, d))))
		}))
		defer srv.Close()
		stop := make(chan struct{})
		go func() { time.Sleep(300 * time.Millisecond); close(stop) }()
		return runOracle(context.Background(), srv.URL, "svc", p, "lct_test", 1, 100, stop)
	}

	right := oracleOf(func(expected bool) string { return fmt.Sprintf(`{"decision":%t}`, expected) })
	if right.Checked == 0 || right.Granted == 0 || right.Mismatches != 0 || right.Unanswered != 0 {
		t.Fatalf("a server that answers as the policy says: %+v", right)
	}
	m := Measurement{Valid: true, Oracle: right}
	m.judgeMeasurement()
	if !m.Valid {
		t.Fatalf("a window whose answers are the policy's is invalid: %v", m.InvalidReasons)
	}

	wrong := oracleOf(func(expected bool) string { return fmt.Sprintf(`{"decision":%t}`, !expected) })
	if wrong.Checked == 0 || wrong.Mismatches != wrong.Checked || len(wrong.Examples) == 0 {
		t.Fatalf("a server that answers the opposite of the policy: %+v", wrong)
	}
	m = Measurement{Valid: true, Oracle: wrong}
	m.judgeMeasurement()
	if m.Valid {
		t.Fatal("a window with wrong answers is valid")
	}
	judgeAsStep(t, m)

	undecided := oracleOf(func(bool) string {
		return `{"decision":false,"context":{"error":{"status":500,"message":"the source did not answer"}}}`
	})
	if undecided.Checked == 0 || undecided.Mismatches != 0 || undecided.Unanswered != undecided.Checked {
		t.Fatalf("an evaluation the API could not decide is taken for a denial: %+v", undecided)
	}
	m = Measurement{Valid: true, Oracle: undecided}
	m.judgeMeasurement()
	if m.Valid {
		t.Fatal("a window whose oracle verified nothing is valid")
	}
	judgeAsStep(t, m)
}

// judgeAsStep judges an invalid window as a ramp step whose other figures are within every objective, so that the step
// fails for the window alone: a window that is not valid does not pass as a step, whatever its rate, errors and p99.
func judgeAsStep(t *testing.T, m Measurement) {
	t.Helper()
	m.RateAsked, m.AchievedRPS = 50, 50
	m.judgeStep(0.01, 100*time.Millisecond)
	if m.passes() || len(m.FailReasons) != 1 || m.FailReasons[0] != "the window is not valid" {
		t.Fatalf("an invalid window judged as a ramp step: passes %v, reasons %v", m.passes(), m.FailReasons)
	}
}
