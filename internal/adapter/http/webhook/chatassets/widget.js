(function () {
  'use strict';

  var root = document.getElementById('chat');
  if (!root) { return; }
  var log = document.getElementById('chat-log');
  var form = document.getElementById('chat-form');
  var input = document.getElementById('chat-input');
  var send = document.getElementById('chat-send');
  var texts = {
    pending: root.getAttribute('data-pending') || '...',
    error: root.getAttribute('data-error') || 'Something went wrong. Please try again.'
  };

  // The widget is served at <chat endpoint>/widget; messages go to the endpoint.
  var endpoint = window.location.pathname.replace(/\/widget\/?$/, '');
  var storageKey = 'engi-chat:' + endpoint;
  var sessionId = readSession();
  var busy = false;

  function readSession() {
    try { return window.sessionStorage.getItem(storageKey) || ''; } catch (e) { return ''; }
  }

  function saveSession(id) {
    sessionId = id;
    try { window.sessionStorage.setItem(storageKey, id); } catch (e) { /* storage unavailable */ }
  }

  function add(kind, text) {
    var el = document.createElement('div');
    el.className = 'msg msg-' + kind;
    if (kind === 'error') { el.setAttribute('role', 'alert'); }
    el.textContent = text;
    log.appendChild(el);
    log.scrollTop = log.scrollHeight;
    return el;
  }

  function setBusy(on) {
    busy = on;
    send.disabled = on;
    input.setAttribute('aria-busy', on ? 'true' : 'false');
  }

  function problemText(res, body) {
    if (body && typeof body.detail === 'string' && body.detail) { return body.detail; }
    if (body && typeof body.title === 'string' && body.title) { return body.title; }
    return texts.error;
  }

  function submit() {
    var message = input.value.trim();
    if (!message || busy) { return; }
    input.value = '';
    add('user', message);
    var pending = add('pending', texts.pending);
    setBusy(true);
    var payload = { message: message };
    if (sessionId) { payload.sessionId = sessionId; }
    fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).then(function (res) {
      return res.text().then(function (text) {
        var body = null;
        try { body = JSON.parse(text); } catch (e) { /* not JSON */ }
        return { res: res, body: body, text: text };
      });
    }).then(function (r) {
      pending.remove();
      if (r.body && typeof r.body.sessionId === 'string') { saveSession(r.body.sessionId); }
      if (r.res.ok && r.body && typeof r.body.output === 'string') {
        add('bot', r.body.output);
      } else if (r.res.ok && r.body === null && r.text) {
        add('bot', r.text);
      } else {
        add('error', problemText(r.res, r.body));
      }
    }).catch(function () {
      pending.remove();
      add('error', texts.error);
    }).then(function () {
      setBusy(false);
      input.focus();
    });
  }

  form.addEventListener('submit', function (e) { e.preventDefault(); submit(); });
  input.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); submit(); }
  });
  input.focus();
})();
