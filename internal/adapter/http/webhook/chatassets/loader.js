(function () {
  'use strict';

  // Embed with: <script src="https://HOST/chat/PATH/loader.js" async></script>
  var script = document.currentScript;
  if (!script || !script.src) { return; }
  var widgetURL = script.src.replace(/\/loader\.js(\?.*)?$/, '/widget');
  var label = script.getAttribute('data-label') || 'Chat';
  var side = script.getAttribute('data-side') === 'left' ? 'left' : 'right';

  var button = document.createElement('button');
  button.type = 'button';
  button.textContent = label;
  button.setAttribute('aria-expanded', 'false');
  var bs = button.style;
  bs.position = 'fixed'; bs.bottom = '16px'; bs[side] = '16px'; bs.zIndex = '2147483000';
  bs.padding = '12px 20px'; bs.border = '0'; bs.borderRadius = '999px'; bs.cursor = 'pointer';
  bs.font = '600 16px system-ui, sans-serif'; bs.background = '#2f5fd0'; bs.color = '#ffffff';
  bs.boxShadow = '0 4px 14px rgba(0,0,0,0.25)';

  var frame = null;
  function open() {
    if (!frame) {
      frame = document.createElement('iframe');
      frame.src = widgetURL;
      frame.title = label;
      var fs = frame.style;
      fs.position = 'fixed'; fs.bottom = '72px'; fs[side] = '16px'; fs.zIndex = '2147483000';
      fs.width = 'min(380px, calc(100vw - 32px))'; fs.height = 'min(600px, calc(100vh - 96px))';
      fs.border = '1px solid #c9ccd2'; fs.borderRadius = '12px'; fs.background = '#ffffff';
      fs.boxShadow = '0 8px 30px rgba(0,0,0,0.3)';
      document.body.appendChild(frame);
    }
    frame.style.display = 'block';
    button.setAttribute('aria-expanded', 'true');
  }
  function close() {
    if (frame) { frame.style.display = 'none'; }
    button.setAttribute('aria-expanded', 'false');
  }
  button.addEventListener('click', function () {
    if (button.getAttribute('aria-expanded') === 'true') { close(); } else { open(); }
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && button.getAttribute('aria-expanded') === 'true') { close(); button.focus(); }
  });
  document.body.appendChild(button);
})();
