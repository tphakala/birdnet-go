import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import ModelRegionSelector from './ModelRegionSelector.svelte';
import { clearRegionMapCache } from './RegionCard.svelte';
import { settingsActions } from '$lib/stores/settings';
import * as modelsApi from '$lib/utils/modelsApi';
import type { ModelRegionsResponse, RegionResolution } from '$lib/types/models';

vi.mock('$lib/utils/modelsApi');
// Deterministic country localization so the search-by-country assertions do not
// depend on the runtime Intl.DisplayNames / ICU data available in CI.
vi.mock('$lib/utils/countryNames', () => ({
  localizedCountryNames: (codes: string[] | null | undefined) => {
    const names: Record<string, string> = {
      ES: 'Spain',
      PT: 'Portugal',
      FR: 'France',
      FI: 'Finland',
      SE: 'Sweden',
      NO: 'Norway',
      DK: 'Denmark',
      IS: 'Iceland',
      EE: 'Estonia',
      CO: 'Colombia',
      EC: 'Ecuador',
      PE: 'Peru',
      RE: 'Réunion',
    };
    return (codes ?? []).map(c => names[c] ?? c);
  },
}));

function response(overrides: Partial<ModelRegionsResponse> = {}): ModelRegionsResponse {
  return {
    modelRegion: 'auto',
    locationConfigured: true,
    resolved: { slug: 'nordic', source: 'auto', ambiguous: false },
    regions: [
      {
        slug: 'nordic',
        name: 'Nordic',
        group: 'europe',
        groupDisplay: 'Europe',
        tier: 50,
        countries: { core: ['FI', 'SE', 'NO', 'DK'], partial: [] },
      },
      {
        slug: 'iberia',
        name: 'Iberia',
        group: 'europe',
        groupDisplay: 'Europe',
        tier: 50,
        countries: { core: ['ES', 'PT'], partial: ['FR'] },
      },
      {
        slug: 'andes',
        name: 'Andes',
        group: 'south-america',
        groupDisplay: 'South America',
        tier: 50,
        countries: { core: ['CO', 'EC', 'PE'], partial: [] },
      },
    ],
    families: [],
    ...overrides,
  };
}

function resolved(overrides: Partial<RegionResolution>): RegionResolution {
  return { slug: 'nordic', source: 'auto', ambiguous: false, ...overrides };
}

function radioByValue(container: HTMLElement, value: string): HTMLInputElement | null {
  return container.querySelector(`input[value="${value}"]`);
}

function radios(container: HTMLElement): HTMLInputElement[] {
  return Array.from(container.querySelectorAll('input[type="radio"]'));
}

function searchInput(container: HTMLElement): HTMLInputElement | null {
  return container.querySelector('input[type="search"]');
}

function buttonByText(container: HTMLElement, text: string): HTMLButtonElement | null {
  return (
    Array.from(container.querySelectorAll('button')).find(b => b.textContent.includes(text)) ?? null
  );
}

function setMode(mode: string) {
  settingsActions.updateSection('birdnet', { modelRegion: mode });
}

async function renderLoaded(props: Record<string, unknown> = {}) {
  const result = render(ModelRegionSelector, { props });
  await waitFor(() => expect(radioByValue(result.container, 'auto')).not.toBeNull());
  return result;
}

