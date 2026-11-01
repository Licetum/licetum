package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/licetum/licetum/core/outbox"
)

type pepMutation struct {
	table     string
	key       func() map[string]any
	serviceID *uuid.UUID
	clientID  *uuid.UUID
	action    string
}
type pepMutationTx struct {
	pgx.Tx
	changed bool
}

func markPEPMutation(tx pgx.Tx) error {
	p, ok := tx.(*pepMutationTx)
	if !ok {
		return fmt.Errorf("PEP write outside PolicyTx")
	}
	p.changed = true
	return nil
}

func runPEPMutation(ctx context.Context, pool *pgxpool.Pool, m pepMutation, write func(pgx.Tx) error) error {
	_, err := PolicyTx(ctx, pool, nil, func(ctx context.Context, tx pgx.Tx) ([]Change, error) {
		before, err := outbox.Project(ctx, tx, m.table, m.key())
		if err != nil {
			return nil, err
		}
		owner := m.serviceID
		if owner == nil && m.table == "pep_credential" {
			var client uuid.UUID
			if m.clientID != nil {
				client = *m.clientID
			} else {
				var row struct {
					ClientID uuid.UUID `json:"client_id"`
				}
				if err := json.Unmarshal(before.Row, &row); err != nil {
					return nil, err
				}
				client = row.ClientID
			}
			var service uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT service_id FROM pep_client WHERE id=$1 AND deleted_at IS NULL`, client).Scan(&service); err != nil {
				return nil, ErrPEPNotFound
			}
			owner = &service
		}
		if owner != nil {
			var live bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service WHERE id=$1 AND deleted_at IS NULL)`, owner).Scan(&live); err != nil {
				return nil, err
			}
			if !live {
				return nil, ErrPEPNotFound
			}
		}
		var credentials []outbox.Image
		if m.table == "pep_client" && m.action == "delete" {
			rows, err := tx.Query(ctx, `SELECT key_id FROM pep_credential WHERE client_id=$1 AND revoked_at IS NULL`, m.key()["id"])
			if err != nil {
				return nil, err
			}
			var ids []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return nil, err
				}
				ids = append(ids, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
			for _, id := range ids {
				image, err := outbox.Project(ctx, tx, "pep_credential", map[string]any{"key_id": id})
				if err != nil {
					return nil, err
				}
				credentials = append(credentials, image)
			}
		}
		var issuerKeys *outbox.Image
		if m.table == "pep_issuer" && m.action == "delete" {
			image, err := outbox.Project(ctx, tx, "pep_issuer_keys", m.key())
			if err != nil {
				return nil, err
			}
			var row map[string]any
			if err := json.Unmarshal(image.Row, &row); err != nil {
				return nil, err
			}
			if row["deleted"] != true {
				issuerKeys = &image
			}
		}
		wrapped := &pepMutationTx{Tx: tx}
		if err := write(wrapped); err != nil {
			return nil, err
		}
		if !wrapped.changed {
			return nil, nil
		}
		after, err := outbox.Project(ctx, tx, m.table, m.key())
		if err != nil {
			return nil, err
		}
		main, err := pepChange(ctx, m.table, m.key(), owner, m.action, before.Row, after.Row)
		if err != nil {
			return nil, err
		}
		changes := []Change{main}
		if issuerKeys != nil {
			next, err := outbox.Project(ctx, tx, issuerKeys.Table, issuerKeys.Key)
			if err != nil {
				return nil, err
			}
			c, err := pepChange(ctx, issuerKeys.Table, issuerKeys.Key, nil, "delete", issuerKeys.Row, next.Row)
			if err != nil {
				return nil, err
			}
			changes = append(changes, c)
		}
		for _, old := range credentials {
			next, err := outbox.Project(ctx, tx, old.Table, old.Key)
			if err != nil {
				return nil, err
			}
			c, err := pepChange(ctx, old.Table, old.Key, owner, "update", old.Row, next.Row)
			if err != nil {
				return nil, err
			}
			changes = append(changes, c)
		}
		return changes, nil
	})
	return err
}

func pepChange(ctx context.Context, table string, key map[string]any, owner *uuid.UUID, action string, before, after json.RawMessage) (Change, error) {
	scrub := func(raw json.RawMessage) (json.RawMessage, bool, error) {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, false, err
		}
		if row["deleted"] == true {
			return nil, true, nil
		}
		delete(row, "sha256")
		out, err := json.Marshal(row)
		return out, false, err
	}
	old, missing, err := scrub(before)
	if err != nil {
		return Change{}, err
	}
	next, _, err := scrub(after)
	if err != nil {
		return Change{}, err
	}
	if action == "upsert" {
		if missing {
			action = "create"
		} else {
			action = "update"
		}
	}
	var id *uuid.UUID
	if v, ok := key["id"].(uuid.UUID); ok {
		id = &v
	}
	if missing {
		before = nil
	}
	return Change{Entity: table, ID: id, ServiceID: owner, Actor: publicationActor(ctx), Action: action, Before: old, After: next, Touched: []Touched{{BeforeKnown: true, BeforeFields: before, Table: table, Key: key, ServiceID: owner, Deleted: action == "delete"}}}, nil
}
