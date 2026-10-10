import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/svelte';
import ChartGrid from './ChartGrid.svelte';
import type { AnyChartComponent, ChartDef } from '../registry/types';

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
    // Placeholder component: the test only counts the card containers
    const component = {} as unknown as AnyChartComponent;
    const charts: ChartDef[] = [
      {
        id: 'a',
        size: 'normal',
        group: 'overview',
        titleKey: 'test',
        descKey: 'test',
        emptyKey: 'test',
        emptyHintKey: 'test',
        component,
        fetch: vi.fn().mockResolvedValue([]),
        supports: { species: true, source: false },
      },
      {
        id: 'b',
        size: 'full',
        group: 'overview',
        titleKey: 'test',
        descKey: 'test',
        emptyKey: 'test',
        emptyHintKey: 'test',
        component,
        fetch: vi.fn().mockResolvedValue([]),
        supports: { species: false, source: true },
      },
    ];
    const { container } = render(ChartGrid, { props: { charts } });
    expect(container.querySelectorAll('[data-chart-id]')).toHaveLength(2);
  });
});
