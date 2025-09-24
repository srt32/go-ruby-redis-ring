# go-ruby-redis-ring

This repository demonstrates how to port the `Redis::HashRing` algorithm from the
[ruby redis client](https://github.com/redis/redis-rb/blob/master/lib/redis/hash_ring.rb)
to Go while producing byte-for-byte identical shard choices. It contains an
experiment harness that:

1. Generates a deterministic set of random keys (including a handful that use
   Redis hash tags such as `user:{tag0}:...`).
2. Routes the keys through the Ruby hash ring implementation.
3. Routes the same keys through the default go-redis ring (rendezvous hashing).
4. Attempts to override go-redis with a custom `ConsistentHash` implementation
   while still relying on the built-in key preprocessing rules.
5. Routes the keys through a Go re-implementation of Ruby's hash ring logic.
6. Compares the shard selections and writes the results to JSON.

The goal is to illustrate why the go-redis ring cannot be used as a drop-in
replacement for the Ruby hash ring and how to build a compatible router on top
of go-redis clients instead.

## Prerequisites

- [Docker](https://www.docker.com/) and [Docker Compose](https://docs.docker.com/compose/)
  for the turn-key experiment harness.
- Alternatively, a local Ruby (>= 3.0) and Go (>= 1.22) toolchain if you want to
  run the scripts directly without containers.

## Running the experiment

### Using Docker Compose (recommended)

```bash
docker compose run --rm experiment
```

The container installs Ruby and Go dependencies, executes the full workflow, and
writes JSON artifacts into the `artifacts/` directory on your host machine. The
console output shows progress so you can follow along:

```
==> Installing Ruby dependencies
==> Generating deterministic key set
==> Capturing ruby hash ring assignments
...
```

After the run completes you can inspect the results with standard tools:

```bash
cat artifacts/comparison_default.json | jq '.match_rate'
```

Expect to see `0.315` for the rendezvous baseline, `0.285` for the go-redis
consistent hash override, and `1.0` for the Ruby-compatible port.

### Running locally without Docker

If you already have Ruby and Go installed you can execute the same steps
directly:

```bash
./experiments/run_experiment.sh
```

The script installs the Ruby gem dependencies into `.bundle/`, runs the three Go
programs, and produces comparison JSON files in `artifacts/`. Use the same `jq`
commands shown above to confirm the match rates locally.

## Outputs

The following JSON files are produced after a successful run:

- `artifacts/keys.json` – the deterministic key set used for every language.
- `artifacts/ruby_assignments.json` – shard choices from the Ruby hash ring.
- `artifacts/go_default_assignments.json` – shard choices from the default
  go-redis rendezvous ring.
- `artifacts/go_consistent_assignments.json` – shard choices from the go-redis
  ring when we swap in a `ConsistentHash` implementation that mimics
  `Redis::HashRing` but still receives hash-tag-normalized keys.
- `artifacts/go_custom_assignments.json` – shard choices from the Go
  Ruby-compatible implementation.
- `artifacts/comparison_default.json` – summary statistics comparing Ruby to the
  default go-redis ring.
- `artifacts/comparison_consistent.json` – summary statistics comparing Ruby to
  the go-redis consistent hash override.
- `artifacts/comparison_custom.json` – summary statistics comparing Ruby to the
  custom Go implementation.

The comparison JSON files include match counts, mismatch examples, and metadata
about the algorithms so they can be consumed by other tooling.

## Assumptions

- The generated dataset intentionally includes a few keys with Redis hash tags
  (e.g. `user:{tag25}:...`) to demonstrate that go-redis always normalizes keys
  before hashing. Ruby's `HashRing` treats the braces as literal characters, so
  the go-redis override still diverges even when the hashing function matches.
- Each shard has equal weight. Ruby's hash ring supports weights by repeating
  nodes. The example uses three equally weighted shards to match the upstream
  redis-rb defaults.
- Redis servers are not required for the experiment. The Go implementation still
  constructs `*redis.Client` objects to prove the integration point, but no
  network calls are executed.

Refer to [`blog.md`](blog.md) for the detailed blog post that walks through the
experiment and findings.
