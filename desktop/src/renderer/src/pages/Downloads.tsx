import { useSignal } from '@preact/signals';
import type { JSX } from 'preact';
import { api, connected, sortedJobs, type Job } from '../api';
import { bytes, duration, host, kindLabel, relative } from '../format';
import { Badge, Button, Empty, IconButton, Progress } from '../components/ui';

type Filter = 'all' | 'active' | 'done' | 'failed';

function matches(j: Job, f: Filter): boolean {
  switch (f) {
    case 'active': return j.status === 'running' || j.status === 'queued' || j.status === 'paused';
    case 'done': return j.status === 'done';
    case 'failed': return j.status === 'error' || j.status === 'cancelled';
    default: return true;
  }
}

function details(j: Job): string {
  const bits: string[] = [];
  const pct = j.total > 0 ? Math.min(100, j.percent || (j.done / j.total) * 100) : null;
  if (pct != null) bits.push(`${pct.toFixed(pct < 10 ? 1 : 0)}%`);
  if (j.unit === 'segments' && j.total) bits.push(`${j.done}/${j.total} segments`);
  const sizeTotal = j.unit === 'bytes' ? j.total : j.estimate || 0;
  const approx = j.unit === 'bytes' ? '' : '≈';
  if (j.bytes) bits.push(bytes(j.bytes) + (sizeTotal ? ` of ${approx}${bytes(sizeTotal)}` : ''));
  else if (sizeTotal) bits.push(`${approx}${bytes(sizeTotal)}`);
  if (j.status === 'running' && j.speed > 0) {
    bits.push(`${bytes(j.speed)}/s`);
    if (sizeTotal > j.bytes) bits.push(`${duration((sizeTotal - j.bytes) / j.speed)} left`);
  }
  if (j.peers) bits.push(`${j.peers} peers`);
  if (j.detail) bits.push(j.detail);
  if (j.status === 'done') bits.push(relative(j.updated));
  return bits.join(' · ');
}

function Row({ job }: { job: Job }): JSX.Element {
  const active = job.status === 'running' || job.status === 'queued';
  const pct = job.total > 0 ? Math.min(100, job.percent || (job.done / job.total) * 100) : null;
  const act = (fn: () => Promise<unknown>) => () => fn().catch((e: Error) => alert(e.message));
  return (
    <li class={`job ${job.status}`}>
      <div class="job-head">
        <Badge kind={job.kind}>{kindLabel(job.kind)}</Badge>
        <span class="job-title" title={job.path || job.url}>{job.title || job.url}</span>
        <span class={`status ${job.status}`}>{job.status}</span>
      </div>
      <Progress value={pct} status={job.status} />
      <div class="job-foot">
        <span class="job-meta">{details(job)}</span>
        <span class="job-host">{host(job.url)}</span>
        <span class="spacer" />
        {active && <IconButton icon="pause" label="Pause" onClick={act(() => api.pause(job.id))} />}
        {(job.status === 'paused' || job.status === 'error' || job.status === 'cancelled') && <IconButton icon="play" label="Resume" onClick={act(() => api.resume(job.id))} />}
        {active && <IconButton icon="x" label="Cancel" onClick={act(() => api.cancel(job.id))} />}
        {job.status === 'done' && job.path && <IconButton icon="folder" label="Show in folder" onClick={() => window.vidhawk.showItem(job.path!)} />}
        {!active && <IconButton icon="trash" label="Remove from list" onClick={act(() => api.remove(job.id))} />}
      </div>
      {job.error && <p class="job-error">{job.error}</p>}
    </li>
  );
}

export function Downloads({ onAdd }: { onAdd: () => void }): JSX.Element {
  const filter = useSignal<Filter>('all');
  const list = sortedJobs.value.filter((j) => matches(j, filter.value));
  const finished = sortedJobs.value.filter((j) => !(j.status === 'running' || j.status === 'queued'));
  return (
    <section class="page">
      <header class="page-head">
        <h1>Downloads</h1>
        <div class="segmented" role="tablist" aria-label="Filter downloads">
          {(['all', 'active', 'done', 'failed'] as Filter[]).map((f) => (
            <button key={f} type="button" role="tab" aria-selected={filter.value === f} class={filter.value === f ? 'on' : ''} onClick={() => (filter.value = f)}>{f}</button>
          ))}
        </div>
        <span class="spacer" />
        {finished.length > 0 && <Button variant="ghost" icon="trash" onClick={() => finished.forEach((j) => api.remove(j.id).catch(() => {}))}>Clear finished</Button>}
        <Button variant="primary" icon="plus" onClick={onAdd} disabled={!connected.value}>Add download</Button>
      </header>
      {list.length === 0 ? (
        <Empty icon="download" title={filter.value === 'all' ? 'No downloads yet' : `No ${filter.value} downloads`} hint="Paste a link, drop a .torrent file, or catch a video with the browser extension." />
      ) : (
        <ul class="jobs">{list.map((j) => <Row key={j.id} job={j} />)}</ul>
      )}
    </section>
  );
}
