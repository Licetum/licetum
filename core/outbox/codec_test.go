package outbox_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/licetum/licetum/core/outbox"
)

func TestContract_PUB05_IndependentChunksAndRawDigest(t *testing.T) {
	rng := rand.New(rand.NewPCG(54, 122))
	var records [][]byte
	var raw []byte
	for range 300 {
		r := make([]byte, rng.IntN(32000))
		for i := range r {
			r[i] = byte(rng.Uint32())
		}
		records = append(records, r)
		raw = binary.AppendUvarint(raw, uint64(len(r)))
		raw = append(raw, r...)
	}
	records = append(records, []byte{}, bytes.Repeat([]byte("compressible"), 150000))
	for _, r := range records[300:] {
		raw = binary.AppendUvarint(raw, uint64(len(r)))
		raw = append(raw, r...)
	}
	got, err := outbox.Encode(records)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != sha256.Sum256(raw) || len(got.Chunks) < 2 {
		t.Fatal("wrong raw digest or no exercised chunk boundary")
	}
	i := 0
	for _, c := range got.Chunks {
		if len(c) > outbox.ChunkLimit {
			t.Fatalf("chunk has %d bytes", len(c))
		}
		if err := outbox.Decode([][]byte{c}, func(r []byte) error {
			if i >= len(records) || !bytes.Equal(r, records[i]) {
				t.Fatalf("record %d differs across an independent chunk boundary", i)
			}
			i++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if i != len(records) {
		t.Fatalf("decoded %d/%d records", i, len(records))
	}
}

func TestContract_PUB05_OversizedRecordAndCorruptionFail(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	large := make([]byte, 2*outbox.ChunkLimit)
	for i := range large {
		large[i] = byte(rng.Uint32())
	}
	if _, err := outbox.Encode([][]byte{large}); err == nil {
		t.Fatal("accepted a record that cannot fit in a chunk")
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	for _, raw := range [][]byte{{0x80}, {3, 1}, binary.AppendUvarint(nil, outbox.MaxRecordSize+1)} {
		if err := outbox.Decode([][]byte{enc.EncodeAll(raw, nil)}, func([]byte) error { return nil }); err == nil {
			t.Fatal("accepted corrupt length or truncated body")
		}
	}
	stop := errors.New("stop")
	got, err := outbox.Encode([][]byte{[]byte("one"), []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Decode(got.Chunks, func([]byte) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("lost callback error")
	}
}
