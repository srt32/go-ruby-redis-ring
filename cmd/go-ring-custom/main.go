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

	"github.com/redis/go-redis/v9"
)

type keysPayload struct {
	Keys []string `json:"keys"`
}

type shardConfig struct {
	Name string
	Addr string
}

type node struct {
	name   string
	client *redis.Client
}

type rubyHashRing struct {
	replicas   int
	sortedKeys []uint32
	ring       map[uint32]*node
	nodes      []*node
}

func newRubyHashRing(configs []shardConfig, replicas int) *rubyHashRing {
	r := &rubyHashRing{
		replicas:   replicas,
		ring:       make(map[uint32]*node),
		sortedKeys: make([]uint32, 0, replicas*len(configs)),
		nodes:      make([]*node, 0, len(configs)),
	}

	for _, cfg := range configs {
		n := &node{
			name:   cfg.Name,
			client: redis.NewClient(&redis.Options{Addr: cfg.Addr}),
		}
		r.nodes = append(r.nodes, n)

		for i := 0; i < replicas; i++ {
			virtualKey := fmt.Sprintf("%s:%d", cfg.Name, i)
			hash := serverHashFor(virtualKey)
			r.ring[hash] = n
			r.sortedKeys = append(r.sortedKeys, hash)
		}
	}

	sort.Slice(r.sortedKeys, func(i, j int) bool {
		return r.sortedKeys[i] < r.sortedKeys[j]
	})

	return r
}

func (r *rubyHashRing) getNode(key string) *node {
	if len(r.sortedKeys) == 0 {
		return nil
	}

	hash := crc32.ChecksumIEEE([]byte(key))
	idx := r.binarySearch(hash)
	if idx < 0 {
		idx = len(r.sortedKeys) - 1
	}

	nodeKey := r.sortedKeys[idx]
	return r.ring[nodeKey]
}

func (r *rubyHashRing) binarySearch(value uint32) int {
	lower := 0
	upper := len(r.sortedKeys)

	for lower < upper {
		mid := (lower + upper) / 2
		if r.sortedKeys[mid] > value {
			upper = mid
		} else {
			lower = mid + 1
		}
	}

	return upper - 1
}

func serverHashFor(key string) uint32 {
	sum := md5.Sum([]byte(key))
	return binary.BigEndian.Uint32(sum[:4])
}

type assignment struct {
	Key   string `json:"key"`
	Shard string `json:"shard"`
}

type output struct {
	Meta struct {
		Algorithm string            `json:"algorithm"`
		Shards    map[string]string `json:"shards"`
		Replicas  int               `json:"replicas"`
		HashFor   string            `json:"hash_for"`
		ServerKey string            `json:"server_hash"`
		KeySource string            `json:"key_source"`
	} `json:"meta"`
	Assignments []assignment `json:"assignments"`
}

func main() {
	keysPath := flag.String("keys", "artifacts/keys.json", "Path to JSON document with generated keys")
	outputPath := flag.String("output", "artifacts/go_custom_assignments.json", "Where to write the assignments JSON")
	flag.Parse()

	file, err := os.ReadFile(*keysPath)
	if err != nil {
		panic(fmt.Errorf("failed to read keys file: %w", err))
	}

	var payload keysPayload
	if err := json.Unmarshal(file, &payload); err != nil {
		panic(fmt.Errorf("failed to parse keys payload: %w", err))
	}

	shardDefs := []shardConfig{
		{Name: "cache-a", Addr: "127.0.0.1:6381"},
		{Name: "cache-b", Addr: "127.0.0.1:6382"},
		{Name: "cache-c", Addr: "127.0.0.1:6383"},
	}

	ring := newRubyHashRing(shardDefs, 160)
	defer func() {
		for _, n := range ring.nodes {
			_ = n.client.Close()
		}
	}()

	assignments := make([]assignment, 0, len(payload.Keys))
	for _, key := range payload.Keys {
		node := ring.getNode(key)
		shardName := ""
		if node != nil {
			shardName = node.name
		}
		assignments = append(assignments, assignment{Key: key, Shard: shardName})
	}

	var out output
	out.Meta.Algorithm = "ruby-compatible hash ring"
	out.Meta.Shards = make(map[string]string, len(shardDefs))
	for _, cfg := range shardDefs {
		out.Meta.Shards[cfg.Name] = cfg.Addr
	}
	out.Meta.Replicas = 160
	out.Meta.HashFor = "crc32"
	out.Meta.ServerKey = "md5 upper 32 bits"
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
