package i18n

// Catalog keys. A key is also the machine-readable error code in extra.code — keep them stable. The codes
// are public, so a key keeps its value when its entity is renamed: the people union was `role` before it became
// a group, and its keys keep the role_* values (the Go names say group). The new roles have role_definition_*
// values, because role_not_found and the others already name groups.
//
// Russian texts keep the wording of the legacy Python backend, so clients that matched on them
// keep working.
const (
	// generic
	InternalError    Key = "internal_error"
	RouteNotFound    Key = "route_not_found"
	ValidationFailed Key = "validation_failed"
	// availability: the 503 answers of a process that is not ready, cannot trust its copy of the policy, is overloaded or ran
	// out of time for a request
	NotReady          Key = "not_ready"
	PolicyUnavailable Key = "policy_unavailable"
	Overloaded        Key = "overloaded"
	RequestTimeout    Key = "request_timeout"

	SynchronizerChangeBusy Key = "synchronizer_change_busy"

	// input validation
	FieldRequired                 Key = "field_required"
	FieldURLInvalid               Key = "field_url_invalid"
	FieldInvalid                  Key = "field_invalid"
	FieldDSNInvalid               Key = "field_dsn_invalid"
	FieldDSNScheme                Key = "field_dsn_scheme"
	FieldDSNHost                  Key = "field_dsn_host"
	PredicateRegexNotString       Key = "predicate_regex_not_string"
	PredicateRegexInvalid         Key = "predicate_regex_invalid"
	PredicateOpInvalid            Key = "predicate_op_invalid"
	PredicateNeNotString          Key = "predicate_ne_not_string"
	RuleEffectInvalid             Key = "rule_effect_invalid"
	RuleExceptionNeedsAllow       Key = "rule_exception_needs_allow"
	ValidityInvalid               Key = "validity_invalid"
	LogSettingInvalid             Key = "log_setting_invalid"
	PredicateSourceInvalid        Key = "predicate_source_invalid"
	PredicateNowAttrInvalid       Key = "predicate_now_attr_invalid"
	PredicateNumberInvalid        Key = "predicate_number_invalid"
	PredicateCIDRInvalid          Key = "predicate_cidr_invalid"
	ResourceWhereSource           Key = "resource_where_source"
	OnMissingApplyNeedsDeny       Key = "on_missing_apply_needs_deny"
	AccessGroupNotFound           Key = "access_group_not_found"
	AccessGroupMissing            Key = "access_group_missing"
	BatchOperationInvalid         Key = "batch_operation_invalid"
	BatchIDRequired               Key = "batch_id_required"
	BatchParentRequired           Key = "batch_parent_required"
	BatchBodyInvalid              Key = "batch_body_invalid"
	BatchForbidden                Key = "batch_forbidden"
	ChangeNotFound                Key = "change_not_found"
	ChangeNotRevertible           Key = "change_not_revertible"
	EntityIDTaken                 Key = "entity_id_taken"
	LintRuleDisabled              Key = "lint_rule_disabled"
	LintRuleNotInForce            Key = "lint_rule_not_in_force"
	LintRuleTargetDead            Key = "lint_rule_target_dead"
	LintRuleLookupDead            Key = "lint_rule_lookup_dead"
	LintRuleEveryone              Key = "lint_rule_everyone"
	LintDenyMissingClaim          Key = "lint_deny_missing_claim"
	LintGroupEmpty                Key = "lint_group_empty"
	LintExceptionNothingToLift    Key = "lint_exception_nothing_to_lift"
	LintResourceWhereEmpty        Key = "lint_resource_where_empty"
	LintRuleDuplicate             Key = "lint_rule_duplicate"
	LintRoleUnused                Key = "lint_role_unused"
	LintAccessGroupUnused         Key = "lint_access_group_unused"
	PolicyFormatInvalid           Key = "policy_format_invalid"
	PolicyServiceMismatch         Key = "policy_service_mismatch"
	PolicyNameDuplicate           Key = "policy_name_duplicate"
	PolicyMemberInvalid           Key = "policy_member_invalid"
	PolicyReferenceMissing        Key = "policy_reference_missing"
	PolicyNameAmbiguous           Key = "policy_name_ambiguous"
	AccessGroupNameTaken          Key = "access_group_name_taken"
	AccessGroupInUse              Key = "access_group_in_use"
	AccessGroupEmpty              Key = "access_group_empty"
	AccessGroupNameInvalid        Key = "access_group_name_invalid"
	ContextTooLarge               Key = "context_too_large"
	ContextNameInvalid            Key = "context_name_invalid"
	ContextValueTooLarge          Key = "context_value_too_large"
	ContextValueNotScalar         Key = "context_value_not_scalar"
	ContextInvalid                Key = "context_invalid"
	ResourceWhereAttrInvalid      Key = "resource_where_attr_invalid"
	AccessEmpty                   Key = "access_empty"
	ResourceTargetExclusive       Key = "resource_target_exclusive"
	SubjectTargetExclusive        Key = "subject_target_exclusive"
	ServiceUpdateEmpty            Key = "service_update_empty"
	SynchronizerKindInvalid       Key = "synchronizer_kind_invalid"
	GroupMemberExclusive          Key = "role_member_exclusive"
	RoleAssignmentInlineExclusive Key = "role_assignment_inline_exclusive"
	AssignmentRuleTargetRequired  Key = "assignment_rule_target_required"
	ServiceSlugInvalid            Key = "service_slug_invalid"
	ServiceSlugTaken              Key = "service_slug_taken"
	CacheTTLInvalid               Key = "synchronizer_cache_ttl_invalid"
	SynchronizerRegionsInvalid    Key = "synchronizer_regions_invalid"
	SynchronizerHeaderInvalid     Key = "synchronizer_header_invalid"

	// service
	ServiceNotFound      Key = "service_not_found"
	ServiceDeleteBlocked Key = "service_delete_blocked"

	// type resource
	TypeResourceNotFound      Key = "type_resource_not_found"
	TypeResourceSelfParent    Key = "type_resource_self_parent"
	TypeResourceCycle         Key = "type_resource_cycle"
	TypeResourceDeleteBlocked Key = "type_resource_delete_blocked"
	TypeResourceNameTaken     Key = "type_resource_name_taken"
	TypeParentDuplicate       Key = "type_parent_duplicate"
	AccessLevelInvalid        Key = "access_level_invalid"
	AccessImpliesCycle        Key = "access_implies_cycle"

	// resources and synchronizers
	ResourceNotFound         Key = "resource_not_found"
	SynchronizerNotFound     Key = "synchronizer_not_found"
	SynchronizerExists       Key = "synchronizer_exists"
	SourceTestFailed         Key = "source_test_failed"
	LastAdminAccess          Key = "last_admin_access"
	DelegationSubjectInvalid Key = "delegation_subject_invalid"
	DelegationClaimsInvalid  Key = "delegation_claims_invalid"
	DirectoryEntryNotFound   Key = "directory_entry_not_found"
	DirectoryEntryRepeated   Key = "directory_entry_repeated"
	PolicyScenarioNotFound   Key = "policy_scenario_not_found"
	PolicyScenarioNameTaken  Key = "policy_scenario_name_taken"
	SynchronizerNoSource     Key = "synchronizer_no_source"
	SynchronizerNoAnswer     Key = "synchronizer_no_answer"
	SynchronizerProbeFailed  Key = "synchronizer_probe_failed"

	// groups (people): the legacy role_* codes of the people union
	GroupNotFound       Key = "role_not_found"
	GroupNameTaken      Key = "role_name_taken"
	GroupDeleteBlocked  Key = "role_delete_blocked"
	GroupMemberNotFound Key = "role_member_not_found"
	GroupSelfNest       Key = "role_self_nest"

	// roles (sets of permissions): codes of their own
	RoleNotFound                    Key = "role_definition_not_found"
	RoleNameTaken                   Key = "role_definition_name_taken"
	RoleDeleteBlocked               Key = "role_definition_delete_blocked"
	RolePermissionNotFound          Key = "role_permission_not_found"
	AssignmentRuleNotFound          Key = "assignment_rule_not_found"
	AssignmentRuleNeedsSynchronizer Key = "assignment_rule_needs_synchronizer"

	// platform
	PlatformNotSeeded Key = "platform_not_seeded"
	// AdminAuthNotConfigured: ADMIN_AUTH__* is not set, so no administrator can authenticate
	AdminAuthNotConfigured Key = "admin_auth_not_configured"
	AccessDenied           Key = "access_denied"

	// oauth / tokens
	AuthHeaderRequired        Key = "auth_header_required"
	JWKSUnavailable           Key = "jwks_unavailable"
	JWKSRejected              Key = "jwks_rejected"
	JWKSBadJSON               Key = "jwks_bad_json"
	JWKSKeyInvalid            Key = "jwks_key_invalid"
	TokenInvalid              Key = "token_invalid"
	TokenNoSubject            Key = "token_no_subject"
	AuthzenIncomplete         Key = "authzen_incomplete"
	AuthzenForbidden          Key = "authzen_forbidden"
	AuthzenRateLimited        Key = "authzen_rate_limited"
	AuthzenFieldRequired      Key = "authzen_field_required"
	AuthzenPageInvalid        Key = "authzen_page_invalid"
	AuthzenMetadataDisabled   Key = "authzen_metadata_disabled"
	AuthzenSubjectTypeInvalid Key = "authzen_subject_type_invalid"
	AuthzenLicetumInvalid     Key = "authzen_licetum_invalid"

	// PEP directory: the issuers, the clients of the services and their API keys
	PEPKeyLimit      Key = "pep_key_limit"
	PEPClientInvalid Key = "pep_client_invalid"
	PEPNotFound      Key = "pep_not_found"
	PEPIssuerInUse   Key = "pep_issuer_in_use"
)

