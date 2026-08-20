import assert from 'node:assert/strict';
import { access } from 'node:fs/promises';
import test from 'node:test';

import { defaultWebRoot, isPreviewProxyPath, resolvePreviewAsset } from './preview.mjs';

test('serves assets from the dashboard package', async () => {
  await access(`${defaultWebRoot}/index.html`);
  await access(`${defaultWebRoot}/app.js`);
  await access(`${defaultWebRoot}/styles.css`);
});

test('maps only the dashboard assets and document routes', () => {
  assert.equal(resolvePreviewAsset('/'), 'index.html');
  assert.equal(resolvePreviewAsset('/jobs'), 'index.html');
  assert.equal(resolvePreviewAsset('/companies'), 'index.html');
  assert.equal(resolvePreviewAsset('/system'), 'index.html');
  assert.equal(resolvePreviewAsset('/docs'), 'docs.html');
  assert.equal(resolvePreviewAsset('/styles.css'), 'styles.css');
  assert.equal(resolvePreviewAsset('/../../.env'), null);
  assert.equal(resolvePreviewAsset('/unknown'), null);
});

test('proxies only the read-only dashboard API and health routes', () => {
  assert.equal(isPreviewProxyPath('/api/jobs'), true);
  assert.equal(isPreviewProxyPath('/api/status'), true);
  assert.equal(isPreviewProxyPath('/healthz'), true);
  assert.equal(isPreviewProxyPath('/readyz'), true);
  assert.equal(isPreviewProxyPath('/docs'), false);
  assert.equal(isPreviewProxyPath('/apiary'), false);
});
