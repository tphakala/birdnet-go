import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import ModelRegionSelector from './ModelRegionSelector.svelte';
import { settingsActions } from '$lib/stores/settings';
import * as modelsApi from '$lib/utils/modelsApi';
import type { ModelRegionsResponse, RegionResolution } from '$lib/types/models';

vi.mock('$lib/utils/modelsApi');

function response(overrides: Partial<ModelRegionsResponse> = {}): ModelRegionsResponse {
  return {
    modelRegion: 'auto',
    locationConfigured: true,
    resolved: { slug: 'nordic', source: 'auto', ambiguous: false },
    regions: [
      { slug: 'nordic', name: 'Nordic', group: 'europe', groupDisplay: 'Europe', tier: 50 },
      { slug: 'iberia', name: 'Iberia', group: 'europe', groupDisplay: 'Europe', tier: 50 },
      {
        slug: 'andes',
        name: 'Andes',
        group: 'south-america',
        groupDisplay: 'South America',
        tier: 50,
      },
    ],
    families: [],
    ...overrides,
  };
}

function resolved(overrides: Partial<RegionResolution>): RegionResolution {
  return { slug: 'nordic', source: 'auto', ambiguous: false, ...overrides };
}

function radios(container: HTMLElement): HTMLInputElement[] {
  return Array.from(container.querySelectorAll('input[type="radio"]'));
}

function radioByValue(container: HTMLElement, value: string): HTMLInputElement | null {
  return container.querySelector(`input[value="${value}"]`);
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
    settingsActions.resetAllSettings();
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(response());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders Automatic and Global, and reveals region tiles behind the pin disclosure', async () => {
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'auto')).not.toBeNull();
    expect(radioByValue(container, 'global')).not.toBeNull();
    // Region tiles hidden until the disclosure is opened.
    expect(radioByValue(container, 'nordic')).toBeNull();
    const pin = buttonByText(container, 'analysis.gallery.region.pinLabel');
    expect(pin).not.toBeNull();
    await fireEvent.click(pin as HTMLButtonElement);
    expect(radioByValue(container, 'nordic')).not.toBeNull();
    expect(radioByValue(container, 'iberia')).not.toBeNull();
    expect(radioByValue(container, 'andes')).not.toBeNull();
  });

  it('state auto-no-location: prompts to set a location', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ locationConfigured: false })
    );
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.noLocation');
  });

  it('state auto-outside-coverage: explains the global fallback', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ resolved: resolved({ slug: '', source: 'global' }) })
    );
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.outsideCoverage');
  });

  it('state auto-resolved: shows the detected region line', async () => {
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.resolved');
  });

  it('state auto-ambiguous: offers two pin shortcuts and pins the winner on click', async () => {
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ resolved: resolved({ slug: 'nordic', ambiguous: true, runnerUp: 'iberia' }) })
    );
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.ambiguous');
    const pinButtons = Array.from(container.querySelectorAll('button')).filter(b =>
      b.textContent.includes('analysis.gallery.region.pinAction')
    );
    expect(pinButtons).toHaveLength(2);

    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(pinButtons[0]);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'nordic' });
  });

  it('state auto-ambiguous: the runner-up pin pins the runner-up', async () => {
    // Fresh render: pinning the winner would flip the mode out of the ambiguous
    // state, so the runner-up pin is exercised on its own render.
    vi.mocked(modelsApi.fetchModelRegions).mockResolvedValue(
      response({ resolved: resolved({ slug: 'nordic', ambiguous: true, runnerUp: 'iberia' }) })
    );
    const { container } = await renderLoaded();
    const pinButtons = Array.from(container.querySelectorAll('button')).filter(b =>
      b.textContent.includes('analysis.gallery.region.pinAction')
    );
    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(pinButtons[1]);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'iberia' });
  });

  it('state global: reflects the saved global mode and checks the Global radio', async () => {
    setMode('global');
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'global')?.checked).toBe(true);
    expect(container.textContent).toContain('analysis.gallery.region.why.global');
  });

  it('state pinned + mismatch: shows the pinned line, the mismatch line, and checks the tile', async () => {
    setMode('iberia');
    const { container } = await renderLoaded();
    expect(radioByValue(container, 'iberia')?.checked).toBe(true); // disclosure auto-opens for a pin
    expect(container.textContent).toContain('analysis.gallery.region.why.pinned');
    expect(container.textContent).toContain('analysis.gallery.region.why.pinnedMismatch');
  });

  it('state pinned-unknown: warns and offers switch-to-automatic', async () => {
    setMode('atlantis'); // not in the catalog regions
    const { container } = await renderLoaded();
    expect(container.textContent).toContain('analysis.gallery.region.why.pinnedUnknown');
    const spy = vi.spyOn(settingsActions, 'updateSection');
    const switchBtn = buttonByText(container, 'analysis.gallery.region.switchToAuto');
    expect(switchBtn).not.toBeNull();
    await fireEvent.click(switchBtn as HTMLButtonElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'auto' });
  });

  it('dispatches the chosen mode when Global or a region tile is selected', async () => {
    const { container } = await renderLoaded();
    const spy = vi.spyOn(settingsActions, 'updateSection');
    await fireEvent.click(radioByValue(container, 'global') as HTMLInputElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'global' });

    // Open the disclosure and pick a tile.
    await fireEvent.click(
      buttonByText(container, 'analysis.gallery.region.pinLabel') as HTMLButtonElement
    );
    await fireEvent.click(radioByValue(container, 'andes') as HTMLInputElement);
    expect(spy).toHaveBeenCalledWith('birdnet', { modelRegion: 'andes' });
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

  it('disables every control when the disabled prop is set', async () => {
    setMode('iberia'); // opens the disclosure so tiles render too
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
});
