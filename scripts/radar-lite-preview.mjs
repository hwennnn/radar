#!/usr/bin/env node

import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const defaultWebRoot = path.join(repoRoot, 'cmd', 'radar-lite', 'web');
const staticRoutes = new Map([
  ['/', 'index.html'],
  ['/index.html', 'index.html'],
  ['/jobs', 'index.html'],
  ['/companies', 'index.html'],
  ['/system', 'index.html'],
  ['/docs', 'docs.html'],
  ['/docs.html', 'docs.html'],
  ['/app.js', 'app.js'],
  ['/docs.js', 'docs.js'],
  ['/styles.css', 'styles.css'],
]);

const contentTypes = new Map([
  ['.css', 'text/css; charset=utf-8'],
  ['.html', 'text/html; charset=utf-8'],
  ['.js', 'text/javascript; charset=utf-8'],
]);

export function resolvePreviewAsset(pathname) {
  return staticRoutes.get(pathname) ?? null;
}

export function isPreviewProxyPath(pathname) {
  return pathname.startsWith('/api/') || pathname === '/healthz' || pathname === '/readyz';
}

function sendJSON(response, status, payload) {
  const body = Buffer.from(JSON.stringify(payload));
  response.writeHead(status, {
    'cache-control': 'no-store',
    'content-length': body.length,
    'content-type': 'application/json; charset=utf-8',
  });
  response.end(body);
}

async function proxyRequest(request, response, requestURL, upstream) {
  if (request.method !== 'GET' && request.method !== 'HEAD') {
    response.writeHead(405, { allow: 'GET, HEAD' });
    response.end();
    return;
  }

  try {
    const target = new URL(`${requestURL.pathname}${requestURL.search}`, upstream);
    const upstreamResponse = await fetch(target, {
      headers: { accept: request.headers.accept ?? '*/*' },
      method: request.method,
      redirect: 'follow',
      signal: AbortSignal.timeout(15_000),
    });
    const body = request.method === 'HEAD'
      ? Buffer.alloc(0)
      : Buffer.from(await upstreamResponse.arrayBuffer());
    const headers = {
      'cache-control': 'no-store',
      'content-length': body.length,
      'content-type': upstreamResponse.headers.get('content-type') ?? 'application/octet-stream',
    };
    response.writeHead(upstreamResponse.status, headers);
    response.end(body);
  } catch (error) {
    sendJSON(response, 502, {
      error: 'preview upstream unavailable',
      detail: error instanceof Error ? error.message : String(error),
    });
  }
}

export function createPreviewServer({
  upstream = 'https://radar.hwendev.com',
  webRoot = defaultWebRoot,
} = {}) {
  return createServer(async (request, response) => {
    const requestURL = new URL(request.url ?? '/', `http://${request.headers.host ?? 'localhost'}`);

    if (requestURL.pathname === '/previewz') {
      sendJSON(response, 200, { ready: true, upstream });
      return;
    }
    if (isPreviewProxyPath(requestURL.pathname)) {
      await proxyRequest(request, response, requestURL, upstream);
      return;
    }
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      response.writeHead(405, { allow: 'GET, HEAD' });
      response.end();
      return;
    }

    const asset = resolvePreviewAsset(requestURL.pathname);
    if (!asset) {
      sendJSON(response, 404, { error: 'not found' });
      return;
    }

    try {
      const body = request.method === 'HEAD'
        ? Buffer.alloc(0)
        : await readFile(path.join(webRoot, asset));
      response.writeHead(200, {
        'cache-control': 'no-store',
        'content-length': body.length,
        'content-type': contentTypes.get(path.extname(asset)) ?? 'application/octet-stream',
      });
      response.end(body);
    } catch (error) {
      sendJSON(response, 500, {
        error: 'preview asset unavailable',
        detail: error instanceof Error ? error.message : String(error),
      });
    }
  });
}

function startPreview() {
  const host = process.env.RADAR_LITE_PREVIEW_HOST || '127.0.0.1';
  const port = Number.parseInt(process.env.RADAR_LITE_PREVIEW_PORT || '8789', 10);
  const upstream = process.env.RADAR_LITE_PREVIEW_UPSTREAM || 'https://radar.hwendev.com';
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new Error('RADAR_LITE_PREVIEW_PORT must be a valid TCP port');
  }

  const server = createPreviewServer({ upstream });
  server.on('error', error => {
    if (error && error.code === 'EADDRINUSE') {
      console.error(`radar lite preview: ${host}:${port} is already in use`);
    } else {
      console.error('radar lite preview failed:', error);
    }
    process.exitCode = 1;
  });
  server.listen(port, host, () => {
    console.log(`radar lite preview: http://${host}:${port}`);
    console.log(`radar lite preview: API upstream ${upstream}`);
  });

  const shutdown = () => server.close(() => process.exit(0));
  process.once('SIGINT', shutdown);
  process.once('SIGTERM', shutdown);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  startPreview();
}
