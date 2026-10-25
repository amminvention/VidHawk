import { useSignal } from '@preact/signals';
import type { JSX } from 'preact';
import { useEffect } from 'preact/hooks';
import { activeCount, connected, endpointState, engine, setEndpoint } from './api';
import { Icon } from './components/ui';
import { Add } from './pages/Add';
import { Downloads } from './pages/Downloads';
import { Settings } from './pages/Settings';

type Page = 'downloads' | 'add' | 'settings';

export function App(): JSX.Element {
  const page = useSignal<Page>('downloads');
  const prefill = useSignal('');

  useEffect(() => {
    window.vidhawk.endpoint().then(setEndpoint);
    const offEp = window.vidhawk.onEndpoint(setEndpoint);
    const offAdd = window.vidhawk.onAddUrl((url) => { prefill.value = url; page.value = 'add'; });
    return () => { offEp(); offAdd(); };
  }, []);

  const nav: Array<{ id: Page; label: string; icon: 'list' | 'plus' | 'settings' }> = [
    { id: 'downloads', label: 'Downloads', icon: 'list' },
    { id: 'add', label: 'Add', icon: 'plus' },
    { id: 'settings', label: 'Settings', icon: 'settings' },
  ];

  return (
    <div class="shell">
      <aside class="sidebar">
        <div class="brand"><Icon name="hawk" size={26} class="brand-mark" /><span>VidHawk</span></div>
        <nav aria-label="Main">
          {nav.map((n) => (
            <button key={n.id} type="button" class={page.value === n.id ? 'on' : ''} aria-current={page.value === n.id ? 'page' : undefined} onClick={() => (page.value = n.id)}>
              <Icon name={n.icon} />
              <span>{n.label}</span>
              {n.id === 'downloads' && activeCount.value > 0 && <span class="pill">{activeCount.value}</span>}
            </button>
          ))}
        </nav>
        <div class="spacer" />
        <div class={`engine-status ${connected.value ? 'ok' : endpointState.value ? 'warn' : 'err'}`} title={connected.value ? 'Connected to the engine' : 'Starting engine…'}>
          <span class="dot" />
          <span>{connected.value ? `Engine ${engine.value?.version || ''}` : endpointState.value ? 'Reconnecting…' : 'Starting engine…'}</span>
        </div>
      </aside>
      <main class="content">
        {page.value === 'downloads' && <Downloads onAdd={() => (page.value = 'add')} />}
        {page.value === 'add' && <Add initialUrl={prefill.value} onDone={() => { prefill.value = ''; page.value = 'downloads'; }} />}
        {page.value === 'settings' && <Settings />}
      </main>
    </div>
  );
}
