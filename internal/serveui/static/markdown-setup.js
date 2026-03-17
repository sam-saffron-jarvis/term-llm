// markdown-setup.js — marked configuration shared between the browser and Node tests.
//
// Browser: loaded as a <script> after marked.umd.min.js; calls setupMarkdown(marked) immediately.
// Node.js: require('./markdown-setup.js') returns setupMarkdown for use in tests.
(function (factory) {
  'use strict';
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = factory;
  } else {
    factory(marked); // eslint-disable-line no-undef
  }
})(function setupMarkdown(marked) {
  marked.use({
    breaks: true,
    gfm: true
  });

  // Disable single-tilde strikethrough. GFM's del rule matches both ~text~
  // and ~~text~~, but a bare ~ is far too common in LLM output (~$100,
  // ~200ms, ~5x). Keep ~~double-tilde~~ as intentional strikethrough;
  // convert single-tilde del tokens back to raw text.
  marked.use({
    walkTokens(token) {
      if (token.type === 'del' && !token.raw.startsWith('~~')) {
        token.type = 'text';
        token.text = token.raw;
        delete token.tokens;
      }
    }
  });
});
