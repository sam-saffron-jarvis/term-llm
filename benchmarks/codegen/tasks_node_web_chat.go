package main

import "time"

type nodeWebChat1000Task struct{}

func (nodeWebChat1000Task) Name() string       { return "node_web_chat_1000" }
func (nodeWebChat1000Task) Language() string   { return "javascript" }
func (nodeWebChat1000Task) Difficulty() string { return "hard-web-concurrency" }
func (nodeWebChat1000Task) Prompt() string {
	return `Write a complete Node.js ES module using only the Node standard library that exports exactly this function:

export function newChatServer()

Return an HTTP request listener suitable for http.createServer(newChatServer()).

Implement a tiny in-memory multi-room web chat API.

Required endpoints:

POST /rooms/{room}/messages
- Request body JSON: {"user":"alice","text":"hello"}
- user and text must be non-empty strings
- Append the message to that room and assign a monotonically increasing seq number starting at 1 per room
- Return HTTP 201 with the stored message as JSON: {"seq":1,"user":"alice","text":"hello"}

GET /rooms/{room}/messages
- Return HTTP 200 with a JSON array of all stored messages for that room in seq order
- Unknown rooms return an empty JSON array []

Anything else should return an appropriate 4xx status.

The handler must survive 1000 concurrent users posting at the same time. Do not start a server or read stdin.`
}

func (nodeWebChat1000Task) Score(response string, timeout time.Duration) ScoreResult {
	return scoreNode(response, timeout, `
import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { performance } from 'node:perf_hooks';
import { newChatServer } from './solution.mjs';

async function startServer() {
  const server = http.createServer(newChatServer());
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  return { server, base: `+"`"+`http://127.0.0.1:${address.port}`+"`"+` };
}

async function postJSON(base, path, body) {
  return fetch(base + path, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
}

async function exerciseChat(base, room, users) {
  const bad = await postJSON(base, '/rooms/' + room + '/messages', { user: '', text: 'hello' });
  assert.ok(bad.status >= 400 && bad.status <= 499, `+"`"+`empty user status ${bad.status}, want 4xx`+"`"+`);

  const started = performance.now();
  const responses = await Promise.all(Array.from({ length: users }, async (_, i) => {
    const res = await postJSON(base, '/rooms/' + room + '/messages', { user: `+"`"+`user-${i}`+"`"+`, text: `+"`"+`hello-${i}`+"`"+` });
    const text = await res.text();
    assert.equal(res.status, 201, `+"`"+`POST ${i} status ${res.status} body=${text}`+"`"+`);
    const msg = JSON.parse(text);
    assert.ok(msg.seq >= 1 && msg.seq <= users, `+"`"+`bad seq ${msg.seq}`+"`"+`);
    assert.equal(msg.user, `+"`"+`user-${i}`+"`"+`);
    assert.equal(msg.text, `+"`"+`hello-${i}`+"`"+`);
    return msg;
  }));
  assert.equal(responses.length, users);

  const get = await fetch(base + '/rooms/' + room + '/messages');
  assert.equal(get.status, 200);
  const messages = await get.json();
  assert.equal(messages.length, users);
  const seenSeq = new Set();
  const seenUsers = new Set();
  let lastSeq = 0;
  for (const msg of messages) {
    assert.ok(msg.seq > lastSeq, `+"`"+`messages not in seq order around ${msg.seq}`+"`"+`);
    lastSeq = msg.seq;
    assert.ok(msg.seq >= 1 && msg.seq <= users, `+"`"+`bad seq ${msg.seq}`+"`"+`);
    assert.ok(!seenSeq.has(msg.seq), `+"`"+`duplicate seq ${msg.seq}`+"`"+`);
    seenSeq.add(msg.seq);
    seenUsers.add(msg.user);
  }
  for (let i = 0; i < users; i++) assert.ok(seenUsers.has(`+"`"+`user-${i}`+"`"+`), `+"`"+`missing user-${i}`+"`"+`);

  const empty = await fetch(base + '/rooms/empty/messages');
  assert.equal(empty.status, 200);
  assert.deepEqual(await empty.json(), []);
  return performance.now() - started;
}

test('generated Node chat server handles 1000 concurrent posters', async () => {
  const { server, base } = await startServer();
  try {
    const warmup = await exerciseChat(base, 'warmup', 100);
    console.log('BENCH_WARMUP_MS=' + warmup.toFixed(3));
    const runtime = await exerciseChat(base, 'lobby', 1000);
    console.log('BENCH_RUNTIME_MS=' + runtime.toFixed(3));
    console.log('BENCH_MEMORY_KB=' + (process.memoryUsage().rss / 1024).toFixed(0));
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
});
`)
}
