package model

// LiveSynchronizerSQL is the source-existence check for configuring resource targets.
// Callers pass a fixed SQL expression, never user input.
func LiveSynchronizerSQL(typeIDExpr string) string {
	return `EXISTS (SELECT 1 FROM synchronizer s WHERE s.type_resource_id = ` + typeIDExpr + ` AND s.deleted_at IS NULL)`
}

// TypeModeSQL is the single predicate defining a type with applied resource data.
// A configured source alone does not make the type sync. alias is a fixed SQL alias.
func TypeModeSQL(alias string) string {
	return `(` + alias + `.synced_at IS NOT NULL AND ` + LiveSynchronizerSQL(alias+`.id`) + `)`
}
