import { describe, it, expect, afterEach, vi } from 'vitest';
import { cleanup, render, screen, within, fireEvent, waitFor } from '@testing-library/svelte';
import type { ComponentProps } from 'svelte';
import { t } from '$lib/i18n';
import type { StatusVariant } from '$lib/desktop/components/ui/StatusPill.svelte';
import type { ProcessInfo } from '../types';
import SystemProcessTable, { processStatusVariant } from './SystemProcessTable.svelte';

const APP_NAME = 'BirdNET-Go';

function proc(overrides: Partial<ProcessInfo> & Pick<ProcessInfo, 'pid' | 'name'>): ProcessInfo {
  return { status: 'sleep', cpu: 0, memory: 1024, uptime: 60, ...overrides };
}

const rows: ProcessInfo[] = [
  proc({ pid: 10, name: 'systemd', cpu: 0.5 }),
  proc({ pid: 20, name: 'main', status: 'running', cpu: 12.5 }),
  proc({ pid: 30, name: 'ffmpeg', cpu: 3 }),
];

type TableProps = Partial<ComponentProps<typeof SystemProcessTable>>;

function tableProps(props: TableProps = {}) {
  return {
    processes: rows,
    loading: false,
    error: null,
    showAllProcesses: false,
    processName: APP_NAME,
    onShowAllChange: () => {},
    onRetry: () => Promise.resolve(),
    ...props,
  };
}

function renderTable(props: TableProps = {}) {
  return render(SystemProcessTable, { props: tableProps(props) });
}

/** Process names in row order, read from the first cell of each body row. */
function shownNames(): string[] {
  const table = screen.getByRole('table');
  return within(table)
    .getAllByRole('row')
    .slice(1)
    .map(row => within(row).getAllByRole('cell')[0].textContent.trim());
}

describe('SystemProcessTable', () => {
  afterEach(() => {
    cleanup();
  });

  it('sorts by CPU descending by default and shows the app name for main', () => {
    renderTable();

    const names = shownNames();
    expect(names[0]).toContain(APP_NAME);
    expect(names[0]).not.toContain('main');
    expect(names[1]).toContain('ffmpeg');
    expect(names[2]).toContain('systemd');
  });

  it.each<[string, StatusVariant]>([
    ['running', 'success'],
    ['sleep', 'neutral'],
    ['sleeping', 'neutral'],
    ['idle', 'neutral'],
    ['stop', 'warning'],
    ['blocked', 'warning'],
    ['wait', 'warning'],
    ['lock', 'warning'],
    ['zombie', 'error'],
    ['unknown', 'neutral'],
    ['', 'neutral'],
    ['some-future-state', 'neutral'],
  ])('maps process state %j to the %s pill', (status, variant) => {
    expect(processStatusVariant(status)).toBe(variant);
  });

  it('shows the raw process state word in the pill', () => {
    renderTable({ processes: [proc({ pid: 1, name: 'init', status: 'zombie' })] });

    expect(within(screen.getByRole('table')).getByText('zombie')).toBeInTheDocument();
  });

  it('shows loading, empty and failed as three different states', () => {
    const noProcesses = t('system.processInfo.noProcesses');

    const loading = renderTable({ processes: [], loading: true });
    expect(screen.getByRole('status', { name: t('common.loading') })).toBeInTheDocument();
    expect(screen.queryByText(noProcesses)).not.toBeInTheDocument();
    loading.unmount();

    const empty = renderTable({ processes: [] });
    expect(screen.getByText(noProcesses)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    empty.unmount();

    renderTable({ processes: [], error: 'Service unavailable' });
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(t('system.errors.processes', { error: 'Service unavailable' }));
    expect(within(alert).getByRole('button', { name: t('common.retry') })).toBeInTheDocument();
    expect(screen.queryByText(noProcesses)).not.toBeInTheDocument();
  });

  it('shows a failed refresh above the rows it already has', async () => {
    const onRetry = vi.fn(() => Promise.resolve());
    renderTable({ error: 'Service unavailable', onRetry });

    const alert = screen.getByRole('alert');
    const table = screen.getByRole('table');
    expect(alert.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(shownNames()).toHaveLength(rows.length);

    await fireEvent.click(within(alert).getByRole('button', { name: t('common.retry') }));
    await waitFor(() => expect(onRetry).toHaveBeenCalledOnce());
  });

  it('moves focus to the first sort button when a Retry brings rows back', async () => {
    const view = renderTable({ error: 'Service unavailable' });
    const retry = within(screen.getByRole('alert')).getByRole('button', {
      name: t('common.retry'),
    });
    retry.focus();
    await fireEvent.click(retry);

    await view.rerender(tableProps({ error: null }));

    await waitFor(() => expect(screen.getByTestId('sort-name')).toHaveFocus());
  });

  it('moves focus to the Show All switch when a Retry leaves no rows', async () => {
    const view = renderTable({ processes: [], error: 'Service unavailable' });
    const retry = within(screen.getByRole('alert')).getByRole('button', {
      name: t('common.retry'),
    });
    retry.focus();
    await fireEvent.click(retry);

    await view.rerender(tableProps({ processes: [], error: null }));

    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: t('system.processInfo.showAll') })).toHaveFocus()
    );
  });

  it('clicking the Show All label calls onShowAllChange', async () => {
    const onShowAllChange = vi.fn();
    renderTable({ onShowAllChange });

    await fireEvent.click(screen.getByText(t('system.processInfo.showAll')));

    expect(onShowAllChange).toHaveBeenCalledWith(true);
  });

  it('searches by name or PID only while Show All is on', async () => {
    const view = renderTable();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();

    await view.rerender(tableProps({ showAllProcesses: true }));
    await fireEvent.input(screen.getByRole('textbox'), { target: { value: '30' } });

    expect(shownNames()).toHaveLength(1);
    expect(shownNames()[0]).toContain('ffmpeg');
  });

  it('says no results when a search matches nothing', async () => {
    renderTable({ showAllProcesses: true });

    await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'nothing-like-this' } });

    expect(screen.getByText(t('search.noResultsFound'))).toBeInTheDocument();
  });

  it('keeps sort and search across a data refresh', async () => {
    const view = renderTable({ showAllProcesses: true });
    await fireEvent.click(screen.getByTestId('sort-name'));
    await fireEvent.input(screen.getByRole('textbox'), { target: { value: 's' } });
    expect(shownNames().map(name => name.split(' ')[0])).toEqual(['systemd']);

    await view.rerender(
      tableProps({
        showAllProcesses: true,
        processes: [...rows, proc({ pid: 40, name: 'sshd', cpu: 99 })],
      })
    );

    expect(screen.getByRole('textbox')).toHaveValue('s');
    expect(shownNames().map(name => name.split(' ')[0])).toEqual(['sshd', 'systemd']);
  });
});
