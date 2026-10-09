import { describe, it, expect, vi } from 'vitest';

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ en: 'English' }),
    post: vi.fn(),
  },
}));

// LocationPickerMap relies on maplibre-gl; LanguageSelector is not under test
vi.mock('../components/LocationPickerMap.svelte');
vi.mock('$lib/desktop/components/ui/LanguageSelector.svelte');

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({
    birdnet: { latitude: 40, longitude: -74, locale: 'en' },
    realtime: { dashboard: { locale: 'en' } },
  });
});

import LocationLanguageStep from './LocationLanguageStep.svelte';
import { flushAsync, renderStep } from './stepTestUtils';

describe('LocationLanguageStep layout', () => {
  async function renderRoot() {
    const { container } = renderStep(LocationLanguageStep);
    await flushAsync();
    const root = container.firstElementChild;
    if (!(root instanceof HTMLElement)) throw new Error('step root not found');
    return root;
  }

  it('lays the fields and the map out in two columns once the step box is wide', async () => {
    const root = await renderRoot();

    expect(root).toHaveClass('grid', '@2xl:grid-cols-2');
    // The fields column and the map column are the grid's two children
    expect(root.children).toHaveLength(2);
  });

  it('gives the map a height of its own, since the map fills its wrapper', async () => {
    const root = await renderRoot();
    const mapColumn = root.lastElementChild;

    // Stacked: a fixed height. Side by side: stretch with a floor, never zero
    expect(mapColumn).toHaveClass('h-36', '@2xl:h-auto', '@2xl:min-h-72');
  });
});
