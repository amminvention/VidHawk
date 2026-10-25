import type { ComponentChildren, JSX } from 'preact';

const PATHS: Record<string, string> = {
  download: 'M12 3v12m0 0 4-4m-4 4-4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2',
  pause: 'M8 5v14M16 5v14',
  play: 'm7 4 13 8-13 8V4Z',
  x: 'M6 6l12 12M18 6 6 18',
  folder: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
  check: 'm5 12 5 5L20 7',
  alert: 'M12 9v4m0 4h.01M10.3 3.9 2.6 17.2A2 2 0 0 0 4.3 20h15.4a2 2 0 0 0 1.7-2.8L13.7 3.9a2 2 0 0 0-3.4 0Z',
  plus: 'M12 5v14M5 12h14',
  list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  history: 'M3 12a9 9 0 1 0 3-6.7M3 4v5h5M12 7v5l3 2',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm7.4-3a7.4 7.4 0 0 0-.1-1l2-1.6-2-3.4-2.4 1a7.5 7.5 0 0 0-1.7-1l-.4-2.6H9.2L8.8 6a7.5 7.5 0 0 0-1.7 1l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 0 0 0 2l-2 1.6 2 3.4 2.4-1a7.5 7.5 0 0 0 1.7 1l.4 2.6h5.6l.4-2.6a7.5 7.5 0 0 0 1.7-1l2.4 1 2-3.4-2-1.6c.1-.3.1-.7.1-1Z',
  magnet: 'M6 3v8a6 6 0 0 0 12 0V3M6 3h4v8a2 2 0 0 0 4 0V3h4',
  refresh: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5',
  search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16Zm10 2-4.3-4.3',
  external: 'M14 4h6v6M20 4l-9 9M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5',
  film: 'M3 6a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6Zm4-2v16M17 4v16M3 9h4M3 15h4M17 9h4M17 15h4',
  hawk: '',
};

export function Icon({ name, size = 16, class: cls }: { name: keyof typeof PATHS; size?: number; class?: string }): JSX.Element {
  if (name === 'hawk') {
    return (
      <svg class={cls} width={size} height={size} viewBox="0 0 128 128" aria-hidden="true">
        <path fill="currentColor" d="M10 46 64 72l54-26-14 12-40 32-40-32Z" />
        <path fill="currentColor" d="M64 72 54 50h20Z" />
        <path fill="#fff" d="M58 86h12v16H58zM44 100h40l-20 20z" />
      </svg>
    );
  }
  return (
    <svg class={cls} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d={PATHS[name]} />
    </svg>
  );
}

type ButtonProps = JSX.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'ghost' | 'danger' | 'default'; icon?: keyof typeof PATHS; children?: ComponentChildren };

export function Button({ variant = 'default', icon, children, class: cls, ...rest }: ButtonProps): JSX.Element {
  return (
    <button type="button" class={`btn ${variant} ${cls || ''}`} {...rest}>
      {icon && <Icon name={icon} />}
      {children && <span>{children}</span>}
    </button>
  );
}

export function IconButton({ icon, label, ...rest }: JSX.ButtonHTMLAttributes<HTMLButtonElement> & { icon: keyof typeof PATHS; label: string }): JSX.Element {
  return (
    <button type="button" class="icon-btn" title={label} aria-label={label} {...rest}>
      <Icon name={icon} />
    </button>
  );
}

export function Badge({ kind, children }: { kind: string; children: ComponentChildren }): JSX.Element {
  return <span class={`badge ${kind}`}>{children}</span>;
}

export function Progress({ value, status }: { value: number | null; status: string }): JSX.Element {
  const indeterminate = value == null && status === 'running';
  return (
    <div class={`bar ${status} ${indeterminate ? 'indeterminate' : ''}`} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={value == null ? undefined : Math.round(value)}>
      <div class="fill" style={{ width: `${value ?? (status === 'done' ? 100 : 0)}%` }} />
    </div>
  );
}

export function Empty({ icon, title, hint }: { icon: keyof typeof PATHS; title: string; hint?: string }): JSX.Element {
  return (
    <div class="empty">
      <Icon name={icon} size={28} />
      <p class="empty-title">{title}</p>
      {hint && <p class="empty-hint">{hint}</p>}
    </div>
  );
}
