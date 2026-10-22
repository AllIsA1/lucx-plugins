// Copyright (c) 2026 LucX-UI Project.
// SPDX-License-Identifier: MIT

var out = document.getElementById('out');

function show(context) {
  lucx.applyTheme();
  document.getElementById('plugin').textContent = context.pluginId;
  document.getElementById('theme').textContent = context.theme.dark ? 'dark' : 'light';
  document.getElementById('lang').textContent = context.lang;
}

function call(path, options) {
  lucx.api(path, options).then(function (res) {
    out.textContent = res.status + ' ' + JSON.stringify(res.body !== undefined ? res.body : res.error, null, 2);
  });
}

document.getElementById('greet').onclick = function () {
  call('/api/hello');
};
document.getElementById('count').onclick = function () {
  call('/api/counter', { method: 'POST' });
};
document.getElementById('clients').onclick = function () {
  call('/api/clients');
};

lucx.ready().then(show);
lucx.onContext(show);
