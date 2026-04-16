// decoration.js — lightbox/video decoration logic, extracted for testability.
//
// Browser: loaded as a <script> before app-render.js; sets window.TermLLMDecoration.
// Node.js: require('./decoration.js') returns the exports for use in tests.
(function (factory) {
  'use strict';
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = factory();
  } else {
    window.TermLLMDecoration = factory();
  }
})(function () {
  'use strict';

  var VIDEO_LINK_PATTERN = /\.(mp4|webm|mov|ogv|ogg)(\?[^#]*)?(#.*)?$/i;

  function decorateLightbox(target, options, openLightbox) {
    if (!target) return;
    var streaming = Boolean(options && options.streaming);

    target.querySelectorAll('a').forEach(function (a) {
      if (!streaming && VIDEO_LINK_PATTERN.test(a.href)) {
        a.addEventListener('click', function (e) {
          e.preventDefault();
          openLightbox(a.href, 'video');
        });
        a.classList.add('video-link');
      }
    });

    if (!streaming) {
      target.querySelectorAll('img').forEach(function (img) {
        if (img.closest('.deferred-video') || img.closest('.attachment-chip')) return;
        img.addEventListener('click', function (e) {
          e.preventDefault();
          e.stopPropagation();
          openLightbox(img.src);
        });
      });
    }
  }

  return { VIDEO_LINK_PATTERN: VIDEO_LINK_PATTERN, decorateLightbox: decorateLightbox };
});
