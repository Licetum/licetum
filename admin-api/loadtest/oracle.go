package loadtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/licetum/licetum/core/testsupport/authzenreq"
)

// Oracle is the correctness check of a measured window: a sample of the answers the AuthZEN API gave during the load,
// compared with the decisions the generated policy must give.
type Oracle struct {
	Checked int `json:"checked"`
	// Mismatches are the answers the server gave (HTTP 200) that differ from the expected decision: wrong answers.
	Mismatches int `json:"mismatches"`
	// Unanswered are the questions that got no decision: a transport error, a timeout, a status other than 200, or an
	// evaluation the API could not decide (it answers decision false with context.error, which is not a denial). They are
	// failures of availability, which the error rate and the latency of the window show, not wrong answers.
	Unanswered int        `json:"unanswered,omitempty"`
	Granted    int        `json:"expected_granted"`
	Examples   []Mismatch `json:"examples,omitempty"`
}

// Mismatch is one answer that differs from the expected one.
type Mismatch struct {
	Subject  string `json:"subject"`
	Document string `json:"document"`
	Expected bool   `json:"expected"`
	Got      bool   `json:"got"`
	Status   int    `json:"status"`
}

// expectedAllowed is the decision the generated policy must give to subject u reading document d. It is computed from
// the rule formulas of ruleFor (seed.go) and does not ask the server: it is the semantics of the generated policy, the
// deny-wins and the exception rules included.
//
// The rules that can grant a document, in terms of the rule index r:
//   - the owner pattern (rule 0): document g is owned by subject g mod users;
//   - kinds 4-6 (a subject on a document) and 9 (an exception: an allow for a subject on a document);
//   - kinds 0-3 (a department's group on a folder) and 7 (a department's claim on a folder), where the department of a
//     rule is (r div 10) mod groups and its folder is r mod folders; a document inherits its folder's grant.
//
// A denial is a kind-8 rule: the department's group is denied a folder, and so every document of it. An exception lifts
// the denial for the document it names, as the exception lies below the folder.
func (p Profile) expectedAllowed(u, d int) bool {
	users, groups, rules := p.Users, p.Groups, p.Rules
	documents, folders := p.documents(), p.folders()
	dept := u % groups
	folder := d % folders

	granted := rules > 0 && d%users == u

	lifted := false
	for r := u; r < rules; r += users {
		if r == 0 {
			continue
		}
		kind := r % 10
		if r%documents != d {
			continue
		}
		switch kind {
		case 4, 5, 6:
			granted = true
		case 9:
			granted = true
			lifted = true
		}
	}
	denied := false
	for r := folder; r < rules; r += folders {
		if r == 0 {
			continue
		}
		kind := r % 10
		if (r/10)%groups != dept {
			continue
		}
		switch kind {
		case 0, 1, 2, 3, 7:
			granted = true
		case 8:
			denied = true
		}
	}
	return granted && (!denied || lifted)
}

// readableBy is how many documents subject u may read under the generated policy: the documents expectedAllowed grants it.
func (p Profile) readableBy(u int) int {
	n := 0
	for d := range p.documents() {
		if p.expectedAllowed(u, d) {
			n++
		}
	}
	return n
}

// oracleSample is a pair the sampler asks about: a subject, and a document it may or may not read. The sample mixes the
// documents a subject owns, the documents of its department's folders, and any document, so that both answers occur.
func (p Profile) oracleSample(rnd *rand.Rand) (int, int) {
	u := rnd.IntN(p.Users)
	switch rnd.IntN(4) {
	case 0, 1:
		owned := rnd.IntN((p.documents() + p.Users - 1) / p.Users)
		d := u + p.Users*owned
		if d < p.documents() {
			return u, d
		}
		return u, rnd.IntN(p.documents())
	case 2:
		dept := u % p.Groups
		r := 10*(dept+p.Groups*rnd.IntN((p.Rules+10*p.Groups-1)/(10*p.Groups))) + []int{0, 1, 2, 3, 7}[rnd.IntN(5)]
		if r >= p.Rules || r == 0 {
			return u, rnd.IntN(p.documents())
		}
		f := r % p.folders()
		d := f + p.folders()*rnd.IntN((p.documents()+p.folders()-1)/p.folders())
		if d < p.documents() {
			return u, d
		}
		return u, f
	default:
		return u, rnd.IntN(p.documents())
	}
}

// runOracle asks the AuthZEN API about sampled pairs at the given rate until stop is closed, and compares each answer
// with expectedAllowed. The requests are real evaluations with the subject's token, so they load the server as one more
// client; they are counted apart from the k6 traffic.
func runOracle(ctx context.Context, base, service string, p Profile, tokens []string, seed uint64, rate float64, stop <-chan struct{}) Oracle {
	var out Oracle
	if rate <= 0 || len(tokens) == 0 {
		return out
	}
	rnd := rand.New(rand.NewPCG(seed, 0x6f7261636c65)) //nolint:gosec // picks the pairs to check: no secret depends on it
	client := &http.Client{Timeout: requestTimeout}
	ticker := time.NewTicker(time.Duration(float64(time.Second) / rate))
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return out
		case <-ctx.Done():
			return out
		case <-ticker.C:
		}
		u, d := p.oracleSample(rnd)
		expected := p.expectedAllowed(u, d)
		got, status, err := askEvaluation(ctx, client, base, service, subjectName(u), tokens[u], fmt.Sprintf("d-%d", d))
		out.Checked++
		if expected {
			out.Granted++
		}
		unanswered := err != nil || status != http.StatusOK
		if unanswered {
			out.Unanswered++
		}
		wrong := !unanswered && got != expected
		if wrong {
			out.Mismatches++
			if len(out.Examples) < 10 {
				out.Examples = append(out.Examples, Mismatch{
					Subject: subjectName(u), Document: fmt.Sprintf("d-%d", d),
					Expected: expected, Got: got, Status: status,
				})
			}
		}
	}
}

// askEvaluation asks whether the subject may read the document, with the subject's own token, and returns the decision.
// A question that got no decision returns a status other than 200 (the API refused or failed it), or an error: a
// transport failure, an answer that is not one, or an evaluation the API could not decide (decision false with
// context.error), which must not be mistaken for a denial.
func askEvaluation(ctx context.Context, client *http.Client, base, service, subject, token, doc string) (bool, int, error) {
	req, err := newRequest(ctx, base, authzenreq.Evaluation(service, subject, authzenreq.On(documentType, doc), readAction), token)
	if err != nil {
		return false, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, resp.StatusCode, err
	}
	allowed, err := authzenreq.Allowed(resp.StatusCode, raw)
	var refused *authzenreq.StatusError
	if errors.As(err, &refused) {
		return false, refused.Status, nil
	}
	return allowed, resp.StatusCode, err
}
