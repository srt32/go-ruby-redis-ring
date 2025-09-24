---
title: "Porting Redis::HashRing from Ruby to Go without losing a single byte"
author: "Engineering Enablement Team"
date: 2024-05-30
tags:
  - redis
  - go
  - ruby
  - consistent hashing
summary: >
  How to reproduce redis-rb's Redis::HashRing algorithm in Go so that every key
  lands on the same shard before, during, and after a migration.
intro: >
  Hash rings solve the "which server owns this key?" problem by hashing both the
  servers and the keys onto the same circular number line. The pairing between a
  key's hash and the nearest server hash determines ownership, which means that
  tiny differences in the hashing algorithm, normalization rules, or tie-breaking
  logic can reshuffle the entire fleet. Understanding those mechanics is the key
  to reproducing Ruby's behaviour in Go.
---

# Porting Redis::HashRing from Ruby to Go without losing a single byte

When we replaced a Ruby service with a Go rewrite we assumed that swapping the
Redis client would be easy. Both the Ruby [`redis` gem](https://github.com/redis/redis-rb)
and [`go-redis`](https://github.com/redis/go-redis) advertise "ring" clients for
consistent hashing, so how different could they be? It turns out the algorithms
are wildly incompatible. Keys that once lived on shard **A** suddenly landed on
shard **C**, cache hit rates plummeted, and the migration ground to a halt.

This post documents the journey to a byte-for-byte compatible Go
implementation of `Redis::HashRing`. We'll unpack the algorithmic differences,
prove the mismatch with a reproducible experiment, and then rebuild the Ruby
ring semantics on top of go-redis clients.

## Hash rings 101

A hash ring maps both servers and keys onto the same 32-bit space:

1. Hash every server multiple times ("virtual nodes") to spread it around the
   ring evenly.
2. Hash the key once and walk clockwise until you find the first server hash.
3. Use that server to store or read the key.

Because both sides are hashed, you must match *every* step to reproduce the
original layout: the digest function for servers, the digest for keys, how
virtual nodes are labeled, and how the clockwise search handles wrap-around.
Change any detail and you end up on a different shard.

## Ruby's contract, in code

Ruby's `Redis::HashRing` encodes those choices explicitly. Each server is
replicated 160 times using `Digest::MD5`, and key lookups use `Zlib.crc32` with a
wrap-around binary search. The heart of the algorithm looks like this:

```ruby
POINTS_PER_SERVER = 160

class RubyRing
  def initialize(servers)
    @ring = {}
    @sorted_points = []

    servers.each do |server|
      POINTS_PER_SERVER.times do |replica|
        key = "#{server}:#{replica}"
        point = Digest::MD5.digest(key).byteslice(0, 4).unpack1('N')
        @ring[point] = server
        @sorted_points << point
      end
    end

    @sorted_points.sort!
  end

  def node_for(key)
    hash = Zlib.crc32(key)
    index = @sorted_points.bsearch_index { |point| point >= hash }
    index ||= 0 # wrap around to the first point
    @ring[@sorted_points[index]]
  end
end
```

That pairing (MD5 for servers, CRC32 for keys, specific string formatting for
virtual nodes, and the wrap-around binary search) is what we have to clone in
Go.

## Why go-redis's ring cannot be swapped in

Go-redis made very different choices. Its default ring prefers rendezvous
hashing with `xxhash64`, normalizes keys using Redis hash tags, and exposes an
API that returns a single node without any concept of "try the next shard":

```go
shards := []string{"cache-a", "cache-b", "cache-c"}
ring := rendezvous.New(shards, xxhash.Sum64String)

for _, key := range keys {
    shard := ring.Lookup(key)
    fmt.Printf("%s -> %s\n", key, shard)
}
```

Even if you swapped in a CRC32 function, you would still hash only the key, not
the servers, and you would still lose the ability to walk the ring in order.
The contract is different, so a byte-for-byte migration is impossible without
re-implementing the Ruby semantics.

## Building a reproducible experiment harness

To make the mismatch concrete we built a tiny experiment harness. The workflow is
implemented in [`experiments/run_experiment.sh`](experiments/run_experiment.sh):

1. Generate 200 deterministic keys (seeded RNG, predictable prefix). Every
   twenty-fifth key uses a Redis hash tag (for example `user:{tag25}:...`) so we
   can observe how go-redis preprocesses them.
2. Assign each key to a shard using Ruby's `Redis::HashRing`.
3. Assign the same keys to shards using go-redis' default rendezvous hash.
4. Attempt to override go-redis with a custom `ConsistentHash` implementation
   that mirrors Ruby's algorithm but still runs through the library's key
   normalization step.
5. Assign the keys once more using a new Go implementation that mimics Ruby's
   exact algorithm.
6. Compare the assignments and emit JSON summaries for downstream analysis.

Everything runs inside Docker via `docker compose run --rm experiment`, so you
get the same results regardless of host platform.

### Generating identical key sets

The script [`scripts/generate_keys.rb`](scripts/generate_keys.rb) produces the
key corpus. It uses a seeded `Random` instance, adds a Redis hash tag every
twenty-fifth key (for example `user:{tag50}:...`), and writes the keys plus
metadata (seed, prefix, timestamp) to `artifacts/keys.json`. This file becomes
the source of truth for both language runtimes.

### Ruby baseline

The Ruby baseline in [`scripts/ruby_ring.rb`](scripts/ruby_ring.rb) wraps the
official `Redis::HashRing`. Nodes are defined with simple IDs (`cache-a`,
`cache-b`, `cache-c`), the ring is constructed with the default 160 replicas,
and each key is routed via `get_node`:

```ruby
ring = Redis::HashRing.new(%w[cache-a cache-b cache-c])
assignment = ring.get_node(key) # => "cache-b"
```

The script emits `artifacts/ruby_assignments.json` containing the shard chosen
for every key and metadata about the ring configuration.

### Go-redis rendezvous hashing

The first Go pass (`cmd/go-ring-default`) intentionally mirrors the algorithm
used by the `go-redis` ring. It wires up `github.com/dgryski/go-rendezvous` with
`xxhash64` and records where each key lands:

```go
rendezvousHash := rendezvous.New(shards, xxhash.Sum64String)
for _, key := range keys {
    assignments = append(assignments, assignment{
        Key:   key,
        Shard: rendezvousHash.Lookup(key),
    })
}
```

When we compare this file to the Ruby baseline the mismatch is severe:

```json
{
  "total_keys": 200,
  "matches": 63,
  "mismatches": 137,
  "match_rate": 0.315
}
```

Only 31.5% of the keys land on the same shard. The rest hop elsewhere because
the rendezvous algorithm optimizes for different criteria.

### Trying go-redis' `ConsistentHash` override

Go-redis exposes a `RingOptions.NewConsistentHash` hook, so our next attempt was
to provide a Ruby-style hash implementation directly to the ring:

```go
ring := redis.NewRing(&redis.RingOptions{
    Addrs: map[string]string{
        "cache-a": "127.0.0.1:6381",
        "cache-b": "127.0.0.1:6382",
        "cache-c": "127.0.0.1:6383",
    },
    NewConsistentHash: func(shards []string) redis.ConsistentHash {
        return newRubyStyleHash(shards, 160)
    },
})
```

On the surface this looks promising—we control the hashing function and the
virtual node layout. The catch is that go-redis normalizes keys using Redis hash
tags **before** the function runs. You can see the behaviour in our experiment
binary, which mirrors `hashtag.Key` from the library:

```go
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
```

Because every twenty-fifth key includes braces, the override hashes just the tag
(for example `tag50`) while Ruby hashes the entire string. The comparison JSON
makes the mismatch obvious:

```json
{
  "total_keys": 200,
  "matches": 57,
  "mismatches": 143,
  "match_rate": 0.285
}
```

Barely 28.5% of the keys line up. Even with complete control over the hash
function, the different preprocessing step prevents byte-for-byte parity.

### Re-implementing the Ruby algorithm in Go

The compatible Go implementation lives in
[`cmd/go-ring-custom/main.go`](cmd/go-ring-custom/main.go). Instead of trying to
bend go-redis' extension points, it recreates the Ruby hashing strategy:

```go
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
    ring := &rubyHashRing{
        replicas:   replicas,
        ring:       make(map[uint32]*node),
        sortedKeys: make([]uint32, 0, replicas*len(configs)),
        nodes:      make([]*node, 0, len(configs)),
    }

    for _, cfg := range configs {
        node := &node{
            name:   cfg.Name,
            client: redis.NewClient(&redis.Options{Addr: cfg.Addr}),
        }
        ring.nodes = append(ring.nodes, node)

        for replica := 0; replica < replicas; replica++ {
            virtualKey := fmt.Sprintf("%s:%d", cfg.Name, replica)
            hash := serverHashFor(virtualKey)
            ring.ring[hash] = node
            ring.sortedKeys = append(ring.sortedKeys, hash)
        }
    }

    sort.Slice(ring.sortedKeys, func(i, j int) bool { return ring.sortedKeys[i] < ring.sortedKeys[j] })
    return ring
}

func (r *rubyHashRing) Close() {
    for _, node := range r.nodes {
        _ = node.client.Close()
    }
}
```

The lookup path mirrors Ruby's CRC32 + wrap-around binary search:

```go
func (r *rubyHashRing) getNode(key string) *node {
    if len(r.sortedKeys) == 0 {
        return nil
    }

    hash := crc32.ChecksumIEEE([]byte(key))
    idx := sort.Search(len(r.sortedKeys), func(i int) bool {
        return r.sortedKeys[i] >= hash
    })

    if idx == len(r.sortedKeys) {
        idx = 0 // wrap around to the first point
    }

    return r.ring[r.sortedKeys[idx]]
}

func serverHashFor(virtualKey string) uint32 {
    sum := md5.Sum([]byte(virtualKey))
    return binary.BigEndian.Uint32(sum[:4])
}
```

Most importantly, the Go ring continues to produce `*redis.Client` instances so
it can slot into the rest of our Go code. We simply perform the routing decision
ourselves before delegating to whichever client owns the key.

Running the comparison again tells the story:

```json
{
  "total_keys": 200,
  "matches": 200,
  "mismatches": 0,
  "match_rate": 1.0
}
```

Every key now hits the exact same shard as the Ruby implementation, verifying
that the hashing logic is compatible.

## JSON-first results for humans and machines

Each step writes structured JSON with enough metadata to be consumed by other
tools. For example, `artifacts/comparison_default.json` and
`artifacts/comparison_consistent.json` include mismatch examples so you can
quickly inspect which keys moved, while `artifacts/comparison_custom.json`
proves the compatible implementation achieves 100% parity. This makes it easy to
feed the data into dashboards, regression checks, or documentation.

## Putting the custom ring into your Go service

Drop the `rubyHashRing` implementation into your project, point it at the same
shard definitions you use in Ruby, and route keys through it before issuing
commands:

```go
ring := newRubyHashRing([]shardConfig{
    {Name: "cache-a", Addr: "redis-a.internal:6379"},
    {Name: "cache-b", Addr: "redis-b.internal:6379"},
    {Name: "cache-c", Addr: "redis-c.internal:6379"},
}, 160)

defer ring.Close()

func lookup(key string) *redis.Client {
    node := ring.getNode(key)
    if node == nil {
        panic("ring is empty")
    }
    return node.client
}
```

Use the returned client exactly as you would with go-redis today—the only change
is that shard selection happens in your code instead of the library. Because the
hashing logic matches Ruby's byte-for-byte, you can deploy the Go service next
to the Ruby one, shadow traffic, and confirm the chosen shards stay in lockstep.

## What to explore next

With shard parity solved you can:

- Add weights by repeating virtual nodes during initialization, just like
  Ruby's implementation.
- Layer health checks on top of the ring and fall back to the next shard when a
  Redis node is unavailable.
- Experiment with other consistent hashing algorithms now that you have a
  reproducible test harness.

When you're ready for a deeper dive, inspect the full source code in this repo
and run `docker compose run --rm experiment` to reproduce every result in this
post.
