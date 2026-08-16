import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
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
    // The toggle reveals the rest.
    expect(getByRole('button')).toHaveAttribute('aria-expanded', 'false');
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
    expect(blocked?.disabled).toBe(true);
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
    expect(blocked?.disabled).toBe(true);
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
});
