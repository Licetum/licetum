package api

import (
	"context"
	"net/http"
	"time"

	"github.com/licetum/licetum/core/server"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/licetum/licetum/admin-api/service"
	"github.com/licetum/licetum/core/model"
)

// adminOpsTags are the tags of the operations in admin_ops.go.
var adminOpsTags = []*huma.Tag{
	{Name: "Change log", Description: "The record of every change the admin API makes: who made it, when, and the entity before and after the change."},
	{Name: "Batch", Description: "Several writes in one transaction, and the history of the policy: reverting one change, or rolling back to a position of the change log."},
	{Name: "Policy", Description: "The tools of a service's policy: tests with their coverage, the linter, and the export and import of the policy as a document."},
	{Name: "Log settings", Description: "The structure of the process logs: format, the fields and their names, the event categories, the treatment of the subject and the sampling of allowed decisions."},
	{Name: "Subject", Description: "The subject directory of a service: the people its application sent, with their properties and the groups they are in."},
	{Name: "Simulation", Description: "What a subject with given properties would be allowed to do, asked as the AuthZEN API of the PDP asks it and without a PEP client: for administrators checking the rules."},
}

// ChangesQuery is the filter of GET /changes.
type ChangesQuery struct {
	After     int64     `query:"after" required:"false" minimum:"0" doc:"Read the records after this position: the id of the last record of the previous read"`
	ServiceID uuid.UUID `query:"service_id" required:"false" format:"uuid" doc:"Only the records of this service"`
	Entity    string    `query:"entity" required:"false" doc:"Only the records of this kind of entity: service, type_resource, synchronizer, group, group_member, role, role_permission, assignment_rule or log_settings"`
	Limit     int       `query:"limit" required:"false" minimum:"1" maximum:"500" default:"100" doc:"At most this many records"`
	Wait      int       `query:"wait" required:"false" minimum:"0" maximum:"30" doc:"Seconds to wait for the first record after the position, when there is none yet: a client follows the log with it (a long poll)"`
}

// registerOperations registers the operations of the change log, the log settings and the simulation.
func (h *adminHandlers) registerOperations(api huma.API) {
	server.Register(api, withPolicy(huma.Operation{
		OperationID: "list_changes",
		Method:      http.MethodGet,
		Path:        "/changes",
		Summary:     "List the change log",
		Description: "The records of the change log after the given position, oldest first. Every write of the admin API leaves one record, in the same transaction as the write. Pass the id of the last record as after to read the next ones.",
		Tags:        []string{"Change log"},
	}, requireWholeType(model.PlatformService, model.ActionRead)), func(ctx context.Context, in *ChangesQuery) (*server.Out[[]model.ChangeRecord], error) {
		f := service.ChangeFilter{After: in.After, Entity: in.Entity, Limit: in.Limit}
		if in.ServiceID != uuid.Nil {
			id := in.ServiceID
			f.Service = &id
		}
		out, err := h.app.WaitChanges(ctx, f, time.Duration(in.Wait)*time.Second)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "get_log_settings",
		Method:      http.MethodGet,
		Path:        "/settings/logs",
		Summary:     "Read the log settings",
		Description: "The structure of the process logs, as every replica applies it.",
		Tags:        []string{"Log settings"},
	}, requireWholeType(model.PlatformService, model.ActionRead)), func(ctx context.Context, _ *struct{}) (*server.Out[model.LogSettings], error) {
		out, err := h.app.GetLogSettings(ctx)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})

	server.Register(api, withPolicy(huma.Operation{
		OperationID: "put_log_settings",
		Method:      http.MethodPut,
		Path:        "/settings/logs",
		Summary:     "Replace the log settings",
		Description: "Replaces the structure of the process logs. The settings are checked the way the logger applies them, recorded in the change log, and applied to every replica within a few seconds, without a restart. The subject is written as a keyed hash by default; the salt is never returned.",
		Tags:        []string{"Log settings"},
		Errors:      []int{http.StatusUnprocessableEntity},
	}, requireWholeType(model.PlatformService, model.ActionWrite)), func(ctx context.Context, in *struct{ Body model.LogSettings }) (*server.Out[model.LogSettings], error) {
		out, err := h.app.PutLogSettings(ctx, in.Body)
		if err != nil {
			return nil, err
		}
		return server.OK(out)
	})
}

// registerSimulation registers the simulation of the AuthZEN API for the administrators: the requests and the answers of the PDP,
// for the subject and the properties a request names (core/server/simulate.go). Reading the service is the right it needs, except for
// the subject search, which lists the people of the directory of the service and so needs what the directory itself needs (until the
// roles of P4: the right to read the memberships).
func (h *adminHandlers) registerSimulation(api huma.API) {
	server.RegisterSimulation(api, h.app.App, h.app.Cfg, func(op huma.Operation) huma.Operation {
		if op.OperationID == "simulate_search_subject" {
			return withPolicy(op, requireWholeType(model.PlatformGroupMember, model.ActionRead))
		}
		return withPolicy(op, requireResource(model.PlatformService, model.ActionRead, "service_id"))
	})
}
