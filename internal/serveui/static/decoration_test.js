#!/usr/bin/env node
// decoration_test.js — Node.js tests for decoration.js lightbox/video logic.
// Run directly: node internal/serveui/static/decoration_test.js
// Or via: go test ./internal/serveui/ (invoked by TestDecorationJS)
'use strict';

const path = require('path');
const dir = __dirname;

const { VIDEO_LINK_PATTERN, decorateLightbox } = require(path.join(dir, 'decoration.js'));

let failures = 0;

function fail(name, message) {
  console.error('FAIL:', name, '—', message);
  failures++;
}

function pass(name) {
  console.log('PASS:', name);
}

function assert(name, condition, message) {
  if (!condition) {
    fail(name, message || 'assertion failed');
  } else {
    pass(name);
  }
}

// --- Minimal DOM shim ---

function mockElement(tag, props) {
  var listeners = {};
  var classes = new Set();
  var parentClasses = (props && props._parentClasses) || [];

  return {
    tagName: tag.toUpperCase(),
    href: (props && props.href) || '',
    src: (props && props.src) || '',
    classList: {
      add: function (cls) { classes.add(cls); },
      has: function (cls) { return classes.has(cls); },
      _set: classes,
    },
    addEventListener: function (type, fn) {
      if (!listeners[type]) listeners[type] = [];
      listeners[type].push(fn);
    },
    closest: function (selector) {
      var cls = selector.startsWith('.') ? selector.slice(1) : selector;
      return parentClasses.indexOf(cls) >= 0 ? {} : null;
    },
    _listeners: listeners,
    _classes: classes,
  };
}

function mockTarget(elements) {
  return {
    querySelectorAll: function (selector) {
      var tag = selector.toUpperCase();
      return (elements || []).filter(function (el) { return el.tagName === tag; });
    },
  };
}

// --- VIDEO_LINK_PATTERN tests ---

var videoPatternCases = [
  { href: 'https://example.com/video.mp4', expected: true },
  { href: 'https://example.com/clip.webm', expected: true },
  { href: 'https://example.com/movie.mov', expected: true },
  { href: 'https://example.com/audio.ogg', expected: true },
  { href: 'https://example.com/video.ogv', expected: true },
  { href: 'https://example.com/video.MP4', expected: true },
  { href: 'https://example.com/video.mp4?token=abc', expected: true },
  { href: 'https://example.com/video.mp4#t=10', expected: true },
  { href: 'https://example.com/page.html', expected: false },
  { href: 'https://example.com/image.png', expected: false },
  { href: 'https://example.com/doc.pdf', expected: false },
  { href: 'https://example.com/mp4-info', expected: false },
];

videoPatternCases.forEach(function (tc) {
  var result = VIDEO_LINK_PATTERN.test(tc.href);
  assert(
    'VIDEO_LINK_PATTERN ' + (tc.expected ? 'matches' : 'rejects') + ' ' + tc.href,
    result === tc.expected,
    'expected ' + tc.expected + ', got ' + result
  );
  VIDEO_LINK_PATTERN.lastIndex = 0;
});

// --- decorateLightbox: non-streaming image gets click handler ---

(function () {
  var img = mockElement('img', { src: 'https://example.com/photo.jpg' });
  var target = mockTarget([img]);
  var calls = [];
  decorateLightbox(target, {}, function (src, type) { calls.push({ src: src, type: type }); });

  assert(
    'non-streaming image gets click handler',
    img._listeners.click && img._listeners.click.length === 1,
    'expected 1 click listener'
  );

  var mockEvent = { preventDefault: function () {}, stopPropagation: function () {} };
  img._listeners.click[0](mockEvent);
  assert(
    'image click calls openLightbox with src',
    calls.length === 1 && calls[0].src === 'https://example.com/photo.jpg' && calls[0].type === undefined,
    'got: ' + JSON.stringify(calls)
  );
})();

// --- decorateLightbox: streaming suppresses image handlers ---

(function () {
  var img = mockElement('img', { src: 'https://example.com/photo.jpg' });
  var target = mockTarget([img]);
  decorateLightbox(target, { streaming: true }, function () {});

  assert(
    'streaming: image gets no click handler',
    !img._listeners.click || img._listeners.click.length === 0,
    'expected 0 click listeners'
  );
})();

