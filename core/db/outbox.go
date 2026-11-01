package db

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/licetum/licetum/core/outbox"
)

type Touched struct {
	Table     string
	Key       map[string]any
	ServiceID *uuid.UUID
	Deleted   bool
}

// Change keeps the history representation distinct from canonical database images.
type Change struct {
	Entity    string
	ID        *uuid.UUID
	ServiceID *uuid.UUID
	Actor     string
	Action    string
	Before    json.RawMessage
	After     json.RawMessage
	Touched   []Touched
	Streams   []string
	Services  []uuid.UUID
}

// Position is a global commit position. Revisions are gap-free within an epoch.
type Position struct {
	Epoch    int32 `json:"epoch"`
	Revision int64 `json:"revision"`
}

func (p Position) String() string {
	return strconv.FormatInt(int64(p.Epoch), 10) + "." + strconv.FormatInt(p.Revision, 10)
}

const takeRevisionSQL = `WITH r AS (
 UPDATE revision_state SET revision=revision+1 WHERE scope='global' RETURNING epoch, revision
) INSERT INTO outbox(revision,epoch,kind,committed_at,services,streams,policy,payload,chunk_count,payload_sha256)
SELECT r.revision,r.epoch,$1,clock_timestamp(),$2,$3,$4::jsonb,$5,$6,$7 FROM r RETURNING epoch,revision`

// TakeRevision is the last write of a revision-only transaction. Payload chunks
// are copied before taking the revision row; only history, NOTIFY and COMMIT follow it.
func TakeRevision(ctx context.Context, tx pgx.Tx, entry outbox.Entry) (Position, error) {
	p, err := finishPolicy(ctx, tx, entry, nil)
	if err == nil {
		notePublication(ctx, p)
	}
	return p, err
}

func prepareEntry(ctx context.Context, tx pgx.Tx, entry outbox.Entry) ([]any, error) {
	if entry.Kind == "" {
		entry.Kind = outbox.Change
	}
	if entry.Kind != outbox.Change && entry.Kind != outbox.EpochStart {
		return nil, fmt.Errorf("invalid outbox kind %q", entry.Kind)
	}
	if entry.Services == nil {
		entry.Services = []uuid.UUID{}
	}
	if entry.Streams == nil {
		entry.Streams = []string{}
	}
	slices.SortFunc(entry.Services, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	entry.Services = slices.Compact(entry.Services)
	slices.Sort(entry.Streams)
	entry.Streams = slices.Compact(entry.Streams)
	if entry.Policy == nil {
		entry.Policy = map[string][]outbox.Image{}
	}
	policy, err := json.Marshal(entry.Policy)
	if err != nil {
		return nil, err
	}
	var payload any
	var digest []byte
	count := 0
	if len(policy) > outbox.ChunkLimit {
		id := uuid.New()
		payload = id
		var rows [][]any
		metadata := map[string]any{}
		streams := make([]string, 0, len(entry.Policy))
		for name := range entry.Policy {
			streams = append(streams, name)
		}
		slices.Sort(streams)
		for _, name := range streams {
			var records [][]byte
			for _, image := range entry.Policy[name] {
				r, err := json.Marshal(image)
				if err != nil {
					return nil, err
				}
				records = append(records, r)
			}
			encoded, err := outbox.Encode(records)
			if err != nil {
				return nil, err
			}
			metadata[name] = map[string]any{"chunks": len(encoded.Chunks), "sha256": encoded.Digest[:]}
			for n, body := range encoded.Chunks {
				rows = append(rows, []any{id, name, n, body})
				count++
			}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"outbox_chunk"}, []string{"payload", "stream", "n", "body"}, pgx.CopyFromRows(rows)); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(policy)
		digest = sum[:]
		policy, err = json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
	}
	return []any{entry.Kind, entry.Services, entry.Streams, string(policy), payload, count, digest}, nil
}

func finishPolicy(ctx context.Context, tx pgx.Tx, entry outbox.Entry, changes []Change) (Position, error) {
	args, err := prepareEntry(ctx, tx, entry)
	if err != nil {
		return Position{}, err
	}
	b := &pgx.Batch{}
	b.Queue(takeRevisionSQL, args...)
	for i, c := range changes {
		b.Queue(`INSERT INTO change_log(revision,seq,actor,service_id,entity,entity_id,action,before,after)
SELECT revision,$1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb FROM revision_state WHERE scope='global'`, i+1, c.Actor, c.ServiceID, c.Entity, c.ID, c.Action, jsonArg(c.Before), jsonArg(c.After))
	}
	b.Queue(`SELECT pg_notify('licetum_outbox',revision::text) FROM revision_state WHERE scope='global'`)
	results := tx.SendBatch(ctx, b)
	defer func() { _ = results.Close() }()
	var p Position
	if err := results.QueryRow().Scan(&p.Epoch, &p.Revision); err != nil {
		return Position{}, err
	}
	for range changes {
		if _, err := results.Exec(); err != nil {
			return Position{}, err
		}
	}
	if _, err := results.Exec(); err != nil {
		return Position{}, err
	}
	return p, results.Close()
}

func jsonArg(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