var catalog = map[Key]Text{
	InternalError: {
		EN: "Internal server error",
		RU: "Внутренняя ошибка сервера",
		ZH: "服务器内部错误",
	},
	RouteNotFound: {
		EN: "Not Found",
		RU: "Не найдено",
		ZH: "未找到",
	},
	ValidationFailed: {
		EN: "Validation failed",
		RU: "Ошибка валидации",
		ZH: "验证失败",
	},
	NotReady: {
		EN: "The service is not ready to take requests",
		RU: "Сервис не готов принимать запросы",
		ZH: "服务尚未准备好接收请求",
	},
	PolicyUnavailable: {
		EN: "The policy store cannot be reached or confirmed to be current, the request is refused; retry it shortly",
		RU: "Хранилище политик недоступно или его актуальность не подтверждена, запрос отклонён; повторите его чуть позже",
		ZH: "无法访问策略存储或无法确认其为最新，请求已被拒绝；请稍后重试",
	},
	Overloaded: {
		EN: "The service is overloaded, retry the request shortly",
		RU: "Сервис перегружен, повторите запрос чуть позже",
		ZH: "服务过载，请稍后重试",
	},
	RequestTimeout: {
		EN: "The request took too long and was cancelled, retry it",
		RU: "Запрос выполнялся слишком долго и был отменён, повторите его",
		ZH: "请求耗时过长已被取消，请重试",
	},
	SynchronizerChangeBusy: {
		EN: "The part of the resource hierarchy this type belongs to is being synchronized, retry the change shortly",
		RU: "Часть иерархии ресурсов, к которой относится этот тип, сейчас синхронизируется, повторите изменение чуть позже",
		ZH: "此类型所属的资源层级部分正在同步，请稍后重试该更改",
	},

	FieldRequired: {
		EN: "Field %s is required",
		RU: "Поле %s обязательно",
		ZH: "字段 %s 为必填项",
	},
	FieldInvalid: {
		EN: "Field %s is not valid",
		RU: "Поле %s недопустимо",
		ZH: "字段 %s 无效",
	},
	FieldURLInvalid: {
		EN: "Field %s: expected a URL with the http or https scheme",
		RU: "Поле %s: ожидается URL со схемой http или https",
		ZH: "字段 %s：应为 http 或 https 协议的 URL",
	},
	FieldDSNInvalid: {
		EN: "Field %s: invalid connection string",
		RU: "Поле %s: невалидная строка подключения",
		ZH: "字段 %s：连接字符串无效",
	},
	FieldDSNScheme: {
		EN: "Field %s: expected the postgres:// or postgresql:// scheme",
		RU: "Поле %s: ожидается схема postgres:// или postgresql://",
		ZH: "字段 %s：应为 postgres:// 或 postgresql:// 协议",
	},
	FieldDSNHost: {
		EN: "Field %s: the connection string has no host",
		RU: "Поле %s: в строке подключения не указан host",
		ZH: "字段 %s：连接字符串中未指定主机",
	},
	PredicateRegexNotString: {
		EN: "predicate: value must be a string for op=regex",
		RU: "predicate: для op=regex value должен быть строкой",
		ZH: "predicate：op=regex 时 value 必须是字符串",
	},
	PredicateRegexInvalid: {
		EN: "predicate: invalid regular expression %q: %v",
		RU: "predicate: невалидное регулярное выражение %q: %v",
		ZH: "predicate：无效的正则表达式 %q：%v",
	},
	PredicateOpInvalid: {
		EN: "predicate: op must be one of eq, in, regex (got %q)",
		RU: "predicate: op должен быть одним из eq, in, regex (получено %q)",
		ZH: "predicate：op 必须是 eq、in、regex 之一（实际为 %q）",
	},
	PredicateNeNotString: {
		EN: "The ne operator compares with a single string, not a list",
		RU: "Оператор ne сравнивает с одной строкой, а не со списком",
		ZH: "ne 运算符只能与单个字符串比较，不能与列表比较",
	},
	PredicateSourceInvalid: {
		EN: "Unknown source %q: expected claim, context or now",
		RU: "Неизвестный источник %q: ожидается claim, context или now",
		ZH: "未知的来源 %q：应为 claim、context 或 now",
	},
	PredicateNowAttrInvalid: {
		EN: "Attribute %q of source now: expected hour (0-23) or weekday (1-7)",
		RU: "Атрибут %q источника now: ожидается hour (0-23) или weekday (1-7)",
		ZH: "来源 now 的属性 %q：应为 hour（0-23）或 weekday（1-7）",
	},
	PredicateNumberInvalid: {
		EN: "Operator %s compares numbers: the value %q is not a number",
		RU: "Оператор %s сравнивает числа: значение %q не число",
		ZH: "运算符 %s 比较数字：值 %q 不是数字",
	},
	PredicateCIDRInvalid: {
		EN: "The value %q is not a network in CIDR notation",
		RU: "Значение %q — не сеть в нотации CIDR",
		ZH: "值 %q 不是 CIDR 表示法的网络",
	},
	ResourceWhereSource: {
		EN: "The conditions of resource_where read the attributes of the resources: they take no source",
		RU: "Условия resource_where читают атрибуты ресурсов: источник у них не задаётся",
		ZH: "resource_where 的条件读取资源属性：不能指定来源",
	},
	OnMissingApplyNeedsDeny: {
		EN: "A missing attribute applies a denial only in a deny rule: an allow rule grants nothing on it",
		RU: "Отсутствующий атрибут применяет запрет только в запрещающем правиле: разрешающее правило на нём ничего не выдаёт",
		ZH: "缺失的属性只会在拒绝规则中应用拒绝：允许规则在此情况下不授予任何权限",
	},
	AccessGroupNotFound: {
		EN: "Access group %q is not defined for this resource type",
		RU: "Группа действий %q не задана для этого типа ресурсов",
		ZH: "该资源类型未定义访问组 %q",
	},
	AccessGroupMissing: {
		EN: "Access group %s not found",
		RU: "Группа действий %s не найдена",
		ZH: "未找到访问组 %s",
	},
	BatchOperationInvalid: {
		EN: "Unknown operation %q: expected create, update or delete",
		RU: "Неизвестная операция %q: ожидается create, update или delete",
		ZH: "未知操作 %q：应为 create、update 或 delete",
	},
	BatchIDRequired: {
		EN: "An update and a delete name the entity they change: pass its id",
		RU: "Обновление и удаление указывают изменяемую сущность: передайте её id",
		ZH: "更新和删除需要指定被修改的实体：请传入其 id",
	},
	BatchParentRequired: {
		EN: "The operation on %s needs its parent: the group, role or resource type it belongs to",
		RU: "Операция над %s требует родителя: группу, роль или тип ресурсов, которым она принадлежит",
		ZH: "对 %s 的操作需要其父级：它所属的组、角色或资源类型",
	},
	BatchBodyInvalid: {
		EN: "The body of the operation does not fit its entity: %v",
		RU: "Тело операции не соответствует сущности: %v",
		ZH: "操作的内容与实体不符：%v",
	},
	BatchForbidden: {
		EN: "The policy does not allow writing %s here",
		RU: "Политика не разрешает записывать %s здесь",
		ZH: "策略不允许在此处写入 %s",
	},
	EntityIDTaken: {
		EN: "The id %s belongs to another entity: pick another id, or none",
		RU: "Идентификатор %s уже принадлежит другой сущности: выберите другой или не задавайте его",
		ZH: "ID %s 已属于其他实体：请选择其他 ID，或不指定 ID",
	},
	ChangeNotFound: {
		EN: "Change %d not found in the change log",
		RU: "Изменение %d не найдено в журнале изменений",
		ZH: "在变更日志中未找到变更 %d",
	},
	ChangeNotRevertible: {
		EN: "Change %d cannot be reverted: a %s is not an entity a batch writes",
		RU: "Изменение %d нельзя откатить: %s не сущность, которую записывает пакет",
		ZH: "变更 %d 无法撤销：%s 不是批量操作写入的实体",
	},
	LintRuleDisabled: {
		EN: "Rule %s is disabled: it decides nothing",
		RU: "Правило %s отключено: оно ничего не решает",
		ZH: "规则 %s 已停用：它不做任何决定",
	},
	LintRuleNotInForce: {
		EN: "Rule %s is outside its validity period: it decides nothing now",
		RU: "Правило %s вне срока действия: сейчас оно ничего не решает",
		ZH: "规则 %s 不在有效期内：目前它不做任何决定",
	},
	LintRuleTargetDead: {
		EN: "Rule %s names a resource, but its type has no synchronizer: it decides nothing",
		RU: "Правило %s указывает ресурс, но у его типа нет синхронизатора: оно ничего не решает",
		ZH: "规则 %s 指定了资源，但其类型没有同步器：它不做任何决定",
	},
	LintRuleLookupDead: {
		EN: "Rule %s looks its resource up in a type that has no synchronizer, or climbs through one: it finds no resource there (a denial still applies below the links to the resources that type left)",
		RU: "Правило %s ищет ресурс в типе без синхронизатора или проходит через такой тип: оно не находит там ресурса (запрет по-прежнему действует ниже ссылок на ресурсы, которые тип оставил)",
		ZH: "规则 %s 在没有同步器的类型中查找资源，或经过这样的类型：它在那里找不到资源（拒绝规则在指向该类型所遗留资源的链接之下仍然适用）",
	},
	LintRuleEveryone: {
		EN: "Rule %s grants its access to everyone who signs in",
		RU: "Правило %s выдаёт доступ всем, кто входит в систему",
		ZH: "规则 %s 向所有登录用户授予访问权限",
	},
	LintDenyMissingClaim: {
		EN: "Rule %s denies on a claim that a token may lack: a token without it is denied",
		RU: "Правило %s запрещает по клейму, которого может не быть в токене: токен без него получает запрет",
		ZH: "规则 %s 依据可能缺失于令牌中的声明拒绝：没有该声明的令牌会被拒绝",
	},
	LintGroupEmpty: {
		EN: "Rule %s names a group that has no members: nobody gets through it",
		RU: "Правило %s указывает группу без участников: через него никто не пройдёт",
		ZH: "规则 %s 指向一个没有成员的组：没有人能通过它",
	},
	LintExceptionNothingToLift: {
		EN: "Rule %s is an exception, but the policy has no denial to lift",
		RU: "Правило %s — исключение, но в политике нет запрета, который оно могло бы снять",
		ZH: "规则 %s 是例外，但策略中没有可解除的拒绝",
	},
	LintResourceWhereEmpty: {
		EN: "Rule %s covers no resource now: no resource matches its resource_where",
		RU: "Правило %s сейчас не охватывает ни одного ресурса: resource_where никому не подходит",
		ZH: "规则 %s 目前没有覆盖任何资源：没有资源匹配其 resource_where",
	},
	LintRuleDuplicate: {
		EN: "Rule %s repeats another rule that does the same",
		RU: "Правило %s повторяет другое правило с тем же действием",
		ZH: "规则 %s 与另一条做同样事情的规则重复",
	},
	LintRoleUnused: {
		EN: "Role %s is not granted to anyone",
		RU: "Роль %s никому не назначена",
		ZH: "角色 %s 未授予任何人",
	},
	LintAccessGroupUnused: {
		EN: "Access group %s is not used by any rule or role item",
		RU: "Группа действий %s не используется ни одним правилом и ни одним элементом роли",
		ZH: "访问组 %s 未被任何规则或角色项使用",
	},
	PolicyFormatInvalid: {
		EN: "The document format is %q: expected licetum.policy/v1",
		RU: "Формат документа %q: ожидается licetum.policy/v1",
		ZH: "文档格式为 %q：应为 licetum.policy/v1",
	},
	PolicyServiceMismatch: {
		EN: "The document is for the service %q, not for %q",
		RU: "Документ для сервиса %q, а не для %q",
		ZH: "该文档属于服务 %q，而非 %q",
	},
	PolicyNameDuplicate: {
		EN: "The %s name %q is used twice in the document",
		RU: "Имя %s %q используется в документе дважды",
		ZH: "文档中 %s 名称 %q 重复使用",
	},
	PolicyMemberInvalid: {
		EN: "A membership of the group %q is either a nested group or a predicate, not both",
		RU: "Участник группы %q — либо вложенная группа, либо предикат, но не то и другое",
		ZH: "组 %q 的成员要么是嵌套组，要么是谓词，不能二者兼有",
	},
	PolicyReferenceMissing: {
		EN: "The %s %q is neither in the document nor in Licetum",
		RU: "%s %q нет ни в документе, ни в Licetum",
		ZH: "%s %q 既不在文档中，也不在 Licetum 中",
	},
	PolicyNameAmbiguous: {
		EN: "Two rules of the service are named %q: the import cannot tell which one the document means",
		RU: "Два правила сервиса называются %q: импорт не может определить, какое из них имеется в виду",
		ZH: "该服务有两条规则名为 %q：导入无法确定文档指的是哪一条",
	},
	AccessGroupNameTaken: {
		EN: "An access group with the name %q already exists for this resource type",
		RU: "Группа действий с именем %q уже есть у этого типа ресурсов",
		ZH: "该资源类型已存在名为 %q 的访问组",
	},
	AccessGroupInUse: {
		EN: "The access group is used by rules: remove the \"@\" references first",
		RU: "Группа действий используется правилами: сначала уберите ссылки «@»",
		ZH: "访问组正被规则使用：请先移除 \"@\" 引用",
	},
	AccessGroupEmpty: {
		EN: "An access group lists at least one access level",
		RU: "В группе действий должен быть хотя бы один уровень доступа",
		ZH: "访问组至少需要一个访问级别",
	},
	AccessGroupNameInvalid: {
		EN: "Access group name %q is not valid: use letters, digits, _ and - (up to 64 characters)",
		RU: "Недопустимое имя группы действий %q: допустимы буквы, цифры, _ и - (до 64 символов)",
		ZH: "访问组名 %q 无效：可使用字母、数字、_ 和 -（最多 64 个字符）",
	},
	ContextTooLarge: {
		EN: "A request carries at most %d context attributes",
		RU: "Запрос несёт не больше %d атрибутов контекста",
		ZH: "一个请求最多携带 %d 个上下文属性",
	},
	ContextInvalid: {
		EN: "The context must be a JSON object of attributes: its values are scalars or lists of scalars",
		RU: "Контекст должен быть JSON-объектом атрибутов: значения — скаляры или списки скаляров",
		ZH: "上下文必须是属性的 JSON 对象：值为标量或标量列表",
	},
	ContextValueTooLarge: {
		EN: "The context attribute %q is too large: a string takes at most %d characters, a list at most %d items",
		RU: "Атрибут контекста %q слишком велик: строка — не больше %d символов, список — не больше %d элементов",
		ZH: "上下文属性 %q 过大：字符串最多 %d 个字符，列表最多 %d 项",
	},
	ContextValueNotScalar: {
		EN: "The context attribute %q must be a string, a number, a boolean or a list of them: an object is not allowed",
		RU: "Атрибут контекста %q должен быть строкой, числом, логическим значением или списком из них: объекты не допускаются",
		ZH: "上下文属性 %q 必须是字符串、数字、布尔值或它们的列表：不允许对象",
	},
	ContextNameInvalid: {
		EN: "The context attribute name %q is not valid: use letters, digits, _ - . and :",
		RU: "Недопустимое имя атрибута контекста %q: допустимы буквы, цифры и _ - . :",
		ZH: "上下文属性名 %q 无效：可使用字母、数字以及 _ - . :",
	},
	ResourceWhereAttrInvalid: {
		EN: "The attribute %q cannot be used in resource_where: use letters, digits, _ and - (up to 63 characters), not id, name, parent_id, parents or meta",
		RU: "Атрибут %q нельзя использовать в resource_where: допустимы буквы, цифры, _ и - (до 63 символов), но не id, name, parent_id, parents и meta",
		ZH: "属性 %q 不能用于 resource_where：只能使用字母、数字、_ 和 -（最多 63 个字符），且不能是 id、name、parent_id、parents 或 meta",
	},
	LogSettingInvalid: {
		EN: "Invalid logging setting: %s",
		RU: "Недопустимая настройка логов: %s",
		ZH: "无效的日志设置：%s",
	},
	RuleEffectInvalid: {
		EN: "Unknown rule effect %q: expected allow or deny",
		RU: "Неизвестный эффект правила %q: ожидается allow или deny",
		ZH: "未知的规则效果 %q：应为 allow 或 deny",
	},
	RuleExceptionNeedsAllow: {
		EN: "Only an allow rule can be an exception",
		RU: "Исключением может быть только разрешающее правило (allow)",
		ZH: "只有 allow 规则才能作为例外",
	},
	ValidityInvalid: {
		EN: "The validity period is empty: expires_at must be later than valid_from",
		RU: "Пустой период действия: expires_at должен быть позже valid_from",
		ZH: "有效期为空：expires_at 必须晚于 valid_from",
	},
	AccessEmpty: {
		EN: "Field access must contain at least one access level",
		RU: "Поле access должно содержать хотя бы один уровень доступа",
		ZH: "字段 access 必须至少包含一个访问级别",
	},
	ResourceTargetExclusive: {
		EN: "Specify at most one of: resource_id or resource_from_claim",
		RU: "Укажите не более одного из: resource_id или resource_from_claim",
		ZH: "resource_id 和 resource_from_claim 最多只能指定一个",
	},
	SubjectTargetExclusive: {
		EN: "Specify at most one of: subject, member_of_group_id or predicate",
		RU: "Укажите не более одного из: subject, member_of_group_id или predicate",
		ZH: "subject、member_of_group_id 和 predicate 最多只能指定一个",
	},
	RoleAssignmentInlineExclusive: {
		EN: "With role_id do not set type_resource_id, resource_id, resource_from_claim or access: the permissions come from the role",
		RU: "С role_id нельзя задавать type_resource_id, resource_id, resource_from_claim и access: права берутся из роли",
		ZH: "指定 role_id 时不能设置 type_resource_id、resource_id、resource_from_claim 或 access：权限来自角色",
	},
	AssignmentRuleTargetRequired: {
		EN: "Specify role_id, or type_resource_id with access",
		RU: "Укажите role_id либо type_resource_id и access",
		ZH: "请指定 role_id，或 type_resource_id 与 access",
	},
	ServiceSlugInvalid: {
		EN: "Invalid service slug %q: use lowercase Latin letters, digits, '-' and '_', starting with a letter or a digit, up to 64 characters; a UUID is not allowed",
		RU: "Недопустимый slug сервиса %q: латиница в нижнем регистре, цифры, «-» и «_», начало с буквы или цифры, до 64 символов; UUID не допускается",
		ZH: "服务 slug %q 无效：仅允许小写拉丁字母、数字、“-”和“_”，以字母或数字开头，最多 64 个字符，且不能是 UUID",
	},
	CacheTTLInvalid: {
		EN: "config.cache_ttl must be between 0 and %d seconds",
		RU: "config.cache_ttl должен быть от 0 до %d секунд",
		ZH: "config.cache_ttl 必须在 0 到 %d 秒之间",
	},
	SynchronizerRegionsInvalid: {
		EN: "regions: at most 16 names of lowercase letters, digits and hyphens (up to 32 characters each); %s",
		RU: "regions: не более 16 имён из строчных латинских букв, цифр и дефисов (до 32 символов каждое); %s",
		ZH: "regions：最多 16 个名称，由小写字母、数字和连字符组成（每个最长 32 个字符）；%s",
	},
	ServiceUpdateEmpty: {
		EN: "At least one field must be set",
		RU: "Хотя бы одно поле не должно быть пустым",
		ZH: "至少需要填写一个字段",
	},
	SynchronizerKindInvalid: {
		EN: "config.kind must be sql or http",
		RU: "config.kind должен быть sql или http",
		ZH: "config.kind 必须是 sql 或 http",
	},
	SynchronizerHeaderInvalid: {
		EN: "config.headers: invalid header %q",
		RU: "config.headers: недопустимый заголовок %q",
		ZH: "config.headers：无效的请求头 %q",
	},
	GroupMemberExclusive: {
		EN: "Specify either member_group_id or predicate, not both",
		RU: "Укажите member_group_id ИЛИ predicate, не оба одновременно",
		ZH: "member_group_id 和 predicate 只能指定其一",
	},

	ServiceNotFound: {
		EN: "Service %s not found",
		RU: "Сервис %s не найден",
		ZH: "未找到服务 %s",
	},
	ServiceDeleteBlocked: {
		EN: "Service %s cannot be deleted: it has active resource types",
		RU: "Сервис %s нельзя удалить: у него есть активные типы ресурсов",
		ZH: "无法删除服务 %s：该服务仍有有效的资源类型",
	},

	TypeResourceNotFound: {
		EN: "Resource type %s not found",
		RU: "Тип ресурса %s не найден",
		ZH: "未找到资源类型 %s",
	},
	TypeResourceSelfParent: {
		EN: "A resource type cannot be its own parent",
		RU: "Тип ресурса не может быть родителем самому себе",
		ZH: "资源类型不能是其自身的父类型",
	},
	TypeResourceCycle: {
		EN: "Resource type %s is a descendant of %s — this link would create a cycle",
		RU: "Тип ресурса %s является потомком %s — такая связь образует цикл",
		ZH: "资源类型 %s 是 %s 的后代——此关联会形成循环",
	},
	TypeResourceDeleteBlocked: {
		EN: "Resource type %s cannot be deleted: it is still referenced",
		RU: "Тип ресурса %s нельзя удалить: на него есть ссылки",
		ZH: "无法删除资源类型 %s：仍有对象引用它",
	},
	TypeResourceNameTaken: {
		EN: "A resource type named «%s» already exists in this service",
		RU: "Тип ресурса с именем «%s» в этом сервисе уже существует",
		ZH: "该服务中已存在名为「%s」的资源类型",
	},
	TypeParentDuplicate: {
		EN: "Parent resource type %s is listed more than once",
		RU: "Родительский тип ресурса %s указан несколько раз",
		ZH: "父资源类型 %s 被重复列出",
	},
	AccessLevelInvalid: {
		EN: "Invalid access level in %s: levels must be non-empty and cannot be the wildcard",
		RU: "Недопустимый уровень доступа в %s: уровни не должны быть пустыми или подстановкой",
		ZH: "%s 中的访问级别无效：级别不能为空，也不能是通配符",
	},
	AccessImpliesCycle: {
		EN: "The access level hierarchy has a cycle: %s includes itself",
		RU: "В иерархии уровней доступа есть цикл: %s включает сам себя",
		ZH: "访问级别层级中存在循环：%s 包含它自身",
	},

	ResourceNotFound: {
		EN: "Resource %s not found",
		RU: "Ресурс %s не найден",
		ZH: "未找到资源 %s",
	},
	PolicyScenarioNotFound: {
		EN: "Scenario %s not found",
		RU: "Сценарий %s не найден",
		ZH: "未找到场景 %s",
	},
	PolicyScenarioNameTaken: {
		EN: "The service already has a scenario named %q",
		RU: "У сервиса уже есть сценарий с названием %q",
		ZH: "该服务已有名为 %q 的场景",
	},
	DirectoryEntryNotFound: {
		EN: "%s %s is not in the directory of the service",
		RU: "%s %s нет в справочнике сервиса",
		ZH: "服务的目录中没有 %s %s",
	},
	DirectoryEntryRepeated: {
		EN: "The same subject is sent twice in one request: %s %s",
		RU: "Один и тот же субъект передан в запросе дважды: %s %s",
		ZH: "同一请求中重复发送了同一主体：%s %s",
	},
	DelegationSubjectInvalid: {
		EN: "The subject must be a non-empty string without control characters, up to 256 characters, and must not look like a group (group:…)",
		RU: "Субъект — непустая строка без управляющих символов, до 256 символов, и не должен выглядеть как группа (group:…)",
		ZH: "主体必须是不含控制字符的非空字符串，最多 256 个字符，且不能看起来像组（group:…）",
	},
	DelegationClaimsInvalid: {
		EN: "The claims are too large or malformed: at most 100 claims and 16 KiB of JSON",
		RU: "Claims слишком велики или некорректны: не больше 100 утверждений и 16 КиБ JSON",
		ZH: "claims 过大或格式不正确：最多 100 个声明和 16 KiB 的 JSON",
	},
	LastAdminAccess: {
		EN: "This would leave nobody able to manage Licetum. Give administrator access to someone else first (or restore it with the restore-admin command)",
		RU: "После этого никто не сможет управлять Licetum. Сначала выдайте доступ администратора кому-то ещё (или восстановите его командой restore-admin)",
		ZH: "这样一来，没有人能管理 Licetum。请先把管理员权限授予其他人（或用 restore-admin 命令恢复）",
	},
	SourceTestFailed: {
		EN: "The source did not answer: %s",
		RU: "Источник не ответил: %s",
		ZH: "数据源没有响应：%s",
	},
	SynchronizerExists: {
		EN: "The resource type %s already has a source: change it instead of adding another",
		RU: "У типа ресурсов %s уже есть источник: измените его, а не добавляйте второй",
		ZH: "资源类型 %s 已有数据源：请修改它，而不是再添加一个",
	},
	SynchronizerNoSource: {
		EN: "The resource type %s has no source to refresh from",
		RU: "У типа ресурсов %s нет источника, из которого можно обновить данные",
		ZH: "资源类型 %s 没有可用于刷新的数据源",
	},
	SynchronizerNoAnswer: {
		EN: "The source of the resource type %s did not answer; the data stays as it was",
		RU: "Источник типа ресурсов %s не ответил, данные остались прежними",
		ZH: "资源类型 %s 的数据源没有响应，数据保持原样",
	},
	SynchronizerNotFound: {
		EN: "Synchronizer %s not found",
		RU: "Синхронизатор %s не найден",
		ZH: "未找到同步器 %s",
	},
	SynchronizerProbeFailed: {
		EN: "The source is unavailable or did not return resource %s",
		RU: "Источник недоступен или не вернул ресурс %s",
		ZH: "数据源不可用或未返回资源 %s",
	},

	GroupNotFound: {
		EN: "Group %s not found",
		RU: "Группа %s не найдена",
		ZH: "未找到用户组 %s",
	},
	GroupNameTaken: {
		EN: "A group named %q already exists",
		RU: "Группа с именем %q уже существует",
		ZH: "名为 %q 的用户组已存在",
	},
	GroupDeleteBlocked: {
		EN: "Group %s cannot be deleted: it is still referenced",
		RU: "Группу %s нельзя удалить: на неё есть ссылки",
		ZH: "无法删除用户组 %s：仍有对象引用它",
	},
	GroupMemberNotFound: {
		EN: "Membership rule %s not found",
		RU: "Правило назначения %s не найдено",
		ZH: "未找到成员规则 %s",
	},
	GroupSelfNest: {
		EN: "A group cannot be nested into itself",
		RU: "Группа не может быть вложена сама в себя",
		ZH: "用户组不能嵌套到自身",
	},
	RoleNotFound: {
		EN: "Role %s not found",
		RU: "Роль %s не найдена",
		ZH: "未找到角色 %s",
	},
	RoleNameTaken: {
		EN: "A role named %q already exists",
		RU: "Роль с именем %q уже существует",
		ZH: "名为 %q 的角色已存在",
	},
	RoleDeleteBlocked: {
		EN: "Role %s cannot be deleted: it is still granted by rules",
		RU: "Роль %s нельзя удалить: её выдают правила назначения",
		ZH: "无法删除角色 %s：仍有规则授予它",
	},
	RolePermissionNotFound: {
		EN: "Role permission %s not found",
		RU: "Пункт роли %s не найден",
		ZH: "未找到角色权限项 %s",
	},
	ServiceSlugTaken: {
		EN: "A service with slug %q already exists",
		RU: "Сервис со slug %q уже существует",
		ZH: "slug 为 %q 的服务已存在",
	},
	AssignmentRuleNotFound: {
		EN: "Assignment rule %s not found",
		RU: "Правило автоназначения %s не найдено",
		ZH: "未找到授权规则 %s",
	},
	AssignmentRuleNeedsSynchronizer: {
		EN: "Resource type %s has no synchronizer and hence no resources: a rule or a role permission can only point at the type as a whole, without resource_id and resource_from_claim",
		RU: "У типа ресурса %s нет синхронизатора, а значит и ресурсов: правило или пункт роли выдаются только на тип целиком, без resource_id и resource_from_claim",
		ZH: "资源类型 %s 没有同步器，因此没有资源：规则或角色权限项只能授予整个类型，不能指定 resource_id 或 resource_from_claim",
	},

	AdminAuthNotConfigured: {
		EN: "Administrator sign-in is not configured: set ADMIN_AUTH__ISSUER, ADMIN_AUTH__AUDIENCE and ADMIN_AUTH__JWKS_URL",
		RU: "Вход администраторов не настроен: задайте ADMIN_AUTH__ISSUER, ADMIN_AUTH__AUDIENCE и ADMIN_AUTH__JWKS_URL",
		ZH: "未配置管理员登录：请设置 ADMIN_AUTH__ISSUER、ADMIN_AUTH__AUDIENCE 和 ADMIN_AUTH__JWKS_URL",
	},
	PlatformNotSeeded: {
		EN: "The self-service is not seeded yet — set the PLATFORM__* env vars and run the migration (licetum migrate)",
		RU: "Self-service ещё не засеян — задайте PLATFORM__* env и выполните миграцию (licetum migrate)",
		ZH: "自助服务尚未初始化——请设置 PLATFORM__* 环境变量并执行迁移（licetum migrate）",
	},

	AccessDenied: {
		EN: "Insufficient permissions: %s on “%s”",
		RU: "Недостаточно прав: %s на «%s»",
		ZH: "权限不足：需要 %s 权限（“%s”）",
	},
	AuthHeaderRequired: {
		EN: "Expected header Authorization: Bearer <token>",
		RU: "Ожидается заголовок Authorization: Bearer <token>",
		ZH: "需要请求头 Authorization: Bearer <token>",
	},
	JWKSUnavailable: {
		EN: "The service JWKS endpoint is unavailable: %v",
		RU: "JWKS endpoint сервиса недоступен: %v",
		ZH: "服务的 JWKS 端点不可用：%v",
	},
	JWKSRejected: {
		EN: "The JWKS endpoint rejected the request: %d",
		RU: "JWKS endpoint отклонил запрос: %d",
		ZH: "JWKS 端点拒绝了请求：%d",
	},
	JWKSBadJSON: {
		EN: "The JWKS endpoint returned invalid JSON: %v",
		RU: "JWKS endpoint вернул невалидный JSON: %v",
		ZH: "JWKS 端点返回了无效的 JSON：%v",
	},
	JWKSKeyInvalid: {
		EN: "Invalid key in the service JWKS: %v",
		RU: "Невалидный ключ в JWKS сервиса: %v",
		ZH: "服务 JWKS 中的密钥无效：%v",
	},
	TokenInvalid: {
		EN: "The credential was not accepted",
		RU: "Учётные данные не приняты",
		ZH: "凭证未被接受",
	},
	TokenNoSubject: {
		EN: "The token has neither preferred_username nor sub",
		RU: "В токене нет ни preferred_username, ни sub",
		ZH: "令牌中既没有 preferred_username 也没有 sub",
	},
	AuthzenIncomplete: {
		EN: "An evaluation needs a subject, a resource and an action (in the evaluation or as defaults)",
		RU: "Для проверки нужны субъект, ресурс и действие (в самой проверке или по умолчанию)",
		ZH: "评估需要主体、资源和操作（在评估中或作为默认值给出）",
	},
	AuthzenForbidden: {
		EN: "The client may not do this",
		RU: "Клиенту это не разрешено",
		ZH: "该客户端无权执行此操作",
	},
	AuthzenRateLimited: {
		EN: "The client sends requests faster than the limit of this server allows; try again after the time in Retry-After",
		RU: "Клиент отправляет запросы чаще, чем позволяет лимит этого сервера; повторите после времени из Retry-After",
		ZH: "客户端的请求速度超过了此服务器的限制；请在 Retry-After 指示的时间后重试",
	},
	AuthzenFieldRequired: {
		EN: "%s is required",
		RU: "Поле %s обязательно",
		ZH: "%s 为必填项",
	},
	AuthzenPageInvalid: {
		EN: "The page token is not valid, or the request differs from the one that gave it",
		RU: "Токен страницы недействителен, или запрос отличается от того, что его выдал",
		ZH: "分页令牌无效，或请求与签发该令牌的请求不一致",
	},
	AuthzenMetadataDisabled: {
		EN: "The AuthZEN discovery document is not available: PDP__PUBLIC_URL is not set",
		RU: "Документ обнаружения AuthZEN недоступен: не задан PDP__PUBLIC_URL",
		ZH: "AuthZEN 发现文档不可用：未设置 PDP__PUBLIC_URL",
	},
	AuthzenSubjectTypeInvalid: {
		EN: "subject.type must be 1 to 64 bytes, and not group",
		RU: "subject.type должен занимать от 1 до 64 байт и не быть group",
		ZH: "subject.type 必须为 1 到 64 个字节，且不能是 group",
	},
	AuthzenLicetumInvalid: {
		EN: "The licetum settings of the context must be an object with min_revision (a revision, as a string of digits) and explain (a boolean), and no other field",
		RU: "Настройки licetum в контексте должны быть объектом с полями min_revision (версия, строкой из цифр) и explain (логическое значение) и без других полей",
		ZH: "上下文中的 licetum 设置必须是对象，只能包含 min_revision（由数字组成的字符串形式的版本号）和 explain（布尔值）",
	},
	PEPKeyLimit: {
		EN: "The PEP client has reached its limit of %d active API keys: revoke one first",
		RU: "Достигнут лимит действующих ключей API клиента PEP (%d): сначала отзовите один ключ",
		ZH: "PEP 客户端的有效 API 密钥已达上限（%d 个）：请先吊销一个",
	},
	PEPClientInvalid: {
		EN: "The PEP client is not valid: its kind, issuer, client id or operations do not fit, or its service has a client with the same name or identity",
		RU: "Клиент PEP недействителен: тип, издатель, идентификатор клиента или операции не подходят, либо у сервиса уже есть клиент с тем же именем или идентификатором",
		ZH: "PEP 客户端无效：其类型、颁发者、客户端 ID 或操作不匹配，或其服务已有同名或同一标识的客户端",
	},
	PEPNotFound: {
		EN: "PEP object %s not found",
		RU: "Объект PEP %s не найден",
		ZH: "未找到 PEP 对象 %s",
	},
	PEPIssuerInUse: {
		EN: "The PEP issuer %s is used by a client",
		RU: "Издатель PEP %s используется клиентом",
		ZH: "PEP 颁发者 %s 正被某个客户端使用",
	},
}
