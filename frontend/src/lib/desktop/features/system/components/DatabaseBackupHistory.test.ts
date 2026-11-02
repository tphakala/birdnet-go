import { describe, it, expect, afterEach, vi } from 'vitest';
import { cleanup, screen, within, fireEvent, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import DatabaseBackupHistory from './DatabaseBackupHistory.svelte';

const completedBackup = {
  job_id: 'job-1',
  db_type: 'legacy',
  status: 'completed',
  progress: 100,
  bytes_written: 2048,
  total_bytes: 2048,
  started_at: '2026-11-01T10:00:00Z',
  completed_at: '2026-11-01T10:01:00Z',
};

interface HistoryProps {
  backups?: (typeof completedBackup)[];
  loadError?: string | null;
  onRetryLoad?: () => Promise<void>;
  createError?: string | null;
  onDismissCreateError?: () => void;
}

function renderHistory(props: HistoryProps = {}) {
  return renderTyped(DatabaseBackupHistory, {
    props: { backups: [], onCreateBackup: () => {}, ...props },
  });
}

describe('DatabaseBackupHistory', () => {
  afterEach(() => {
    cleanup();
  });

  it('says there are no backups when the list is empty and nothing failed', () => {
    renderHistory();

    expect(screen.getByText(t('system.database.backup.history.noBackups'))).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows a failed load with a Retry in place of the empty text', async () => {
    const onRetryLoad = vi.fn(() => Promise.resolve());
    renderHistory({ loadError: 'Backups unavailable', onRetryLoad });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.backup.history.loadFailed'));
    expect(alert).toHaveTextContent('Backups unavailable');
    expect(
      screen.queryByText(t('system.database.backup.history.noBackups'))
    ).not.toBeInTheDocument();

    await fireEvent.click(within(alert).getByRole('button', { name: t('common.retry') }));
    await waitFor(() => expect(onRetryLoad).toHaveBeenCalledOnce());
  });

  it('shows a failed load without a Retry when the caller cannot reload', () => {
    renderHistory({ loadError: 'Backups unavailable' });

    expect(within(screen.getByRole('alert')).queryByRole('button')).not.toBeInTheDocument();
  });

  it('keeps the earlier backups on screen beside a failed reload', () => {
    renderHistory({ backups: [completedBackup], loadError: 'Backups unavailable' });

    expect(screen.getByRole('alert')).toHaveTextContent('Backups unavailable');
    expect(screen.getByRole('table')).toBeInTheDocument();
  });

  it('shows a refused backup as a dismissible alert', async () => {
    const onDismissCreateError = vi.fn();
    renderHistory({ createError: 'Disk is full', onDismissCreateError });

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.backup.history.createFailed'));
    expect(alert).toHaveTextContent('Disk is full');

    await fireEvent.click(
      within(alert).getByRole('button', { name: t('common.aria.dismissAlert') })
    );

    expect(onDismissCreateError).toHaveBeenCalledOnce();
  });

  it('shows both alerts when the backup was refused and the list could not load', () => {
    renderHistory({ createError: 'Disk is full', loadError: 'Backups unavailable' });

    expect(screen.getAllByRole('alert')).toHaveLength(2);
  });
});
