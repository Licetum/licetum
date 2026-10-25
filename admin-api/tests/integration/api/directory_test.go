//go:build integration

package api_test

// Integration tests of the subject directory of a service (ADR 0001, 8.8): the six routes under /service/{id}/subjects, what a
// write touches, and the retention.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/licetum/licetum/core/model"
)

// person is a directory entry as a PUT sends it.
func person(id, name string, properties map[string]any) map[string]any {
	entry := map[string]any{"id": id, "name": name}
	if properties != nil {
		entry["properties"] = properties
	}
	return entry
}

// sendPeople writes the people to the directory of the service and returns the result of the write.
func (e *env) sendPeople(serviceID string, people ...map[string]any) map[string]any {
	e.t.Helper()
	subjects := make([]any, len(people))
	for i, p := range people {
		subjects[i] = p
	}
	return e.expect(http.StatusOK, http.MethodPut, "/service/"+serviceID+"/subjects", map[string]any{"subjects": subjects})
}

// directoryRows reads the rows of the directory of a service as the database holds them: the version of each row (xmin) and
// when its content changed.
func (e *env) directoryRows(serviceID string) map[string]string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT subject_type || '/' || subject_id, xmin::text || ' ' || updated_at::text
		FROM subject_directory WHERE service_id = $1`, serviceID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, version string
		if err := rows.Scan(&key, &version); err != nil {
			e.t.Fatal(err)
		}
		out[key] = version
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) revisions() string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT scope || '=' || revision FROM revision_state ORDER BY scope`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

// sentAt is when the entry was last sent, as the database holds it.
func (e *env) sentAt(serviceID, subjectType, subjectID string) time.Time {
	e.t.Helper()
	var at time.Time
	if err := e.pool.QueryRow(context.Background(), `
		SELECT seen_at FROM subject_directory_seen WHERE service_id = $1 AND subject_type = $2 AND subject_id = $3`,
		serviceID, subjectType, subjectID).Scan(&at); err != nil {
		e.t.Fatal(err)
	}
	return at
}

func counted(t *testing.T, out map[string]any, created, updated, unchanged int) {
	t.Helper()
	got := [3]int{int(out["created"].(float64)), int(out["updated"].(float64)), int(out["unchanged"].(float64))}
	if got != [3]int{created, updated, unchanged} {
		t.Fatalf("the write created/updated/unchanged %v, want %d/%d/%d", got, created, updated, unchanged)
	}
}

