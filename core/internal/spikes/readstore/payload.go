package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/klauspost/compress/zstd"
)

// The stream format measured here ("lp-binary"): a sequence of length-prefixed records.
//
//	record = uvarint(len(body)) body
//	body   = str(ext) str(name) u8(active) uvarint(nparents) {uuid16 str(ext)} str(meta)
//	         uvarint(nlineage) {uuid16 str(ext) mask}      -- lineage includes the resource itself
//	mask   = uvarint(0) for all levels | uvarint(n+1) n*str
//	str    = uvarint(len) bytes
//
// unresolved_up is empty in this dataset (every parent is known) and is not encoded.

const chunkLimit = 1 << 20 // 1 MiB of zstd per log_chunk.body

func putStr(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// encodeBinary appends the record of resource i.
func (d *Dataset) encodeBinary(b []byte, i int32, lin []LineageEntry) []byte {
	r := &d.Res[i]
	var body []byte
	body = putStr(body, r.Ext)
	body = putStr(body, r.Name)
	body = append(body, 1)
	if r.Parent >= 0 {
		p := &d.Res[r.Parent]
		body = binary.AppendUvarint(body, 1)
		body = append(body, types[p.Type].ID[:]...)
		body = putStr(body, p.Ext)
	} else {
		body = binary.AppendUvarint(body, 0)
	}
	body = putStr(body, r.MetaJSON())
	body = binary.AppendUvarint(body, uint64(len(lin)))
	for _, e := range lin {
		a := &d.Res[e.Idx]
		body = append(body, types[a.Type].ID[:]...)
		body = putStr(body, a.Ext)
		body = encodeMask(body, e.Mask)
	}
	b = binary.AppendUvarint(b, uint64(len(body)))
	return append(b, body...)
}

func encodeMask(b []byte, m []string) []byte {
	if m == nil {
		return binary.AppendUvarint(b, 0)
	}
	b = binary.AppendUvarint(b, uint64(len(m)+1))
	for _, s := range m {
		b = putStr(b, s)
	}
	return b
}

// jsonRecord is the JSON-lines variant, measured for comparison only.
type jsonRecord struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Active  bool            `json:"active"`
	Parents [][2]string     `json:"parents"`
	Meta    json.RawMessage `json:"meta"`
	Lineage []any           `json:"lineage"`
}

func (d *Dataset) encodeJSON(b []byte, i int32, lin []LineageEntry) []byte {
	r := &d.Res[i]
	rec := jsonRecord{ID: r.Ext, Name: r.Name, Active: true, Meta: json.RawMessage(r.MetaJSON()), Parents: [][2]string{}}
	if r.Parent >= 0 {
		p := &d.Res[r.Parent]
		rec.Parents = append(rec.Parents, [2]string{types[p.Type].ID.String(), p.Ext})
	}
	for _, e := range lin {
		a := &d.Res[e.Idx]
		rec.Lineage = append(rec.Lineage, []any{types[a.Type].ID.String(), a.Ext, e.Mask})
	}
	out, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	b = append(b, out...)
	return append(b, '\n')
}

// Stream is the encoded data:<type> stream of one type, cut into chunks.
type Stream struct {
	Name    string
	Records int
	Raw     int
	Chunks  [][]byte
	Digest  [32]byte // sha256 of the raw stream
	Took    time.Duration
}

func (s *Stream) Compressed() int {
	n := 0
	for _, c := range s.Chunks {
		n += len(c)
	}
	return n
}

// encodeStream builds the stream of type ti in the given format ("binary" or "json") and chunks it.
func (d *Dataset) encodeStream(ti int, format string, enc *zstd.Encoder) *Stream {
	start := time.Now()
	var raw []byte
	offs := []int{0}
	var lin []LineageEntry
	for i := d.TypeOff[ti]; i < d.TypeOff[ti+1]; i++ {
		lin = d.Lineage(int32(i), lin)
		if format == "json" {
			raw = d.encodeJSON(raw, int32(i), lin)
		} else {
			raw = d.encodeBinary(raw, int32(i), lin)
		}
		offs = append(offs, len(raw))
	}
	s := &Stream{Name: "data:" + types[ti].Name, Records: len(offs) - 1, Raw: len(raw), Digest: sha256.Sum256(raw)}
	s.Chunks = chunk(raw, offs, enc)
	s.Took = time.Since(start)
	return s
}

// chunk cuts the stream at record boundaries so that every compressed chunk is at most chunkLimit.
// The raw size of the next chunk is estimated from the ratio of the previous one and shrunk until
// the compressed chunk fits.
func chunk(raw []byte, offs []int, enc *zstd.Encoder) [][]byte {
	var out [][]byte
	ratio := 4.0
	first := 0 // index into offs
	for first < len(offs)-1 {
		target := int(float64(chunkLimit) * ratio * 0.97)
		for {
			last := first + 1
			// the largest record boundary within the target (at least one record)
			lo, hi := first+1, len(offs)-1
			for lo <= hi {
				mid := (lo + hi) / 2
				if offs[mid]-offs[first] <= target {
					last, lo = mid, mid+1
				} else {
					hi = mid - 1
				}
			}
			c := enc.EncodeAll(raw[offs[first]:offs[last]], nil)
			if len(c) <= chunkLimit || last == first+1 {
				ratio = float64(offs[last]-offs[first]) / float64(len(c))
				out = append(out, c)
				first = last
				break
			}
			target = int(float64(target) * float64(chunkLimit) / float64(len(c)) * 0.98)
		}
	}
	return out
}

