package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The subject directory (migration 00008, ADR 0001, 8.8): the people of a service, sent by the application or an editor. A write
// takes no revision and no change record: no decision reads the directory.

// DirectoryEntry is a person of a service as the directory holds it.
type DirectoryEntry struct {
	Type        string
	ID          string
	DisplayName string
	Properties  map[string]any
	// UpdatedAt is when the content of the entry last changed. SentAt is when the entry was last sent, which sending the same
	// content again moves without touching the entry.
	UpdatedAt time.Time
	SentAt    time.Time
}

// DirectoryKey is the place of an entry in the order of the scans: by type, then by id, bytewise.
type DirectoryKey struct{ Type, ID string }

// DirectoryWrite says what an upsert did: entries that were new, entries whose content changed, and entries that were
// already as sent (nothing was written to them).
type DirectoryWrite struct{ Created, Updated, Unchanged int }

// ErrDirectoryNotFound is returned when the entry does not exist.
var ErrDirectoryNotFound = errors.New("no such directory entry")

// sentRefresh is how stale the time an entry was sent must be before it is written again: retention counts days, and a
// directory that is sent again every few minutes would otherwise rewrite one row (and its index entry) per person each time.
const sentRefresh = "1 hour"

// UpsertDirectory writes the entries of a service in one statement. A row whose content (name and properties) is the same is not
// touched: no new version, no new updated_at. The time of the sending is kept apart, in subject_directory_seen. The caller has
// checked the service and the entries, and sends no entry twice.
func UpsertDirectory(ctx context.Context, pool *pgxpool.Pool, serviceID uuid.UUID, entries []DirectoryEntry) (DirectoryWrite, error) {
	types := make([]string, len(entries))
	ids := make([]string, len(entries))
	names := make([]string, len(entries))
	properties := make([]string, len(entries))
	for i, e := range entries {
		raw, err := json.Marshal(e.Properties)
		if err != nil {
			return DirectoryWrite{}, err
		}
		if e.Properties == nil {
			raw = []byte("{}")
		}
		types[i], ids[i], names[i], properties[i] = e.Type, e.ID, e.DisplayName, string(raw)
	}
	var out DirectoryWrite
	err := pool.QueryRow(ctx, `
		WITH input AS (
			SELECT * FROM unnest($2::text[], $3::text[], $4::text[], $5::text[]) AS t(subject_type, subject_id, display_name, properties)
		), written AS (
			INSERT INTO subject_directory (service_id, subject_type, subject_id, display_name, properties)
			SELECT $1, subject_type, subject_id, display_name, properties::jsonb FROM input
			ON CONFLICT (service_id, subject_type, subject_id) DO UPDATE
			    SET display_name = EXCLUDED.display_name, properties = EXCLUDED.properties, updated_at = now()
			    WHERE subject_directory.display_name IS DISTINCT FROM EXCLUDED.display_name
			       OR subject_directory.properties IS DISTINCT FROM EXCLUDED.properties
			RETURNING (xmax = 0) AS inserted
		), sent AS (
			INSERT INTO subject_directory_seen (service_id, subject_type, subject_id)
			SELECT $1, subject_type, subject_id FROM input
			ON CONFLICT (service_id, subject_type, subject_id) DO UPDATE SET seen_at = now()
			    WHERE subject_directory_seen.seen_at < now() - interval '`+sentRefresh+`'
		)
		SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM written`,
		serviceID, types, ids, names, properties).Scan(&out.Created, &out.Updated)
	if err != nil {
		return DirectoryWrite{}, err
	}
	out.Unchanged = len(entries) - out.Created - out.Updated
	return out, nil
}

const directoryColumns = `d.subject_type, d.subject_id, d.display_name, d.properties, d.updated_at, s.seen_at`

func scanDirectory(row pgx.Row) (DirectoryEntry, error) {
	var e DirectoryEntry
	var raw []byte
	if err := row.Scan(&e.Type, &e.ID, &e.DisplayName, &raw, &e.UpdatedAt, &e.SentAt); err != nil {
		return DirectoryEntry{}, err
	}
	if err := json.Unmarshal(raw, &e.Properties); err != nil {
		return DirectoryEntry{}, err
	}
	if e.Properties == nil {
		e.Properties = map[string]any{}
	}
	return e, nil
}

