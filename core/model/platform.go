package model

import "github.com/google/uuid"

// PlatformType is a resource type of the self-service — an access policy for Licetum itself.
// The type name is also the key in the /permission response and the resource name in the admin UI.
type PlatformType string

const (
	PlatformService        PlatformType = "service"
	PlatformTypeResource   PlatformType = "type_resource" // also covers its synchronizers and resources
	PlatformGroup          PlatformType = "group"
	PlatformGroupMember    PlatformType = "group_member"
	PlatformRole           PlatformType = "role" // also covers its permission items (role_permission)
	PlatformAssignmentRule PlatformType = "assignment_rule"
)

// PlatformTypes lists every platform policy in a stable order.
var PlatformTypes = []PlatformType{
	PlatformService, PlatformTypeResource, PlatformGroup, PlatformGroupMember, PlatformRole, PlatformAssignmentRule,
}

// Action is an access level checked on platform policies.
type Action string

const (
	ActionRead  Action = "read"
	ActionWrite Action = "write"
)

// Deterministic self-service ids: uuid5(NAMESPACE_URL, ...), identical in every environment and
// independent of insertion order. Changing these strings orphans already seeded data.
// PlatformSlug is the slug of Licetum registered within itself: it does not depend on the configured name.
const PlatformSlug = "licetum"

// The admin group and its membership keep the ids they were seeded with when they were still a role:
// changing them would orphan the rows that already exist.
var (
	PlatformServiceID          = platformID("service")
	PlatformAdminGroupID       = platformID("role:admin")
	PlatformAdminGroupMemberID = platformID("role_member:admin")
)

// platformIDNamespace is the prefix of the names the self-service ids are derived from. It is the name the product had
// before it became Licetum, and it stays: the ids are stored in every installed database, in the literal ids of the
// and in the redirect URIs of the identity providers, so changing it would orphan them.
const platformIDNamespace = "reauthz:platform:"

func platformID(name string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(platformIDNamespace+name))
}

// PlatformTypeResourceID is the id of the resource type backing a platform policy.
func PlatformTypeResourceID(t PlatformType) uuid.UUID {
	return platformID("type_resource:" + string(t))
}

// PlatformSynchronizerID is the id of the synchronizer listing the entities of a platform policy.
func PlatformSynchronizerID(t PlatformType) uuid.UUID { return platformID("synchronizer:" + string(t)) }

// PlatformRestoredMemberID is the id of the membership `licetum restore-admin -user` adds for a user, so that running
// it twice for the same user adds one row.
func PlatformRestoredMemberID(user string) uuid.UUID {
	return platformID("group_member:restored:" + user)
}

// PlatformAdminRuleID is the id of the rule granting the admin group access to a platform policy. A fresh
// install seeds these ids; an upgraded one has the rules of the previous release, which kept their ids when
// the policies were renamed, so the ids below are the same on both.
func PlatformAdminRuleID(t PlatformType) uuid.UUID {
	switch t {
	case PlatformGroup:
		return platformID("assignment_rule:admin:role") // the rule of the previous `role` policy, moved to `group`
	case PlatformGroupMember:
		return platformID("assignment_rule:admin:role_member") // likewise for `role_member`
	case PlatformRole:
		// "assignment_rule:admin:role" is taken by the moved rule, so the new policy's rule has its own name.
		return platformID("assignment_rule:admin:role:0005")
	}
	return platformID("assignment_rule:admin:" + string(t))
}

// Platform is the GET /platform response.
type Platform struct {
	ServiceID    uuid.UUID          `json:"service_id" format:"uuid" doc:"Id of the service under which Licetum is registered within itself"`
	AdminGroupID uuid.UUID          `json:"admin_group_id" format:"uuid" doc:"Id of the group of platform administrators that the self-service is seeded with"`
	Types        []PlatformTypeInfo `json:"types" nullable:"false" doc:"The platform policies: the resource types of the self-service"`
}

// PlatformTypeInfo names one platform policy and gives the id of the resource type behind it.
type PlatformTypeInfo struct {
	Name string    `json:"name" doc:"The policy: service, type_resource, group, group_member, role or assignment_rule"`
	ID   uuid.UUID `json:"id" format:"uuid" doc:"Id of the resource type of the policy"`
}

// TypeAccess is the current user's access to one platform policy.
type TypeAccess struct {
	WholeType []string         `json:"whole_type" doc:"Access levels on the whole type (including creating new entities)" example:"[\"read\",\"write\"]"`
	Entries   []ResourceAccess `json:"entries" doc:"Entities whose access differs from the whole-type access: their own levels (more or fewer)"`
	Denied    []ResourceAccess `json:"denied" doc:"Entities on which access levels are denied (a denial rule): the levels that are taken away, even where the whole-type access grants them"`
}

// PlatformAccess is the GET /platform/me response.
type PlatformAccess struct {
	Subject string                `json:"subject" doc:"Current user id (preferred_username, or sub if it is missing)" example:"alice"`
	Types   map[string]TypeAccess `json:"types" doc:"Access per platform resource type (service, type_resource, group, group_member, role, assignment_rule); types without any access are omitted"`
}
