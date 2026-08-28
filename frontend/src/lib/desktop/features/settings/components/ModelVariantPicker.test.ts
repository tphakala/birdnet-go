import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import ModelVariantPicker from './ModelVariantPicker.svelte';
import type { CatalogVariant } from '$lib/types/models';

function variant(overrides: Partial<CatalogVariant> & { id: string }): CatalogVariant {
  return {
    speciesCount: 0,
    default: false,
    installed: false,
    sizeBytes: 1000,
    compatible: true,
    recommended: false,
    ...overrides,
  };
}

const variants: CatalogVariant[] = [
  variant({
    id: 'fp16',
    precision: 'fp16',
    recommended: true,
    reasons: [{ code: 'backend.recommended', args: { backend: 'openvino-gpu' } }],
  }),
  variant({ id: 'fp32', precision: 'fp32', default: true }),
  variant({
    id: 'int8-arm',
    precision: 'int8',
    compatible: false,
    blockers: [{ code: 'arch.unsupported', args: { required: 'aarch64' } }],
  }),
];

function radios(container: HTMLElement): HTMLInputElement[] {
  return Array.from(container.querySelectorAll('input[type="radio"]'));
}

function radioByValue(container: HTMLElement, value: string): HTMLInputElement | null {
  return container.querySelector(`input[value="${value}"]`);
}

