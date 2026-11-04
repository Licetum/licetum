#!/bin/sh
# mvcc-stages.sh <container> <host> <data dir> <out> <phase> [-bypass-cache]
#   phase a: after `readstore mvcc -stage write`: memtables, a late re-insert, flush, major compaction (default gc_grace)
#   phase b: then gc_grace_seconds 0, major compaction, and a late re-insert after the purge
#   phase c: writes again, flushing every 100 versions with autocompaction off, then the reads from sstables
#            (pass -bypass-cache on ScyllaDB, so that its row cache does not answer)
# READSTORE is the binary (default /var/tmp/p05spike/readstore).
set -e
C=$1; H=$2; D=$3; O=$4
DIR=$(dirname "$0")
B="${READSTORE:-/var/tmp/p05spike/readstore} mvcc -hosts $H"
if [ "$5" = c ]; then
  $B -stage write -flush-every 100 -container "$C" >> "$O" 2>&1
  docker exec "$C" nodetool flush licetum_mvcc
  "$DIR"/du.sh "$D" licetum_mvcc >> "$O"
  $B -stage measure $6 -label "sstables, flushed every 100 versions" >> "$O" 2>&1
  $B -stage reinsert >> "$O" 2>&1
  docker exec "$C" nodetool flush licetum_mvcc
  $B -stage measure $6 -label "sstables, after a late re-insert" >> "$O" 2>&1
  docker exec "$C" nodetool compact licetum_mvcc
  "$DIR"/du.sh "$D" licetum_mvcc >> "$O"
  $B -stage measure $6 -label "sstables, major compaction, gc_grace_seconds 864000" >> "$O" 2>&1
  for t in rv_k1 rv_k10; do
    docker exec "$C" cqlsh -e "ALTER TABLE licetum_mvcc.$t WITH gc_grace_seconds = 0"
  done
  sleep 2
  docker exec "$C" nodetool compact licetum_mvcc
  "$DIR"/du.sh "$D" licetum_mvcc >> "$O"
  $B -stage measure $6 -label "sstables, major compaction, gc_grace_seconds 0" >> "$O" 2>&1
elif [ "$5" = a ]; then
  $B -stage measure -label memtable >> "$O" 2>&1
  $B -stage reinsert >> "$O" 2>&1
  $B -stage measure -label "memtable, after a late re-insert" >> "$O" 2>&1
  docker exec "$C" nodetool flush licetum_mvcc
  "$DIR"/du.sh "$D" licetum_mvcc >> "$O"
  $B -stage measure -label "flushed" >> "$O" 2>&1
  docker exec "$C" nodetool compact licetum_mvcc
  "$DIR"/du.sh "$D" licetum_mvcc >> "$O"
  $B -stage measure -label "major compaction, gc_grace_seconds 864000" >> "$O" 2>&1
else
  for t in rv_k1 rv_k10; do
    docker exec "$C" cqlsh -e "ALTER TABLE licetum_mvcc.$t WITH gc_grace_seconds = 0"
  done
  $B -stage describe >> "$O" 2>&1
  sleep 2
  docker exec "$C" nodetool compact licetum_mvcc
  "$DIR"/du.sh "$D" licetum_mvcc >> "$O"
  $B -stage measure -label "major compaction, gc_grace_seconds 0" >> "$O" 2>&1
  $B -stage reinsert >> "$O" 2>&1
  $B -stage measure -label "late re-insert after the purge (memtable)" >> "$O" 2>&1
  docker exec "$C" nodetool flush licetum_mvcc
  docker exec "$C" nodetool compact licetum_mvcc
  $B -stage measure -label "late re-insert after the purge, compacted" >> "$O" 2>&1
fi
