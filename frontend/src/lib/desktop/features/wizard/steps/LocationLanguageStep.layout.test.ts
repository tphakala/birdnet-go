import { describe, it, expect, vi } from 'vitest';
import type { ComponentProps } from 'svelte';

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn().mockResolvedValue({ en: 'English' }),
    post: vi.fn(),
  },
}));

// LocationMap relies on maplibre-gl; LanguageSelector is not under test
vi.mock('$lib/desktop/components/forms/LocationMap.svelte');
vi.mock('$lib/desktop/components/ui/LanguageSelector.svelte');

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({
    birdnet: { latitude: 40, longitude: -74, locale: 'en' },
    realtime: { dashboard: { locale: 'en' } },
  });
});

import LocationLanguageStep from './LocationLanguageStep.svelte';
import LocationMap from '$lib/desktop/components/forms/LocationMap.svelte';
import { flushAsync, renderStep } from './stepTestUtils';

/** Props of the last rendered LocationMap (the component is automocked). */
function latestMapProps(): ComponentProps<typeof LocationMap> {
  const call = vi.mocked(LocationMap).mock.calls.at(-1);
  const props = call?.[1];
  // The lint type checker types a mocked component's call as a one-element tuple,
  // so it cannot see that the props argument can be missing.
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
  if (!props) throw new Error('LocationMap was not rendered');
  return props;
}

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

  it('gives the map a fixed height of at least 300 px in both layouts', async () => {
    const root = await renderRoot();
    const mapColumn = root.lastElementChild;

    // The map sets its own height, so the column no longer sizes it
    expect(latestMapProps().mapClass).toBe('h-[300px]');
    expect(mapColumn).toHaveClass('min-w-0');
    expect(mapColumn).not.toHaveClass('h-36');
  });
});
