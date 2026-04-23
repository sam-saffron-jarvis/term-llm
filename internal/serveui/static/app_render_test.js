#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const source = fs.readFileSync(path.join(__dirname, 'app-render.js'), 'utf8');
let failures = 0;

function fail(name, message, details) {
  console.error('FAIL:', name, '-', message);
  if (details) console.error('      ', details);
  failures += 1;
}

function pass(name) {
  console.log('PASS:', name);
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function assertEqual(actual, expected, message) {
  if (actual !== expected) {
    throw new Error(`${message}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

class ClassList {
  constructor(element) {
    this.element = element;
  }

  _set(values) {
    this.element.className = Array.from(values).join(' ');
  }

  _values() {
    return new Set(String(this.element.className || '').split(/\s+/).filter(Boolean));
  }

  add(...tokens) {
    const values = this._values();
    tokens.forEach((token) => values.add(token));
    this._set(values);
  }

  remove(...tokens) {
    const values = this._values();
    tokens.forEach((token) => values.delete(token));
    this._set(values);
  }

  contains(token) {
    return this._values().has(token);
  }

  toggle(token, force) {
    const values = this._values();
    const shouldAdd = force === undefined ? !values.has(token) : Boolean(force);
    if (shouldAdd) values.add(token);
    else values.delete(token);
    this._set(values);
    return shouldAdd;
  }
}

class Element {
  constructor(tagName) {
    this.tagName = String(tagName || '').toUpperCase();
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.attributes = new Map();
    this.className = '';
    this.classList = new ClassList(this);
    this.style = {};
    this.listeners = new Map();
    this.textContent = '';
    this.innerHTML = '';
    this.disabled = false;
    this.title = '';
    this.type = '';
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(child, reference) {
    child.parentNode = this;
    const index = this.children.indexOf(reference);
    if (index === -1) {
      this.children.push(child);
    } else {
      this.children.splice(index, 0, child);
    }
    return child;
  }

  replaceWith(replacement) {
    if (!this.parentNode) return;
    const siblings = this.parentNode.children;
    const index = siblings.indexOf(this);
    if (index !== -1) {
      replacement.parentNode = this.parentNode;
      siblings[index] = replacement;
      this.parentNode = null;
    }
  }

  remove() {
    if (!this.parentNode) return;
    const siblings = this.parentNode.children;
    const index = siblings.indexOf(this);
    if (index !== -1) siblings.splice(index, 1);
    this.parentNode = null;
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === 'class') this.className = String(value);
  }

  getAttribute(name) {
    return this.attributes.get(name) || null;
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) || [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  async dispatchEvent(event) {
    const evt = event || { type: '' };
    const listeners = this.listeners.get(evt.type) || [];
    for (const listener of listeners) {
      await listener(evt);
    }
  }

  matches(selector) {
    if (!selector) return false;
    if (selector.startsWith('.')) return this.classList.contains(selector.slice(1));
    if (selector.startsWith('[data-message-id="') && selector.endsWith('"]')) {
      return this.dataset.messageId === selector.slice(18, -2);
    }
    return this.tagName.toLowerCase() === selector.toLowerCase();
  }

  querySelectorAll(selector) {
    const results = [];
    const walk = (node) => {
      node.children.forEach((child) => {
        if (child.matches(selector)) results.push(child);
        walk(child);
      });
    };
    walk(this);
    return results;
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
}

function createDocument() {
  const document = {
    body: new Element('body'),
    createElement(tagName) { return new Element(tagName); },
    createTextNode(text) {
      const node = new Element('#text');
      node.textContent = String(text || '');
      return node;
    },
    addEventListener() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
  };
  document.body.contains = (target) => {
    let node = target;
    while (node) {
      if (node === document.body) return true;
      node = node.parentNode;
    }
    return false;
  };
  return document;
}

function findByMessageId(root, id) {
  if (!root || !id) return null;
  if (root.dataset?.messageId === id) return root;
  for (const child of root.children || []) {
    const found = findByMessageId(child, id);
    if (found) return found;
  }
  return null;
}

function createHarness() {
  const document = createDocument();
  const messages = new Element('div');
  document.body.appendChild(messages);
  const session = { id: 's1', title: 'Chat', created: Date.now(), messages: [] };
  const state = { activeSessionId: 's1', sessions: [session], sidebarCollapsed: false };
  const timers = [];
  let timerId = 0;
  const copied = [];

  const app = {
    STORAGE_KEYS: { sidebarCollapsed: 'sidebar' },
    state,
    elements: {
      messages,
      sidebar: new Element('div'),
      sidebarBackdrop: new Element('div'),
      sidebarToggleBtn: new Element('button'),
      sidebarPanelToggleBtn: new Element('button'),
      appShell: new Element('div'),
      activeSessionTitle: new Element('div'),
      sessionGroups: new Element('div'),
    },
    INTERRUPT_BADGE_META: {},
    sanitizeInterruptState(value) { return value || ''; },
    relativeTime() { return 'now'; },
    fullDate() { return 'today'; },
    sessionBucket() { return 'Today'; },
    toolIcon() { return '·'; },
    formatUsage() { return ''; },
    saveSessions() {},
    findMessageElement(id) { return findByMessageId(messages, id); },
    scrollToBottom() {},
    refreshRelativeTimes() {},
    ensureActiveSession() { return session; },
    updateDocumentTitle() {},
    updateSessionUsageDisplay() {},
    renderMath() {},
    visibleSessions() { return []; },
    sessionHasInProgressState() { return false; },
    setSessionServerActiveRun() {},
    openLightbox() {},
  };

  const windowObj = {
    TermLLMApp: app,
    TermLLMDecoration: { decorateLightbox() {} },
    matchMedia() { return { matches: false }; },
    setTimeout(callback, ms) {
      const id = ++timerId;
      timers.push({ id, callback, ms, cleared: false });
      return id;
    },
    clearTimeout(id) {
      const timer = timers.find((item) => item.id === id);
      if (timer) timer.cleared = true;
    },
    requestAnimationFrame(callback) { return setTimeout(callback, 0); },
    cancelAnimationFrame(id) { clearTimeout(id); },
    addEventListener() {},
  };

  const context = {
    window: windowObj,
    document,
    console,
    localStorage: { getItem() { return null; }, setItem() {} },
    navigator: { clipboard: { async writeText(text) { copied.push(text); } } },
    marked: { parse(text) { return String(text || ''); } },
    DOMPurify: { sanitize(html) { return String(html || ''); } },
    hljs: { highlightElement() {} },
    CSS: { escape(value) { return String(value); } },
    setTimeout,
    clearTimeout,
  };
  context.globalThis = context;
  windowObj.document = document;
  windowObj.navigator = context.navigator;
  windowObj.localStorage = context.localStorage;

  vm.runInNewContext(source, context, { filename: 'app-render.js' });
  return { app, session, messages, document, timers, copied };
}

function messageNode(id, role) {
  const node = new Element('article');
  node.className = `message ${role}`;
  node.dataset.messageId = id;
  const body = new Element('div');
  body.className = 'message-body';
  const meta = new Element('div');
  meta.className = 'message-meta';
  node.appendChild(body);
  node.appendChild(meta);
  return node;
}

async function run(name, fn) {
  try {
    await fn();
    pass(name);
  } catch (err) {
    fail(name, err.message, err.stack);
  }
}

(async () => {
  await run('discovers every assistant turn and skips empty assistant segments', () => {
    const { app, session } = createHarness();
    session.messages = [
      { id: 'a0', role: 'assistant', content: 'initial' },
      { id: 'u1', role: 'user', content: 'one' },
      { id: 'a1', role: 'assistant', content: 'first' },
      { id: 'u2', role: 'user', content: 'two' },
      { id: 'a-empty', role: 'assistant', content: '   ' },
      { id: 'u3', role: 'user', content: 'three' },
      { id: 'a2', role: 'assistant', content: 'second' },
    ];
    const turns = app.getAssistantTurns(session);
    assertEqual(turns.length, 3, 'turn count');
    assertEqual(turns[0].lastAssistantId, 'a0', 'initial turn target');
    assertEqual(turns[1].lastAssistantId, 'a1', 'first user-bounded turn target');
    assertEqual(turns[2].lastAssistantId, 'a2', 'second user-bounded turn target');
  });

  await run('copies a whole user-bounded turn in message order', () => {
    const { app, session } = createHarness();
    session.messages = [
      { id: 'u1', role: 'user', content: 'question' },
      { id: 'a1', role: 'assistant', content: 'Before tools' },
      { id: 'tg1', role: 'tool-group', tools: [
        { id: 't1', name: 'read_file', status: 'done', arguments: '{"path":"internal/serveui/static/app-render.js","start_line":1}' },
      ] },
      { id: 'a2', role: 'assistant', content: 'After tools' },
      { id: 'u2', role: 'user', content: 'next' },
      { id: 'a3', role: 'assistant', content: 'Next turn' },
    ];
    const turn = app.getAssistantTurns(session).find((item) => item.lastAssistantId === 'a2');
    const text = app.buildTurnClipboardText(turn);
    assert(text.indexOf('Before tools') < text.indexOf('Tools:'), 'assistant text should precede tools');
    assert(text.indexOf('Tools:') < text.indexOf('After tools'), 'tools should precede later assistant text');
    assert(text.includes('- read_file [done]\n  path: internal/serveui/static/app-render.js'), 'tool summary should include prioritized path');
    assert(!text.includes('Next turn'), 'copy should stop at next user boundary');
  });

  await run('tool clipboard summaries are capped at two lines with prioritized keys', () => {
    const { app } = createHarness();
    const lines = app.formatToolClipboardLines({
      name: 'shell',
      status: 'done',
      arguments: JSON.stringify({ command: 'printf "hello\\nworld"', path: '/tmp', extra: 'ignored' }),
    });
    assertEqual(lines.length, 2, 'line cap');
    assertEqual(lines[0], '- shell [done]', 'summary line');
    assert(lines[1].includes('command: printf "hello\\nworld"'), 'detail should include shell command');
    assert(!lines[1].includes('extra:'), 'detail should not include excess keys');
  });

  await run('askUser messages do not split assistant turns', () => {
    const { app, session } = createHarness();
    session.messages = [
      { id: 'u1', role: 'user', content: 'start' },
      { id: 'a1', role: 'assistant', content: 'Part one' },
      { id: 'ask', role: 'user', askUser: true, content: 'Need input' },
      { id: 'tg1', role: 'tool-group', tools: [
        { id: 't1', name: 'web_search', status: 'done', arguments: '{"query":"term llm"}' },
      ] },
      { id: 'a2', role: 'assistant', content: 'Part two' },
    ];
    const turns = app.getAssistantTurns(session);
    assertEqual(turns.length, 1, 'askUser should not create boundary');
    assertEqual(turns[0].lastAssistantId, 'a2', 'last assistant after askUser');
    const text = app.buildTurnClipboardText(turns[0]);
    assert(text.includes('Part one'), 'first assistant text included');
    assert(text.includes('- web_search [done]\n  query: term llm'), 'tool after askUser included');
    assert(text.includes('Part two'), 'later assistant text included');
  });

  await run('syncs one copy panel per assistant turn and click copies target turn', async () => {
    const { app, session, messages, timers, copied } = createHarness();
    session.messages = [
      { id: 'u1', role: 'user', content: 'question' },
      { id: 'a1', role: 'assistant', content: 'Earlier assistant' },
      { id: 'tg1', role: 'tool-group', tools: [
        { id: 't1', name: 'grep', status: 'done', arguments: '{"pattern":"needle","path":"src"}' },
      ] },
      { id: 'a2', role: 'assistant', content: 'Final assistant in first turn' },
      { id: 'u2', role: 'user', content: 'second' },
      { id: 'a3', role: 'assistant', content: 'Second turn answer' },
    ];
    ['a1', 'a2', 'a3'].forEach((id) => messages.appendChild(messageNode(id, 'assistant')));

    app.syncTurnActionPanels();
    app.syncTurnActionPanels();

    assertEqual(messages.querySelectorAll('.turn-action-panel').length, 2, 'panel count after resync');
    assertEqual(messages.children[0].querySelectorAll('.turn-action-panel').length, 0, 'earlier assistant in same turn has no panel');
    assertEqual(messages.children[1].querySelectorAll('.turn-action-panel').length, 1, 'first turn panel on last assistant');
    assertEqual(messages.children[2].querySelectorAll('.turn-action-panel').length, 1, 'second turn panel');

    const button = messages.children[1].querySelector('.turn-copy-btn');
    await button.dispatchEvent({ type: 'click', preventDefault() {} });
    assertEqual(copied.length, 1, 'clipboard writes');
    assert(copied[0].includes('Earlier assistant'), 'copied earlier assistant text');
    assert(copied[0].includes('Final assistant in first turn'), 'copied final assistant text');
    assert(copied[0].includes('- grep [done]\n  pattern: needle · path: src'), 'copied tool summary');
    assert(!copied[0].includes('Second turn answer'), 'did not copy later turn');
    assert(button.classList.contains('copied'), 'button gets copied class');

    const reset = timers.find((timer) => timer.ms === 1500 && !timer.cleared);
    assert(reset, 'reset timer scheduled');
    reset.callback();
    assert(!button.classList.contains('copied'), 'copied class resets');
  });

  if (failures > 0) {
    process.exit(1);
  }
})();
