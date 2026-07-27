import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import ChartGrid from './ChartGrid.svelte';

vi.mock('../registry/analyticsControls.svelte', () => ({
  analyticsControls: {
    params: {
      range: 'month',
      start: '',
      end: '',
      species: [],
      source: '',
      startDate: new Date(),
      endDate: new Date(),
    },
    speciesNames: new Map(),
    loadingSpecies: false,
    applyParams: vi.fn(),
  },
}));

describe('ChartGrid', () => {
  it('renders one card container per chart', () => {
    const charts = [
      {
        id: 'a',
        size: 'normal',
        group: 'overview' as const,
        titleKey: 'test',
        descKey: 'test',
        emptyKey: 'test',
        emptyHintKey: 'test',
        component: {} as never,
        fetch: vi.fn().mockResolvedValue([]),
        supports: { species: true, source: false },
      },
      {
        id: 'b',
        size: 'full',
        group: 'overview' as const,
        titleKey: 'test',
        descKey: 'test',
        emptyKey: 'test',
        emptyHintKey: 'test',
        component: {} as never,
        fetch: vi.fn().mockResolvedValue([]),
        supports: { species: false, source: true },
      },
    ] as never[];
    const { container } = render(ChartGrid, { props: { charts } });
    expect(container.querySelectorAll('[data-chart-id]')).toHaveLength(2);
  });
});
