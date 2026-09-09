import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { tick } from 'svelte';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import RegionCard, { clearRegionMapCache } from './RegionCard.svelte';
import * as modelsApi from '$lib/utils/modelsApi';
import type { RegionOption } from '$lib/types/models';

vi.mock('$lib/utils/modelsApi');
// Localize to a deterministic identity so chip assertions can use the raw codes.
vi.mock('$lib/utils/countryNames', () => ({
  localizedCountryNames: (codes: string[] | null | undefined) => [...(codes ?? [])],
}));

// jsdom lacks a firing IntersectionObserver; the shared setup stub never invokes
// the callback. Install a controllable one that (by default) immediately reports
// the observed element as intersecting, so lazy fetches run deterministically. A
// registry of live instances lets a test withhold or trigger intersection.
interface FakeIO {
  cb: IntersectionObserverCallback;
  el?: Element;
  fire: (intersecting: boolean) => void;
}
let ioInstances: FakeIO[] = [];
let autoIntersect = true;

class ControllableIO {
  cb: IntersectionObserverCallback;
  el?: Element;
  constructor(cb: IntersectionObserverCallback) {
    this.cb = cb;
    ioInstances.push({ cb, fire: (i: boolean) => this.emit(i) } as FakeIO);
  }
  observe(el: Element) {
    this.el = el;
    const inst = ioInstances.find(i => i.cb === this.cb);
    if (inst) inst.el = el;
    if (autoIntersect) this.emit(true);
  }
  emit(intersecting: boolean) {
    this.cb(
      [{ isIntersecting: intersecting, target: this.el } as IntersectionObserverEntry],
      this as unknown as IntersectionObserver
    );
  }
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}

function region(overrides: Partial<RegionOption> = {}): RegionOption {
  return {
    slug: 'nordic',
    name: 'Nordic',
    group: 'europe',
    groupDisplay: 'Europe',
    tier: 50,
    countries: { core: ['FI', 'SE', 'NO', 'DK'], partial: ['IS', 'EE'] },
    ...overrides,
  };
}

function baseProps(overrides: Record<string, unknown> = {}) {
  return {
    region: region(),
    selected: false,
    name: 'model-region-choice',
    locale: 'en',
    onSelect: vi.fn(),
    ...overrides,
  };
}

function radio(container: HTMLElement): HTMLInputElement | null {
  return container.querySelector('input[type="radio"]');
}

