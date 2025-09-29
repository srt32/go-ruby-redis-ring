# Supporting Redis Replicas with Ruby-Compatible Hash Ring

The hash ring implementation routes keys to primary Redis nodes for both reads and writes. In production, you often want to distribute read traffic across multiple read replicas while keeping writes directed to primaries. Here's how to extend the Ruby-compatible ring to support this pattern.

## Extending the node structure

First, modify the `node` struct to track both primary and replica clients:

```go
type node struct {
    name     string
    primary  *redis.Client
    replicas []*redis.Client
}

type shardConfig struct {
    Name         string
    PrimaryAddr  string
    ReplicaAddrs []string
}
```

Update the ring constructor to create clients for all addresses:

```go
func newRubyHashRingWithReplicas(configs []shardConfig, replicas int) *rubyHashRing {
    r := &rubyHashRing{
        replicas:   replicas,
        ring:       make(map[uint32]*node),
        sortedKeys: make([]uint32, 0, replicas*len(configs)),
        nodes:      make([]*node, 0, len(configs)),
    }

    for _, cfg := range configs {
        n := &node{
            name:    cfg.Name,
            primary: redis.NewClient(&redis.Options{Addr: cfg.PrimaryAddr}),
        }
        
        // Connect to read replicas
        for _, replicaAddr := range cfg.ReplicaAddrs {
            n.replicas = append(n.replicas, redis.NewClient(&redis.Options{
                Addr:     replicaAddr,
                ReadOnly: true, // Client-side protection against write commands
            }))
        }
        
        r.nodes = append(r.nodes, n)

        // Virtual nodes still hash based on primary shard name
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
```

## Read/write client selection

Add methods to get the appropriate client based on operation type:

```go
func (r *rubyHashRing) getPrimaryClient(key string) *redis.Client {
    node := r.getNode(key)
    if node == nil {
        return nil
    }
    return node.primary
}

func (r *rubyHashRing) getReplicaClient(key string) *redis.Client {
    node := r.getNode(key)
    if node == nil {
        return nil
    }
    
    // If no replicas configured, fall back to primary
    if len(node.replicas) == 0 {
        return node.primary
    }
    
    // Round-robin or random selection among replicas
    // For consistency, use key hash to select replica
    hash := crc32.ChecksumIEEE([]byte(key))
    idx := int(hash) % len(node.replicas)
    return node.replicas[idx]
}

func (r *rubyHashRing) getClientForRead(key string) *redis.Client {
    return r.getReplicaClient(key)
}

func (r *rubyHashRing) getClientForWrite(key string) *redis.Client {
    return r.getPrimaryClient(key)
}
```

## Usage patterns

Configure your ring with primary and replica addresses. Note that the Redis servers should be properly configured with primary-replica replication, and replica servers should have `replica-read-only yes` in their configuration:

```go
shardDefs := []shardConfig{
    {
        Name:        "cache-a",
        PrimaryAddr: "redis-a-primary.internal:6379",
        ReplicaAddrs: []string{
            "redis-a-replica-1.internal:6379",
            "redis-a-replica-2.internal:6379",
        },
    },
    {
        Name:        "cache-b", 
        PrimaryAddr: "redis-b-primary.internal:6379",
        ReplicaAddrs: []string{
            "redis-b-replica-1.internal:6379",
        },
    },
    {
        Name:        "cache-c",
        PrimaryAddr: "redis-c-primary.internal:6379",
        ReplicaAddrs: []string{}, // No replicas for this shard
    },
}

ring := newRubyHashRingWithReplicas(shardDefs, 160)
defer ring.Close()
```

Use different clients for reads and writes:

```go
// For write operations, always use primary
func setCache(key, value string) error {
    client := ring.getClientForWrite(key)
    return client.Set(ctx, key, value, time.Hour).Err()
}

// For read operations, prefer replicas when available
func getCache(key string) (string, error) {
    client := ring.getClientForRead(key)
    return client.Get(ctx, key).Result()
}

// For operations requiring strong consistency, use primary
func getWithConsistency(key string) (string, error) {
    client := ring.getClientForWrite(key) // Use primary for consistent reads
    return client.Get(ctx, key).Result()
}
```

## Replica selection strategies

The example above uses key-based hashing to select replicas consistently. You can customize this based on your needs:

```go
// Random replica selection (better load distribution)
// Note: Consider using rand.New() with crypto/rand seed for production use
func (r *rubyHashRing) getRandomReplicaClient(key string) *redis.Client {
    node := r.getNode(key)
    if node == nil || len(node.replicas) == 0 {
        return node.primary
    }
    
    idx := rand.Intn(len(node.replicas))
    return node.replicas[idx]
}

// Health-aware replica selection
func (r *rubyHashRing) getHealthyReplicaClient(key string) *redis.Client {
    node := r.getNode(key)
    if node == nil {
        return nil
    }
    
    // Try replicas first, fall back to primary if all unhealthy
    for _, replica := range node.replicas {
        if isHealthy(replica) {
            return replica
        }
    }
    
    return node.primary // Fallback to primary
}

// Example health check implementation
func isHealthy(client *redis.Client) bool {
    // Short timeout to quickly detect unhealthy replicas
    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()
    
    // Use PING command to check if replica is responsive
    return client.Ping(ctx).Err() == nil
}
```

## Maintaining Ruby compatibility

The key insight is that **shard selection remains identical** to the Ruby implementation—only the client routing changes. The hash ring still uses the same CRC32 + MD5 algorithm to determine which shard owns each key. Whether that shard serves the request from a primary or replica is a separate concern that doesn't affect compatibility.

This means you can:

1. Deploy the Go service with replica support alongside your Ruby service
2. Both will route keys to the same logical shards
3. The Go service can leverage read replicas for better performance
4. Writes remain consistent across both implementations

The replica selection happens **after** the Ruby-compatible shard selection, preserving byte-for-byte compatibility for the core routing decision.