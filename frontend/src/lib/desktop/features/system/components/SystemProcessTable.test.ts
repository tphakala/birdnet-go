import { describe, it, expect, afterEach, vi } from 'vitest';
import { tick } from 'svelte';
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

// Four rows whose listed order and every sorted order are pairwise different (shown names:
// sshd, BirdNET-Go, systemd, ffmpeg): CPU descending ffmpeg, BirdNET-Go, sshd, systemd; shown
// name BirdNET-Go, ffmpeg, sshd, systemd; raw name ffmpeg, main, sshd, systemd (the same as the
// CPU order, so a name sort that ignores the shown name changes nothing); status ascending
// ffmpeg, BirdNET-Go, systemd, sshd; memory descending BirdNET-Go, sshd, systemd, ffmpeg;
// uptime descending systemd, ffmpeg, sshd, BirdNET-Go.
const rows: ProcessInfo[] = [
  proc({ pid: 40, name: 'sshd', status: 'zombie', cpu: 5, memory: 300, uptime: 200 }),
  proc({ pid: 20, name: 'main', status: 'running', cpu: 12.5, memory: 400, uptime: 100 }),
  proc({ pid: 10, name: 'systemd', status: 'sleep', cpu: 0.5, memory: 200, uptime: 400 }),
  proc({ pid: 30, name: 'ffmpeg', status: 'idle', cpu: 20, memory: 100, uptime: 300 }),
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

/** Shown process names alone, without the PID line. */
function shownOrder(): string[] {
  return shownNames().map(name => name.split(/\s/)[0].replace(/PID.*$/, ''));
}

describe('SystemProcessTable', () => {
  afterEach(() => {
    cleanup();
  });

  it('sorts by CPU descending by default and shows the app name for main', () => {
    renderTable();

    const names = shownNames();
    expect(names[0]).toContain('ffmpeg');
    expect(names[1]).toContain(APP_NAME);
    expect(names[1]).not.toContain('main');
    expect(names[2]).toContain('sshd');
    expect(names[3]).toContain('systemd');
  });

  it.each([
    ['name', ['BirdNET-Go', 'ffmpeg', 'sshd', 'systemd']],
    ['status', ['ffmpeg', 'BirdNET-Go', 'systemd', 'sshd']],
    ['memory', ['BirdNET-Go', 'sshd', 'systemd', 'ffmpeg']],
    ['uptime', ['systemd', 'ffmpeg', 'sshd', 'BirdNET-Go']],
  ])('the first click on %s sorts %j', async (key, order) => {
    renderTable();

    await fireEvent.click(screen.getByTestId(`sort-${key}`));

    expect(shownOrder()).toEqual(order);
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

  it('shows the raw process state word in a pill of its variant', () => {
    renderTable({ processes: [proc({ pid: 1, name: 'init', status: 'zombie' })] });

    const pill = within(screen.getByRole('table'))
      .getByText('zombie')
      .closest<HTMLElement>('span.rounded-full.border');
    expect(pill?.getAttribute('style')).toContain('color: var(--text-error)');
  });

  it('shows CPU clamped to 0-100 and memory and uptime in compact units', () => {
    renderTable({
      processes: [
        proc({ pid: 1, name: 'busy', cpu: 250, memory: 5 * 1024 * 1024, uptime: 90_061 }),
        proc({ pid: 2, name: 'odd', cpu: Number.NaN }),
        proc({ pid: 3, name: 'below', cpu: -5 }),
      ],
    });

    const cells = within(screen.getByRole('table'))
      .getAllByRole('row')
      .slice(1)
      .map(row =>
        within(row)
          .getAllByRole('cell')
          .map(cell => cell.textContent.trim())
      );
    // Columns: process, status, CPU, memory, uptime
    expect(cells[0].slice(2)).toEqual(['100.0%', '5.0 MB', '1d 1h 1m']);
    expect(cells[1][2]).toBe('0.0%');
    expect(cells[2][2]).toBe('0.0%');
  });

  it('shows loading, empty and failed as three different states', () => {
    const noProcesses = t('system.processInfo.noProcesses');

    const loading = renderTable({ processes: [], loading: true });
    expect(screen.getByRole('status')).toHaveTextContent(t('system.processInfo.loading'));
    expect(screen.queryByText(noProcesses)).not.toBeInTheDocument();
    loading.unmount();

    const empty = renderTable({ processes: [] });
    expect(screen.getByText(noProcesses)).toBeInTheDocument();
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    empty.unmount();

    renderTable({ processes: [], error: 'Service unavailable' });
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(t('system.errors.processes', { error: 'Service unavailable' }));
    expect(within(alert).getByRole('button', { name: t('common.retry') })).toBeInTheDocument();
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
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

    // The shown name is searched, not the raw one ("main")
    await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'bird' } });

    expect(shownNames()).toHaveLength(1);
    expect(shownNames()[0]).toContain(APP_NAME);
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
    expect(shownOrder()).toEqual(['sshd', 'systemd']);

    await view.rerender(
      tableProps({
        showAllProcesses: true,
        // snapd has the lowest CPU, so a sort reset to CPU would put it last
        processes: [...rows, proc({ pid: 50, name: 'snapd', cpu: 0 })],
      })
    );

    expect(screen.getByRole('textbox')).toHaveValue('s');
    expect(shownOrder()).toEqual(['snapd', 'sshd', 'systemd']);
  });

  describe('focus after the alert goes', () => {
    function renderFailed() {
      const view = renderTable({ error: 'Service unavailable' });
      const retry = within(screen.getByRole('alert')).getByRole('button', {
        name: t('common.retry'),
      });
      const cell = within(screen.getByRole('table')).getAllByRole('cell')[0];
      return { view, retry, cell };
    }

    async function clearAlert(view: ReturnType<typeof renderTable>) {
      await view.rerender(tableProps({ error: null }));
      await tick();
    }

    // jsdom does not move focus on a press, so each sequence blurs or focuses explicitly
    it('stays on the page body after a click in the rows', async () => {
      const { view, retry, cell } = renderFailed();
      retry.focus();
      await fireEvent.click(retry);
      retry.blur();
      await fireEvent.click(cell);

      await clearAlert(view);

      expect(document.body).toHaveFocus();
    });

    it('stays on the page body when another part of the panel is pressed after the row click', async () => {
      const { view, retry, cell } = renderFailed();
      retry.focus();
      await fireEvent.click(retry);
      retry.blur();
      await fireEvent.click(cell);
      const title = screen.getByText(t('system.processInfo.title'));
      await fireEvent.pointerDown(title);
      await fireEvent.click(title);

      await clearAlert(view);

      expect(document.body).toHaveFocus();
    });

    it('goes back to the first sort button when focus returns to the alert after a row click', async () => {
      const { view, retry, cell } = renderFailed();
      retry.focus();
      retry.blur();
      await fireEvent.click(cell);
      retry.focus();

      await clearAlert(view);

      expect(screen.getByTestId('sort-name')).toHaveFocus();
    });

    it('goes back to the first sort button after a press on an alert that took no focus', async () => {
      const { view, retry, cell } = renderFailed();
      // Safari does not focus a clicked button
      await fireEvent.pointerDown(retry);
      await fireEvent.click(cell);
      await fireEvent.pointerDown(retry);
      await fireEvent.click(retry);

      await clearAlert(view);

      expect(screen.getByTestId('sort-name')).toHaveFocus();
    });

    it('goes to the first sort button when a row press left focus in the alert', async () => {
      const { view, retry, cell } = renderFailed();
      retry.focus();
      // A touch pan, or any press that does not move focus
      await fireEvent.click(cell);

      await clearAlert(view);

      expect(screen.getByTestId('sort-name')).toHaveFocus();
    });
  });

  it('has no wrapper around the rows that is a tab stop', () => {
    renderTable();

    expect(screen.getByRole('table').closest('[tabindex]')).toBeNull();
  });
});
