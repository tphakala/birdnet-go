import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { flushSync } from 'svelte';
import { t } from '$lib/i18n';
import { navigation } from '$lib/stores/navigation.svelte';
import { setCsrfToken } from '$lib/stores/appState.svelte';
import { connectionState } from '$lib/stores/connectionState.svelte';
import System from './System.svelte';

const SLOW_REFRESH_MS = 30_000;
const OVERVIEW_PATH = '/ui/system';
const DATABASE_PATH = '/ui/system/database';

const INFO_URL = '/api/v2/system/info';
const DISKS_URL = '/api/v2/system/disks';
const PROCESSES_URL = '/api/v2/system/processes';
const ALL_PROCESSES_URL = `${PROCESSES_URL}?all=true`;
const SLOW_URLS = [INFO_URL, DISKS_URL, PROCESSES_URL];

// jsdom has no EventSource; count what the view opens and closes
const stream = vi.hoisted(() => ({ opened: 0, closed: 0 }));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class {
    constructor() {
      stream.opened++;
    }
    addEventListener() {}
    close() {
      stream.closed++;
    }
  },
}));

// The other subpages are not under test and each brings its own requests or streams
vi.mock('$lib/desktop/components/database', () => ({ DatabaseDashboard: () => {} }));
vi.mock('$lib/desktop/features/system/TerminalPage.svelte', () => ({ default: () => {} }));
vi.mock('$lib/desktop/views/SystemInference.svelte', () => ({ default: () => {} }));
vi.mock('$lib/desktop/features/import-export/pages/ImportExportPage.svelte', () => ({
  default: () => {},
}));

const defaultBodies: Array<[string, unknown]> = [
  [
    ALL_PROCESSES_URL,
    [{ pid: 7, name: 'sshd', status: 'sleep', cpu: 1, memory: 2048, uptime: 90 }],
  ],
  [PROCESSES_URL, [{ pid: 1, name: 'main', status: 'running', cpu: 2, memory: 4096, uptime: 60 }]],
  [INFO_URL, { os_display: 'Linux', hostname: 'host', uptime_seconds: 100, num_cpu: 4 }],
  [DISKS_URL, []],
  [
    '/api/v2/system/resources',
    { cpu_usage_percent: 1, memory_total: 100, memory_usage_percent: 10 },
  ],
  ['/api/v2/system/temperature/cpu', { is_available: false }],
  ['/api/v2/settings/birdnet', { overlap: 0 }],
  ['/api/v2/system/models', []],
  ['/api/v2/system/metrics/history', { metrics: {} }],
];

let requests: string[] = [];
let held = new Map<string, Promise<void>>();
let failures = new Map<string, number[]>();

/** Requests made so far to a URL, counting a path with a query as its own URL. */
function count(url: string): number {
  return requests.filter(request => request === url).length;
}

function counts(): number[] {
  return SLOW_URLS.map(count);
}

/** Holds every answer for a URL until the returned function is called. */
function hold(url: string): () => void {
  let release = () => {};
  held.set(
    url,
    new Promise<void>(resolve => {
      release = resolve;
    })
  );
  return () => {
    held.delete(url);
    release();
  };
}

/** Answers the next requests to a URL with these HTTP statuses, in order. */
function failWith(url: string, ...statuses: number[]) {
  failures.set(url, statuses);
}