// TestContract_DIR02_AWriteChangesOnlyTheRowsWhoseContentDiffers: an unchanged batch writes no row of the directory and takes no
// revision, the time an entry was last sent is kept apart from it, and the same id in two services is two entries.
func TestContract_DIR02_AWriteChangesOnlyTheRowsWhoseContentDiffers(t *testing.T) {
	e := setup(t)
	a, b := e.newService("dir-a"), e.newService("dir-b")
	ctx := context.Background()

	counted(t, e.sendPeople(a,
		person("alice", "Alice", map[string]any{"department": "sales", "roles": []string{"x", "y"}}),
		person("bob", "Bob", nil),
		person("carol", "Carol", map[string]any{"department": "hr"}),
	), 3, 0, 0)
	before, revisions := e.directoryRows(a), e.revisions()
	if len(before) != 3 {
		t.Fatalf("%d rows after the first write: %v", len(before), before)
	}

	// The same people again, the properties of Alice with their keys in another order: nothing is written to the directory,
	// and no revision is taken (no decision reads the directory).
	counted(t, e.sendPeople(a,
		person("alice", "Alice", map[string]any{"roles": []string{"x", "y"}, "department": "sales"}),
		person("bob", "Bob", map[string]any{}),
		person("carol", "Carol", map[string]any{"department": "hr"}),
	), 0, 0, 3)
	if after := e.directoryRows(a); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("an unchanged batch wrote rows:\nbefore %v\nafter  %v", before, after)
	}
	if got := e.revisions(); got != revisions {
		t.Fatalf("a directory write took a revision: %s, was %s", got, revisions)
	}

	// One changed, one new: only those two rows differ afterwards.
	time.Sleep(10 * time.Millisecond)
	counted(t, e.sendPeople(a,
		person("alice", "Alice", map[string]any{"department": "sales", "roles": []string{"x", "y"}}),
		person("bob", "Bob Builder", nil),
		person("dave", "Dave", nil),
		person("carol", "Carol", map[string]any{"department": "hr"}),
	), 1, 1, 2)
	after := e.directoryRows(a)
	for key, version := range before {
		changed := after[key] != version
		if want := key == "user/bob"; changed != want {
			t.Errorf("%s: changed=%v, want %v (%s -> %s)", key, changed, want, version, after[key])
		}
	}
	if _, ok := after["user/dave"]; !ok || len(after) != 4 {
		t.Fatalf("the rows after the third write: %v", after)
	}

	// The time of the sending lives apart: sent again an hour and more later, an entry that has not changed is not rewritten,
	// and its sending time moves; sent again at once, even that stays.
	old := time.Now().Add(-3 * time.Hour)
	if _, err := e.pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at = $2 WHERE service_id = $1`, a, old); err != nil {
		t.Fatal(err)
	}
	rows := e.directoryRows(a)
	counted(t, e.sendPeople(a, person("carol", "Carol", map[string]any{"department": "hr"})), 0, 0, 1)
	if fmt.Sprint(e.directoryRows(a)) != fmt.Sprint(rows) {
		t.Fatal("sending an unchanged entry rewrote it")
	}
	moved := e.sentAt(a, "user", "carol")
	if !moved.After(old.Add(time.Hour)) {
		t.Errorf("the sending time of carol did not move: %s", moved)
	}
	if !e.sentAt(a, "user", "alice").Equal(old.Truncate(time.Microsecond)) && e.sentAt(a, "user", "alice").After(old.Add(time.Minute)) {
		t.Error("an entry that was not sent had its sending time moved")
	}
	e.sendPeople(a, person("carol", "Carol", map[string]any{"department": "hr"}))
	if again := e.sentAt(a, "user", "carol"); !again.Equal(moved) {
		t.Errorf("sent again at once, the sending time moved from %s to %s", moved, again)
	}

	// The same id in two services is two entries, which do not touch one another.
	counted(t, e.sendPeople(b, person("alice", "Alice of B", map[string]any{"department": "ops"})), 1, 0, 0)
	counted(t, e.sendPeople(a, person("alice", "Alice renamed", nil)), 0, 1, 0)
	listA := e.expect(http.StatusOK, http.MethodGet, "/service/"+a+"/subjects?search=alice", nil)
	listB := e.expect(http.StatusOK, http.MethodGet, "/service/"+b+"/subjects?search=alice", nil)
	if name := listA["items"].([]any)[0].(map[string]any)["name"]; name != "Alice renamed" {
		t.Errorf("alice of A is %v", name)
	}
	if one := listB["items"].([]any)[0].(map[string]any); one["name"] != "Alice of B" || one["properties"].(map[string]any)["department"] != "ops" {
		t.Errorf("alice of B was touched by the write to A: %v", one)
	}
	e.expect(http.StatusOK, http.MethodDelete, "/service/"+a+"/subjects/user/alice", nil)
	e.expect(http.StatusNotFound, http.MethodGet, "/service/"+a+"/subjects/user/alice", nil)
	e.expect(http.StatusOK, http.MethodGet, "/service/"+b+"/subjects/user/alice", nil)
}

// An entry that the directory cannot hold is refused as a whole: nothing of the request is written.
func TestDirectoryRefusesWhatItCannotHold(t *testing.T) {
	e := setup(t)
	s := e.newService("dir-bad")
	tooMany := map[string]any{}
	for i := range 101 {
		tooMany[fmt.Sprintf("p%d", i)] = "x"
	}
	for name, body := range map[string]map[string]any{
		"the same person twice":    {"subjects": []any{person("alice", "A", nil), person("alice", "B", nil)}},
		"a group as a person":      {"subjects": []any{person("group:4b0e2b1a-3b8e-4a6e-9a63-0f6a6a5b6c7d", "G", nil)}},
		"a type that is no name":   {"subjects": []any{map[string]any{"type": "9 users", "id": "alice"}}},
		"an empty id":              {"subjects": []any{person("", "A", nil)}},
		"too many properties":      {"subjects": []any{person("alice", "A", tooMany)}},
		"properties too big":       {"subjects": []any{person("alice", "A", map[string]any{"bio": strings.Repeat("x", 17<<10)})}},
		"no people at all":         {"subjects": []any{}},
		"a NUL in the name":        {"subjects": []any{person("alice", "A\x00B", nil)}},
		"a good one and a bad one": {"subjects": []any{person("bob", "Bob", nil), person("", "", nil)}},
	} {
		if status, raw := e.do(http.MethodPut, e.admin.URL+"/service/"+s+"/subjects", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d (%s), want 422", name, status, raw)
		}
	}
	people := make([]map[string]any, 1001)
	for i := range people {
		people[i] = person(fmt.Sprintf("u%04d", i), "", nil)
	}
	over := make([]any, len(people))
	for i, p := range people {
		over[i] = p
	}
	if status, raw := e.do(http.MethodPut, e.admin.URL+"/service/"+s+"/subjects", map[string]any{"subjects": over}); status != http.StatusUnprocessableEntity {
		t.Errorf("1001 people = %d (%s), want 422", status, raw)
	}
	if rows := e.directoryRows(s); len(rows) != 0 {
		t.Errorf("a refused request wrote %v", rows)
	}
	// 1000 are the limit; a service that does not exist has no directory.
	counted(t, e.sendPeople(s, people[:1000]...), 1000, 0, 0)
	e.expect(http.StatusNotFound, http.MethodPut, "/service/00000000-0000-4000-8000-000000000000/subjects",
		map[string]any{"subjects": []any{person("alice", "", nil)}})
}

func TestDirectoryListsPagesSearchesAndShowsTheGroupsOfAPerson(t *testing.T) {
	e := setup(t)
	s := e.newService("dir-list")
	sales := e.must(http.MethodPost, "/group", map[string]any{"name": "sales"})["id"].(string)
	e.must(http.MethodPost, "/group/"+sales+"/member", map[string]any{
		"predicate": []map[string]any{{"attr": "department", "op": "eq", "value": "sales"}}})

	e.sendPeople(s,
		person("alice", "Alice Liddell", map[string]any{"department": "sales"}),
		person("Bob", "Bob", map[string]any{"department": "hr"}),
		person("a/b", "Slash", nil),
		person("100%_real", "Percent", nil),
		map[string]any{"type": "service", "id": "billing", "name": "Billing robot"},
	)

	list := e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects", nil)
	var ids []string
	for _, item := range list["items"].([]any) {
		one := item.(map[string]any)
		ids = append(ids, one["type"].(string)+"/"+one["id"].(string))
	}
	// By type, then bytewise by id: the capital B before the lower case a.
	if want := "service/billing user/100%_real user/Bob user/a/b user/alice"; strings.Join(ids, " ") != want || int(list["total"].(float64)) != 5 {
		t.Fatalf("the list: %v (total %v), want %s", ids, list["total"], want)
	}
	page := e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects?type=user&limit=2&offset=1", nil)
	if items := page["items"].([]any); len(items) != 2 || items[0].(map[string]any)["id"] != "Bob" || int(page["total"].(float64)) != 4 {
		t.Errorf("the second page of the people: %v", page)
	}
	// The search is by part of the id or of the name, and % and _ in it are characters.
	for search, want := range map[string]string{"liddell": "alice", "BOB": "Bob", "%_": "100%_real", "robot": "billing"} {
		got := e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects?search="+url.QueryEscape(search), nil)
		items := got["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] != want {
			t.Errorf("search %q found %v, want %s", search, items, want)
		}
	}

	// One person, with the groups their stored properties put them in; an id with a slash goes percent-encoded.
	alice := e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects/user/alice", nil)
	groups := alice["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["name"] != "sales" || alice["properties"].(map[string]any)["department"] != "sales" {
		t.Errorf("alice: %v", alice)
	}
	if bob := e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects/user/Bob", nil); len(bob["groups"].([]any)) != 0 {
		t.Errorf("bob is in %v", bob["groups"])
	}
	e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects/user/"+url.PathEscape("a/b"), nil)
	e.expect(http.StatusOK, http.MethodDelete, "/service/"+s+"/subjects/user/"+url.PathEscape("a/b"), nil)
	e.expect(http.StatusNotFound, http.MethodGet, "/service/"+s+"/subjects/user/"+url.PathEscape("a/b"), nil)
	e.expect(http.StatusNotFound, http.MethodDelete, "/service/"+s+"/subjects/user/nobody", nil)
	e.expect(http.StatusNotFound, http.MethodGet, "/service/"+s+"/subjects/service/alice", nil) // the type is part of the key
}

func TestDirectoryIsGovernedByThePolicyOfTheMemberships(t *testing.T) {
	e := setup(t)
	s := e.newService("dir-policy")
	whole := model.PlatformTypeResourceID(model.PlatformGroupMember).String()
	e.must(http.MethodPost, "/assignment_rule", map[string]any{"type_resource_id": whole, "subject": "reader", "access": []string{"read"}})
	reader := "Bearer " + e.token(jwt.MapClaims{"preferred_username": "reader"})
	stranger := "Bearer " + e.token(jwt.MapClaims{"preferred_username": "stranger"})
	body := map[string]any{"subjects": []any{person("alice", "A", nil)}}
	e.sendPeople(s, person("bob", "Bob", nil))

	e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects", nil, "Authorization", reader)
	e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects/user/bob", nil, "Authorization", reader)
	e.expect(http.StatusOK, http.MethodGet, "/service/"+s+"/subjects/settings", nil, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodPut, "/service/"+s+"/subjects", body, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodDelete, "/service/"+s+"/subjects/user/bob", nil, "Authorization", reader)
	e.expect(http.StatusForbidden, http.MethodPut, "/service/"+s+"/subjects/settings", map[string]any{"retention_days": 7}, "Authorization", reader)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		e.expect(http.StatusForbidden, method, "/service/"+s+"/subjects", body, "Authorization", stranger)
	}
	e.expect(http.StatusForbidden, http.MethodGet, "/service/"+s+"/subjects/user/bob", nil, "Authorization", stranger)
}

// TestContract_DIR04_RetentionRemovesInChunksTheEntriesNotSentWithinTheRetentionOfTheirService: an entry sent again survives, a
// service without a retention period keeps everything, and the other services are untouched.
func TestContract_DIR04_RetentionRemovesInChunksTheEntriesNotSentWithinTheRetentionOfTheirService(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	short, none, long := e.newService("ret-short"), e.newService("ret-none"), e.newService("ret-long")

	// The default is no retention; 0 and a negative number are no period.
	if got := e.expect(http.StatusOK, http.MethodGet, "/service/"+short+"/subjects/settings", nil); got["retention_days"] != nil {
		t.Fatalf("the default retention is %v, want none", got["retention_days"])
	}
	for _, days := range []int{0, -1, 36501} {
		e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/service/"+short+"/subjects/settings", map[string]any{"retention_days": days})
	}
	e.expect(http.StatusOK, http.MethodPut, "/service/"+short+"/subjects/settings", map[string]any{"retention_days": 30})
	e.expect(http.StatusOK, http.MethodPut, "/service/"+long+"/subjects/settings", map[string]any{"retention_days": 365})
	if got := e.expect(http.StatusOK, http.MethodGet, "/service/"+short+"/subjects/settings", nil); got["retention_days"] != float64(30) {
		t.Fatalf("the retention of the short service is %v", got["retention_days"])
	}

	for _, s := range []string{short, none, long} {
		e.sendPeople(s, person("old", "Old", nil), person("again", "Sent again", nil), person("fresh", "Fresh", nil))
		if _, err := e.pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at = now() - interval '40 days'
			WHERE service_id = $1 AND subject_id IN ('old', 'again')`, s); err != nil {
			t.Fatal(err)
		}
	}
	// "again" is sent once more in every service: its sending time moves, the entry itself is not written.
	for _, s := range []string{short, none, long} {
		counted(t, e.sendPeople(s, person("again", "Sent again", nil)), 0, 0, 1)
	}

	e.app.RunRetention(ctx)

	left := func(s string) string {
		rows, err := e.pool.Query(ctx, `SELECT subject_id FROM subject_directory WHERE service_id = $1 ORDER BY subject_id`, s)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return strings.Join(ids, " ")
	}
	for s, want := range map[string]string{
		short: "again fresh",     // "old" was not sent within 30 days
		none:  "again fresh old", // no retention period: everything is kept
		long:  "again fresh old", // 40 days is within its 365
	} {
		if got := left(s); got != want {
			t.Errorf("after the retention %s keeps %q, want %q", s, got, want)
		}
	}
	// What the retention removed took its sending time with it.
	var seen int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM subject_directory_seen WHERE service_id = $1`, short).Scan(&seen); err != nil || seen != 2 {
		t.Errorf("%d sending times left for the short service (%v), want 2", seen, err)
	}
	// Removing the period keeps what is left for good.
	e.expect(http.StatusOK, http.MethodPut, "/service/"+short+"/subjects/settings", map[string]any{"retention_days": nil})
	if _, err := e.pool.Exec(ctx, `UPDATE subject_directory_seen SET seen_at = now() - interval '4000 days' WHERE service_id = $1`, short); err != nil {
		t.Fatal(err)
	}
	e.app.RunRetention(ctx)
	if got := left(short); got != "again fresh" {
		t.Errorf("a service without a period lost entries: %q", got)
	}
}
