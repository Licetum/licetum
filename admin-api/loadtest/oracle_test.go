package loadtest

import (
	"context"
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
	tokens := make([]string, p.Users)

	run := func(handler http.HandlerFunc) Oracle {
		srv := httptest.NewServer(handler)
		defer srv.Close()
		stop := make(chan struct{})
		go func() { time.Sleep(300 * time.Millisecond); close(stop) }()
		return runOracle(context.Background(), srv.URL, "svc", p, tokens, 1, 100, stop)
	}

	down := run(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "busy", http.StatusServiceUnavailable) })
	if down.Checked == 0 || down.Unanswered != down.Checked || down.Mismatches != 0 {
		t.Fatalf("a server that answers 503: %+v", down)
	}

	denyAll := run(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"allowed":false}]}`))
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
