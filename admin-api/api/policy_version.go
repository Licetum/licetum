package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
	"github.com/licetum/licetum/core/server"
)

type policyContext interface{ huma.Context }

type policyBody struct {
	policyContext
	body io.Reader
}

func (c policyBody) BodyReader() io.Reader { return c.body }
func (c policyBody) Unwrap() huma.Context  { return c.policyContext }

// Version rejection precedes validation of the old dialect's nested fields.
// Keep Huma's normalized body bound and deadline, then replay v2 for normal validation.
func rejectPolicyV1(ctx huma.Context, next func(huma.Context)) {
	op := ctx.Operation()
	if op.BodyReadTimeout > 0 {
		_ = ctx.SetReadDeadline(time.Now().Add(op.BodyReadTimeout))
	}
	reader := ctx.BodyReader()
	if op.MaxBodyBytes > 0 {
		reader = io.LimitReader(reader, op.MaxBodyBytes+1)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		apiErr := server.LocalizeError(ctx.Context(), op.OperationID, apperr.New(http.StatusBadRequest, i18n.ValidationFailed).WithSuffix(err.Error()))
		ctx.SetHeader("Content-Type", "application/json")
		ctx.SetStatus(apiErr.GetStatus())
		_ = json.NewEncoder(ctx.BodyWriter()).Encode(apiErr)
		return
	}
	var envelope struct {
		Format string `json:"format"`
	}
	if (op.MaxBodyBytes <= 0 || int64(len(raw)) <= op.MaxBodyBytes) && json.Unmarshal(raw, &envelope) == nil && envelope.Format == "licetum.policy/v1" {
		apiErr := server.LocalizeError(ctx.Context(), op.OperationID, apperr.Unprocessable(i18n.PolicyV1NotImported))
		ctx.SetHeader("Content-Type", "application/json")
		ctx.SetStatus(apiErr.GetStatus())
		_ = json.NewEncoder(ctx.BodyWriter()).Encode(apiErr)
		return
	}
	next(policyBody{policyContext: ctx, body: bytes.NewReader(raw)})
}
