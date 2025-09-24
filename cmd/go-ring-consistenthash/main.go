package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
	"strings"
)

type keysPayload struct {
	Keys []string `json:"keys"`
}

type rubyStyleHash struct {
	replicas   int
	sortedKeys []uint32
	ring       map[uint32]string
}

func newRubyStyleHash(shards []string, replicas int) *rubyStyleHash {
	h := &rubyStyleHash{
		replicas:   replicas,
		sortedKeys: make([]uint32, 0, replicas*len(shards)),
		ring:       make(map[uint32]string, replicas*len(shards)),
	}

	for _, shard := range shards {
		for i := 0; i < replicas; i++ {
			virtualKey := fmt.Sprintf("%s:%d", shard, i)
			hash := serverHashFor(virtualKey)
			h.ring[hash] = shard
			h.sortedKeys = append(h.sortedKeys, hash)
		}
	}

	sort.Slice(h.sortedKeys, func(i, j int) bool {
		return h.sortedKeys[i] < h.sortedKeys[j]
	})

	return h
}

func (h *rubyStyleHash) Get(key string) string {
	if len(h.sortedKeys) == 0 {
		return ""
	}

	hash := crc32.ChecksumIEEE([]byte(key))
	idx := sort.Search(len(h.sortedKeys), func(i int) bool {
		return h.sortedKeys[i] >= hash
	})

	if idx == len(h.sortedKeys) {
		idx = 0
	}

	return h.ring[h.sortedKeys[idx]]
}

func serverHashFor(key string) uint32 {
	sum := md5.Sum([]byte(key))
	return binary.BigEndian.Uint32(sum[:4])
}

func normalizeForGoRedis(key string) string {
	start := strings.IndexByte(key, '{')
	if start == -1 {
		return key
	}

	if end := strings.IndexByte(key[start+1:], '}'); end > 0 {
		return key[start+1 : start+end+1]
	}

	return key
}

type assignment struct {
	Key   string `json:"key"`
	Shard string `json:"shard"`
}

type output struct {
	Meta struct {
		Algorithm string   `json:"algorithm"`
		Shards    []string `json:"shards"`
		Replicas  int      `json:"replicas"`
		Notes     string   `json:"notes"`
		KeySource string   `json:"key_source"`
	} `json:"meta"`
	Assignments []assignment `json:"assignments"`
}

func main() {
	keysPath := flag.String("keys", "artifacts/keys.json", "Path to JSON document with generated keys")
	outputPath := flag.String("output", "artifacts/go_consistent_assignments.json", "Where to write the assignments JSON")
	flag.Parse()

	file, err := os.ReadFile(*keysPath)
	if err != nil {
		panic(fmt.Errorf("failed to read keys file: %w", err))
	}

	var payload keysPayload
	if err := json.Unmarshal(file, &payload); err != nil {
		panic(fmt.Errorf("failed to parse keys payload: %w", err))
	}

	shards := []string{"cache-a", "cache-b", "cache-c"}
	hash := newRubyStyleHash(shards, 160)

	assignments := make([]assignment, 0, len(payload.Keys))
	for _, key := range payload.Keys {
		normalized := normalizeForGoRedis(key)
		shard := hash.Get(normalized)
		assignments = append(assignments, assignment{Key: key, Shard: shard})
	}

	var out output
	out.Meta.Algorithm = "go-redis Ring with custom ConsistentHash"
	out.Meta.Shards = shards
	out.Meta.Replicas = 160
	out.Meta.Notes = "hash.Get receives hashtag-normalized keys before hashing"
	out.Meta.KeySource = *keysPath
	out.Assignments = assignments

	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		panic(fmt.Errorf("failed to encode output: %w", err))
	}

	if err := os.WriteFile(*outputPath, encoded, 0o644); err != nil {
		panic(fmt.Errorf("failed to write output: %w", err))
	}
}
