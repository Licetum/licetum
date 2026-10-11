package engine

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/logx"
	"github.com/licetum/licetum/core/model"
)

// LogDecisions writes one decision event per answer of a check: the subject, the question and the answer, with the
// request id that ties it to the access log line of the request. Denials are always written; allowed answers are
// sampled (see logx.Switch.KeepAllowed). Nothing is built when the decisions are switched off.
func (a *App) LogDecisions(ctx context.Context, serviceID uuid.UUID, subject string, checks []model.CheckRequest,
	results []model.CheckResult, consistency Consistency) {
	if a.Logs == nil || !a.Logs.Wants(logx.DecisionCategory) {
		return
	}
	requestID := ""
	if t := TraceFrom(ctx); t != nil {
		requestID = t.Request()
	}
	for i, c := range checks {
		if i >= len(results) {
			break
		}
		allowed := results[i].Allowed
		if allowed && !a.Logs.KeepAllowed() {
			continue
		}
		resourceID := ""
		if c.ResourceID != nil {
			resourceID = *c.ResourceID
		}
		slog.InfoContext(ctx, "decision",
			"request_id", requestID, "service", serviceID.String(), "subject", subject,
			"type", c.Type, "resource_id", resourceID, "action", c.Action,
			"allowed", allowed, "consistency", string(consistency))
	}
}
