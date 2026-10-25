import { spawn, type ChildProcess } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export interface Endpoint {
  port: number;
  token: string;
  pid: number;
}

const configDir = (): string => process.env.VIDHAWK_HOME || join(homedir(), '.vidhawk');

export function readEndpoint(): Endpoint | null {
  try {
    const ep = JSON.parse(readFileSync(join(configDir(), 'api.json'), 'utf8')) as Endpoint;
    return ep.port && ep.token ? ep : null;
  } catch {
    return null;
  }
}

async function healthy(ep: Endpoint): Promise<boolean> {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), 800);
    const r = await fetch(`http://127.0.0.1:${ep.port}/health`, { signal: ctrl.signal });
    clearTimeout(t);
    if (!r.ok) return false;
    const h = (await r.json()) as { ok?: boolean; pid?: number };
    return !!h.ok && h.pid === ep.pid;
  } catch {
    return false;
  }
}

// Runs `vidhawk serve` and keeps it alive; if another engine already owns the
// API (e.g. started by the browser extension), simply attaches to it.
export class Sidecar {
  endpoint: Endpoint | null = null;
  private proc: ChildProcess | null = null;
  private stopping = false;
  private backoff = 1000;
  private listeners: Array<(ep: Endpoint | null) => void> = [];
  private watchTimer: NodeJS.Timeout | null = null;

  constructor(public readonly exe: string) {}

  onEndpoint(fn: (ep: Endpoint | null) => void): void {
    this.listeners.push(fn);
  }

  private setEndpoint(ep: Endpoint | null): void {
    const changed = JSON.stringify(ep) !== JSON.stringify(this.endpoint);
    this.endpoint = ep;
    if (changed) for (const fn of this.listeners) fn(ep);
  }

  start(): void {
    this.stopping = false;
    if (!existsSync(this.exe)) {
      console.error(`[sidecar] engine binary not found at ${this.exe}`);
      this.watch();
      return;
    }
    const proc = spawn(this.exe, ['serve'], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
    this.proc = proc;
    proc.stdout?.on('data', (d: Buffer) => process.stdout.write(`[engine] ${d}`));
    proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[engine] ${d}`));
    proc.on('exit', (code) => {
      if (this.proc === proc) this.proc = null;
      if (this.stopping) return;
      // Exit code 1 right away usually means another engine is already running; the watcher will attach to it.
      console.warn(`[sidecar] engine exited with code ${code}; retrying in ${this.backoff}ms`);
      setTimeout(() => { if (!this.stopping && !this.proc) this.start(); }, this.backoff);
      this.backoff = Math.min(this.backoff * 2, 30000);
    });
    this.watch();
  }

  private watch(): void {
    if (this.watchTimer) return;
    const tick = async () => {
      const ep = readEndpoint();
      const ok = ep ? await healthy(ep) : false;
      this.setEndpoint(ok ? ep : null);
      if (ok) this.backoff = 1000;
      this.watchTimer = setTimeout(tick, ok ? 5000 : 700);
    };
    tick();
  }

  stop(): void {
    this.stopping = true;
    if (this.watchTimer) { clearTimeout(this.watchTimer); this.watchTimer = null; }
    this.proc?.kill();
    this.proc = null;
  }
}
