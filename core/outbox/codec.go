// Package outbox defines the canonical change streams and their transport encoding.
package outbox

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

const (
	// ChunkLimit bounds a compressed body, rather than the uncompressed records.
	ChunkLimit = 1 << 20
	// MaxRecordSize bounds decoder allocation for an untrusted length prefix.
	MaxRecordSize = 64 << 20
)

// Encoded is one stream. Digest covers the raw, uvarint-length-prefixed records.
type Encoded struct {
	Chunks [][]byte
	Digest [sha256.Size]byte
}

// Encode encodes opaque binary record bodies using ADR 0002's measured format.
// Each chunk is an independent zstd frame and ends on a record boundary.
func Encode(records [][]byte) (Encoded, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return Encoded{}, err
	}
	defer enc.Close()
	out := Encoded{}
	digest := sha256.New()
	var raw []byte
	var offsets []int
	flush := func() error {
		var split func([]byte, []int) error
		split = func(data []byte, ends []int) error {
			compressed := enc.EncodeAll(data, nil)
			if len(compressed) <= ChunkLimit {
				out.Chunks = append(out.Chunks, compressed)
				return nil
			}
			if len(ends) == 1 {
				return fmt.Errorf("outbox record exceeds the %d-byte compressed chunk limit", ChunkLimit)
			}
			mid := len(ends) / 2
			cut := ends[mid-1]
			if err := split(data[:cut], ends[:mid]); err != nil {
				return err
			}
			right := make([]int, len(ends)-mid)
			for i, end := range ends[mid:] {
				right[i] = end - cut
			}
			return split(data[cut:], right)
		}
		if len(offsets) == 0 {
			return nil
		}
		err := split(raw, offsets)
		raw, offsets = nil, nil
		return err
	}
	for _, record := range records {
		if len(record) > MaxRecordSize {
			return Encoded{}, fmt.Errorf("outbox record exceeds %d raw bytes", MaxRecordSize)
		}
		if len(raw) > 0 && len(raw)+len(record)+binary.MaxVarintLen64 > 4*ChunkLimit {
			if err := flush(); err != nil {
				return Encoded{}, err
			}
		}
		start := len(raw)
		raw = binary.AppendUvarint(raw, uint64(len(record)))
		raw = append(raw, record...)
		offsets = append(offsets, len(raw))
		_, _ = digest.Write(raw[start:])
	}
	if err := flush(); err != nil {
		return Encoded{}, err
	}
	copy(out.Digest[:], digest.Sum(nil))
	return out, nil
}

// Decode streams records to visit, allocating at most one record at a time.
// A callback error stops decoding immediately. The callback owns its byte slice.
func Decode(chunks [][]byte, visit func([]byte) error) error {
	for _, chunk := range chunks {
		if len(chunk) > ChunkLimit {
			return fmt.Errorf("outbox chunk exceeds %d bytes", ChunkLimit)
		}
		dec, err := zstd.NewReader(bytes.NewReader(chunk), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(128<<20))
		if err != nil {
			return err
		}
		err = decodeRecords(bufio.NewReader(dec), visit)
		dec.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func decodeRecords(r *bufio.Reader, visit func([]byte) error) error {
	for {
		n, err := binary.ReadUvarint(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("outbox record length: %w", err)
		}
		if n > MaxRecordSize {
			return fmt.Errorf("outbox record length %d exceeds %d", n, MaxRecordSize)
		}
		body := make([]byte, int(n))
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("outbox record body: %w", err)
		}
		if err := visit(body); err != nil {
			return err
		}
	}
}