// --- decorateLightbox: image inside .deferred-video is skipped ---

(function () {
  var img = mockElement('img', { src: 'poster.jpg', _parentClasses: ['deferred-video'] });
  var target = mockTarget([img]);
  decorateLightbox(target, {}, function () {});

  assert(
    'image inside .deferred-video is skipped',
    !img._listeners.click || img._listeners.click.length === 0,
    'expected 0 click listeners'
  );
})();

// --- decorateLightbox: image inside .attachment-chip is skipped ---

(function () {
  var img = mockElement('img', { src: 'thumb.jpg', _parentClasses: ['attachment-chip'] });
  var target = mockTarget([img]);
  decorateLightbox(target, {}, function () {});

  assert(
    'image inside .attachment-chip is skipped',
    !img._listeners.click || img._listeners.click.length === 0,
    'expected 0 click listeners'
  );
})();

// --- decorateLightbox: video link gets intercepted ---

(function () {
  var a = mockElement('a', { href: 'https://example.com/clip.mp4' });
  var target = mockTarget([a]);
  var calls = [];
  decorateLightbox(target, {}, function (src, type) { calls.push({ src: src, type: type }); });

  assert(
    'video link gets click handler',
    a._listeners.click && a._listeners.click.length === 1,
    'expected 1 click listener'
  );
  assert(
    'video link gets video-link class',
    a._classes.has('video-link'),
    'expected video-link class'
  );

  var prevented = false;
  a._listeners.click[0]({ preventDefault: function () { prevented = true; } });
  assert(
    'video link click calls openLightbox with video type',
    calls.length === 1 && calls[0].src === 'https://example.com/clip.mp4' && calls[0].type === 'video',
    'got: ' + JSON.stringify(calls)
  );
  assert(
    'video link click prevents default',
    prevented,
    'expected preventDefault to be called'
  );
})();

// --- decorateLightbox: streaming suppresses video link handlers ---

(function () {
  var a = mockElement('a', { href: 'https://example.com/clip.mp4' });
  var target = mockTarget([a]);
  decorateLightbox(target, { streaming: true }, function () {});

  assert(
    'streaming: video link gets no click handler',
    !a._listeners.click || a._listeners.click.length === 0,
    'expected 0 click listeners'
  );
  assert(
    'streaming: video link gets no video-link class',
    !a._classes.has('video-link'),
    'expected no video-link class'
  );
})();

// --- decorateLightbox: non-video link is not intercepted ---

(function () {
  var a = mockElement('a', { href: 'https://example.com/page.html' });
  var target = mockTarget([a]);
  decorateLightbox(target, {}, function () {});

  assert(
    'non-video link gets no click handler',
    !a._listeners.click || a._listeners.click.length === 0,
    'expected 0 click listeners'
  );
  assert(
    'non-video link gets no video-link class',
    !a._classes.has('video-link'),
    'expected no video-link class'
  );
})();

// --- decorateLightbox: linked image prevents default and stops propagation ---

(function () {
  var img = mockElement('img', { src: 'https://example.com/photo.jpg', _parentClasses: ['some-wrapper'] });
  var target = mockTarget([img]);
  var calls = [];
  decorateLightbox(target, {}, function (src, type) { calls.push({ src: src, type: type }); });

  var prevented = false;
  var stopped = false;
  img._listeners.click[0]({
    preventDefault: function () { prevented = true; },
    stopPropagation: function () { stopped = true; }
  });

  assert(
    'image click calls preventDefault',
    prevented,
    'expected preventDefault to be called'
  );
  assert(
    'image click calls stopPropagation',
    stopped,
    'expected stopPropagation to be called'
  );
  assert(
    'linked image still opens lightbox',
    calls.length === 1 && calls[0].src === 'https://example.com/photo.jpg',
    'got: ' + JSON.stringify(calls)
  );
})();

// --- decorateLightbox: null target is safe ---

(function () {
  var threw = false;
  try {
    decorateLightbox(null, {}, function () {});
  } catch (e) {
    threw = true;
  }
  assert('null target does not throw', !threw, 'unexpected exception');
})();

// --- Results ---

if (failures > 0) {
  console.error('\n' + failures + ' test(s) failed');
  process.exit(1);
} else {
  console.log('\nAll tests passed');
  process.exit(0);
}
