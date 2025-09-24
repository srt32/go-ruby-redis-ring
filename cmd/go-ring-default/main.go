package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/cespare/xxhash/v2"
	"github.com/dgryski/go-rendezvous"
)

type keysPayload struct {
	Keys []string `json:"keys"`
}

type assignment struct {
	Key   string `json:"key"`
	Shard string `json:"shard"`
}

type output struct {
	Meta struct {
		Algorithm string   `json:"algorithm"`
		Shards    []string `json:"shards"`
		Details   string   `json:"details"`
		KeySource string   `json:"key_source"`
	} `json:"meta"`
	Assignments []assignment `json:"assignments"`
}

func main() {
	keysPath := flag.String("keys", "artifacts/keys.json", "Path to JSON document with generated keys")
	outputPath := flag.String("output", "artifacts/go_default_assignments.json", "Where to write the assignments JSON")
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
	rendezvousHash := rendezvous.New(shards, xxhash.Sum64String)

	assignments := make([]assignment, 0, len(payload.Keys))
	for _, key := range payload.Keys {
		shard := rendezvousHash.Lookup(key)
		assignments = append(assignments, assignment{Key: key, Shard: shard})
	}

	var out output
	out.Meta.Algorithm = "go-redis rendezvous hashing"
	out.Meta.Shards = shards
	out.Meta.Details = "github.com/dgryski/go-rendezvous using xxhash64"
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
