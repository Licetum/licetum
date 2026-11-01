package harness

// CannedWrite is a successful request template shared by WR03, WR05 and WR06.
// Braced values are fixture parameters; the integration fixture resolves them.
type CannedWrite struct {
	OperationID string
	Method      string
	Path        string
	Body        string
}

// CannedWrites is exhaustive: route-table tests reject missing and orphan entries.
var CannedWrites = []CannedWrite{
	{OperationID: "create_access_group", Method: "POST", Path: "/service/{service_id}/type_resource/{type_resource_id}/access_group", Body: `{"name":"created","levels":["write"]}`},
	{OperationID: "create_assignment_rule", Method: "POST", Path: "/assignment_rule", Body: `{"subject":"created","type_resource_id":"{branch}","access":["read"]}`},
	{OperationID: "create_group", Method: "POST", Path: "/group", Body: `{"service_id":"{service_id}","name":"created"}`},
	{OperationID: "create_group_member", Method: "POST", Path: "/group/{group_id}/member", Body: `{"predicate":[]}`},
	{OperationID: "create_pep_client", Method: "POST", Path: "/service/{service_id}/pep-clients", Body: `{"name":"created","kind":"api_key"}`},
	{OperationID: "create_policy_scenario", Method: "POST", Path: "/service/{service_id}/policy/scenarios", Body: `{"name":"created","subject":"alice","checks":[{"type":"branch","action":"read","expect":"deny"}]}`},
	{OperationID: "create_role", Method: "POST", Path: "/role", Body: `{"service_id":"{service_id}","name":"created"}`},
	{OperationID: "create_role_permission", Method: "POST", Path: "/role/{role_id}/permission", Body: `{"type_resource_id":"{branch}","access":["write"]}`},
	{OperationID: "create_service", Method: "POST", Path: "/service", Body: `{"name":"created","slug":"created","url":"{mock}/app","icon":"{mock}/icon.png"}`},
	{OperationID: "create_synchronizer", Method: "POST", Path: "/service/{service_id}/type_resource/{type_resource_id}/resource/synchronizer", Body: `{"config":{"kind":"sql","dsn":"{dsn}","query":"SELECT id, name, parent_id FROM src_branch WHERE id = :id","list_query":"SELECT id, name, parent_id FROM src_branch"},"probe_resource_id":"B1","regions":["renamed"]}`},
	{OperationID: "create_type_resource", Method: "POST", Path: "/service/{service_id}/type_resource", Body: `{"name":"created","parents":[{"type_resource_id":"{branch}"}]}`},
	{OperationID: "delete_access_group", Method: "DELETE", Path: "/service/{service_id}/type_resource/{type_resource_id}/access_group/{access_group_id}", Body: ``},
	{OperationID: "delete_assignment_rule", Method: "DELETE", Path: "/assignment_rule/{rule_id}", Body: ``},
	{OperationID: "delete_directory_entry", Method: "DELETE", Path: "/service/{service_id}/subjects/{subject_type}/{subject_id}", Body: ``},
	{OperationID: "delete_group", Method: "DELETE", Path: "/group/{group_id}", Body: ``},
	{OperationID: "delete_group_member", Method: "DELETE", Path: "/group/{group_id}/member/{member_id}", Body: ``},
	{OperationID: "delete_pep_client", Method: "DELETE", Path: "/service/{service_id}/pep-clients/{client_id}", Body: ``},
	{OperationID: "delete_pep_issuer", Method: "DELETE", Path: "/platform/pep-issuers", Body: ``},
	{OperationID: "delete_policy_scenario", Method: "DELETE", Path: "/service/{service_id}/policy/scenarios/{scenario_id}", Body: ``},
	{OperationID: "delete_role", Method: "DELETE", Path: "/role/{role_id}", Body: ``},
	{OperationID: "delete_role_permission", Method: "DELETE", Path: "/role/{role_id}/permission/{permission_id}", Body: ``},
	{OperationID: "delete_service", Method: "DELETE", Path: "/service/{service_id}", Body: ``},
	{OperationID: "delete_synchronizer", Method: "DELETE", Path: "/service/{service_id}/type_resource/{type_resource_id}/resource/synchronizer/{synchronizer_id}", Body: ``},
	{OperationID: "delete_type_resource", Method: "DELETE", Path: "/service/{service_id}/type_resource/{type_resource_id}", Body: ``},
	{OperationID: "import_policy", Method: "POST", Path: "/service/{service_id}/policy/import", Body: `{"format":"licetum.policy/v2","service":"org","groups":[{"name":"imported","members":[]}]}`},
	{OperationID: "issue_pep_key", Method: "POST", Path: "/service/{service_id}/pep-clients/{client_id}/keys", Body: `{}`},
	{OperationID: "preview_assignment_rule", Method: "POST", Path: "/assignment_rule/preview", Body: `{"subject":"created","type_resource_id":"{branch}","access":["read"]}`},
	{OperationID: "put_directory", Method: "PUT", Path: "/service/{service_id}/subjects", Body: `{"subjects":[{"id":"created","name":"Created"}]}`},
	{OperationID: "put_directory_settings", Method: "PUT", Path: "/service/{service_id}/subjects/settings", Body: `{"retention_days":40}`},
	{OperationID: "put_log_settings", Method: "PUT", Path: "/settings/logs", Body: `{"format":"json","level":"debug","fields":[],"events":{"access":false,"decision":false,"sync":false},"subject":"omit","sample_allowed":0}`},
	{OperationID: "put_pep_issuer", Method: "PUT", Path: "/platform/pep-issuers", Body: `{"issuer":"{mock}/created-issuer","jwks_url":"{mock}/jwks","audiences":["pdp"]}`},
	{OperationID: "refresh_resources_of_type", Method: "POST", Path: "/type_resource/{type_resource_id}/resource/refresh", Body: ``},
	{OperationID: "revert_change", Method: "POST", Path: "/changes/{revision}/revert", Body: `{}`},
	{OperationID: "revoke_pep_key", Method: "DELETE", Path: "/service/{service_id}/pep-clients/{client_id}/keys/{key_id}", Body: ``},
	{OperationID: "rollback_changes", Method: "POST", Path: "/changes/rollback", Body: `{"to":"{base_revision}"}`},
	{OperationID: "run_batch", Method: "POST", Path: "/batch", Body: `{"operations":[{"op":"create","entity":"group","body":{"service_id":"{service_id}","name":"created"}}]}`},
	{OperationID: "run_policy_tests", Method: "POST", Path: "/service/{service_id}/policy/test", Body: `{"cases":[{"name":"case","subject":"alice","checks":[{"type":"branch","action":"read","expect":"deny"}]}]}`},
	{OperationID: "test_synchronizer", Method: "POST", Path: "/service/{service_id}/type_resource/{type_resource_id}/resource/synchronizer/test", Body: `{"config":{"kind":"sql","dsn":"{dsn}","query":"SELECT id, name, parent_id FROM src_branch WHERE id = :id","list_query":"SELECT id, name, parent_id FROM src_branch"},"probe_resource_id":"B1","regions":["renamed"]}`},
	{OperationID: "update_access_group", Method: "PATCH", Path: "/service/{service_id}/type_resource/{type_resource_id}/access_group/{access_group_id}", Body: `{"name":"renamed","levels":["write"]}`},
	{OperationID: "update_assignment_rule", Method: "PATCH", Path: "/assignment_rule/{rule_id}", Body: `{"subject":"renamed","type_resource_id":"{branch}","access":["write"]}`},
	{OperationID: "update_group", Method: "PATCH", Path: "/group/{group_id}", Body: `{"name":"renamed"}`},
	{OperationID: "update_group_member", Method: "PATCH", Path: "/group/{group_id}/member/{member_id}", Body: `{"predicate":[],"enabled":false}`},
	{OperationID: "update_pep_client", Method: "PATCH", Path: "/service/{service_id}/pep-clients/{client_id}", Body: `{"name":"renamed","kind":"api_key","enabled":false}`},
	{OperationID: "update_policy_scenario", Method: "PUT", Path: "/service/{service_id}/policy/scenarios/{scenario_id}", Body: `{"name":"renamed","subject":"alice","checks":[{"type":"branch","action":"write","expect":"deny"}]}`},
	{OperationID: "update_role", Method: "PATCH", Path: "/role/{role_id}", Body: `{"name":"renamed"}`},
	{OperationID: "update_role_permission", Method: "PATCH", Path: "/role/{role_id}/permission/{permission_id}", Body: `{"type_resource_id":"{branch}","access":["write"]}`},
	{OperationID: "update_service", Method: "PATCH", Path: "/service/{service_id}", Body: `{"name":"renamed","url":"{mock}/app","icon":"{mock}/icon.png"}`},
	{OperationID: "update_synchronizer", Method: "PATCH", Path: "/service/{service_id}/type_resource/{type_resource_id}/resource/synchronizer/{synchronizer_id}", Body: `{"config":{"kind":"sql","dsn":"{dsn}","query":"SELECT id, name, parent_id FROM src_branch WHERE id = :id","list_query":"SELECT id, name, parent_id FROM src_branch"},"probe_resource_id":"B1","regions":["renamed"]}`},
	{OperationID: "update_type_resource", Method: "PATCH", Path: "/service/{service_id}/type_resource/{type_resource_id}", Body: `{"name":"renamed"}`},
	{OperationID: "simulate_evaluation", Method: "POST", Path: "/service/{service_id}/simulate/access/v1/evaluation", Body: `{"subject":{"type":"user","id":"alice"},"resource":{"type":"branch","id":"B1"},"action":{"name":"read"}}`},
	{OperationID: "simulate_evaluations", Method: "POST", Path: "/service/{service_id}/simulate/access/v1/evaluations", Body: `{"evaluations":[{"subject":{"type":"user","id":"alice"},"resource":{"type":"branch","id":"B1"},"action":{"name":"read"}}]}`},
	{OperationID: "simulate_search_resource", Method: "POST", Path: "/service/{service_id}/simulate/access/v1/search/resource", Body: `{"subject":{"type":"user","id":"alice"},"resource":{"type":"branch","id":"B1"},"action":{"name":"read"}}`},
	{OperationID: "simulate_search_action", Method: "POST", Path: "/service/{service_id}/simulate/access/v1/search/action", Body: `{"subject":{"type":"user","id":"alice"},"resource":{"type":"branch","id":"B1"},"action":{"name":"read"}}`},
	{OperationID: "simulate_search_subject", Method: "POST", Path: "/service/{service_id}/simulate/access/v1/search/subject", Body: `{"subject":{"type":"user","id":"alice"},"resource":{"type":"branch","id":"B1"},"action":{"name":"read"}}`},
}
