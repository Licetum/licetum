package model

import (
	"encoding/json"

	"github.com/licetum/licetum/core/apperr"
	"github.com/licetum/licetum/core/i18n"
)

// Retired target fields must fail instead of being ignored as an unknown field:
// otherwise an old resource-specific grant would become a whole-type grant.
func rejectRetiredTargetField(data []byte, old, replacement string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields[old]; present {
		return apperr.Unprocessable(i18n.ResourceTargetRetired, old, replacement)
	}
	return nil
}

func decodeResourceTarget(data []byte, target any) error {
	if err := rejectRetiredTargetField(data, "resource_from_claim", "resource_from_property"); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (r *AssignmentRuleInput) UnmarshalJSON(data []byte) error {
	type wire AssignmentRuleInput
	return decodeResourceTarget(data, (*wire)(r))
}

func (r *RolePermissionInput) UnmarshalJSON(data []byte) error {
	type wire RolePermissionInput
	return decodeResourceTarget(data, (*wire)(r))
}

func (r *PolicyRule) UnmarshalJSON(data []byte) error {
	type wire PolicyRule
	return decodeResourceTarget(data, (*wire)(r))
}

func (r *PolicyRoleItem) UnmarshalJSON(data []byte) error {
	type wire PolicyRoleItem
	return decodeResourceTarget(data, (*wire)(r))
}

func (r *PolicyResourceFromProperty) UnmarshalJSON(data []byte) error {
	if err := rejectRetiredTargetField(data, "claim", "property"); err != nil {
		return err
	}
	type wire PolicyResourceFromProperty
	return json.Unmarshal(data, (*wire)(r))
}