async function answer(input: string): Promise<Response> {
  const url = input.replace(/^.*(?=\/api\/)/, '');
  requests.push(url);
  const gate = held.get(url);
  if (gate) await gate;
  const status = failures.get(url)?.shift();
  if (status && status >= 400) {
    return new Response(JSON.stringify({ error: 'failed' }), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  }
  const body = defaultBodies.find(([prefix]) => url.startsWith(prefix))?.[1] ?? {};
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** Lets pending promises and zero-delay timers run; waitFor would hang under fake timers. */
async function flush() {
  await vi.advanceTimersByTimeAsync(0);
}

async function advance(ms: number) {
  await vi.advanceTimersByTimeAsync(ms);
}

async function goTo(path: string) {
  navigation.navigate(path);
  flushSync();
  await flush();
}

async function mountAt(path: string) {
  navigation.navigate(path);
  const view = render(System);
  await flush();
  return view;
}

describe('System view', () => {
  const originalFetch = vi.mocked(fetch).getMockImplementation();

  beforeEach(() => {
    vi.useFakeTimers();
    requests = [];
    held = new Map();
    failures = new Map();
    stream.opened = 0;
    stream.closed = 0;
    navigation.navigate(OVERVIEW_PATH);
    setCsrfToken('token-1');
    vi.mocked(fetch).mockImplementation(input => answer(String(input)));
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.mocked(fetch).mockReset();
    if (originalFetch) vi.mocked(fetch).mockImplementation(originalFetch);
    connectionState.isOnline = true;
  });

  it('shows the labelled loading state until the process list answers, then on a Show All switch', async () => {
    const releaseProcesses = hold(PROCESSES_URL);
    await mountAt(OVERVIEW_PATH);

    const label = t('system.processInfo.loading');
    expect(screen.getByText(label).closest('[role="status"]')).not.toBeNull();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();

    releaseProcesses();
    await flush();
    expect(screen.queryByText(label)).not.toBeInTheDocument();
    expect(screen.getByRole('table')).toHaveTextContent('BirdNET-Go');

    const releaseAll = hold(ALL_PROCESSES_URL);
    await fireEvent.click(screen.getByRole('checkbox', { name: t('system.processInfo.showAll') }));
    await flush();
    expect(screen.getByText(label).closest('[role="status"]')).not.toBeNull();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();

    releaseAll();
    await flush();
    expect(screen.getByRole('table')).toHaveTextContent('sshd');
    expect(screen.getByRole('table')).not.toHaveTextContent('BirdNET-Go');
  });

  it('shows a failed process load with Retry, and Retry loads again', async () => {
    failWith(PROCESSES_URL, 500);
    await mountAt(OVERVIEW_PATH);

    const alert = screen.getByRole('alert');
    expect(t).toHaveBeenCalledWith('system.errors.processes', {
      error: 'errors.api.serverError',
    });
    expect(count(PROCESSES_URL)).toBe(1);

    await fireEvent.click(within(alert).getByRole('button', { name: t('common.retry') }));
    await flush();

    expect(count(PROCESSES_URL)).toBe(2);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('table')).toHaveTextContent('BirdNET-Go');
  });

  it('refreshes system info, disks and processes every 30 s on the overview only', async () => {
    await mountAt(DATABASE_PATH);
    // The mount load runs on every subpage
    expect(counts()).toEqual([1, 1, 1]);
    await advance(SLOW_REFRESH_MS);
    expect(counts()).toEqual([1, 1, 1]);

    await goTo(OVERVIEW_PATH);
    await advance(SLOW_REFRESH_MS - 1);
    expect(counts()).toEqual([1, 1, 1]);
    await advance(1);
    expect(counts()).toEqual([2, 2, 2]);

    await goTo(DATABASE_PATH);
    await advance(SLOW_REFRESH_MS);
    expect(counts()).toEqual([2, 2, 2]);

    await goTo(OVERVIEW_PATH);
    await advance(SLOW_REFRESH_MS);
    expect(counts()).toEqual([3, 3, 3]);
  });

  it('a refresh in flight when the subpage changes does not keep polling there', async () => {
    await mountAt(OVERVIEW_PATH);
    const release = hold(PROCESSES_URL);
    await advance(SLOW_REFRESH_MS);
    expect(counts()).toEqual([2, 2, 2]);

    await goTo(DATABASE_PATH);
    release();
    await flush();
    await advance(SLOW_REFRESH_MS);

    expect(counts()).toEqual([2, 2, 2]);
  });

  it('leaving and returning while a refresh is in flight leaves one loop', async () => {
    await mountAt(OVERVIEW_PATH);

    for (let round = 0; round < 3; round++) {
      const release = hold(PROCESSES_URL);
      await advance(SLOW_REFRESH_MS);
      await goTo(DATABASE_PATH);
      await goTo(OVERVIEW_PATH);
      release();
      await flush();
    }

    for (let interval = 0; interval < 2; interval++) {
      const before = counts();
      await advance(SLOW_REFRESH_MS);
      expect(counts()).toEqual(before.map(n => n + 1));
    }
  });

  it('unmounting with a refresh in flight stops the loop', async () => {
    const view = await mountAt(OVERVIEW_PATH);
    const release = hold(PROCESSES_URL);
    await advance(SLOW_REFRESH_MS);

    view.unmount();
    release();
    await flush();
    const before = counts();
    await advance(SLOW_REFRESH_MS * 2);

    expect(counts()).toEqual(before);
  });

  it('skips requests while offline and resumes when back online', async () => {
    await mountAt(OVERVIEW_PATH);
    // Every answered request marks the app online, so go offline only after the mount settled
    connectionState.isOnline = false;

    await advance(SLOW_REFRESH_MS);
    expect(counts()).toEqual([1, 1, 1]);

    connectionState.isOnline = true;
    await advance(SLOW_REFRESH_MS);
    expect(counts()).toEqual([2, 2, 2]);
  });

  it('a CSRF token change does not reload the page or reconnect the metrics stream', async () => {
    await mountAt(OVERVIEW_PATH);
    expect(count(INFO_URL)).toBe(1);
    expect(stream.opened).toBe(1);

    setCsrfToken('token-2');
    flushSync();
    await flush();

    expect(count(INFO_URL)).toBe(1);
    expect(stream.opened).toBe(1);
    expect(stream.closed).toBe(0);
  });
});
