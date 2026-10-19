#!/bin/sh
# du.sh <data dir> <keyspace prefix>: per table directory, the bytes of all its files (snapshots and backups
# excluded) and the number of Data.db files: "<keyspace> <table> <bytes> <data files>"
for ks in "$1"/$2*; do
  for t in "$ks"/*; do
    [ -d "$t" ] || continue
    b=$(find "$t" -maxdepth 1 -type f -printf '%s\n' | awk '{s+=$1} END {print s+0}')
    n=$(find "$t" -maxdepth 1 -type f -name '*Data.db' | wc -l)
    printf '%s %s %d %d\n' "$(basename "$ks")" "$(basename "$t" | cut -d- -f1)" "$b" "$n"
  done
done
