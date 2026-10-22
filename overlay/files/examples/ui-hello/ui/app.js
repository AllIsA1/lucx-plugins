// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

function show(context) {
  lucx.applyTheme();
  document.getElementById('plugin').textContent = context.pluginId;
  document.getElementById('theme').textContent =
    (context.theme.dark ? 'dark' : 'light') + (context.theme.ultra ? ' (ultra)' : '') + ', palette ' + context.theme.palette;
  document.getElementById('lang').textContent = context.lang;
}

lucx.ready().then(show);
lucx.onContext(show);