describe('ModelVariantPicker', () => {
  beforeEach(() => {
    cleanup();
  });

  it('collapses non-recommended options behind a show-all toggle', () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p1',
      },
    });
    // Only the recommended variant is shown initially.
    expect(radios(container)).toHaveLength(1);
    expect(radioByValue(container, 'fp16')).not.toBeNull();
    // The disclosure button reveals the rest, labeled honestly as "show all"
    // because this fixture has no regional variants to scope to. Assert it is NOT
    // the region-scoped showAllRegions (of which showAll is a string prefix).
    const toggle = getByRole('button');
    expect(toggle.textContent).toContain('analysis.gallery.variants.showAll');
    expect(toggle.textContent).not.toContain('showAllRegions');
    // The disclosure button reveals collapsed content, so it exposes aria-expanded.
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });

  it('reveals all variants when show-all is clicked', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p2',
      },
    });
    await fireEvent.click(getByRole('button'));
    expect(radios(container)).toHaveLength(3);
  });

  it('preselects the recommended variant', () => {
    const { container } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p3',
      },
    });
    expect(radioByValue(container, 'fp16')?.checked).toBe(true);
  });

  it('disables an incompatible variant and shows its blocker reason', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p4',
      },
    });
    await fireEvent.click(getByRole('button')); // reveal all
    const blocked = radioByValue(container, 'int8-arm');
    // Blocked options use aria-disabled, not native disabled, so they keep keyboard
    // focus and can announce their reason (accessibility requirement).
    expect(blocked?.getAttribute('aria-disabled')).toBe('true');
    expect(blocked?.disabled).toBe(false);
    // reasonText falls back to the raw code when t returns the key (t is mocked to
    // echo the key in tests), so the blocker reason renders the raw dotted code.
    expect(container.textContent).toContain('arch.unsupported');
  });

  it('shows a generic reason for a blocked variant that has no structured blocker', async () => {
    const noReasonVariants: CatalogVariant[] = [
      variant({ id: 'ok', precision: 'fp32', recommended: true }),
      variant({ id: 'blocked-no-reason', precision: 'int8', compatible: false }),
    ];
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants: noReasonVariants,
        selectedVariantId: 'ok',
        onSelect: vi.fn(),
        idPrefix: 'p6',
      },
    });
    await fireEvent.click(getByRole('button')); // reveal all
    const blocked = radioByValue(container, 'blocked-no-reason');
    expect(blocked?.getAttribute('aria-disabled')).toBe('true');
    expect(blocked?.disabled).toBe(false);
    // Falls back to the generic localized "incompatible" line, never left blank.
    expect(container.textContent).toContain('analysis.gallery.variants.incompatible');
  });

  it('keeps an installed non-recommended variant visible in the collapsed view', () => {
    const withInstalled: CatalogVariant[] = [
      variant({ id: 'fp16', precision: 'fp16', recommended: true }),
      variant({ id: 'int8-arm', precision: 'int8', installed: true }),
      variant({ id: 'fp32', precision: 'fp32', default: true }),
    ];
    const { container } = render(ModelVariantPicker, {
      props: {
        variants: withInstalled,
        installedVariantId: 'int8-arm',
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p7',
      },
    });
    // Recommended + installed shown; the plain default stays collapsed.
    expect(radioByValue(container, 'fp16')).not.toBeNull();
    expect(radioByValue(container, 'int8-arm')).not.toBeNull();
    expect(radioByValue(container, 'fp32')).toBeNull();
  });

  it('shows all variants and no toggle when none is recommended', () => {
    const noneRecommended: CatalogVariant[] = [
      variant({ id: 'a', precision: 'fp32', default: true }),
      variant({ id: 'b', precision: 'int8' }),
    ];
    const { container, queryByRole } = render(ModelVariantPicker, {
      props: {
        variants: noneRecommended,
        selectedVariantId: 'a',
        onSelect: vi.fn(),
        idPrefix: 'p8',
      },
    });
    expect(radios(container)).toHaveLength(2);
    expect(queryByRole('button')).toBeNull();
  });

  it('fires onSelect when the user picks a different variant (manual override)', async () => {
    const onSelect = vi.fn();
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect,
        idPrefix: 'p5',
      },
    });
    await fireEvent.click(getByRole('button')); // reveal all
    const fp32 = radioByValue(container, 'fp32');
    expect(fp32).not.toBeNull();
    await fireEvent.click(fp32 as HTMLInputElement);
    expect(onSelect).toHaveBeenCalledWith('fp32');
  });

  it('surfaces the top two recommendation reasons, including the region reason at index 1', () => {
    const regional: CatalogVariant[] = [
      variant({
        id: 'fp16',
        precision: 'fp16',
        recommended: true,
        reasons: [
          { code: 'backend.recommended', args: { backend: 'openvino-gpu' } },
          { code: 'region.matched', args: { region: 'Finland' } },
        ],
      }),
    ];
    const { container } = render(ModelVariantPicker, {
      props: {
        variants: regional,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p9',
      },
    });
    // Under the echo mock both reasons render as their raw codes. The region
    // reason sits at reasons[1], which the old single-reason render dropped.
    expect(container.textContent).toContain('backend.recommended');
    expect(container.textContent).toContain('region.matched');
  });

  it('renders a single reason without a second line when only one is present', () => {
    const single: CatalogVariant[] = [
      variant({
        id: 'fp32',
        precision: 'fp32',
        recommended: true,
        reasons: [{ code: 'region.global_fallback', args: {} }],
      }),
    ];
    const { container } = render(ModelVariantPicker, {
      props: {
        variants: single,
        selectedVariantId: 'fp32',
        onSelect: vi.fn(),
        idPrefix: 'p10',
      },
    });
    // Exactly one reason item renders; a length>1 -> length>=1 regression would
    // render a stray empty second list item. Count the <li> reasons, not the
    // heading, so the assertion tracks the actual reason list.
    const reasonItems = container.querySelectorAll('[class*="text-primary/90"] li');
    expect(reasonItems).toHaveLength(1);
    expect(reasonItems[0].textContent).toContain('region.global_fallback');
  });

  it('renders each of two recommended reasons as its own list item', () => {
    const twoReasons: CatalogVariant[] = [
      variant({
        id: 'fp16',
        precision: 'fp16',
        recommended: true,
        reasons: [
          { code: 'backend.recommended', args: { backend: 'openvino-gpu' } },
          { code: 'region.matched', args: { region: 'Finland' } },
        ],
      }),
    ];
    const { container } = render(ModelVariantPicker, {
      props: {
        variants: twoReasons,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        idPrefix: 'p11',
      },
    });
    // Two reasons must render as two distinct <li> items (not one concatenated
    // line, and not just the first): a "render only reasons[0]" regression drops
    // to one item, and a "join into one li" regression also fails the count.
    const reasonItems = container.querySelectorAll('[class*="text-primary/90"] li');
    expect(reasonItems).toHaveLength(2);
    expect(reasonItems[0].textContent).toContain('backend.recommended');
    expect(reasonItems[1].textContent).toContain('region.matched');
  });

  it('does not select a blocked variant when clicked (aria-disabled soft-disable)', async () => {
    const onSelect = vi.fn();
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect,
        idPrefix: 'p12',
      },
    });
    await fireEvent.click(getByRole('button')); // reveal all
    const blocked = radioByValue(container, 'int8-arm');
    expect(blocked).not.toBeNull();
    await fireEvent.click(blocked as HTMLInputElement);
    // The soft-disable must swallow the interaction: no selection callback, and
    // the blocked radio does not end up checked.
    expect(onSelect).not.toHaveBeenCalled();
    expect(blocked?.checked).toBe(false);
  });

  it('restores the prior selection when arrow-key focus lands on a blocked variant', async () => {
    const onSelect = vi.fn();
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants,
        selectedVariantId: 'fp16',
        onSelect,
        idPrefix: 'p13',
      },
    });
    await fireEvent.click(getByRole('button')); // reveal all
    const blocked = radioByValue(container, 'int8-arm');
    const selected = radioByValue(container, 'fp16');
    expect(blocked).not.toBeNull();
    expect(selected).not.toBeNull();
    // Simulate arrow-key roving: the browser synchronously checks the focused
    // blocked radio and unchecks the prior selection, then fires change. There is
    // no cancelable click here, so the onclick preventDefault cannot intervene;
    // only the onchange revert can, and it must restore the prior selection.
    (blocked as HTMLInputElement).checked = true;
    (selected as HTMLInputElement).checked = false;
    await fireEvent.change(blocked as HTMLInputElement);
    expect(onSelect).not.toHaveBeenCalled();
    expect((blocked as HTMLInputElement).checked).toBe(false);
    expect((selected as HTMLInputElement).checked).toBe(true);
  });

  // Regional fixture: 2 globals, 2 tiles for the active region (nordic), 2 tiles
  // for another region (iberia). The picker must scope the middle stage to the
  // active region and never label a filtered stage "all".
  const regionalVariants: CatalogVariant[] = [
    variant({ id: 'fp16', precision: 'fp16', recommended: true }),
    variant({ id: 'fp32', precision: 'fp32', default: true }),
    variant({ id: 'fp16@nordic', precision: 'fp16', region: 'nordic' }),
    variant({ id: 'fp32@nordic', precision: 'fp32', region: 'nordic' }),
    variant({ id: 'fp16@iberia', precision: 'fp16', region: 'iberia' }),
    variant({ id: 'fp32@iberia', precision: 'fp32', region: 'iberia' }),
  ];
  const regionNames = new Map([
    ['nordic', 'Nordic and Baltic'],
    ['iberia', 'Iberia'],
  ]);

  it('scopes the middle disclosure stage to the active region and never over-promises', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants: regionalVariants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        activeRegionSlug: 'nordic',
        regionNames,
        idPrefix: 'r1',
      },
    });
    // Collapsed: only the recommended variant, and the "Other regions" heading is
    // not shown yet (it belongs to the final stage only).
    expect(radios(container)).toHaveLength(1);
    expect(container.textContent).not.toContain('analysis.gallery.variants.otherRegions');
    // A context line states which region is scoping the list.
    expect(container.textContent).toContain('analysis.gallery.variants.regionContext');
    // The first disclosure button is region-scoped, not "show all".
    expect(getByRole('button').textContent).toContain('analysis.gallery.variants.showRegion');

    // Region stage: globals + the nordic tiles, but NOT iberia, and still no
    // "Other regions" heading until the final stage.
    await fireEvent.click(getByRole('button'));
    expect(radioByValue(container, 'fp32')).not.toBeNull();
    expect(radioByValue(container, 'fp16@nordic')).not.toBeNull();
    expect(radioByValue(container, 'fp32@nordic')).not.toBeNull();
    expect(radioByValue(container, 'fp16@iberia')).toBeNull();
    expect(container.textContent).not.toContain('analysis.gallery.variants.otherRegions');

    // The next button reveals every remaining region under an "Other regions"
    // heading, and only now is a button labeled to include all regions.
    expect(getByRole('button').textContent).toContain('analysis.gallery.variants.showAllRegions');
    await fireEvent.click(getByRole('button'));
    expect(radioByValue(container, 'fp16@iberia')).not.toBeNull();
    expect(container.textContent).toContain('analysis.gallery.variants.otherRegions');
  });

  it('renders the localized region name in a regional variant label', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants: regionalVariants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        activeRegionSlug: 'nordic',
        regionNames,
        idPrefix: 'r2',
      },
    });
    await fireEvent.click(getByRole('button')); // reveal the region stage
    // The nordic tile shows the display name from the map, not the raw slug.
    expect(container.textContent).toContain('Nordic and Baltic');
    expect(container.textContent).not.toContain('(nordic)');
  });

  it('prompts to pick a region when regional variants exist but none is active', () => {
    const { container } = render(ModelVariantPicker, {
      props: {
        variants: regionalVariants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        activeRegionSlug: '',
        regionNames,
        idPrefix: 'r3',
      },
    });
    expect(container.textContent).toContain('analysis.gallery.variants.regionContextNone');
  });

  it('wraps the other-region tiles in a labelled group in the all stage', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants: regionalVariants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        activeRegionSlug: 'nordic',
        regionNames,
        idPrefix: 'r5',
      },
    });
    await fireEvent.click(getByRole('button')); // region stage
    await fireEvent.click(getByRole('button')); // all stage
    // The other-region tiles form a semantic group whose accessible name comes
    // from the "Other regions" heading, so a screen reader announces the boundary.
    const group = container.querySelector('[role="group"]');
    expect(group).not.toBeNull();
    expect(group?.getAttribute('aria-labelledby')).toBe('r5-other-regions');
    const label = document.getElementById('r5-other-regions');
    expect(label).not.toBeNull();
    expect(label?.textContent).toContain('analysis.gallery.variants.otherRegions');
  });

  it('moves focus to the region filter when the final stage reveals it', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants: regionalVariants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        activeRegionSlug: 'nordic',
        regionNames,
        idPrefix: 'r6',
      },
    });
    // collapsed -> region keeps the button mounted (focus stays); region -> all
    // reveals the "other regions" panel with its filter box, so focus moves to that
    // search input (a keyboard user filters first, then ArrowDowns into the tiles)
    // instead of falling to <body> when the toggle unmounts.
    await fireEvent.click(getByRole('button')); // region stage
    await fireEvent.click(getByRole('button')); // all stage (button unmounts)
    const search = container.querySelector('input[type="text"]');
    expect(search).not.toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(search));
  });

  it('bridges keyboard focus from the region filter into the first tile on ArrowDown', async () => {
    const { container, getByRole } = render(ModelVariantPicker, {
      props: {
        variants: regionalVariants,
        selectedVariantId: 'fp16',
        onSelect: vi.fn(),
        activeRegionSlug: 'nordic',
        regionNames,
        idPrefix: 'r7',
      },
    });
    await fireEvent.click(getByRole('button')); // region stage
    await fireEvent.click(getByRole('button')); // all stage
    const search = container.querySelector('input[type="text"]');
    expect(search).not.toBeNull();
    // The native radio group uses roving tabindex, so Tab from the search would skip
    // the tiles; ArrowDown must bridge focus into the first other-region tile.
    if (search instanceof HTMLInputElement) {
      search.focus();
      await fireEvent.keyDown(search, { key: 'ArrowDown' });
    }
    await waitFor(() =>
      expect(document.activeElement).toBe(radioByValue(container, 'fp16@iberia'))
    );
  });
});