// GetDirectoryEntry returns one entry of the service, or ErrDirectoryNotFound.
func GetDirectoryEntry(ctx context.Context, q Querier, serviceID uuid.UUID, subjectType, subjectID string) (DirectoryEntry, error) {
	e, err := scanDirectory(q.QueryRow(ctx, `
		SELECT `+directoryColumns+` FROM subject_directory d
		JOIN subject_directory_seen s USING (service_id, subject_type, subject_id)
		WHERE d.service_id = $1 AND d.subject_type = $2 AND d.subject_id = $3`, serviceID, subjectType, subjectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return DirectoryEntry{}, ErrDirectoryNotFound
	}
	return e, err
}

// DeleteDirectoryEntry removes one entry of the service (its sending time goes with it), or is ErrDirectoryNotFound.
func DeleteDirectoryEntry(ctx context.Context, q Querier, serviceID uuid.UUID, subjectType, subjectID string) error {
	tag, err := q.Exec(ctx, `DELETE FROM subject_directory WHERE service_id = $1 AND subject_type = $2 AND subject_id = $3`,
		serviceID, subjectType, subjectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDirectoryNotFound
	}
	return nil
}

// DirectoryAfter reads up to limit entries of the service of the given type (empty: any), in the order of the scans (type, then
// id, bytewise), after the key (nil: from the first). The order does not move when an entry is sent again, so a cursor over
// it neither skips nor repeats anybody.
func DirectoryAfter(ctx context.Context, q Querier, serviceID uuid.UUID, subjectType string, after *DirectoryKey, limit int) ([]DirectoryEntry, error) {
	// Two statements, not one with "$3 IS FALSE OR ...": the range condition then stays an index condition in a generic plan too,
	// and a deep page does not scan the directory from its first entry.
	const head = `
		SELECT ` + directoryColumns + ` FROM subject_directory d
		JOIN subject_directory_seen s USING (service_id, subject_type, subject_id)
		WHERE d.service_id = $1 AND ($2 = '' OR d.subject_type = $2)`
	const order = ` ORDER BY d.subject_type, d.subject_id COLLATE "C"`
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = q.Query(ctx, head+order+` LIMIT $3`, serviceID, subjectType, limit)
	} else {
		rows, err = q.Query(ctx, head+` AND (d.subject_type, d.subject_id COLLATE "C") > ($3::text, $4::text COLLATE "C")`+order+` LIMIT $5`,
			serviceID, subjectType, after.Type, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectoryEntry{}
	for rows.Next() {
		e, err := scanDirectory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DirectoryPage is a page of the list for the interface: the entries that match, in the order of the scans, and how many match.
type DirectoryPage struct {
	Total   int
	Entries []DirectoryEntry
}

// SearchDirectory pages the entries of the service whose id or display name contains the text (case-insensitive), of the given
// type (empty: any).
func SearchDirectory(ctx context.Context, q Querier, serviceID uuid.UUID, subjectType, search string, limit, offset int) (DirectoryPage, error) {
	pattern := "%" + escapeLike(search) + "%"
	const where = `WHERE d.service_id = $1 AND ($2 = '' OR d.subject_type = $2)
		AND (d.subject_id ILIKE $3 ESCAPE '\' OR d.display_name ILIKE $3 ESCAPE '\')`
	var page DirectoryPage
	if err := q.QueryRow(ctx, `SELECT count(*) FROM subject_directory d `+where, serviceID, subjectType, pattern).Scan(&page.Total); err != nil {
		return DirectoryPage{}, err
	}
	rows, err := q.Query(ctx, `
		SELECT `+directoryColumns+` FROM subject_directory d
		JOIN subject_directory_seen s USING (service_id, subject_type, subject_id) `+where+`
		ORDER BY d.subject_type, d.subject_id COLLATE "C" LIMIT $4 OFFSET $5`, serviceID, subjectType, pattern, limit, offset)
	if err != nil {
		return DirectoryPage{}, err
	}
	defer rows.Close()
	page.Entries = []DirectoryEntry{}
	for rows.Next() {
		e, err := scanDirectory(rows)
		if err != nil {
			return DirectoryPage{}, err
		}
		page.Entries = append(page.Entries, e)
	}
	return page, rows.Err()
}

func escapeLike(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '%', '_':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// DirectorySettings are the settings of the directory of a service.
type DirectorySettings struct {
	// RetentionDays is how many days an entry that is not sent again is kept; nil: until it is deleted.
	RetentionDays *int
}

// GetDirectorySettings returns the settings of the service (the defaults when none were written).
func GetDirectorySettings(ctx context.Context, q Querier, serviceID uuid.UUID) (DirectorySettings, error) {
	var s DirectorySettings
	err := q.QueryRow(ctx, `SELECT retention_days FROM subject_directory_settings WHERE service_id = $1`, serviceID).Scan(&s.RetentionDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return DirectorySettings{}, nil
	}
	return s, err
}

// PutDirectorySettings replaces the settings of the service.
func PutDirectorySettings(ctx context.Context, q Querier, serviceID uuid.UUID, s DirectorySettings) error {
	_, err := q.Exec(ctx, `
		INSERT INTO subject_directory_settings (service_id, retention_days) VALUES ($1, $2)
		ON CONFLICT (service_id) DO UPDATE SET retention_days = EXCLUDED.retention_days`, serviceID, s.RetentionDays)
	return err
}

// PruneDirectory deletes up to limit entries that were not sent within the retention period of their service (a service with
// no retention period keeps everything) and returns how many. The caller repeats it while it deletes a full chunk, so that no
// statement holds its locks for long. Several workers may run it at once: a chunk takes the rows of its entries (the entry and
// the record of its sending, in that order) with FOR UPDATE SKIP LOCKED, so each entry is deleted by one of them and none waits
// for another. The order is the one of UpsertDirectory, which locks the entry before the record of its sending: taking them the
// other way round would let a retention chunk and a sending of the same entry wait for each other's row.
func PruneDirectory(ctx context.Context, q Querier, limit int) (int64, error) {
	tag, err := q.Exec(ctx, `
		WITH doomed AS (
			SELECT d.service_id, d.subject_type, d.subject_id
			FROM subject_directory_settings t
			JOIN subject_directory d ON d.service_id = t.service_id
			JOIN subject_directory_seen s ON s.service_id = d.service_id AND s.subject_type = d.subject_type AND s.subject_id = d.subject_id
			WHERE t.retention_days IS NOT NULL AND s.seen_at < now() - make_interval(days => t.retention_days)
			LIMIT $1
			FOR UPDATE OF d, s SKIP LOCKED)
		DELETE FROM subject_directory d USING doomed
		WHERE d.service_id = doomed.service_id AND d.subject_type = doomed.subject_type AND d.subject_id = doomed.subject_id`, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
