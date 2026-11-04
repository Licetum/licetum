//go:build integration

package api_test

import (
	"context"
	"net/http"
	"testing"
)

func TestAdminTypeLinksMoveDesiredAndEffectiveStateTogether(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	service := e.must(http.MethodPost, "/service", servicePayload(e, "effective-links"))["id"].(string)
	parent := e.must(http.MethodPost, "/service/"+service+"/type_resource", map[string]any{"name": "parent"})["id"].(string)
	path := "/service/" + service + "/type_resource"
	child := e.must(http.MethodPost, path, map[string]any{"name": "child", "parents": []any{map[string]any{"type_resource_id": parent, "inherit": []string{"read"}}}})["id"].(string)
	var equal, exported bool
	var buckets int
	if err := e.pool.QueryRow(ctx, `SELECT live AND eff_live AND inherit IS NOT DISTINCT FROM eff_inherit FROM type_resource_parent WHERE type_resource_id=$1`, child).Scan(&equal); err != nil || !equal {
		t.Fatalf("split states: %v %v", equal, err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT exported,buckets FROM type_resource WHERE id=$1`, child).Scan(&exported, &buckets); err != nil || exported || buckets != 16 {
		t.Fatalf("type defaults: %v %d %v", exported, buckets, err)
	}
	e.must(http.MethodPatch, path+"/"+child, map[string]any{"name": "child", "parents": []any{}})
	var remaining int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM type_resource_parent WHERE type_resource_id=$1`, child).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("removed link retained: %d %v", remaining, err)
	}
}
