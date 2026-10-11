package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/model"
)

func TestCheckOperationsNamesTheOperationThatIsWrong(t *testing.T) {
	role := uuid.New()
	ops := []model.BatchOperation{
		{Op: model.BatchCreate, Entity: model.BatchRole, Body: map[string]any{"name": "reader"}},
		{Op: model.BatchCreate, Entity: model.BatchRolePermission, Parent: &role, Body: map[string]any{}},
		{Op: model.BatchUpdate, Entity: model.BatchGroup, Body: map[string]any{"name": "x"}},
	}
	err := CheckOperations(ops)
	e, ok := apperr.As(err)
	if !ok || e.Extra.Operation == nil || *e.Extra.Operation != 2 {
		t.Fatalf("the update without an id is not named by its index: %v", err)
	}
	if e.Extra.Code != "batch_id_required" {
		t.Fatalf("code: %q", e.Extra.Code)
	}

	// An item without its role, an unknown operation and an unknown entity are each refused.
	for _, op := range []model.BatchOperation{
		{Op: model.BatchCreate, Entity: model.BatchRolePermission},
		{Op: "upsert", Entity: model.BatchRole},
		{Op: model.BatchCreate, Entity: "service"},
	} {
		if err := CheckOperations([]model.BatchOperation{op}); err == nil {
			t.Errorf("operation %+v accepted", op)
		}
	}
	id := uuid.New()
	if err := CheckOperations([]model.BatchOperation{{Op: model.BatchDelete, Entity: model.BatchGroup, ID: &id}}); err != nil {
		t.Fatal(err)
	}
}

// Claims compare as numbers and as networks too; an absent claim does not hold, negative conditions included.
