package loadtest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// warmSubject is the subject the warm-up asks as, and warmDocument the document it asks about: the owner pattern grants
// subject 0 the documents it owns, and document 0 is owned by subject 0.
const (
	warmSubject  = 0
	warmDocument = "d-0"
	// synchronizeRetry is the pause between two tries of the first search, while the type synchronizes.
	synchronizeRetry = 10 * time.Second
	// searchPage is the most results a search gives in one page (the API's own limit).
	searchPage = 1000
)

// warmer sends one request of each kind, as subject 0, before the traffic starts. The answers are checked: a run whose
// policy answers otherwise would measure the denials, not the decisions it means to measure.
type warmer struct {
	base        string
	client      *http.Client
	tokens      []string
	backend     string
	profile     Profile
	consistency string
	// retry is the pause between two tries of the first search.
	retry time.Duration
}

// warmUp returns the time each request took. The first search of a type synchronizes the whole type from its source:
// that takes as long as the source is large, and while it runs the server may answer 5xx (the computation of a request
// is bounded) or answer from what it has, so the search is repeated until it lists what the policy gives subject 0 or the
// time is up.
func (w warmer) warmUp(ctx context.Context) ([]phase, error) {
	ctx, cancel := context.WithTimeout(ctx, warmUpTimeout)
	defer cancel()

	start := time.Now()
	if err := w.synchronize(ctx); err != nil {
		return nil, err
	}
	phases := []phase{{Name: "warm-up " + opNames[opSearchResource] + " (first synchronization)", Seconds: time.Since(start).Seconds()}}

	probes := []struct {
		kind op
		req  authzenreq.Request
		// token is the token of the caller: the subject's own, or the trusted client's.
		token string
	}{
		{opEvaluation, w.evaluation(), w.tokens[warmSubject]},
		{opEvaluations10, w.evaluations(10), w.tokens[warmSubject]},
		{opEvaluationProperties, w.evaluationProperties(), w.backend},
	}
	for _, p := range probes {
		start := time.Now()
		status, body, err := w.send(ctx, p.req, p.token)
		if err != nil {
			return nil, fmt.Errorf("warm-up %s: %w", opNames[p.kind], err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("warm-up %s: status %d: %s", opNames[p.kind], status, head(body))
		}
		// A batch that could not decide an evaluation answers decision false for it, which fails the probe here as a
		// denial of the owner does: the probe wants every decision granted.
		if decisions, allowed := countDecisions(body); decisions == 0 || allowed != decisions {
			return nil, fmt.Errorf("warm-up %s: subject %d may not read %s, the document it owns: %s",
				opNames[p.kind], warmSubject, warmDocument, head(body))
		}
		phases = append(phases, phase{Name: "warm-up " + opNames[p.kind], Seconds: time.Since(start).Seconds()})
	}
	return phases, nil
}

// synchronize lists the documents subject 0 may read until the list is as long as the policy makes it. A request of a
// type that is not synchronized waits a while for the synchronization and then answers from what there is: a list with
// fewer documents than the policy grants, or none, with nothing in it to tell it from a final answer. So the profile,
// which knows what it granted, says when the list is whole. A list longer than that cannot be a synchronization in
// progress, and any status other than 200 below 500 ends the warm-up at once: it is a setup error.
func (w warmer) synchronize(ctx context.Context) error {
	want := w.profile.readableBy(warmSubject)
	for attempt := 1; ; attempt++ {
		got, status, body, err := w.listReadable(ctx)
		switch {
		case err == nil && status == http.StatusOK && got == want:
			return nil
		case err == nil && status == http.StatusOK && got > want:
			return fmt.Errorf("warm-up %s: subject %d may read %d documents, the policy grants it %d",
				opNames[opSearchResource], warmSubject, got, want)
		case err == nil && status != http.StatusOK && status < 500:
			return fmt.Errorf("warm-up %s: status %d: %s", opNames[opSearchResource], status, head(body))
		}
		slog.Info("the first search is not whole yet, trying again", "attempt", attempt, "status", status, "listed", got, "want", want, "error", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("warm-up %s: the list has %d of the %d documents after %v: %w", opNames[opSearchResource], got, want, warmUpTimeout, ctx.Err())
		case <-time.After(w.retry):
		}
	}
}

// listReadable pages through the documents subject 0 may read and counts them. A page that is not a 200 ends it: the
// status and the body of that page come back.
func (w warmer) listReadable(ctx context.Context) (count, status int, body []byte, err error) {
	token := ""
	for {
		req := authzenreq.SearchResource(serviceSlug, subjectName(warmSubject), documentType, readAction,
			append(w.freshness(), authzenreq.Page(searchPage, token))...)
		status, body, err = w.send(ctx, req, w.tokens[warmSubject])
		if err != nil || status != http.StatusOK {
			return count, status, body, err
		}
		ids, next, err := authzenreq.ResourceIDs(status, body)
		if err != nil {
			return count, status, body, err
		}
		count += len(ids)
		if next == "" {
			return count, status, body, nil
		}
		token = next
	}
}

// evaluation asks whether subject 0 may read the document it owns.
func (w warmer) evaluation() authzenreq.Request {
	return authzenreq.Evaluation(serviceSlug, subjectName(warmSubject), authzenreq.On(documentType, warmDocument), readAction, w.freshness()...)
}

// evaluations asks the same question n times in one request.
func (w warmer) evaluations(n int) authzenreq.Request {
	questions := make([]authzenreq.Question, n)
	for i := range questions {
		questions[i] = authzenreq.Question{Resource: authzenreq.On(documentType, warmDocument), Action: readAction}
	}
	return authzenreq.Evaluations(serviceSlug, subjectName(warmSubject), questions, w.freshness()...)
}

// evaluationProperties asks as the trusted client whether subject 0 may read its document: the client names the person
// and brings the claims of the person, the department that picks its groups.
func (w warmer) evaluationProperties() authzenreq.Request {
	return authzenreq.Evaluation(serviceSlug, subjectName(warmSubject), authzenreq.On(documentType, warmDocument), readAction,
		append(w.freshness(), authzenreq.WithProperties(map[string]any{"department": departmentName(warmSubject)}))...)
}

// freshness is the option of the consistency of the run.
func (w warmer) freshness() []authzenreq.Option {
	if w.consistency == "higher" {
		return []authzenreq.Option{authzenreq.Fresh()}
	}
	return nil
}

// send performs the request with the token and reads the whole body.
func (w warmer) send(ctx context.Context, r authzenreq.Request, token string) (int, []byte, error) {
	req, err := newRequest(ctx, w.base, r, token)
	if err != nil {
		return 0, nil, err
	}
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
