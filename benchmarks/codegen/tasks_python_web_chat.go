package main

import "time"

type pythonWebChat1000Task struct{}

func (pythonWebChat1000Task) Name() string       { return "python_web_chat_1000" }
func (pythonWebChat1000Task) Language() string   { return "python" }
func (pythonWebChat1000Task) Difficulty() string { return "hard-concurrency" }
func (pythonWebChat1000Task) Prompt() string {
	return `Write a complete Python 3 source file using only the Python standard library that defines exactly this function:

def new_chat_server():

Return a callable object/function. The benchmark will call it as:

status, json_body = server(method, path, json_request_body)

Implement a tiny in-memory multi-room chat API.

Required operations:

POST /rooms/{room}/messages
- Request body JSON: {"user":"alice","text":"hello"}
- user and text must be non-empty strings
- Append the message to that room and assign a monotonically increasing seq number starting at 1 per room
- Return (201, JSON string like {"seq":1,"user":"alice","text":"hello"})

GET /rooms/{room}/messages
- Return (200, JSON array string) with all stored messages for that room in seq order
- Unknown rooms return []

Anything else should return an appropriate 4xx status and a JSON body.

The callable must be safe for 1000 concurrent Python threads posting at the same time. Do not start a server, read stdin, or print anything.`
}

func (pythonWebChat1000Task) Score(response string, timeout time.Duration) ScoreResult {
	return scorePython(response, timeout, `
import json
import resource
import threading
import time
from solution import new_chat_server

server = new_chat_server()
if not callable(server):
    raise SystemExit('new_chat_server must return a callable object')

bad_status, _ = server('POST', '/rooms/lobby/messages', json.dumps({'user': '', 'text': 'hello'}))
if not isinstance(bad_status, int) or bad_status < 400 or bad_status > 499:
    raise SystemExit(f'empty user status {bad_status!r}, want 4xx')

def exercise(room, users):
    started = time.perf_counter()
    responses = [None] * users
    errors = []
    lock = threading.Lock()

    def worker(i):
        try:
            status, body = server('POST', f'/rooms/{room}/messages', json.dumps({'user': f'user-{i}', 'text': f'hello-{i}'}))
            if status != 201:
                raise RuntimeError(f'POST {i} status {status!r} body={body!r}')
            msg = json.loads(body)
            if not isinstance(msg.get('seq'), int) or msg['seq'] < 1 or msg['seq'] > users:
                raise RuntimeError(f'bad seq {msg.get("seq")!r}')
            if msg.get('user') != f'user-{i}' or msg.get('text') != f'hello-{i}':
                raise RuntimeError(f'bad user/text {msg!r}')
            responses[i] = msg
        except Exception as e:
            with lock:
                errors.append(str(e))

    threads = [threading.Thread(target=worker, args=(i,)) for i in range(users)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    if errors:
        raise SystemExit(errors[0])
    if sum(1 for r in responses if r is not None) != users:
        raise SystemExit('missing responses')

    status, body = server('GET', f'/rooms/{room}/messages', None)
    if status != 200:
        raise SystemExit(f'GET status {status!r} body={body!r}')
    messages = json.loads(body)
    if len(messages) != users:
        raise SystemExit(f'message count {len(messages)}, want {users}')
    seen_seq = set()
    seen_users = set()
    last_seq = 0
    for msg in messages:
        seq = msg['seq']
        if seq <= last_seq:
            raise SystemExit(f'messages not in seq order around {seq} after {last_seq}')
        last_seq = seq
        if seq < 1 or seq > users or seq in seen_seq:
            raise SystemExit(f'bad or duplicate seq {seq!r}')
        seen_seq.add(seq)
        seen_users.add(msg['user'])
    for i in range(users):
        if f'user-{i}' not in seen_users:
            raise SystemExit(f'missing user-{i}')
    return (time.perf_counter() - started) * 1000.0

warmup = exercise('warmup', 100)
print(f'BENCH_WARMUP_MS={warmup:.3f}')
runtime = exercise('lobby', 1000)
print(f'BENCH_RUNTIME_MS={runtime:.3f}')
status, body = server('GET', '/rooms/empty/messages', None)
if status != 200 or json.loads(body) != []:
    raise SystemExit(f'empty room status={status!r} body={body!r}')
print(f'BENCH_MEMORY_KB={resource.getrusage(resource.RUSAGE_SELF).ru_maxrss}')
`)
}
