package refeval

import (
	"maps"
	"slices"
)

// levelSet is a set of access levels. all stands for every level: the inherit of a type link that is null, the mask of a
// resource onto itself, and the wildcard of a denial. Values are never changed once built, so they can be shared.
type levelSet struct {
	all bool
	set map[string]bool
}

// everyLevel is the set of all levels.
var everyLevel = levelSet{all: true}

// noLevel is the empty set.
var noLevel = levelSet{}

// levelsOf returns the set of the given levels.
func levelsOf(levels ...string) levelSet {
	set := make(map[string]bool, len(levels))
	for _, l := range levels {
		set[l] = true
	}
	return levelSet{set: set}
}

// inheritMask turns the inherit list of a type link into a set: nil lets every level through, an empty list none.
func inheritMask(inherit []string) levelSet {
	if inherit == nil {
		return everyLevel
	}
	return levelsOf(inherit...)
}

// has reports whether the set holds a level.
func (l levelSet) has(level string) bool { return l.all || l.set[level] }

// empty reports whether nothing is in the set.
func (l levelSet) empty() bool { return !l.all && len(l.set) == 0 }

// and is the intersection of two sets.
func (l levelSet) and(o levelSet) levelSet {
	switch {
	case l.all:
		return o
	case o.all:
		return l
	}
	out := map[string]bool{}
	for level := range l.set {
		if o.set[level] {
			out[level] = true
		}
	}
	return levelSet{set: out}
}

// or is the union of two sets.
func (l levelSet) or(o levelSet) levelSet {
	if l.all || o.all {
		return everyLevel
	}
	out := maps.Clone(l.set)
	if out == nil {
		out = map[string]bool{}
	}
	for level := range o.set {
		out[level] = true
	}
	return levelSet{set: out}
}

// sorted lists the levels of a set that is not all.
func (l levelSet) sorted() []string {
	return slices.Sorted(maps.Keys(l.set))
}
