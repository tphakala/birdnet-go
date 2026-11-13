import { describe, it, expect, afterEach, vi } from 'vitest';
import { cleanup, screen, fireEvent } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import DatabaseDetectionRateCard from './DatabaseDetectionRateCard.svelte';

const createBackupLabel = () => t('system.database.dashboard.detectionRate.createBackup');
const mysqlNoteLabel = () => t('system.database.dashboard.detectionRate.mysqlBackupNote');

// The card is one flex row whose children are the chart column and, when the engine has one,
// the side panel. An empty side panel would add a bordered strip without any label.
function layoutColumnCount(container: HTMLElement): number {
  return container.firstElementChild?.firstElementChild?.children.length ?? 0;
}

describe('DatabaseDetectionRateCard side panel', () => {
  afterEach(() => {
    cleanup();
  });

  it('shows no side panel content for SQLite when no backup handler is given', () => {
    const { container } = renderTyped(DatabaseDetectionRateCard, {
      props: { data: [], engine: 'sqlite' },
    });

    expect(layoutColumnCount(container)).toBe(1);
    expect(screen.queryByText(createBackupLabel())).not.toBeInTheDocument();
    expect(screen.queryByText(mysqlNoteLabel())).not.toBeInTheDocument();
  });

  it('shows the backup button for SQLite and calls the handler on click', async () => {
    const onBackup = vi.fn();
    renderTyped(DatabaseDetectionRateCard, { props: { data: [], engine: 'sqlite', onBackup } });

    await fireEvent.click(screen.getByRole('button', { name: createBackupLabel() }));

    expect(onBackup).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(mysqlNoteLabel())).not.toBeInTheDocument();
  });

  it('shows the host and the backup note for MySQL and no backup button', () => {
    const { container } = renderTyped(DatabaseDetectionRateCard, {
      props: { data: [], engine: 'mysql', mysqlHost: 'db.example.test:3306', onBackup: vi.fn() },
    });

    expect(layoutColumnCount(container)).toBe(2);
    expect(screen.getByText('db.example.test:3306')).toBeInTheDocument();
    expect(screen.getByText(mysqlNoteLabel())).toBeInTheDocument();
    expect(screen.queryByText(createBackupLabel())).not.toBeInTheDocument();
  });

  it('renders zero min, average and maximum labels for empty data', () => {
    renderTyped(DatabaseDetectionRateCard, { props: { data: [], engine: 'mysql' } });

    const perHour = t('system.database.dashboard.detectionRate.perHour');
    expect(screen.getAllByText(content => content.trimEnd().endsWith(`0${perHour}`))).toHaveLength(
      3
    );
  });
});
