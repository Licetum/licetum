package main

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// typeDef is one level of the hierarchy. Inherit is the set of levels that flow down the link from
// the parent type to this one; nil means all levels (the NULL of type_resource_parent.inherit).
type typeDef struct {
	Name    string
	ID      uuid.UUID
	Count   int
	Parent  int // index of the parent type, -1 for the root
	Inherit []string
}

// The dataset: 1,000,000 resources in four levels. One link carries a mask, so lineage entries
// with and without a mask both occur.
var types = []typeDef{
	{Name: "division", Count: 20, Parent: -1},
	{Name: "rrs", Count: 2_000, Parent: 0},
	{Name: "branch", Count: 100_000, Parent: 1, Inherit: []string{"read", "write"}},
	{Name: "item", Count: 897_980, Parent: 2},
}

func init() {
	ns := uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8") // the URL namespace of RFC 4122
	for i := range types {
		types[i].ID = uuid.NewSHA1(ns, []byte("licetum-spike-type:"+types[i].Name))
	}
}

// Resource is one resource of the dataset. Parent is an index into Dataset.Res, -1 for a root.
type Resource struct {
	Type   int8
	Parent int32
	Ext    string
	Name   string
	Region string
	Status string
	Tags   []string
}

// Dataset is the generated hierarchy with its child index (CSR: Kids[KidStart[i]:KidStart[i+1]]).
type Dataset struct {
	Res      []Resource
	TypeOff  []int // first index of each type in Res
	KidStart []int32
	Kids     []int32
}

var (
	cities   = []string{"Moscow", "Kazan", "Samara", "Tula", "Omsk", "Perm", "Ufa", "Tver", "Sochi", "Vologda", "Kursk", "Penza", "Tomsk", "Irkutsk", "Chita", "Orel"}
	streets  = []string{"Lenina", "Mira", "Sadovaya", "Gagarina", "Pushkina", "Sovetskaya", "Nabereznaya", "Lesnaya", "Shkolnaya", "Polevaya"}
	adjs     = []string{"red", "blue", "large", "small", "fresh", "frozen", "organic", "classic", "premium", "basic", "light", "dark"}
	nouns    = []string{"shelf", "fridge", "display", "pallet", "rack", "counter", "bay", "stand", "box", "crate", "cooler", "case"}
	tagWords = []string{"seasonal", "promo", "fragile", "heavy", "imported", "local", "bio", "kids", "pets", "home", "garden", "auto", "tools", "office", "sport", "travel", "beauty", "health", "food", "drinks", "bakery", "dairy", "meat", "fish", "frozen", "snacks", "tea", "coffee", "toys", "books"}
	statuses = []string{"active", "active", "active", "active", "active", "active", "active", "active", "closed", "planned"}
)

// Generate builds the dataset deterministically from the seed.
func Generate(seed uint64) *Dataset {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	total := 0
	for _, t := range types {
		total += t.Count
	}
	d := &Dataset{Res: make([]Resource, 0, total)}
	for ti, t := range types {
		d.TypeOff = append(d.TypeOff, len(d.Res))
		for i := range t.Count {
			r := Resource{Type: int8(ti), Parent: -1}
			switch t.Name {
			case "division":
				r.Ext = fmt.Sprintf("div-%02d", i)
				r.Name = "Division " + cities[i%len(cities)] + fmt.Sprintf(" %d", i)
			case "rrs":
				r.Parent = int32(d.TypeOff[0] + i/100)
				r.Ext = fmt.Sprintf("rrs-%04d", i)
				r.Name = fmt.Sprintf("RRS %d %s", i, cities[rng.IntN(len(cities))])
			case "branch":
				r.Parent = int32(d.TypeOff[1] + i/50)
				r.Ext = fmt.Sprintf("br-%06d", i)
				r.Name = fmt.Sprintf("Branch %d %s st. %d", i, streets[rng.IntN(len(streets))], 1+rng.IntN(200))
			case "item":
				r.Parent = int32(d.TypeOff[2] + rng.IntN(types[2].Count))
				var b [16]byte
				for k := range b {
					b[k] = byte(rng.Uint32())
				}
				b[6] = b[6]&0x0f | 0x40
				b[8] = b[8]&0x3f | 0x80
				r.Ext = uuid.UUID(b).String()
				r.Name = fmt.Sprintf("%s %s %d", adjs[rng.IntN(len(adjs))], nouns[rng.IntN(len(nouns))], rng.IntN(100_000))
			}
			r.Region = fmt.Sprintf("region-%02d", rng.IntN(85))
			r.Status = statuses[rng.IntN(len(statuses))]
			nt := 2 + rng.IntN(2)
			for len(r.Tags) < nt {
				w := tagWords[rng.IntN(len(tagWords))]
				if !slices.Contains(r.Tags, w) {
					r.Tags = append(r.Tags, w)
				}
			}
			slices.Sort(r.Tags)
			d.Res = append(d.Res, r)
		}
	}
	d.TypeOff = append(d.TypeOff, len(d.Res))
	// Child index.
	counts := make([]int32, len(d.Res)+1)
	for _, r := range d.Res {
		if r.Parent >= 0 {
			counts[r.Parent+1]++
		}
	}
	for i := 1; i < len(counts); i++ {
		counts[i] += counts[i-1]
	}
	d.KidStart = counts
	d.Kids = make([]int32, counts[len(counts)-1])
	fill := slices.Clone(counts)
	for i, r := range d.Res {
		if r.Parent >= 0 {
			d.Kids[fill[r.Parent]] = int32(i)
			fill[r.Parent]++
		}
	}
	return d
}

// MetaJSON is the canonical JSON of the attributes (keys sorted, no spaces).
func (r *Resource) MetaJSON() string {
	var b strings.Builder
	b.WriteString(`{"region":"`)
	b.WriteString(r.Region)
	b.WriteString(`","status":"`)
	b.WriteString(r.Status)
	b.WriteString(`","tags":[`)
	for i, t := range r.Tags {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(t)
		b.WriteByte('"')
	}
	b.WriteString("]}")
	return b.String()
}

// Attrs lists the attr_v entries of a resource: the name, one per scalar and one per list element.
func (r *Resource) Attrs() [][2]string {
	out := [][2]string{{"name", r.Name}, {"meta.region", r.Region}, {"meta.status", r.Status}}
	for _, t := range r.Tags {
		out = append(out, [2]string{"meta.tags", t})
	}
	return out
}

// LineageEntry is one ancestor of a resource (itself included) with the levels that flow from it;
// Mask nil means all levels.
type LineageEntry struct {
	Idx  int32
	Mask []string
}

// Lineage returns the resource itself and all its ancestors, nearest first.
func (d *Dataset) Lineage(i int32, buf []LineageEntry) []LineageEntry {
	buf = append(buf[:0], LineageEntry{Idx: i})
	var mask []string
	all := true
	for cur := i; d.Res[cur].Parent >= 0; cur = d.Res[cur].Parent {
		if inh := types[d.Res[cur].Type].Inherit; inh != nil {
			if all {
				mask, all = inh, false
			} else {
				mask = intersect(mask, inh)
			}
		}
		buf = append(buf, LineageEntry{Idx: d.Res[cur].Parent, Mask: mask})
	}
	return buf
}

func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		if slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// bucketOf is fnv32a(ext) mod n.
func bucketOf(ext string, n int) int16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(ext))
	return int16(h.Sum32() % uint32(n))
}

// Bucket counts of the region tables.
const (
	typeBuckets = 16
	attrBuckets = 8
)
