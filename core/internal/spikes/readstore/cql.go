package main

import (
	"fmt"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// ts is the write timestamp of a position: phase 0 deletes, phase 1 upserts.
func ts(position int64, phase int64) int64 { return position*2 + phase }

// connect opens a session at LOCAL_QUORUM and returns it with the name of the local data center.
func connect(hosts string, keyspace string) (*gocql.Session, string, error) {
	c := gocql.NewCluster(strings.Split(hosts, ",")...)
	c.Consistency = gocql.LocalQuorum
	c.Timeout = 20 * time.Second
	c.ConnectTimeout = 10 * time.Second
	c.NumConns = 2
	c.Keyspace = keyspace
	s, err := c.CreateSession()
	if err != nil {
		return nil, "", err
	}
	var dc string
	if err := s.Query(`SELECT data_center FROM system.local`).Scan(&dc); err != nil {
		s.Close()
		return nil, "", err
	}
	return s, dc, nil
}

// regionKeyspace names the generation keyspace of a region.
func regionKeyspace(dc string, gen int) string {
	return fmt.Sprintf("licetum_r_%s_g%d", strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(dc)), gen)
}

// createSchema creates the log keyspace and one generation keyspace (RF 1 in the local DC).
func createSchema(s *gocql.Session, dc string, gen int) error {
	ks := regionKeyspace(dc, gen)
	stmts := []string{
		fmt.Sprintf(`CREATE KEYSPACE IF NOT EXISTS licetum_log WITH replication = {'class': 'NetworkTopologyStrategy', '%s': 1}`, dc),
		`CREATE TABLE IF NOT EXISTS licetum_log.log_entry (
			epoch int, bucket bigint, position bigint,
			kind text, committed_at timestamp, services set<uuid>,
			streams map<text, frozen<tuple<bigint, int, blob>>>,
			base_position bigint, format smallint,
			PRIMARY KEY ((epoch, bucket), position)
		) WITH default_time_to_live = 1209600`,
		`CREATE TABLE IF NOT EXISTS licetum_log.log_chunk (
			epoch int, position bigint, stream text, n int, body blob,
			PRIMARY KEY ((epoch, position, stream), n)
		) WITH default_time_to_live = 1209600`,
		fmt.Sprintf(`CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'NetworkTopologyStrategy', '%s': 1}`, ks, dc),
	}
	for _, t := range regionTables {
		stmts = append(stmts, fmt.Sprintf(t, ks))
	}
	for _, q := range stmts {
		if err := s.Query(q).Exec(); err != nil {
			return fmt.Errorf("%s: %w", firstLine(q), err)
		}
	}
	return nil
}

// regionTables are the tables of a generation keyspace (%s is the keyspace).
var regionTables = []string{
	`CREATE TABLE IF NOT EXISTS %s.resource_v (
		type_id uuid, ext text, ver bigint,
		active boolean, name text, meta text, lineage blob, unresolved_up blob, committed_at timestamp,
		PRIMARY KEY ((type_id, ext), ver)
	) WITH CLUSTERING ORDER BY (ver DESC) AND compaction = {'class': 'LeveledCompactionStrategy'}`,
	`CREATE TABLE IF NOT EXISTS %s.descendant_v (
		anchor_type uuid, anchor_ext text, desc_type uuid, bucket smallint, desc_ext text, ver bigint,
		mask frozen<set<text>>, name text, gone boolean,
		PRIMARY KEY ((anchor_type, anchor_ext, desc_type, bucket), desc_ext, ver)
	) WITH CLUSTERING ORDER BY (desc_ext ASC, ver DESC)`,
	`CREATE TABLE IF NOT EXISTS %s.type_member_v (
		type_id uuid, bucket smallint, ext text, ver bigint, name text, gone boolean,
		PRIMARY KEY ((type_id, bucket), ext, ver)
	) WITH CLUSTERING ORDER BY (ext ASC, ver DESC)`,
	`CREATE TABLE IF NOT EXISTS %s.attr_v (
		type_id uuid, attr text, value text, bucket smallint, ext text, ver bigint, gone boolean,
		PRIMARY KEY ((type_id, attr, value, bucket), ext, ver)
	) WITH CLUSTERING ORDER BY (ext ASC, ver DESC)`,
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// lineageBlob encodes a lineage the way the stream does: {uuid16 str(ext) mask}.
func (d *Dataset) lineageBlob(lin []LineageEntry) []byte {
	var b []byte
	for _, e := range lin {
		a := &d.Res[e.Idx]
		b = append(b, types[a.Type].ID[:]...)
		b = putStr(b, a.Ext)
		b = encodeMask(b, e.Mask)
	}
	return b
}

// maskValue binds a mask: an absent mask (all levels) is left unset, so the INSERT writes no cell
// tombstone for it.
func maskValue(m []string) any {
	if m == nil {
		return gocql.UnsetValue
	}
	return m
}
