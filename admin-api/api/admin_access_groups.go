package api

import (
	"context"
	"net/http"

	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

// AccessGroupPath names an access group of a resource type.
type AccessGroupPath struct {
	TypeResourcePath
	AccessGroupID uuid.UUID `path:"access_group_id" format:"uuid" doc:"Access group id"`
}

// registerAccessGroups: the access groups of a resource type are part of its permissions, so they are governed by
// the type's policy — reading them needs read on the type, changing them needs write on it.
func (h *adminHandlers) registerAccessGroups(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_access_groups",
		Method:      http.MethodGet,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/access_group",
		Summary:     "Access groups of a resource type",
		Description: "The named sets of access levels of the type. A rule or a role item lists @name in its access and gets the levels of the group.",
		Tags:        []string{"Type resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionRead, "type_resource_id")), func(ctx context.Context, in *TypeResourcePath) (*server.Out[[]model.AccessGroup], error) {
		out, err := h.app.ListAccessGroups(ctx, in.TypeResourceID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "create_access_group",
		Method:      http.MethodPost,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/access_group",
		Summary:     "Create an access group",
		Description: "Names a set of access levels of the type. The name is unique within the type (409). A level is neither * nor a group reference: a deny of everything is an inline deny rule with *.",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Type resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		TypeResourcePath
		Body model.AccessGroupInput
	}) (*server.Out[*model.AccessGroup], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.CreateAccessGroup(ctx, in.TypeResourceID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "update_access_group",
		Method:      http.MethodPatch,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/access_group/{access_group_id}",
		Summary:     "Update an access group",
		Description: "Replaces the levels of the group, and its name. A group that a rule or a role item still names cannot be renamed (409, extra.blocked_by); its levels can change, and the rules that name it follow.",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Type resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *struct {
		AccessGroupPath
		Body model.AccessGroupInput
	}) (*server.Out[*model.AccessGroup], error) {
		if err := validate(&in.Body); err != nil {
			return nil, err
		}
		out, err := h.app.UpdateAccessGroup(ctx, in.TypeResourceID, in.AccessGroupID, &in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "delete_access_group",
		Method:      http.MethodDelete,
		Path:        "/service/{service_id}/type_resource/{type_resource_id}/access_group/{access_group_id}",
		Summary:     "Delete an access group",
		Description: "Not allowed while a rule or a role item names the group (409, extra.blocked_by).",
		Errors:      []int{http.StatusConflict},
		Tags:        []string{"Type resource"},
	}, requireResource(model.PlatformTypeResource, model.ActionWrite, "type_resource_id")), func(ctx context.Context, in *AccessGroupPath) (*server.Out[uuid.UUID], error) {
		out, err := h.app.DeleteAccessGroup(ctx, in.TypeResourceID, in.AccessGroupID)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}