describe('ModelRegionSelector', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    clearRegionMapCache();
    settingsActions.resetAllSettings();
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(response());
    vi.mocked(modelsApi.fetchRegionCoverageMap).mockResolvedValue(
      '<svg class="cov" viewBox="0 0 800 600"></svg>'
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders Automatic and Manual mode options, and no separate Global row', async () => {
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'auto')).not.toBeNull();
    expect(radioByValue(container, 'manual')).not.toBeNull();
    // The old top-level Global radio is gone; global lives inside the manual list.
    const modeGlobal = container.querySelector('input[name="model-region-mode"][value="global"]');
    expect(modeGlobal).toBeNull();
    // Region cards hidden until manual mode is entered.
    expect(radioByValue(container, 'nordic')).toBeNull();
  });

  it('automatic + resolved: shows the resolved region as a featured card with its map', async () => {
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.resolved');
    expect(container.textContent).toContain('Nordic');
    await waitFor(() => expect(container.querySelector('svg.cov')).not.toBeNull());
    expect(modelsApi.fetchRegionCoverageMap).toHaveBeenCalledWith('nordic');
  });

  it('automatic + no location: prompts to set a location and shows no featured map', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ locationConfigured: false })
    );
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.noLocation');
    expect(modelsApi.fetchRegionCoverageMap).not.toHaveBeenCalled();
  });

  it('automatic + outside coverage: explains the global fallback', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ resolved: resolved({ slug: '', source: 'global' }) })
    );
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.outsideCoverage');
  });

  it('automatic + ambiguous: offers two quick-select shortcuts and selects on click', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ resolved: resolved({ slug: 'nordic', ambiguous: true, runnerUp: 'iberia' }) })
    );
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.ambiguous');
    const quick = Array.from(container.querySelectorAll('button')).filter(b =>
      b.textContent.includes('analysis.gallery.region.pinAction')
    );
    expect(quick).toHaveLength(2);
    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(quick[0]);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'nordic' });
  });

  it('clicking Manual reveals the search box, the Worldwide card, and the region grid', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    expect(searchInput(container)).not.toBeNull();
    expect(radioByValue(container, 'global')).not.toBeNull(); // Worldwide card
    expect(radioByValue(container, 'nordic')).not.toBeNull();
    expect(radioByValue(container, 'andes')).not.toBeNull();
  });

  it('selecting the Worldwide card saves the global model', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(radioByValue(container, 'global') as HTMLInputElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'global' });
  });

  it('the Worldwide card carries the resource note', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    expect(container.textContent).toContain('analysis.gallery.region.worldwideResourceNote');
  });

  it('the Worldwide icon box avoids the grid class so the global .drawer-content .grid rule cannot stretch it', async () => {
    // Regression guard: a global `.drawer-content .grid { width: 100% }` rule
    // stretched a `grid`-classed fixed-size box to full width, pushing the card
    // text outside the card. The icon wrapper must center with flex, not `grid`.
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    const label = (radioByValue(container, 'global') as HTMLInputElement).closest(
      'label'
    ) as HTMLElement;
    const iconWrap = label.querySelector('span[aria-hidden="true"]') as HTMLElement;
    expect(iconWrap).not.toBeNull();
    expect(iconWrap.classList.contains('grid')).toBe(false);
    expect(iconWrap.classList.contains('flex')).toBe(true);
  });

  it('selecting a region card saves that region slug', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(radioByValue(container, 'andes') as HTMLInputElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'andes' });
  });

  it('saved global: opens manual mode with the Worldwide card checked', async () => {
    setMode('global');
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'manual')?.checked).toBe(true);
    expect(radioByValue(container, 'global')?.checked).toBe(true);
    expect(container.textContent).toContain('analysis.gallery.region.why.global');
  });

  it('saved slug: opens manual mode with the region checked and shows the mismatch line', async () => {
    setMode('iberia');
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'iberia')?.checked).toBe(true);
    expect(container.textContent).toContain('analysis.gallery.region.why.pinned');
    expect(container.textContent).toContain('analysis.gallery.region.why.pinnedMismatch');
  });

  it('saved unknown slug: warns and offers switch-to-automatic', async () => {
    setMode('atlantis');
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.pinnedUnknown');
    const spy = vi.spyOn(settingsActions, 'updateSection');
    const switchBtn = buttonByText(container, 'analysis.gallery.region.switchToAuto');
    await fireEvent.click(switchBtn as HTMLButtonElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'auto' });
  });

  it('search filters the grid by region name and keeps Worldwide visible', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    await fireEvent.input(searchInput(container) as HTMLInputElement, {
      target: { value: 'nordic' },
    });
    await waitFor(() => expect(radioByValue(container, 'iberia')).toBeNull());
    expect(radioByValue(container, 'nordic')).not.toBeNull();
    expect(radioByValue(container, 'global')).not.toBeNull(); // Worldwide never filtered out
  });

  it('matches a region by a country name that is not in its title', async () => {
    // 'portugal' appears only as a country of Iberia (core ES/PT), never in a
    // region name, so the match must come from the country term of the haystack.
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    await fireEvent.input(searchInput(container) as HTMLInputElement, {
      target: { value: 'portugal' },
    });
    await waitFor(() => expect(radioByValue(container, 'nordic')).toBeNull());
    expect(radioByValue(container, 'iberia')).not.toBeNull();
    expect(radioByValue(container, 'andes')).toBeNull();
  });

  it('manual mode with nothing selected yet shows the choose-a-region prompt', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    expect(container.textContent).toContain('analysis.gallery.region.manualPrompt');
  });

  it('search matches by country name, including diacritic folding', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({
        regions: [
          {
            slug: 'reunion',
            name: 'Réunion',
            group: 'africa',
            groupDisplay: 'Africa',
            tier: 50,
            countries: { core: ['RE'], partial: [] },
          },
          {
            slug: 'iberia',
            name: 'Iberia',
            group: 'europe',
            groupDisplay: 'Europe',
            tier: 50,
            countries: { core: ['ES', 'PT'], partial: [] },
          },
        ],
      })
    );
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    // "reunion" (no accent) must match "Réunion" (accented) via diacritic folding.
    await fireEvent.input(searchInput(container) as HTMLInputElement, {
      target: { value: 'reunion' },
    });
    await waitFor(() => expect(radioByValue(container, 'iberia')).toBeNull());
    expect(radioByValue(container, 'reunion')).not.toBeNull();
  });

  it('search with no matches shows a no-results message but keeps Worldwide', async () => {
    const { container } = await renderLoaded();
    await fireEvent.click(radioByValue(container, 'manual') as HTMLInputElement);
    await fireEvent.input(searchInput(container) as HTMLInputElement, {
      target: { value: 'zzzznowhere' },
    });
    await waitFor(() =>
      expect(container.textContent).toContain('analysis.gallery.region.searchNoResults')
    );
    expect(radioByValue(container, 'global')).not.toBeNull();
  });

  it('external reset to auto collapses the manual view back to Automatic', async () => {
    setMode('iberia');
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'manual')?.checked).toBe(true);
    // Simulate a "Discard changes" / external store reset back to auto.
    setMode('auto');
    await waitFor(() => expect(radioByValue(container, 'auto')?.checked).toBe(true));
    expect(radioByValue(container, 'manual')?.checked).toBe(false);
    expect(radioByValue(container, 'nordic')).toBeNull(); // grid collapsed
  });

  it('dispatches auto when Automatic is chosen from manual mode', async () => {
    setMode('iberia');
    const { container } = await renderLoaded();
    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(radioByValue(container, 'auto') as HTMLInputElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'auto' });
  });

  it('disables every control when the disabled prop is set', async () => {
    setMode('iberia');
    const { container } = await renderLoaded({ disabled: true });
    for (const radio of radios(container)) {
      expect(radio.disabled).toBe(true);
    }
  });

  it('treats an empty modelRegion as automatic', async () => {
    setMode('');
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'auto')?.checked).toBe(true);
    expect(container.textContent).toContain('analysis.gallery.region.why.resolved');
  });

  it('shows an error with a retry that refetches', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockRejectedValueOnce(new Error('boom'));
    const { container, getByRole } = render(ModelRegionSelector, {});
    await waitFor(() => expect(getByRole('alert')).toBeTruthy());
    expect(container.textContent).toContain('analysis.gallery.region.loadFailed');
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(response());
    await fireEvent.click(buttonByText(container, 'analysis.gallery.retry') as HTMLButtonElement);
    await waitFor(() => expect(radioByValue(container, 'auto')).not.toBeNull());
  });
});
