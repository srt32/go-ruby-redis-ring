#!/usr/bin/env ruby
# frozen_string_literal: true

require 'json'
require 'optparse'

options = {
  baseline: 'artifacts/ruby_assignments.json',
  candidate: nil,
  output: 'artifacts/comparison.json',
  limit: 10
}

OptionParser.new do |opts|
  opts.banner = 'Usage: compare_results.rb [options]'

  opts.on('--baseline PATH', String, 'JSON assignments to compare against') do |value|
    options[:baseline] = value
  end

  opts.on('--candidate PATH', String, 'JSON assignments to evaluate') do |value|
    options[:candidate] = value
  end

  opts.on('--output PATH', String, 'Where to write the comparison JSON') do |value|
    options[:output] = value
  end

  opts.on('--limit N', Integer, 'Maximum number of mismatch samples to include') do |value|
    options[:limit] = value
  end
end.parse!

raise ArgumentError, '--candidate is required' unless options[:candidate]

baseline = JSON.parse(File.read(options[:baseline]))
candidate = JSON.parse(File.read(options[:candidate]))

baseline_assignments = baseline.fetch('assignments')
candidate_assignments = candidate.fetch('assignments')

unless baseline_assignments.size == candidate_assignments.size
  raise "Assignment count mismatch: #{baseline_assignments.size} vs #{candidate_assignments.size}"
end

mismatches = []
matches = 0

data = baseline_assignments.zip(candidate_assignments)
data.each do |(base, cand)|
  if base['key'] == cand['key'] && base['shard'] == cand['shard']
    matches += 1
  else
    mismatches << {
      key: base['key'],
      baseline_shard: base['shard'],
      candidate_shard: cand['shard']
    }
  end
end

total = data.size

output = {
  baseline: baseline.fetch('meta', {}),
  candidate: candidate.fetch('meta', {}),
  total_keys: total,
  matches: matches,
  mismatches: mismatches.size,
  match_rate: total.zero? ? 0.0 : (matches.to_f / total).round(6),
  mismatch_examples: mismatches.first(options[:limit])
}

File.write(options[:output], JSON.pretty_generate(output))
