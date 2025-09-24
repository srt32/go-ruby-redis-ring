#!/usr/bin/env ruby
# frozen_string_literal: true

require 'json'
require 'optparse'
require 'redis/hash_ring'

options = {
  keys: 'artifacts/keys.json',
  output: 'artifacts/ruby_assignments.json'
}

OptionParser.new do |opts|
  opts.banner = 'Usage: ruby_ring.rb [options]'

  opts.on('--keys PATH', String, 'JSON document containing generated keys') do |value|
    options[:keys] = value
  end

  opts.on('--output PATH', String, 'File to write assignments to') do |value|
    options[:output] = value
  end
end.parse!

payload = JSON.parse(File.read(options[:keys]))
keys = payload.fetch('keys')

Node = Struct.new(:id)
nodes = %w[cache-a cache-b cache-c].map { |name| Node.new(name) }
ring = Redis::HashRing.new(nodes)

assignments = keys.map do |key|
  node = ring.get_node(key)
  {
    key: key,
    shard: node&.id
  }
end

File.write(
  options[:output],
  JSON.pretty_generate(
    meta: {
      algorithm: 'redis-rb hash ring',
      shards: nodes.map(&:id),
      replicas: Redis::HashRing::POINTS_PER_SERVER,
      key_source: options[:keys]
    },
    assignments: assignments
  )
)