describe('RegionCard', () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    clearRegionMapCache(); // shared module cache persists across tests
    ioInstances = [];
    autoIntersect = true;
    globalThis.IntersectionObserver = ControllableIO as unknown as typeof IntersectionObserver;
    vi.mocked(modelsApi.fetchRegionCoverageMap).mockResolvedValue(
      '<svg class="cov" viewBox="0 0 800 600"></svg>'
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders the region name', () => {
    const { getByText } = render(RegionCard, { props: baseProps() });
    expect(getByText('Nordic')).toBeTruthy();
  });

  it('renders core and partial country names as chips', () => {
    const { container } = render(RegionCard, { props: baseProps() });
    const text = container.textContent;
    expect(text).toContain('FI');
    expect(text).toContain('DK');
    expect(text).toContain('IS'); // partial
  });

  it('uses a native radio in the shared group with the region slug as its value', () => {
    const { container } = render(RegionCard, { props: baseProps() });
    const input = radio(container);
    expect(input).not.toBeNull();
    expect(input?.getAttribute('name')).toBe('model-region-choice');
    expect(input?.value).toBe('nordic');
  });

  it('calls onSelect with the slug when the card radio is chosen', async () => {
    const onSelect = vi.fn();
    const { container } = render(RegionCard, { props: baseProps({ onSelect }) });
    await fireEvent.click(radio(container) as HTMLInputElement);
    expect(onSelect).toHaveBeenCalledWith('nordic');
  });

  it('reflects the selected state as the radio checked state', () => {
    const { container } = render(RegionCard, { props: baseProps({ selected: true }) });
    expect(radio(container)?.checked).toBe(true);
  });

  it('lazily fetches the coverage map only after the card intersects the viewport', async () => {
    autoIntersect = false; // hold intersection
    render(RegionCard, { props: baseProps() });
    expect(modelsApi.fetchRegionCoverageMap).not.toHaveBeenCalled();

    ioInstances[0].fire(true); // card scrolls into view
    await waitFor(() => expect(modelsApi.fetchRegionCoverageMap).toHaveBeenCalledWith('nordic'));
  });

  it('renders the fetched map with the inner svg aria-hidden', async () => {
    const { container } = render(RegionCard, { props: baseProps() });
    await waitFor(() => expect(container.querySelector('svg.cov')).not.toBeNull());
    expect(container.querySelector('svg.cov')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('does not call onSelect when disabled', async () => {
    const onSelect = vi.fn();
    const { container } = render(RegionCard, { props: baseProps({ disabled: true, onSelect }) });
    const input = radio(container) as HTMLInputElement;
    expect(input.disabled).toBe(true);
    await fireEvent.click(input);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('renders a non-interactive display card (no radio) when interactive is false', () => {
    const { container, getByText } = render(RegionCard, {
      props: baseProps({ interactive: false, lazy: false }),
    });
    expect(radio(container)).toBeNull();
    expect(getByText('Nordic')).toBeTruthy();
  });

  it('ignores a stale coverage-map response after the region prop changes', async () => {
    // Deferred per slug so the two in-flight fetches resolve out of order.
    const deferreds = new Map<string, (svg: string) => void>();
    vi.mocked(modelsApi.fetchRegionCoverageMap).mockImplementation(
      (slug: string) => new Promise<string>(resolve => deferreds.set(slug, resolve))
    );

    const { container, rerender } = render(RegionCard, { props: baseProps() }); // nordic
    await waitFor(() => expect(deferreds.get('nordic')).toBeDefined());

    // Switch the region while nordic is still in flight: the effect cancels nordic
    // and starts iberia.
    rerender(
      baseProps({
        region: region({
          slug: 'iberia',
          name: 'Iberia',
          countries: { core: ['ES'], partial: [] },
        }),
      })
    );
    await waitFor(() => expect(deferreds.get('iberia')).toBeDefined());

    // The current region resolves first and renders.
    deferreds.get('iberia')?.('<svg class="cov" data-region="iberia"></svg>');
    await waitFor(() =>
      expect(container.querySelector('svg[data-region="iberia"]')).not.toBeNull()
    );

    // The stale nordic response resolves late; the cancel guard must drop it.
    deferreds.get('nordic')?.('<svg class="cov" data-region="nordic"></svg>');
    await Promise.resolve();
    await tick();
    expect(container.querySelector('svg[data-region="nordic"]')).toBeNull();
    expect(container.querySelector('svg[data-region="iberia"]')).not.toBeNull();
  });

  it('shares one in-flight fetch across cards for the same slug (dedup)', async () => {
    render(RegionCard, { props: baseProps() });
    render(RegionCard, { props: baseProps() }); // same slug 'nordic'
    await waitFor(() => expect(modelsApi.fetchRegionCoverageMap).toHaveBeenCalled());
    expect(modelsApi.fetchRegionCoverageMap).toHaveBeenCalledTimes(1);
  });

  it('lists the hidden countries in the overflow chip title', () => {
    const many = region({
      countries: {
        core: ['US', 'CA', 'MX', 'BR', 'AR', 'CL', 'PE', 'CO'], // 8 core, grid cap is 6
        partial: [],
      },
    });
    const { container } = render(RegionCard, { props: baseProps({ region: many }) });
    const overflow = Array.from(container.querySelectorAll('.region-chip')).find(el =>
      el.textContent.includes('analysis.gallery.region.countriesOverflow')
    );
    expect(overflow?.getAttribute('title')).toBe('PE, CO'); // the 2 beyond the cap
  });

  it('shows the unavailable fallback but keeps chips when the map fetch fails', async () => {
    vi.mocked(modelsApi.fetchRegionCoverageMap).mockRejectedValue(new Error('boom'));
    const { container } = render(RegionCard, { props: baseProps() });
    await waitFor(() =>
      expect(container.textContent).toContain('analysis.gallery.region.mapUnavailable')
    );
    expect(container.textContent).toContain('FI'); // chips still present
  });
});
