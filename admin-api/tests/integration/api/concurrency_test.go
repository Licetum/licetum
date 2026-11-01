//go:build integration

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/licetum/licetum/core/model"
)

func concurrentPair(f, g func() (int, []byte)) [2]struct {
	status int
	body   []byte
} {
	start := make(chan struct{})
	done := make(chan struct {
		index, status int
		body          []byte
	}, 2)
	for i, fn := range []func() (int, []byte){f, g} {
		go func() {
			<-start
			s, b := fn()
			done <- struct {
				index, status int
				body          []byte
			}{i, s, b}
		}()
	}
	close(start)
	var out [2]struct {
		status int
		body   []byte
	}
	for range 2 {
		r := <-done
		out[r.index] = struct {
			status int
			body   []byte
		}{r.status, r.body}
	}
	return out
}

func TestContract_WR04_DeleteRacingUseNeverLeavesDanglingPolicy(t *testing.T) {
	for _, kind := range []string{"role", "group", "type"} {
		t.Run(kind, func(t *testing.T) {
			e := setup(t)
			service := e.must("POST", "/service", servicePayload(e, "use-race-"+kind))["id"].(string)
			for round := range 5 {
				var removePath, createPath, checkSQL string
				var body map[string]any
				var id string
				switch kind {
				case "role":
					id = e.must("POST", "/role", map[string]any{"name": fmt.Sprintf("race-%d", round), "service_id": model.PlatformServiceID})["id"].(string)
					removePath = "/role/" + id
					createPath = "/assignment_rule"
					body = map[string]any{"role_id": id, "subject": "alice", "service_id": model.PlatformServiceID}
					checkSQL = `SELECT count(*) FROM assignment_rule a LEFT JOIN role r ON r.id=a.role_id WHERE a.role_id=$1 AND r.id IS NULL`
				case "group":
					id = e.must("POST", "/group", map[string]any{"name": fmt.Sprintf("race-%d", round), "service_id": model.PlatformServiceID})["id"].(string)
					removePath = "/group/" + id
					createPath = removePath + "/member"
					body = map[string]any{"predicate": []map[string]any{{"attr": "sub", "op": "eq", "value": "alice"}}}
					checkSQL = `SELECT count(*) FROM group_member m LEFT JOIN user_group g ON g.id=m.group_id WHERE m.group_id=$1 AND g.id IS NULL`
				case "type":
					id = e.must("POST", "/service/"+service+"/type_resource", map[string]any{"name": fmt.Sprintf("parent-%d", round)})["id"].(string)
					removePath = "/service/" + service + "/type_resource/" + id
					createPath = "/service/" + service + "/type_resource"
					body = map[string]any{"name": fmt.Sprintf("child-%d", round), "parents": []map[string]any{{"type_resource_id": id}}}
					checkSQL = `SELECT count(*) FROM type_resource_parent p JOIN type_resource child ON child.id=p.type_resource_id JOIN type_resource parent ON parent.id=p.parent_id WHERE p.parent_id=$1 AND parent.deleted_at IS NOT NULL AND child.deleted_at IS NULL`
				}
				results := concurrentPair(func() (int, []byte) { return e.do("DELETE", e.admin.URL+removePath, nil) }, func() (int, []byte) { return e.do("POST", e.admin.URL+createPath, body) })
				success := 0
				for _, r := range results {
					if r.status == http.StatusOK {
						success++
					} else if r.status != http.StatusNotFound && r.status != http.StatusConflict {
						t.Fatalf("unexpected %s race status: %d %s", kind, r.status, r.body)
					}
				}
				if success != 1 {
					t.Fatalf("delete and use both committed: %+v", results)
				}
				var dangling int
				if err := e.pool.QueryRow(context.Background(), checkSQL, id).Scan(&dangling); err != nil {
					t.Fatal(err)
				}
				if dangling != 0 {
					t.Fatalf("%s race left dangling policy", kind)
				}
			}
		})
	}
}

