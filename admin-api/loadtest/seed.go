package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/db"
	"github.com/licetum/licetum/core/engine"
	"github.com/licetum/licetum/core/model"
)

const (
	serviceSlug = "loadtest"
	// pepClientName is the name of the PEP client the traffic asks as.
	pepClientName = "loadtest"
	// probe ids are the resources the synchronizers check their source with before they are saved.
	folderProbe   = "f-0"
	documentProbe = "d-0"
)

// fixture is what the seeding made, and what the traffic draws its requests from.
type fixture struct {
	serviceID    uuid.UUID
	folderType   uuid.UUID
	documentType uuid.UUID
	folders      int
	documents    int
	groups       []uuid.UUID
	rules        int
	phases       []phase
	// apiKey is the key of the PEP client of the service: the credential of every request of the run.
	apiKey string
}

// phase is the time one stage of a run took.
type phase struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

// seed builds the policy of the profile. It writes through the admin application (the same code the admin API runs),
// because the policy has to be valid: the closure of the resources, the cache versions and the change log are its work.
// The admin API itself takes no load in a run: it is only used here, once.
func seed(ctx context.Context, e *env, p Profile) (*fixture, error) {
	fx := &fixture{folders: p.folders(), documents: p.documents()}
	steps := []struct {
		name string
		run  func(context.Context, *env, Profile, *fixture) error
	}{
		{"source tables", createSource},
		{"service and types", createServiceAndTypes},
		{"PEP client and key", createPEPClient},
		{"groups", createGroups},
		{"rules", createRules},
	}
	for _, s := range steps {
		start := time.Now()
		if err := s.run(ctx, e, p, fx); err != nil {
			return nil, fmt.Errorf("seed %s: %w", s.name, err)
		}
		fx.phases = append(fx.phases, phase{Name: s.name, Seconds: time.Since(start).Seconds()})
	}
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM assignment_rule`).Scan(&fx.rules); err != nil {
		return nil, fmt.Errorf("count the rules: %w", err)
	}
	return fx, nil
}

// createSource fills the tables the synchronizers read: the folders, and the documents with their folder and owner.
// The owner is the attribute the owner pattern rule matches the claims against (meta.owner).
func createSource(ctx context.Context, e *env, p Profile, fx *fixture) error {
	return execAll(ctx, e.pool, []statement{
		{`CREATE TABLE lt_folder (id text PRIMARY KEY, name text NOT NULL)`, nil},
		{`INSERT INTO lt_folder SELECT 'f-' || g, 'Folder ' || g FROM generate_series(0, $1::int - 1) AS g`,
			[]any{fx.folders}},
		{`CREATE TABLE lt_document (id text PRIMARY KEY, name text NOT NULL, folder_id text NOT NULL, owner text NOT NULL)`, nil},
		{`INSERT INTO lt_document
			SELECT 'd-' || g, 'Document ' || g, 'f-' || (g % $1::int), 'user-' || (g % $2::int)
			FROM generate_series(0, $3::int - 1) AS g`, []any{fx.folders, p.Users, fx.documents}},
		{`CREATE INDEX lt_document_folder ON lt_document (folder_id)`, nil},
		{`ANALYZE lt_folder`, nil},
		{`ANALYZE lt_document`, nil},
	})
}

// createServiceAndTypes registers the service, its two resource types (a document belongs to a folder) and their
// synchronizers, which read the tables above.
func createServiceAndTypes(ctx context.Context, e *env, _ Profile, fx *fixture) error {
	svcIn := &model.ServiceInput{
		Name: "Load test",
		Slug: serviceSlug,
	}
	if err := svcIn.Validate(); err != nil {
		return fmt.Errorf("the service: %w", err)
	}
	svc, err := e.admin.CreateService(ctx, svcIn)
	if err != nil {
		return fmt.Errorf("create the service: %w", err)
	}
	fx.serviceID = svc.ID
	implies := map[string][]string{"write": {"read"}}

	folderIn := &model.TypeResourceInput{Name: "folder", AccessImplies: implies}
	if err := folderIn.Validate(); err != nil {
		return fmt.Errorf("the folder type: %w", err)
	}
	folder, err := e.admin.CreateTypeResource(ctx, svc.ID, folderIn)
	if err != nil {
		return fmt.Errorf("create the folder type: %w", err)
	}
	documentIn := &model.TypeResourceInput{
		Name:          "document",
		Parents:       []model.TypeParent{{TypeResourceID: folder.ID}},
		AccessImplies: implies,
	}
	if err := documentIn.Validate(); err != nil {
		return fmt.Errorf("the document type: %w", err)
	}
	document, err := e.admin.CreateTypeResource(ctx, svc.ID, documentIn)
	if err != nil {
		return fmt.Errorf("create the document type: %w", err)
	}
	fx.folderType, fx.documentType = folder.ID, document.ID

	folderSync := &model.SynchronizerInput{
		Config: model.SynchronizerConfig{
			Kind:      model.KindSQL,
			DSN:       e.dbURI,
			Query:     `SELECT id, name, NULL::text AS parent_id FROM lt_folder WHERE id = :id`,
			ListQuery: `SELECT id, name, NULL::text AS parent_id FROM lt_folder`,
		},
		ProbeResourceID: folderProbe,
	}
	if err := folderSync.Validate(); err != nil {
		return fmt.Errorf("the folder synchronizer: %w", err)
	}
	if _, err := e.admin.CreateSynchronizer(ctx, svc.ID, folder.ID, folderSync); err != nil {
		return fmt.Errorf("synchronize the folders: %w", err)
	}
	documentSync := &model.SynchronizerInput{
		Config: model.SynchronizerConfig{
			Kind:      model.KindSQL,
			DSN:       e.dbURI,
			Query:     `SELECT id, name, folder_id AS parent_id, owner FROM lt_document WHERE id = :id`,
			ListQuery: `SELECT id, name, folder_id AS parent_id, owner FROM lt_document`,
		},
		ProbeResourceID: documentProbe,
	}
	if err := documentSync.Validate(); err != nil {
		return fmt.Errorf("the document synchronizer: %w", err)
	}
	if _, err := e.admin.CreateSynchronizer(ctx, svc.ID, document.ID, documentSync); err != nil {
		return fmt.Errorf("synchronize the documents: %w", err)
	}
	return nil
}

// loadtestOperations are what the PEP client of the run may do: every operation of the PDP.
var loadtestOperations = []string{engine.PEPEvaluate, engine.PEPSearchResource, engine.PEPSearchAction, engine.PEPSearchSubject, engine.PEPExplain}

// createPEPClient makes the one PEP client of the service and issues its API key, the credential of every request of the run:
// the traffic names the person a question is about in the body, and brings the claims the rules read as the properties of the
// subject. The check servers read the PEP directory from the database when they start, after the seeding; AfterPEPWrite makes
// the engine of this process read it too.
func createPEPClient(ctx context.Context, e *env, _ Profile, fx *fixture) error {
	client, err := db.CreatePEPClient(ctx, e.pool, db.PEPClient{
		ServiceID: fx.serviceID, Name: pepClientName, Kind: db.PEPKindAPIKey, Operations: loadtestOperations, Enabled: true,
	})
	if err != nil {
		return fmt.Errorf("create the PEP client: %w", err)
	}
	if fx.apiKey, _, err = db.IssueAPIKey(ctx, e.pool, client.ID); err != nil {
		return fmt.Errorf("issue the API key: %w", err)
	}
	e.app.AfterPEPWrite()
	return nil
}

// createGroups makes one group per department. A member is whoever carries the department claim: the membership is a
// predicate over the claims, so a group has no list of people to keep.
func createGroups(ctx context.Context, e *env, p Profile, fx *fixture) error {
	fx.groups = make([]uuid.UUID, p.Groups)
	for g := range p.Groups {
		group, err := e.admin.CreateGroup(ctx, &model.GroupInput{Name: departmentName(g)})
		if err != nil {
			return fmt.Errorf("create group %s: %w", departmentName(g), err)
		}
		fx.groups[g] = group.ID
		member := &model.GroupMemberInput{Predicate: []model.PredicateCondition{departmentIs(departmentName(g))}}
		if err := member.Validate(); err != nil {
			return fmt.Errorf("the members of %s: %w", departmentName(g), err)
		}
		if _, err := e.admin.CreateGroupMember(ctx, group.ID, member); err != nil {
			return fmt.Errorf("add the members of %s: %w", departmentName(g), err)
		}
	}
	return nil
}

// createRules writes the assignment rules straight into the table with COPY. The check API reads the table and nothing
// else of a rule, so each row needs the constraints of the table and the defaults of a new rule, and no more: the
// change log and the revisions belong to the admin API's writes, which the measured path does not make. The table
// is analyzed afterwards, so the planner sees its real size, as it would after a few writes.
func createRules(ctx context.Context, e *env, p Profile, fx *fixture) error {
	columns := []string{"id", "type_resource_id", "resource_id", "resource_from_claim", "access", "subject", "predicate",
		"member_of_group_id", "enabled", "effect", "exception", "on_missing_attribute", "requires_predicate"}
	n, err := e.pool.CopyFrom(ctx, pgx.Identifier{"assignment_rule"}, columns,
		pgx.CopyFromSlice(p.Rules, func(i int) ([]any, error) {
			return ruleRow(ruleFor(i, p, fx))
		}))
	if err != nil {
		return fmt.Errorf("copy the rules: %w", err)
	}
	if n != int64(p.Rules) {
		return fmt.Errorf("copied %d rules, want %d", n, p.Rules)
	}
	if _, err := e.pool.Exec(ctx, `ANALYZE assignment_rule`); err != nil {
		return fmt.Errorf("analyze the rules: %w", err)
	}
	return nil
}

// ruleRow is the row of a rule input, in the columns of createRules: the defaults are the ones a new rule gets from the
// admin API (a deny rule applies its condition when an attribute is missing, an allow rule skips it).
func ruleRow(in *model.AssignmentRuleInput) ([]any, error) {
	predicate := in.Predicate
	if predicate == nil {
		predicate = []model.PredicateCondition{}
	}
	rawPredicate, err := json.Marshal(predicate)
	if err != nil {
		return nil, fmt.Errorf("encode a predicate: %w", err)
	}
	var claim any // NULL unless the rule takes its resource from a claim
	if in.ResourceFromClaim != nil {
		raw, err := json.Marshal(in.ResourceFromClaim)
		if err != nil {
			return nil, fmt.Errorf("encode a claim: %w", err)
		}
		claim = raw
	}
	effect := in.Effect
	if effect == "" {
		effect = model.EffectAllow
	}
	onMissing := model.OnMissingSkip
	if effect == model.EffectDeny {
		onMissing = model.OnMissingApply
	}
	return []any{
		uuid.New(), in.TypeResourceID, in.ResourceID, claim, in.Access, in.Subject, rawPredicate,
		in.MemberOfGroupID, true, effect, in.Exception, onMissing, []byte("[]"),
	}, nil
}

// ruleFor is the i-th rule of the policy. The mix is the one a service accumulates over time: departments read folders
// (and so their documents), people read single documents, claims reach a folder, a department is denied a folder
// and one document is excepted back below it. Rule 0 is the owner pattern: every subject reads and writes the
// documents whose owner attribute is its own name.
func ruleFor(i int, p Profile, fx *fixture) *model.AssignmentRuleInput {
	folder := ptr(fmt.Sprintf("f-%d", i%fx.folders))
	document := ptr(fmt.Sprintf("d-%d", i%fx.documents))
	person := ptr(subjectName(i % p.Users))
	// The department of a rule is taken from the block of ten rules, not from the rule itself: with a group count that
	// divides ten, i mod groups would fix the kind of the rule (i mod 10) for each group, and some groups would get no grant.
	dept := (i / 10) % p.Groups
	group := ptr(fx.groups[dept])

	if i == 0 {
		return &model.AssignmentRuleInput{
			Name:              ptr("owner pattern"),
			Predicate:         []model.PredicateCondition{{Attr: "preferred_username", Op: model.OpRegex, Value: model.PredicateValue{Str: "^user-"}}},
			TypeResourceID:    ptr(fx.documentType),
			ResourceFromClaim: &model.ResourceFromClaim{Claim: "preferred_username", LookupTypeResourceID: fx.documentType, MatchBy: "meta.owner"},
			Access:            []string{"read", "write"},
		}
	}
	switch i % 10 {
	case 0, 1, 2, 3:
		access := []string{"read"}
		if i%5 == 0 {
			access = []string{"read", "write"}
		}
		return &model.AssignmentRuleInput{MemberOfGroupID: group, TypeResourceID: ptr(fx.folderType), ResourceID: folder, Access: access}
	case 4, 5, 6:
		return &model.AssignmentRuleInput{Subject: person, TypeResourceID: ptr(fx.documentType), ResourceID: document, Access: []string{"read"}}
	case 7:
		return &model.AssignmentRuleInput{
			Predicate:      []model.PredicateCondition{departmentIs(departmentName(dept))},
			TypeResourceID: ptr(fx.folderType),
			ResourceID:     folder,
			Access:         []string{"read"},
		}
	case 8:
		return &model.AssignmentRuleInput{
			MemberOfGroupID: group,
			TypeResourceID:  ptr(fx.folderType),
			ResourceID:      folder,
			Effect:          model.EffectDeny,
			Access:          []string{"read"},
		}
	default:
		return &model.AssignmentRuleInput{
			Subject:        person,
			TypeResourceID: ptr(fx.documentType),
			ResourceID:     document,
			Effect:         model.EffectAllow,
			Exception:      true,
			Access:         []string{"read"},
		}
	}
}

func departmentIs(name string) model.PredicateCondition {
	return model.PredicateCondition{Attr: "department", Op: model.OpEq, Value: model.PredicateValue{Str: name}}
}

func ptr[T any](v T) *T { return &v }

// statement is one SQL statement of the source with its arguments.
type statement struct {
	sql  string
	args []any
}

func execAll(ctx context.Context, pool *pgxpool.Pool, stmts []statement) error {
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s.sql, s.args...); err != nil {
			return fmt.Errorf("exec %.40q: %w", s.sql, err)
		}
	}
	return nil
}
