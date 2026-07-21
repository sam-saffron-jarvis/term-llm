#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const source = fs.readFileSync(path.join(__dirname, 'app-worktrees.js'), 'utf8');
let failures = 0;

function fail(name, message) {
  console.error('FAIL:', name, '-', message);
  failures += 1;
}

function pass(name) {
  console.log('PASS:', name);
}

function makeClassList() {
  const values = new Set();
  return {
    add(value) { values.add(value); },
    remove(value) { values.delete(value); },
    toggle(value, force) {
      const enabled = force === undefined ? !values.has(value) : Boolean(force);
      if (enabled) values.add(value); else values.delete(value);
      return enabled;
    },
    contains(value) { return values.has(value); },
  };
}

function makeNode(tagName = 'div') {
  const listeners = {};
  const attributes = {};
  const node = {
    tagName: tagName.toUpperCase(),
    listeners,
    attributes,
    children: [],
    parentNode: null,
    classList: makeClassList(),
    style: {},
    hidden: false,
    textContent: '',
    appendChild(child) {
      child.parentNode = node;
      node.children.push(child);
      return child;
    },
    addEventListener(type, listener) {
      (listeners[type] = listeners[type] || []).push(listener);
    },
    setAttribute(name, value) { attributes[name] = String(value); },
    getAttribute(name) { return attributes[name] || null; },
    contains(target) {
      let current = target;
      while (current) {
        if (current === node) return true;
        current = current.parentNode;
      }
      return false;
    },
    remove() {
      if (!node.parentNode) return;
      node.parentNode.children = node.parentNode.children.filter((child) => child !== node);
      node.parentNode = null;
    },
    focus() {},
  };
  Object.defineProperty(node, 'className', {
    get() { return node._className || ''; },
    set(value) {
      node._className = String(value || '');
      for (const name of node._className.split(/\s+/)) if (name) node.classList.add(name);
    },
  });
  return node;
}

function flushAsync() {
  return new Promise((resolve) => setImmediate(resolve));
}

async function testActiveRootOpensStableResponsiveMenu() {
  const name = 'active root chip opens a stable responsive worktree menu';
  const documentListeners = {};
  const body = makeNode('body');
  const trigger = makeNode('button');
  const label = makeNode('span');
  trigger.appendChild(label);
  const backdrop = makeNode('div');
  backdrop.hidden = true;
  const positionCalls = [];
  const windowListeners = {};
  const state = {
    activeSessionId: 'session-root',
    draftSessionActive: false,
    selectedWorktreeDir: '',
    selectedWorktreeName: '',
    sessions: [{ id: 'session-root', worktreeDir: '' }],
    worktrees: [],
  };
  const elements = {
    chipWorktree: makeNode(),
    chipWorktreeTrigger: trigger,
    chipWorktreeLabel: label,
    chipSepEffortWorktree: makeNode(),
    chipPopoverBackdrop: backdrop,
  };
  const document = {
    body,
    createElement: (tag) => makeNode(tag),
    addEventListener(type, listener) {
      (documentListeners[type] = documentListeners[type] || []).push(listener);
    },
  };
  const windowObj = {
    TermLLMApp: {
      UI_PREFIX: '/chat',
      state,
      elements,
      getActiveSession: () => state.sessions[0],
      requestHeaders: () => ({}),
      positionChipPopover(...args) { positionCalls.push(args); },
    },
    addEventListener(type, listener) {
      (windowListeners[type] = windowListeners[type] || []).push(listener);
    },
    visualViewport: null,
    prompt: () => null,
    alert() {},
    confirm: () => false,
  };
  const fetch = async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      worktrees: [
        { root: true, name: 'root', dir: '/repo' },
        { name: 'feature', dir: '/repo-worktrees/feature', branch: 'feature', dirty_files: 2 },
      ],
    }),
    text: async () => '',
  });
  const context = {
    window: windowObj,
    document,
    fetch,
    setInterval() { return 1; },
    clearInterval() {},
    console,
  };
  context.globalThis = context;
  vm.runInNewContext(source, context, { filename: 'app-worktrees.js' });
  await flushAsync();

  const clickListener = trigger.listeners.click && trigger.listeners.click[0];
  if (!clickListener) {
    fail(name, 'worktree trigger has no click listener');
    return;
  }
  clickListener({ target: label, preventDefault() {} });
  await flushAsync();
  await flushAsync();

  const menu = body.children.find((child) => child.classList.contains('worktree-popover'));
  if (!menu) {
    fail(name, 'clicking root did not render a menu');
    return;
  }
  if (menu.getAttribute('role') !== 'menu' || menu.children.some((child) => child.getAttribute('role') !== 'menuitem')) {
    fail(name, 'active-session worktree actions do not use menu/menuitem semantics');
    return;
  }
  if (!menu.children[0].disabled) {
    fail(name, 'the current root checkout is exposed as an actionable menu item');
    return;
  }
  const lastPositionCall = positionCalls[positionCalls.length - 1];
  if (!lastPositionCall || lastPositionCall[2]?.mobileSheet !== true) {
    fail(name, 'worktree menu did not request mobile bottom-sheet positioning');
    return;
  }
  if (backdrop.hidden) {
    fail(name, 'worktree menu backdrop stayed hidden');
    return;
  }

  for (const listener of documentListeners.click || []) {
    listener({ target: label });
  }
  if (!body.children.includes(menu)) {
    fail(name, 'the opening click bubbling from the trigger label immediately closed the menu');
    return;
  }

  for (const listener of windowListeners.resize || []) {
    listener();
  }
  const resizePositionCall = positionCalls[positionCalls.length - 1];
  if (!resizePositionCall || resizePositionCall[2]?.mobileSheet !== true) {
    fail(name, 'worktree menu lost bottom-sheet positioning after a viewport resize');
    return;
  }

  for (const listener of backdrop.listeners.click || []) {
    listener({ target: backdrop });
  }
  if (body.children.includes(menu) || !backdrop.hidden) {
    fail(name, 'backdrop click did not close the worktree menu');
    return;
  }
  pass(name);
}

(async () => {
  await testActiveRootOpensStableResponsiveMenu();
  if (failures > 0) process.exit(1);
  console.log('\nAll tests passed');
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
