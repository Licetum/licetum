package api

import (
	"context"
	"net/http"

	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/model"
)

// SubjectPath names a known subject.
type SubjectPath struct {
	Subject string `path:"subject" minLength:"1" doc:"The subject as the rules name it (preferred_username, or sub)" example:"alice"`
}

// registerSubjects: the registry shows people, so it is governed by the policy of the memberships — whoever manages
// who belongs to a group may see who is known.
func (h *adminHandlers) registerSubjects(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_subjects",
		Method:      http.MethodGet,
		Path:        "/subject",
		Summary:     "Known subjects",
		Description: "The subjects Licetum has seen in verified tokens (SUBJECTS__ENABLED), the most recently seen first. search matches the subject and the display name.",
		Tags:        []string{"Subject"},
	}, requireWholeType(model.PlatformGroupMember, model.ActionRead)), func(ctx context.Context, in *struct {
		Search string `query:"search" required:"false" maxLength:"200" doc:"Part of the subject or of the name"`
		Limit  int    `query:"limit" required:"false" default:"50" minimum:"1" maximum:"200" doc:"Page size"`
		Offset int    `query:"offset" required:"false" default:"0" minimum:"0" doc:"How many to skip"`
	}) (*server.Out[*model.KnownSubjectList], error) {
		out, err := h.app.ListSubjects(ctx, in.Search, in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_subject",
		Method:      http.MethodGet,
		Path:        "/subject/{subject}",
		Summary:     "A known subject",
		Description: "The subject with the claims kept for it (only those the policy reads) and the groups it is in as of those claims.",
		Tags:        []string{"Subject"},
	}, requireWholeType(model.PlatformGroupMember, model.ActionRead)), func(ctx context.Context, in *SubjectPath) (*server.Out[*model.KnownSubjectDetail], error) {
		out, err := h.app.GetSubject(ctx, in.Subject)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "forget_subject",
		Method:      http.MethodDelete,
		Path:        "/subject/{subject}",
		Summary:     "Forget a subject",
		Description: "Removes the subject from the registry. It is noted again the next time one of its tokens is verified.",
		Tags:        []string{"Subject"},
	}, requireWholeType(model.PlatformGroupMember, model.ActionWrite)), func(ctx context.Context, in *SubjectPath) (*server.Out[string], error) {
		if err := h.app.ForgetSubject(ctx, in.Subject); err != nil {
			return nil, err
		}
		return server.OK(in.Subject)
	})
}