func TestContract_WR04_ConcurrentRevocationsKeepTheLastAdministrator(t *testing.T) {
	for _, kind := range []string{"rule", "membership", "group", "role", "item"} {
		t.Run(kind, func(t *testing.T) {
			for round := range 3 {
				e := setup(t)
				settle := func(typ model.PlatformType) {
					ctx := context.Background()
					graph, err := e.app.LoadTypeGraph(ctx)
					if err != nil {
						t.Fatal(err)
					}
					id := model.PlatformTypeResourceID(typ)
					synced, err := e.app.EnsureSynced(ctx, graph, []uuid.UUID{id}, false)
					if err != nil || !synced[id] {
						t.Fatalf("prepare platform resources: %v %v", synced, err)
					}
				}
				makePath := func(n int) []map[string]any {
					var remove []map[string]any
					body := map[string]any{"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule), "subject": "admin", "access": []string{"read", "write"}, "service_id": model.PlatformServiceID}
					if kind == "membership" || kind == "group" {
						group := e.must("POST", "/group", map[string]any{"name": fmt.Sprintf("backup-%d-%d", round, n), "service_id": model.PlatformServiceID})["id"].(string)
						member := e.must("POST", "/group/"+group+"/member", map[string]any{"predicate": []map[string]any{{"attr": "preferred_username", "op": "eq", "value": "admin"}}})["id"].(string)
						body["subject"] = "group:" + group
						remove = append(remove, map[string]any{"op": "delete", "entity": "group_member", "id": member, "parent": group})
						if kind == "group" {
							remove = append(remove, map[string]any{"op": "delete", "entity": "group", "id": group})
						}
					}
					if kind == "role" || kind == "item" {
						role := e.must("POST", "/role", map[string]any{"name": fmt.Sprintf("backup-%d-%d", round, n), "service_id": model.PlatformServiceID})["id"].(string)
						settle(model.PlatformRole)
						item := e.must("POST", "/role/"+role+"/permission", map[string]any{"type_resource_id": model.PlatformTypeResourceID(model.PlatformAssignmentRule), "access": []string{"read", "write"}})["id"].(string)
						delete(body, "type_resource_id")
						delete(body, "access")
						body["role_id"] = role
						if kind == "item" {
							remove = append(remove, map[string]any{"op": "delete", "entity": "role_permission", "id": item, "parent": role})
						} else {
							remove = append(remove, map[string]any{"op": "delete", "entity": "role", "id": role})
						}
					}
					rule := e.must("POST", "/assignment_rule", body)["id"].(string)
					if kind == "rule" || kind == "group" || kind == "role" {
						remove = append([]map[string]any{{"op": "delete", "entity": "assignment_rule", "id": rule}}, remove...)
					}
					return remove
				}
				left, right := makePath(0), makePath(1)
				settle(model.PlatformAssignmentRule)
				e.must("DELETE", "/assignment_rule/"+model.PlatformAdminRuleID(model.PlatformAssignmentRule).String(), nil)
				apply := func(ops []map[string]any) func() (int, []byte) {
					return func() (int, []byte) { return e.do("POST", e.admin.URL+"/batch", map[string]any{"operations": ops}) }
				}
				result := concurrentPair(apply(left), apply(right))
				success, conflict := 0, 0
				for _, r := range result {
					if r.status == http.StatusOK {
						success++
					}
					if r.status == http.StatusConflict && strings.Contains(string(r.body), "last_admin_access") {
						conflict++
					}
				}
				if success != 1 || conflict != 1 {
					t.Fatalf("concurrent %s revocations: %+v", kind, result)
				}
			}
		})
	}
}

func TestContract_WR04_ConcurrentLinksCannotCloseATypeCycle(t *testing.T) {
	e := setup(t)
	service := e.must("POST", "/service", servicePayload(e, "cycle-race"))["id"].(string)
	for round := range 5 {
		a := e.must("POST", "/service/"+service+"/type_resource", map[string]any{"name": fmt.Sprintf("a%d", round)})["id"].(string)
		b := e.must("POST", "/service/"+service+"/type_resource", map[string]any{"name": fmt.Sprintf("b%d", round)})["id"].(string)
		link := func(id, parent, name string) func() (int, []byte) {
			return func() (int, []byte) {
				return e.do("PATCH", e.admin.URL+"/service/"+service+"/type_resource/"+id, map[string]any{"name": name, "parents": []map[string]any{{"type_resource_id": parent}}})
			}
		}
		results := concurrentPair(link(a, b, fmt.Sprintf("a%d", round)), link(b, a, fmt.Sprintf("b%d", round)))
		success := 0
		for _, r := range results {
			if r.status == http.StatusOK {
				success++
			} else if r.status != http.StatusBadRequest {
				t.Fatalf("unexpected link result: %d %s", r.status, r.body)
			}
		}
		if success != 1 {
			t.Fatalf("both halves of a type cycle committed: %+v", results)
		}
	}
}
