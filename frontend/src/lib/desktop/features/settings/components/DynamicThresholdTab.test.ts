import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import type { DynamicThreshold, ThresholdStats } from '$lib/types/dynamic-threshold';
import DynamicThresholdTab from './DynamicThresholdTab.svelte';

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock('$lib/utils/api', () => ({
  api: { get: apiGet, delete: vi.fn() },
}));

const EXPIRES_AT = '2026-11-20T10:00:00Z';

function threshold(speciesName: string, currentValue: number): DynamicThreshold {
  return {
    speciesName,
    scientificName: '',
    level: 1,
    currentValue,
    baseThreshold: 0.8,
    highConfCount: 1,
    expiresAt: EXPIRES_AT,
    lastTriggered: EXPIRES_AT,
    firstCreated: EXPIRES_AT,
    triggerCount: 1,
    isActive: true,
  };
}

const stats: ThresholdStats = {
  totalCount: 2,
  activeCount: 2,
  atMinimumCount: 0,
  levelDistribution: [],
  validHours: 24,
  minThreshold: 0.2,
};

function mockApi(): void {
  apiGet.mockImplementation((url: string) =>
    Promise.resolve(
      url.endsWith('/stats')
        ? stats
        : { data: [threshold('Robin', 0.6), threshold('Wren', 0.4)], total: 2 }
    )
  );
}

describe('DynamicThresholdTab sorting', () => {
  afterEach(cleanup);

  it('sorts from a header button, marks only that column and announces it', async () => {
    mockApi();
    renderTyped(DynamicThresholdTab, { props: {} });
    const thresholdLabel = t('settings.species.dynamicThreshold.header.threshold');

    const button = await screen.findByRole('button', { name: thresholdLabel });
    const thresholdHeader = screen.getByRole('columnheader', { name: thresholdLabel });
    expect(thresholdHeader).toContainElement(button);
    expect(screen.getByRole('status').textContent).toBe('');

    await fireEvent.click(button);

    await waitFor(() => expect(thresholdHeader).toHaveAttribute('aria-sort', 'ascending'));
    const sorted = screen.getAllByRole('columnheader').filter(th => th.hasAttribute('aria-sort'));
    expect(sorted).toEqual([thresholdHeader]);
    expect(screen.getByRole('status')).toHaveTextContent(
      t('dataDisplay.table.sortedAscending', { column: thresholdLabel })
    );
  });

  it('names the table by its visible title', async () => {
    mockApi();
    renderTyped(DynamicThresholdTab, { props: {} });

    expect(
      await screen.findByRole('table', {
        name: t('settings.species.dynamicThreshold.header.species'),
      })
    ).toBeInTheDocument();
  });

  it('drops the sort announcement while the list reloads', async () => {
    mockApi();
    renderTyped(DynamicThresholdTab, { props: {} });
    const thresholdLabel = t('settings.species.dynamicThreshold.header.threshold');

    await fireEvent.click(await screen.findByRole('button', { name: thresholdLabel }));
    expect(screen.getByRole('status')).toHaveTextContent(
      t('dataDisplay.table.sortedAscending', { column: thresholdLabel })
    );

    await fireEvent.click(screen.getByRole('button', { name: t('common.refresh') }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toBe(''));
    await screen.findByRole('button', { name: thresholdLabel });
    expect(screen.getByRole('status').textContent).toBe('');
  });
});
