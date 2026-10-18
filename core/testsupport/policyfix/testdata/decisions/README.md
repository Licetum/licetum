# Decision tables

The fixtures of the decision contracts (DEC01–DEC13 of `contracts.yaml`; the format is `core/testsupport/policyfix`). Each one
is checked twice: by the engine through the check API (`admin-api/tests/integration/contracts`, with the permission list of
every case cross-checked) and by the reference evaluator (`core/testsupport/refeval`).

A fixture is one state. A state that only a change made after the first synchronization can reach — a resource its source
no longer reports, a synchronizer or a parent type deleted, the levels of an access group changed under its rules — is
written as the fixture's state with a `before` block: the engine runner loads `before` first, synchronizes and writes the
policy, then makes the changes; the reference evaluator decides the fixture's state only.

## The API tests the tables come from

Issue #52 (P1): every decision scenario of the six files of `admin-api/tests/integration/api` below has a row in these
tables, so that a regression the API test would catch is caught by a table too. What a test asserts besides decisions
(API shape, validation, caching, synchronization mechanics, admin write rules, metrics) is not in the tables.

### graph_test.go

| Test | Tables |
|---|---|
| TestIntegrationDenyAndExceptions | dec02_deny_and_exceptions, worked_example |
| TestIntegrationDenyWholeTypeAndWildcard | dec02_whole_type_and_wildcard, worked_example |
| TestIntegrationNeAndNotInPredicates | dec08_operators |
| TestIntegrationMultipleParentsAndInherit | dec05_several_parents (the shape of the response and the refused cycle are not decisions) |
| TestIntegrationAccessImplies | dec04_access_implies |
| TestIntegrationValidity | dec07_validity (the refused window is not a decision) |
| TestIntegrationOrphanFailsClosed | dec11_unresolved_parent (the recovery is synchronization) |
| TestIntegrationOrphanAboveFailsClosed | dec11_unresolved_above (the recovery is synchronization) |
| TestIntegrationCheckExplainAuthzenMetrics | dec13_explanations (one denial on B1, one exception on B2: the runner's check of repeated entries); the AuthZEN envelope, the subject mismatch and the metrics are not decisions |
| TestIntegrationResourceAttributes | dec10_resource_from_claim (the attributes in the admin list and the refused attribute name are not decisions) |

### hierarchy_test.go

| Test | Tables |
|---|---|
| TestIntegrationHierarchyCascade | dec05_cross_service_cascade, dec01_what_a_rule_covers (the sync state is not a decision) |
| TestIntegrationHierarchyResourceFromClaim | dec10_resource_from_claim |
| TestIntegrationHierarchyWholeTypeAndMerge | dec05_cross_service_cascade |
| TestIntegrationHierarchySourceChanges | dec05_source_changes, dec05_cross_service_cascade (the cache rows and the admin list are not decisions) |
| TestIntegrationHierarchyTypeTreeChanges | dec05_retyped_parent_link |
| TestIntegrationHierarchyDeletedMiddleType | dec05_deleted_parent_type (the closure rows are not a decision) |
| TestIntegrationTypeModes | dec06_not_sync, dec06_synchronizer_deleted (and dec06_below_a_deleted_synchronizer: a type below it, whose resources name the rows it left), dec06_type_without_synchronizer, dec01_what_a_rule_covers, dec05_cross_service_cascade (the refused point rule, target_valid and the PATCH of the dead rule are not decisions) |
| TestIntegrationTypeResourceNameUnique | not a decision test |
| TestIntegrationSynchronizerSecrets | not a decision test |
| TestIntegrationOAuthRefresh | not a decision test |

### conditions_test.go

| Test | Tables |
|---|---|
| TestIntegrationRequestAttributesDecideAndAreCachedByValue | dec08_request_context, dec13_explanations (the cache keyed by the attribute, the AuthZEN and simulation endpoints and the refused context and condition are not decisions) |
| TestIntegrationDenyWithAMissingAttributeAppliesUnlessTheRuleSkipsIt | dec09_missing_attribute, dec13_explanations (missing_attribute of the denial included; the refused apply on an allow rule is not a decision) |
| TestIntegrationClockConditionsReadTheHourOfTheDecision | dec08_clock (the refused clock attribute is not a decision) |
| TestIntegrationGuardsNarrowARuleAndBlockTheGroupTheyUse | dec10_guards (the refused delete of the guard group is not a decision) |
| TestIntegrationResourceWhereCoversTheResourcesWhoseAttributesMatch | dec10_resource_where (the refused targets are not decisions) |
| TestIntegrationAccessGroupsNameLevelsAndProtectTheirReferences | dec12_access_groups, dec12_access_group_levels (levels changed under the rules through `before`; the protected references and the refused groups are not decisions) |
| TestIntegrationSubjectKindIsStoredAndChecked | not a decision test |

### roles_test.go

| Test | Tables |
|---|---|
| TestRolePermissionsReachSubjectsThroughAssignments | dec12_roles (the shape of the response is not a decision) |
| TestRoleValidationAndBlocking | not a decision test (dec12_roles holds its policy) |
| TestServiceSlugAddressesTheServiceInTheCheckAPI | not a decision test |
| TestSynchronizerCacheTTLKeepsTheSourceListInRedis | dec02_whole_type_and_wildcard, dec01_what_a_rule_covers (the whole-type read it asks incidentally; the list kept in Redis is synchronization) |
| TestRoleItemOnAPlatformPolicyNeedsWriteOnThePolicy | not a decision test |
| TestLookupTypeOfAClaimTargetCannotBeDeleted | not a decision test |

### gaps_test.go

| Test | Tables |
|---|---|
| TestIntegrationBatchRefusesAnIdAnotherEntityHas | not a decision test |
| TestIntegrationPolicyImportRefusesARuleIdOfAnotherService | dec01_what_a_rule_covers (the rule that still decides after the refused import) |
| TestIntegrationRoleItemsKeepTheirConditionsAndAccessGroupsThroughTheDocument | dec12_role_items_with_conditions (the document and its import are not decisions) |
| TestIntegrationNestedGroupsAndSubjectKindsRoundTrip | not a decision test (the decision it relies on is in dec12_nested_groups_sixteen_levels) |
| TestIntegrationResourceWhereMatchesListAttributes | dec10_resource_where_lists |

### escalation_test.go

| Test | Tables |
|---|---|
| TestIntegrationPointWriterCannotPutAPlatformPolicyIntoARoleThroughABatch | not a decision test |
| TestIntegrationPointWriterChangesWhatARuleAppliesToButNotItsTarget | not a decision test |
| TestIntegrationDenyOfAGroupWhoseMembershipReadsAMissingAttributeApplies | dec09_deny_on_unknown_membership |
| TestIntegrationAnAllowRuleTurnedIntoADenyAppliesOnAMissingAttribute | dec09_denial_from_a_network, dec09_missing_attribute (the value the PATCH stores is an admin write) |
| TestIntegrationPointWriterCannotGiveItselfARoleWithAPlatformItem | not a decision test |
