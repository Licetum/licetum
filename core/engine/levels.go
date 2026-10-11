package engine

import (
	"slices"
	"strings"
)

// LevelSet is a set of access levels; all means every level (a NULL inherit mask in the DB). Values
// are immutable: every operation returns a new set.
type LevelSet struct {
	All bool
	Set map[string]struct{}
}

var AllLevels = LevelSet{All: true}

// LevelsOf builds a set of the given levels.
func LevelsOf(levels ...string) LevelSet {
	s := LevelSet{Set: make(map[string]struct{}, len(levels))}
	for _, l := range levels {
		s.Set[l] = struct{}{}
	}
	return s
}

// levelsFromDB reads an inherit mask: NULL (nil) means all levels.
func levelsFromDB(mask []string) LevelSet {
	if mask == nil {
		return AllLevels
	}
	return LevelsOf(mask...)
}

func (s LevelSet) has(level string) bool {
	if s.All {
		return true
	}
	_, ok := s.Set[level]
	return ok
}

func (s LevelSet) empty() bool { return !s.All && len(s.Set) == 0 }

func (s LevelSet) Sorted() []string {
	out := make([]string, 0, len(s.Set))
	for l := range s.Set {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

func (s LevelSet) Intersect(o LevelSet) LevelSet {
	switch {
	case s.All:
		return o
	case o.All:
		return s
	}
	out := LevelSet{Set: map[string]struct{}{}}
	for l := range s.Set {
		if _, ok := o.Set[l]; ok {
			out.Set[l] = struct{}{}
		}
	}
	return out
}

func (s LevelSet) union(o LevelSet) LevelSet {
	if s.All || o.All {
		return AllLevels
	}
	out := LevelSet{Set: make(map[string]struct{}, len(s.Set)+len(o.Set))}
	for l := range s.Set {
		out.Set[l] = struct{}{}
	}
	for l := range o.Set {
		out.Set[l] = struct{}{}
	}
	return out
}

// SubsetOf reports whether every level of s is in o.
func (s LevelSet) SubsetOf(o LevelSet) bool {
	if o.All {
		return true
	}
	if s.All {
		return false
	}
	for l := range s.Set {
		if _, ok := o.Set[l]; !ok {
			return false
		}
	}
	return true
}

// key is a canonical string of the set, usable in map keys and hashes.
func (s LevelSet) key() string {
	if s.All {
		return "*"
	}
	return "{" + strings.Join(s.Sorted(), ",") + "}"
}
