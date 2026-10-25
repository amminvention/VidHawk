// The renderer talks to the engine only through its local REST + WebSocket
// API, so this UI can be hosted by Electron, Tauri or a plain browser tab.
import { computed, signal } from '@preact/signals';

export interface Job {
  id: string;
  kind: string;
  url: string;
  title: string;
  quality?: string;
  pageUrl?: string;
  outDir: string;
  status: 'queued' | 'running' | 'paused' | 'done' | 'error' | 'cancelled' | 'removed';
  done: number;
  total: number;
  unit: 'bytes' | 'segments';
  bytes: number;
  speed: number;
  percent: number;
  detail?: string;
  peers?: number;
  path?: string;
  error?: string;
  created: number;
  updated: number;
  expectedSize?: number;
  estimate?: number;
}

export interface EngineInfo {
  version: string;
  ffmpeg: boolean;
  ytdlp: boolean;
  torrent: boolean;
  downloadDir: string;
  pid: number;
}

export interface Config {
  downloadDir: string;
  connections: number;
  segmentConcurrency: number;
  maxJobs: number;
  ffmpegPath: string;
  ytDlpPath: string;
  userAgent: string;
  verifyTLS: boolean;
  torrent: { listenPort: number; sequential: boolean; seedAfterComplete: boolean };
}

export interface Quality {
  label: string;
  height?: number;
  width?: number;
  bandwidth?: number;
  codecs?: string;
  url?: string;
}

export interface ProbeResult {
  kind: string;
  finalUrl?: string;
  master?: boolean;
  variants?: Quality[];
  audio?: { name?: string; language?: string; bandwidth?: number }[];
  segments?: number;
  duration?: number;
  encrypted?: boolean;
  drm?: boolean;
  live?: boolean;
  size?: number;
  contentType?: string;
  filename?: string;
  acceptRanges?: boolean;
}

export interface Spec {
  url: string;
  kind?: string;
  title?: string;
  quality?: string;
  pageUrl?: string;
  outDir?: string;
  headers?: Record<string, string>;
}

export interface Endpoint {
  port: number;
  token: string;
}

export interface Integration {
  browsers: Record<string, boolean>;
  manifest: string;
  extensionId: string;
}

export const jobs = signal<Record<string, Job>>({});
export const engine = signal<EngineInfo | null>(null);
export const config = signal<Config | null>(null);
export const connected = signal(false);
export const endpointState = signal<Endpoint | null>(null);

const isActive = (j: Job) => j.status === 'running' || j.status === 'queued';
export const sortedJobs = computed(() =>
  Object.values(jobs.value).sort((a, b) => Number(isActive(b)) - Number(isActive(a)) || b.created - a.created),
);
export const activeCount = computed(() => Object.values(jobs.value).filter(isActive).length);

let ws: WebSocket | null = null;
let reconnect: ReturnType<typeof setTimeout> | null = null;

export function setEndpoint(ep: Endpoint | null): void {
  endpointState.value = ep;
  connectWs();
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const ep = endpointState.value;
  if (!ep) throw new Error('The engine is not running yet.');
  const r = await fetch(`http://127.0.0.1:${ep.port}${path}`, {
    method,
    headers: { Authorization: `Bearer ${ep.token}`, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await r.text();
  const data = text ? JSON.parse(text) : null;
  if (!r.ok) throw new Error((data && data.error) || r.statusText);
  return data as T;
}

export const api = {
  submit: (spec: Spec) => req<Job>('POST', '/jobs', spec),
  probe: (url: string, headers: Record<string, string> = {}) => req<ProbeResult>('POST', '/probe', { url, headers }),
  cancel: (id: string) => req<Job>('POST', `/jobs/${id}/cancel`),
  pause: (id: string) => req<Job>('POST', `/jobs/${id}/pause`),
  resume: (id: string) => req<Job>('POST', `/jobs/${id}/resume`),
  remove: (id: string, deleteFile = false) => req<unknown>('DELETE', `/jobs/${id}${deleteFile ? '?file=1' : ''}`),
  getConfig: () => req<Config>('GET', '/config'),
  updateConfig: (patch: Record<string, unknown>) => req<Config>('PUT', '/config', patch),
  open: (path: string) => req<unknown>('POST', '/open', { path }),
  integration: () => req<Integration>('GET', '/integration'),
  installIntegration: () => req<{ installed: string[]; browsers: Record<string, boolean> }>('POST', '/integration'),
};

function connectWs(): void {
  if (reconnect) { clearTimeout(reconnect); reconnect = null; }
  if (ws) { const old = ws; ws = null; old.onclose = null; old.close(); }
  const ep = endpointState.value;
  if (!ep) { connected.value = false; return; }
  const sock = new WebSocket(`ws://127.0.0.1:${ep.port}/ws?token=${encodeURIComponent(ep.token)}`);
  ws = sock;
  sock.onopen = () => { connected.value = true; };
  sock.onmessage = (e) => {
    const m = JSON.parse(e.data as string);
    if (m.event === 'hello') {
      engine.value = m.engine;
      config.value = m.config;
      const map: Record<string, Job> = {};
      for (const j of (m.jobs || []) as Job[]) map[j.id] = j;
      jobs.value = map;
    } else if (m.event === 'job' && m.job) {
      const next = { ...jobs.value };
      if (m.job.status === 'removed') delete next[m.job.id];
      else next[m.job.id] = m.job;
      jobs.value = next;
    }
  };
  sock.onclose = () => {
    if (ws !== sock) return;
    connected.value = false;
    reconnect = setTimeout(connectWs, 1500);
  };
  sock.onerror = () => sock.close();
}
