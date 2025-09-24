#!/usr/bin/env ruby
# frozen_string_literal: true

require 'json'
require 'optparse'
require 'time'

options = {
  count: 200,
  output: 'artifacts/keys.json',
  seed: 1337,
  prefix: 'user',
  hashtags: true
}

OptionParser.new do |opts|
  opts.banner = 'Usage: generate_keys.rb [options]'

  opts.on('--count N', Integer, 'Number of random keys to generate') do |value|
    options[:count] = value
  end

  opts.on('--output PATH', String, 'File to write the JSON payload to') do |value|
    options[:output] = value
  end

  opts.on('--seed N', Integer, 'Seed used for deterministic generation') do |value|
    options[:seed] = value
  end

  opts.on('--prefix PREFIX', String, 'String prepended to each key') do |value|
    options[:prefix] = value
  end

  opts.on('--no-hashtags', 'Disable Redis hash tag injection') do
    options[:hashtags] = false
  end
end.parse!

rng = Random.new(options[:seed])
keys = Array.new(options[:count]) do |i|
  token = rng.bytes(8).unpack1('H*')

  if options[:hashtags] && (i % 25).zero?
    tag = "tag#{i}"
    "#{options[:prefix]}:{#{tag}}:#{token}"
  else
    "#{options[:prefix]}:#{i}:#{token}"
  end
end

payload = {
  meta: {
    generated_at: Time.now.utc.iso8601,
    seed: options[:seed],
    count: options[:count],
    prefix: options[:prefix],
    hashtags: options[:hashtags]
  },
  keys: keys
}

File.write(options[:output], JSON.pretty_generate(payload))
