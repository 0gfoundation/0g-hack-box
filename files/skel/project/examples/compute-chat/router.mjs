// Tiny 0G Compute Router client using the built in fetch. Server side only: never ship the key to a browser.
import { readFileSync } from 'node:fs';
try { process.loadEnvFile(new URL('./.env', import.meta.url)); } catch {}

export const BASE = (process.env.OG_ROUTER_URL || 'https://router-api.0g.ai').replace(/\/+$/, '').replace(/\/v1$/, '') + '/v1';
export const MODEL = process.env.OG_MODEL || 'glm-5.3';

export function apiKey() {
  if (process.env.ZG_ROUTER_API_KEY) return process.env.ZG_ROUTER_API_KEY.trim();
  // Key files the hack box may provide for the attendee account.
  for (const f of ['/etc/opencode/0g-router.key', '/etc/hackbox/secrets/0g-router-key']) {
    try { const k = readFileSync(f, 'utf8').trim(); if (k) return k; } catch {}
  }
  throw new Error('No router key. Set ZG_ROUTER_API_KEY in .env (an sk- key from pc.0g.ai).');
}

export async function chat(messages, opts = {}) {
  const res = await fetch(`${BASE}/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey()}` },
    body: JSON.stringify({ model: MODEL, messages, ...opts }),
  });
  if (!res.ok) {
    const retry = res.headers.get('retry-after');
    throw new Error(`router ${res.status}${retry ? ` (retry after ${retry}s)` : ''}: ${await res.text()}`);
  }
  return res.json();
}
