package model

import "encoding/json"

func (c *PolicyTestCase) UnmarshalJSON(data []byte) error {
	if err := rejectRetiredTargetField(data, "claims", "properties"); err != nil {
		return err
	}
	type wire PolicyTestCase
	if err := json.Unmarshal(data, (*wire)(c)); err != nil {
		return err
	}
	if c.SubjectType != "" {
		return checkSubjectType(c.SubjectType)
	}
	return nil
}
