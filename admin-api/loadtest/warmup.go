package loadtest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// warmSubject is the subject the warm-up asks as, and warmDocument the document it asks about: the owner pattern grants
// subject 0 the documents it owns, and document 0 is owned by subject 0.
const (
	warmSubject  = 0
	warmDocument = "d-0"
	// synchronizeRetry is the pause between two tries of the first permission, while the type synchronizes.
	synchronizeRetry = 10 * time.Second
)

// warmer sends one request of each kind, as subject 0, before the traffic starts. The answers are checked: a run whose
// policy answers otherwise would measure the denials, not the decisions it means to measure.
type warmer struct {
	base        string
	client      *http.Client
	tokens      []string
	backend     string
	consistency string
}

// warmUp returns the time each request took. The first permission of a type synchronizes the whole type from its
// source: that takes as long as the source is large, and while it runs the server may answer 5xx (the computation of
// a request is bounded), so the permission is retried until it answers or the time is up.
func (w warmer) warmUp(ctx context.Context) ([]phase, error) {
	ctx, cancel := context.WithTimeout(ctx, warmUpTimeout)
	defer cancel()

	start := time.Now()
	if err := w.synchronize(ctx); err != nil {
		return nil, err
	}
	phases := []phase{{Name: "warm-up permission (first synchronization)", Seconds: time.Since(start).Seconds()}}

	probes := []struct {
		kind op
		req  func() (*http.Request, error)
		want func(decisions, allowed int) bool
	}{
		{opCheck1, w.check, granted},
		{opAuthzen, w.authzen, granted},
		{opAuthzenBatch, w.authzenBatch, granted},
		{opOnBehalf, w.onBehalf, granted},
	}
	for _, p := range probes {
		req, err := p.req()
		if err != nil {
			return nil, err
		}
		start := time.Now()
		status, body, err := w.do(req)
		if err != nil {
			return nil, fmt.Errorf("warm-up %s: %w", opNames[p.kind], err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("warm-up %s: status %d: %s", opNames[p.kind], status, head(body))
		}
		if decisions, allowed := countDecisions(body); !p.want(decisions, allowed) {
			return nil, fmt.Errorf("warm-up %s: subject %d may not read %s, the document it owns: %s",
				opNames[p.kind], warmSubject, warmDocument, head(body))
		}
		phases = append(phases, phase{Name: "warm-up " + opNames[p.kind], Seconds: time.Since(start).Seconds()})
	}
	return phases, nil
}

// granted is the answer the probes expect: at least one decision, and it is a grant.
func granted(decisions, allowed int) bool { return decisions > 0 && allowed == decisions }

// synchronize asks for the first permission of the document type until it answers 200. Any other status ends the
// warm-up at once: it is a setup error, not a synchronization still in progress.
func (w warmer) synchronize(ctx context.Context) error {
	for attempt := 1; ; attempt++ {
		req, err := w.request(ctx, http.MethodGet, "/permission", url.Values{"type": {"document"}, "limit": {"20"}}, w.tokens[warmSubject], "")
		if err != nil {
			return err
		}
		status, body, err := w.do(req)
		switch {
		case err == nil && status == http.StatusOK:
			return nil
		case err == nil && status < 500:
			return fmt.Errorf("warm-up permission: status %d: %s", status, head(body))
		}
		slog.Info("the first permission is not ready yet, trying again", "attempt", attempt, "status", status, "error", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("warm-up permission: no answer in %v: %w", warmUpTimeout, ctx.Err())
		case <-time.After(synchronizeRetry):
		}
	}
}

func (w warmer) check() (*http.Request, error) {
	return w.request(context.Background(), http.MethodPost, "/check", nil, w.tokens[warmSubject],
		fmt.Sprintf(`{"checks":[{"type":"document","resource_id":%q,"action":"read"}]}`, warmDocument))
}

func (w warmer) authzen() (*http.Request, error) {
	return w.request(context.Background(), http.MethodPost, "/access/v1/evaluation", nil, w.tokens[warmSubject],
		fmt.Sprintf(`{"subject":{"type":"user","id":%q},"resource":{"type":"document","id":%q},"action":{"name":"read"}}`,
			subjectName(warmSubject), warmDocument))
}

func (w warmer) authzenBatch() (*http.Request, error) {
	return w.request(context.Background(), http.MethodPost, "/access/v1/evaluations", nil, w.tokens[warmSubject],
		fmt.Sprintf(`{"subject":{"type":"user","id":%q},"action":{"name":"read"},"evaluations":[{"resource":{"type":"document","id":%q}}]}`,
			subjectName(warmSubject), warmDocument))
}

func (w warmer) onBehalf() (*http.Request, error) {
	return w.request(context.Background(), http.MethodPost, "/on_behalf/check", nil, w.backend,
		fmt.Sprintf(`{"subject":%q,"claims":{"department":%q},"checks":[{"type":"document","resource_id":%q,"action":"read"}]}`,
			subjectName(warmSubject), departmentName(warmSubject), warmDocument))
}

// request builds a request of the check API for the service, with the consistency of the run.
func (w warmer) request(ctx context.Context, method, path string, query url.Values, token, body string) (*http.Request, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("consistency", w.consistency)
	req, err := http.NewRequestWithContext(ctx, method, w.base+"/service/"+serviceSlug+path+"?"+query.Encode(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do performs the request and reads the whole body.
func (w warmer) do(req *http.Request) (int, []byte, error) {
	resp, err := w.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// head is the start of a body, for an error message.
func head(body []byte) string {
	return string(body[:min(len(body), 512)])
}