// derivedCounts are the rows the applier derives from the first synchronization, with partition
// statistics.
type derivedCounts struct {
	Table      string
	Rows       int
	Partitions int
	MaxRows    int
}

func (d *Dataset) derive() []derivedCounts {
	n := len(d.Res)
	res := derivedCounts{Table: "resource_v", Rows: n, Partitions: n, MaxRows: 1}

	// descendant_v: per anchor, its subtree (itself included) by descendant type and bucket.
	desc := derivedCounts{Table: "descendant_v"}
	var stack []int32
	for a := range n {
		var cnt [4][typeBuckets]int
		stack = append(stack[:0], int32(a))
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			r := &d.Res[x]
			cnt[r.Type][bucketOf(r.Ext, typeBuckets)]++
			stack = append(stack, d.Kids[d.KidStart[x]:d.KidStart[x+1]]...)
		}
		for t := range cnt {
			for _, c := range cnt[t] {
				if c > 0 {
					desc.Rows += c
					desc.Partitions++
					desc.MaxRows = max(desc.MaxRows, c)
				}
			}
		}
	}

	tm := derivedCounts{Table: "type_member_v", Rows: n}
	var tmCnt [4][typeBuckets]int
	attr := derivedCounts{Table: "attr_v"}
	type attrKey struct {
		t           int8
		attr, value string
		b           int16
	}
	ac := map[attrKey]int{}
	for i := range d.Res {
		r := &d.Res[i]
		tmCnt[r.Type][bucketOf(r.Ext, typeBuckets)]++
		b := bucketOf(r.Ext, attrBuckets)
		for _, kv := range r.Attrs() {
			ac[attrKey{r.Type, kv[0], kv[1], b}]++
			attr.Rows++
		}
	}
	for t := range tmCnt {
		for _, c := range tmCnt[t] {
			if c > 0 {
				tm.Partitions++
				tm.MaxRows = max(tm.MaxRows, c)
			}
		}
	}
	attr.Partitions = len(ac)
	for _, c := range ac {
		attr.MaxRows = max(attr.MaxRows, c)
	}
	return []derivedCounts{res, desc, tm, attr}
}

// cmdGen reports the payload of the first synchronization and the rows the applier derives.
func cmdGen(seed uint64) error {
	t0 := time.Now()
	d := Generate(seed)
	fmt.Printf("dataset: %d resources generated in %s\n", len(d.Res), time.Since(t0).Round(time.Millisecond))
	for _, level := range []zstd.EncoderLevel{zstd.SpeedDefault, zstd.SpeedBetterCompression} {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
		if err != nil {
			return err
		}
		for _, format := range []string{"binary", "json"} {
			fmt.Printf("\n### payload: format %s, zstd %s, chunks <= %d bytes\n\n", format, level, chunkLimit)
			fmt.Println("| stream | records | raw bytes | zstd bytes | ratio | chunks | avg chunk | zstd B/resource | encode+compress |")
			fmt.Println("|---|---:|---:|---:|---:|---:|---:|---:|---:|")
			var tr, traw, tc, tch int
			var took time.Duration
			for ti := range types {
				s := d.encodeStream(ti, format, enc)
				c := s.Compressed()
				fmt.Printf("| %s | %d | %d | %d | %.2f | %d | %d | %.1f | %s |\n", s.Name, s.Records, s.Raw, c,
					float64(s.Raw)/float64(c), len(s.Chunks), c/len(s.Chunks), float64(c)/float64(s.Records), s.Took.Round(time.Millisecond))
				tr += s.Records
				traw += s.Raw
				tc += c
				tch += len(s.Chunks)
				took += s.Took
			}
			fmt.Printf("| total | %d | %d | %d | %.2f | %d | %d | %.1f | %s |\n", tr, traw, tc, float64(traw)/float64(tc), tch, tc/tch,
				float64(tc)/float64(tr), took.Round(time.Millisecond))
			fmt.Printf("raw bytes per resource: %.1f\n", float64(traw)/float64(tr))
		}
		_ = enc.Close()
	}
	fmt.Print("\n### region rows derived by the applier\n\n")
	fmt.Println("| table | rows | partitions | max rows per partition |")
	fmt.Println("|---|---:|---:|---:|")
	total := 0
	for _, c := range d.derive() {
		fmt.Printf("| %s | %d | %d | %d |\n", c.Table, c.Rows, c.Partitions, c.MaxRows)
		total += c.Rows
	}
	fmt.Printf("| total | %d | | |\n", total)
	return nil
}
