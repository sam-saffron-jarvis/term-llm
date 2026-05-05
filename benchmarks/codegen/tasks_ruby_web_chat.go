package main

import "time"

type rubyWebChat1000Task struct{}

func (rubyWebChat1000Task) Name() string       { return "ruby_web_chat_1000" }
func (rubyWebChat1000Task) Language() string   { return "ruby" }
func (rubyWebChat1000Task) Difficulty() string { return "hard-concurrency" }
func (rubyWebChat1000Task) Prompt() string {
	return `Write a complete Ruby source file using only the Ruby standard library that defines exactly this method:

def new_chat_server

Return a callable object/lambda/proc. The benchmark will call it as:

status, json_body = server.call(method, path, json_request_body)

Implement a tiny in-memory multi-room chat API.

Required operations:

POST /rooms/{room}/messages
- Request body JSON: {"user":"alice","text":"hello"}
- user and text must be non-empty strings
- Append the message to that room and assign a monotonically increasing seq number starting at 1 per room
- Return [201, JSON.generate({seq: 1, user: "alice", text: "hello"})]

GET /rooms/{room}/messages
- Return [200, JSON array string] with all stored messages for that room in seq order
- Unknown rooms return []

Anything else should return an appropriate 4xx status and a JSON body.

The callable must be safe for 1000 concurrent Ruby threads posting at the same time. Do not start a server, read stdin, or print anything.`
}

func (rubyWebChat1000Task) Score(response string, timeout time.Duration) ScoreResult {
	return scoreRuby(response, timeout, `
require 'json'
require 'set'
require 'thread'
require_relative './solution'

server = new_chat_server
unless server.respond_to?(:call)
  warn "new_chat_server must return a callable object"
  exit 1
end

bad_status, = server.call('POST', '/rooms/lobby/messages', JSON.generate({user: '', text: 'hello'}))
unless bad_status.is_a?(Integer) && bad_status >= 400 && bad_status <= 499
  warn "empty user status #{bad_status.inspect}, want 4xx"
  exit 1
end

def exercise(server, room, users)
  started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  responses = Array.new(users)
  threads = users.times.map do |i|
    Thread.new do
      status, body = server.call('POST', "/rooms/#{room}/messages", JSON.generate({user: "user-#{i}", text: "hello-#{i}"}))
      raise "POST #{i} status #{status.inspect} body=#{body.inspect}" unless status == 201
      msg = JSON.parse(body)
      raise "bad seq #{msg['seq'].inspect}" unless msg['seq'].is_a?(Integer) && msg['seq'] >= 1 && msg['seq'] <= users
      raise "bad user/text #{msg.inspect}" unless msg['user'] == "user-#{i}" && msg['text'] == "hello-#{i}"
      responses[i] = msg
    end
  end
  threads.each(&:join)
  raise "missing responses" unless responses.compact.length == users

  status, body = server.call('GET', "/rooms/#{room}/messages", nil)
  raise "GET status #{status.inspect} body=#{body.inspect}" unless status == 200
  messages = JSON.parse(body)
  raise "message count #{messages.length}, want #{users}" unless messages.length == users
  seen_seq = Set.new
  seen_users = Set.new
  last_seq = 0
  messages.each do |msg|
    seq = msg['seq']
    raise "messages not in seq order around #{seq} after #{last_seq}" unless seq > last_seq
    last_seq = seq
    raise "bad or duplicate seq #{seq.inspect}" if seq < 1 || seq > users || seen_seq.include?(seq)
    seen_seq.add(seq)
    seen_users.add(msg['user'])
  end
  users.times { |i| raise "missing user-#{i}" unless seen_users.include?("user-#{i}") }
  (Process.clock_gettime(Process::CLOCK_MONOTONIC) - started) * 1000.0
end

warmup_ms = exercise(server, 'warmup', 100)
puts "BENCH_WARMUP_MS=#{format('%.3f', warmup_ms)}"
runtime_ms = exercise(server, 'lobby', 1000)
puts "BENCH_RUNTIME_MS=#{format('%.3f', runtime_ms)}"

status, body = server.call('GET', '/rooms/empty/messages', nil)
raise "empty room status=#{status.inspect} body=#{body.inspect}" unless status == 200 && JSON.parse(body) == []
rss_kb = File.read('/proc/self/status')[/^VmRSS:\s+(\d+)\s+kB$/, 1].to_i
puts "BENCH_MEMORY_KB=#{rss_kb}"
`)
}
