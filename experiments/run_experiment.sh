#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ARTIFACT_DIR="$ROOT_DIR/artifacts"

cd "$ROOT_DIR"

export BUNDLE_SILENCE_ROOT_WARNING=1

mkdir -p "$ARTIFACT_DIR"
rm -f "$ARTIFACT_DIR"/*.json

echo "==> Installing Ruby dependencies"
bundle config set --local path .bundle >/dev/null
bundle install --quiet

echo "==> Generating deterministic key set"
bundle exec ruby scripts/generate_keys.rb --output "$ARTIFACT_DIR/keys.json"

echo "==> Capturing ruby hash ring assignments"
bundle exec ruby scripts/ruby_ring.rb --keys "$ARTIFACT_DIR/keys.json" --output "$ARTIFACT_DIR/ruby_assignments.json"

echo "==> Capturing go-redis default rendezvous assignments"
go run ./cmd/go-ring-default --keys "$ARTIFACT_DIR/keys.json" --output "$ARTIFACT_DIR/go_default_assignments.json"

echo "==> Capturing go-redis consistent hash override assignments"
go run ./cmd/go-ring-consistenthash --keys "$ARTIFACT_DIR/keys.json" --output "$ARTIFACT_DIR/go_consistent_assignments.json"

echo "==> Capturing ruby-compatible go assignments"
go run ./cmd/go-ring-custom --keys "$ARTIFACT_DIR/keys.json" --output "$ARTIFACT_DIR/go_custom_assignments.json"

echo "==> Comparing ruby vs go default"
bundle exec ruby scripts/compare_results.rb \
  --baseline "$ARTIFACT_DIR/ruby_assignments.json" \
  --candidate "$ARTIFACT_DIR/go_default_assignments.json" \
  --output "$ARTIFACT_DIR/comparison_default.json"

echo "==> Comparing ruby vs go consistent hash override"
bundle exec ruby scripts/compare_results.rb \
  --baseline "$ARTIFACT_DIR/ruby_assignments.json" \
  --candidate "$ARTIFACT_DIR/go_consistent_assignments.json" \
  --output "$ARTIFACT_DIR/comparison_consistent.json"

echo "==> Comparing ruby vs go custom"
bundle exec ruby scripts/compare_results.rb \
  --baseline "$ARTIFACT_DIR/ruby_assignments.json" \
  --candidate "$ARTIFACT_DIR/go_custom_assignments.json" \
  --output "$ARTIFACT_DIR/comparison_custom.json"

echo "Artifacts written to $ARTIFACT_DIR"
