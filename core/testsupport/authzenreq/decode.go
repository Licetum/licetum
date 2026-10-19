package authzenreq

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/licetum/licetum/core/model"
)

// StatusError is an answer that is not a 200: the body is the error of the API, not a decision or a page.
type StatusError struct {
	Status int
	// Detail is the detail of the error body when it has one, else the start of the body.
	Detail string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("authzen: status %d: %s", e.Status, e.Detail)
}

// EvaluationError is an evaluation of a batch that the API could not decide: it answers decision false with
// context.error. It is not a denial.
type EvaluationError struct {
	// Index is the place of the evaluation in the request (0 for a request without evaluations).
	Index   int
	Status  int
	Message string
}

func (e *EvaluationError) Error() string {
	return fmt.Sprintf("authzen: evaluation %d could not be decided: status %d: %s", e.Index, e.Status, e.Message)
}

// Decision is the answer to one evaluation.
type Decision struct {
	Allowed bool
	// Explanation is context.reason_admin: present when the request asked for Explain.
	Explanation *model.Explanation
	// Context is the context of the answer as it came, reason_admin included; nil when the answer has none. A batch that
	// stopped early (deny_on_first_deny, permit_on_first_permit) names the semantic in its reason.
	Context map[string]any
}

// answer is the members of an evaluation answer and of a search answer that the decoders read.
type answer struct {
	Decision    *bool                      `json:"decision"`
	Context     json.RawMessage            `json:"context"`
	Evaluations []answer                   `json:"evaluations"`
	Results     []resultItem               `json:"results"`
	Page        *model.AuthzenPageResponse `json:"page"`
}

type resultItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type contextMembers struct {
	ReasonAdmin *model.Explanation `json:"reason_admin"`
	Error       *struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

// read is the first step of every decoder: an answer that is not a 200 is a StatusError, a body that is not JSON an error.
func read(status int, body []byte) (*answer, error) {
	if status != http.StatusOK {
		return nil, &StatusError{Status: status, Detail: detailOf(body)}
	}
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("authzen: the answer is not JSON: %w: %s", err, abbreviate(string(body)))
	}
	return &a, nil
}

// detailOf is the detail member of an error body, or the start of the body.
func detailOf(body []byte) string {
	var e struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &e) == nil && e.Detail != "" {
		return e.Detail
	}
	return abbreviate(string(body))
}

func abbreviate(s string) string {
	const most = 200
	if s = strings.TrimSpace(s); len(s) > most {
		return s[:most] + "..."
	}
	return s
}

// decision turns the answer to one evaluation into a Decision, or the *EvaluationError that it is.
func (a *answer) decision(index int) (Decision, error) {
	var d Decision
	if len(a.Context) > 0 {
		var members contextMembers
		if err := json.Unmarshal(a.Context, &members); err != nil {
			return d, fmt.Errorf("authzen: the context of evaluation %d is not an object: %w", index, err)
		}
		if members.Error != nil {
			return d, &EvaluationError{Index: index, Status: members.Error.Status, Message: members.Error.Message}
		}
		d.Explanation = members.ReasonAdmin
		if err := json.Unmarshal(a.Context, &d.Context); err != nil {
			return d, err
		}
	}
	if a.Decision == nil {
		return d, fmt.Errorf("authzen: evaluation %d has no decision", index)
	}
	d.Allowed = *a.Decision
	return d, nil
}

// Decisions are the decisions of the answer to an Evaluation or to an Evaluations request, in the order asked (fewer
// when the semantic of the batch stopped it). An evaluation that could not be decided makes it an *EvaluationError, an
// answer that is not a 200 a *StatusError.
func Decisions(status int, body []byte) ([]Decision, error) {
	a, err := read(status, body)
	if err != nil {
		return nil, err
	}
	if a.Evaluations == nil {
		d, err := a.decision(0)
		if err != nil {
			return nil, err
		}
		return []Decision{d}, nil
	}
	out := make([]Decision, len(a.Evaluations))
	for i := range a.Evaluations {
		if out[i], err = a.Evaluations[i].decision(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Allowed is the decision of the answer to a request of one evaluation.
func Allowed(status int, body []byte) (bool, error) {
	ds, err := Decisions(status, body)
	if err != nil {
		return false, err
	}
	if len(ds) != 1 {
		return false, fmt.Errorf("authzen: %d decisions, want 1", len(ds))
	}
	return ds[0].Allowed, nil
}

// results are the identifiers of the results of a search and the token of the next page (empty on the last).
func results(status int, body []byte, name func(resultItem) string) (ids []string, next string, err error) {
	a, err := read(status, body)
	if err != nil {
		return nil, "", err
	}
	ids = make([]string, len(a.Results))
	for i, r := range a.Results {
		ids[i] = name(r)
	}
	if a.Page != nil {
		next = a.Page.NextToken
	}
	return ids, next, nil
}

// ResourceIDs are the ids of the resources of the answer to SearchResource, in the order the API gives them (by id), and
// the token of the next page.
func ResourceIDs(status int, body []byte) (ids []string, next string, err error) {
	return results(status, body, func(r resultItem) string { return r.ID })
}

// Actions are the action names of the answer to SearchAction, and the token of the next page.
func Actions(status int, body []byte) (names []string, next string, err error) {
	return results(status, body, func(r resultItem) string { return r.Name })
}

// SubjectIDs are the subject ids of the answer to SearchSubject, and the token of the next page.
func SubjectIDs(status int, body []byte) (ids []string, next string, err error) {
	return results(status, body, func(r resultItem) string { return r.ID })
}
