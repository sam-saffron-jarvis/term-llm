#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const source = fs.readFileSync(path.join(__dirname, 'slash-commands.js'), 'utf8');

class ClassList {
  constructor(element) { this.element = element; }
  values() { return new Set(String(this.element.className || '').split(/\s+/).filter(Boolean)); }
  toggle(token, force) {
    const values = this.values();
    if (force) values.add(token); else values.delete(token);
    this.element.className = [...values].join(' ');
  }
}

class Element {
  constructor() {
    this.children = [];
    this.listeners = {};
    this.attributes = {};
    this.className = '';
    this.classList = new ClassList(this);
    this.hidden = false;
    this.value = '';
  }
  addEventListener(type, listener) { (this.listeners[type] ||= []).push(listener); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = [...children]; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  focus() { this.focused = true; }
  dispatch(type, init = {}) {
    const event = {
      type,
      key: '',
      isComposing: false,
      defaultPrevented: false,
      immediatePropagationStopped: false,
      preventDefault() { this.defaultPrevented = true; },
      stopImmediatePropagation() { this.immediatePropagationStopped = true; },
      ...init,
    };
    for (const listener of this.listeners[type] || []) {
      listener(event);
      if (event.immediatePropagationStopped) break;
    }
    return event;
  }
}

const promptInput = new Element();
const slashCommandMenu = new Element();
const document = {
  createElement() { return new Element(); },
};
const app = {
  elements: { promptInput, slashCommandMenu },
  autoGrowPrompt() { app.growCalls = (app.growCalls || 0) + 1; },
};
const window = { TermLLMApp: app };
vm.runInNewContext(source, { window, document, console }, { filename: 'slash-commands.js' });

const assert = (condition, message) => { if (!condition) throw new Error(message); };

promptInput.value = '/';
promptInput.dispatch('input');
assert(!slashCommandMenu.hidden, 'typing / did not show slash commands');
assert(slashCommandMenu.children.length === 7, 'expected all matching slash commands');
const commandNames = slashCommandMenu.children.map((option) => option.children[0].textContent);
assert(JSON.stringify(commandNames) === JSON.stringify(['/compact', '/compress', '/goal', '/mcp', '/model', '/new', '/side']), `commands were not alphabetized: ${JSON.stringify(commandNames)}`);
assert(slashCommandMenu.children[6].children[1].textContent.includes('without interrupting'), '/side description was not useful');
assert(promptInput.attributes['aria-expanded'] === 'true', 'composer did not expose expanded autocomplete state');

promptInput.value = '/si';
promptInput.dispatch('input');
const accepted = promptInput.dispatch('keydown', { key: 'Tab' });
assert(accepted.defaultPrevented && accepted.immediatePropagationStopped, 'accepting autocomplete did not consume the key');
assert(promptInput.value === '/side ', 'Tab did not complete /side with a trailing space');
assert(slashCommandMenu.hidden, 'autocomplete remained open after acceptance');
assert(promptInput.focused, 'autocomplete did not return focus to composer');
assert(app.growCalls === 1, 'autocomplete did not resize the composer');

promptInput.value = '/si';
promptInput.dispatch('input');
const entered = promptInput.dispatch('keydown', { key: 'Enter' });
assert(entered.defaultPrevented && promptInput.value === '/side ', 'Enter did not accept /side without sending');

promptInput.value = '/';
promptInput.dispatch('input');
const escaped = promptInput.dispatch('keydown', { key: 'Escape' });
assert(escaped.defaultPrevented && slashCommandMenu.hidden, 'Escape did not dismiss autocomplete');
assert(promptInput.value === '/', 'Escape changed composer text');

promptInput.value = '/compa';
promptInput.dispatch('input');
assert(slashCommandMenu.children.length === 1, '/compact filter did not produce one command');
assert(slashCommandMenu.children[0].children[0].textContent === '/compact', '/compact command was not discoverable');

promptInput.value = '/compr';
promptInput.dispatch('input');
assert(slashCommandMenu.children.length === 1, '/compress filter did not produce one command');
assert(slashCommandMenu.children[0].children[0].textContent === '/compress', '/compress command was not discoverable');
promptInput.dispatch('keydown', { key: 'Enter' });
assert(promptInput.value === '/compress ', 'Enter did not complete /compress');

promptInput.value = '/unknown';
promptInput.dispatch('input');
assert(slashCommandMenu.hidden, 'autocomplete displayed with no matching commands');

console.log('PASS: slash command discovery and keyboard completion');
