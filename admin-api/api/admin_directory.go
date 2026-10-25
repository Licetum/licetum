package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/licetum/licetum/core/model"
	"github.com/licetum/licetum/core/server"
)

// DirectoryEntryPath names a person in the directory of a service.
type DirectoryEntryPath struct {
	ServicePath
	SubjectType string `path:"subject_type" minLength:"1" maxLength:"64" doc:"The type of the subject" example:"user"`
	SubjectID   string `path:"subject_id" minLength:"1" maxLength:"512" doc:"The id of the subject; a character such as / is sent percent-encoded" example:"alice"`
}

// registerDirectory: the people of a service. Until the roles of the service (P4) the directory is governed by the policy of
// the memberships, as the registry it replaces was: whoever manages who belongs to a group may see and send who is known.
func (h *adminHandlers) registerDirectory(api huma.API) {
	const base = "/service/{service_id}/subjects"
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "put_directory",
		Method:      http.MethodPut,
		Path:        base,
		Summary:     "Send people to the directory",
		Description: "Writes up to 1000 people of the service, each once: the application sends the people its PEPs ask about. An entry that is already as sent is left untouched (no new version, no new updated_at; only the time it was sent moves, at most hourly); a new one is created and a changed one replaced as a whole. The properties are stored as sent. A write takes no revision.",
		Tags:        []string{"Subject"},
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, requireWholeType(model.PlatformGroupMember, model.ActionWrite)), func(ctx context.Context, in *struct {
		ServicePath
		Body model.DirectoryPut
	}) (*server.Out[*model.DirectoryWriteResult], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.PutDirectory(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_directory",
		Method:      http.MethodGet,
		Path:        base,
		Summary:     "People of a service",
		Description: "The directory of the service, by type and then by id. search matches the id and the name.",
		Tags:        []string{"Subject"},
		Errors:      []int{http.StatusNotFound},
	}, requireWholeType(model.PlatformGroupMember, model.ActionRead)), func(ctx context.Context, in *struct {
		ServicePath
		Type   string `query:"type" required:"false" maxLength:"64" doc:"Only people of this type"`
		Search string `query:"search" required:"false" maxLength:"200" doc:"Part of the id or of the name"`
		Limit  int    `query:"limit" required:"false" default:"50" minimum:"1" maximum:"200" doc:"Page size"`
		Offset int    `query:"offset" required:"false" default:"0" minimum:"0" doc:"How many to skip"`
	}) (*server.Out[*model.DirectoryList], error) {
		out, err := h.app.ListDirectory(ctx, in.ServiceID, in.Type, in.Search, in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_directory_settings",
		Method:      http.MethodGet,
		Path:        base + "/settings",
		Summary:     "Settings of the directory",
		Tags:        []string{"Subject"},
		Errors:      []int{http.StatusNotFound},
	}, requireWholeType(model.PlatformGroupMember, model.ActionRead)), func(ctx context.Context, in *ServicePath) (*server.Out[*model.DirectorySettings], error) {
		out, err := h.app.GetDirectorySettings(ctx, in.ServiceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "put_directory_settings",
		Method:      http.MethodPut,
		Path:        base + "/settings",
		Summary:     "Change the settings of the directory",
		Description: "retention_days is how long an entry that is not sent again is kept; the worker removes the others, hourly, in chunks. null keeps every entry until it is deleted (the default).",
		Tags:        []string{"Subject"},
		Errors:      []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, requireWholeType(model.PlatformGroupMember, model.ActionWrite)), func(ctx context.Context, in *struct {
		ServicePath
		Body model.DirectorySettings
	}) (*server.Out[*model.DirectorySettings], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.PutDirectorySettings(ctx, in.ServiceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_directory_entry",
		Method:      http.MethodGet,
		Path:        base + "/{subject_type}/{subject_id}",
		Summary:     "A person of the service",
		Description: "The entry with its properties and the groups the person is in now, decided with the stored properties as claims.",
		Tags:        []string{"Subject"},
		Errors:      []int{http.StatusNotFound},
	}, requireWholeType(model.PlatformGroupMember, model.ActionRead)), func(ctx context.Context, in *DirectoryEntryPath) (*server.Out[*model.DirectoryEntryDetail], error) {
		out, err := h.app.GetDirectoryEntry(ctx, in.ServiceID, in.SubjectType, in.SubjectID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_directory_entry",
		Method:      http.MethodDelete,
		Path:        base + "/{subject_type}/{subject_id}",
		Summary:     "Remove a person from the directory",
		Description: "Removes the entry. The person is in the directory again the next time the application sends them.",
		Tags:        []string{"Subject"},
		Errors:      []int{http.StatusNotFound},
	}, requireWholeType(model.PlatformGroupMember, model.ActionWrite)), func(ctx context.Context, in *DirectoryEntryPath) (*server.Out[string], error) {
		if err := h.app.DeleteDirectoryEntry(ctx, in.ServiceID, in.SubjectType, in.SubjectID); err != nil {
			return nil, err
		}
		return server.OK(in.SubjectID)
	})
}
