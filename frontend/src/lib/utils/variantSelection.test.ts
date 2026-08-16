import { describe, it, expect, vi } from 'vitest';
import {
  pickPreselectedVariant,
  reasonKey,
  variantLabel,
  translateReason,
  topReasons,
} from './variantSelection';
import type { CatalogEntry, CatalogVariant, VariantReason } from '$lib/types/models';

// Controlled i18n mock: only these two keys have a "translation"; every other key
// echoes back, which is how the real t() signals an untranslated key. This lets us
// exercise both the translated branch and the fallback branch of translateReason.
vi.mock('$lib/i18n', () => ({
  t: (key: string) => {
    const dict: Record<string, string> = {
      'analysis.gallery.reasons.backendRecommended': 'Best for your hardware',
      'analysis.gallery.reasons.regionMatched': 'Matched to your region',
    };
    return dict[key] ?? key;
  },
}));

function variant(overrides: Partial<CatalogVariant> & { id: string }): CatalogVariant {
  return {
    speciesCount: 0,
    default: false,
    installed: false,
    sizeBytes: 0,
    compatible: true,
    recommended: false,
    ...overrides,
  };
}

function entry(overrides: Partial<CatalogEntry>): CatalogEntry {
  return {
    id: 'e',
    name: 'E',
    description: '',
    author: '',
    license: '',
    commercialUse: false,
    category: 'wildlife',
    region: '',
    speciesCount: 0,
    version: '1',
    installed: false,
    compatible: true,
    totalSizeBytes: 0,
    hasGeomodel: false,
    ...overrides,
  };
}

describe('pickPreselectedVariant', () => {
  it('returns empty string when the entry has no variants', () => {
    expect(pickPreselectedVariant(entry({}))).toBe('');
  });

  it('prefers the recommended variant', () => {
    const e = entry({
      recommendedVariantId: 'fp16',
      installedVariantId: 'fp32',
      variants: [
        variant({ id: 'fp32', default: true }),
        variant({ id: 'fp16', recommended: true }),
      ],
    });
    expect(pickPreselectedVariant(e)).toBe('fp16');
  });

  it('falls back to the installed variant when no recommendation', () => {
    const e = entry({
      installedVariantId: 'int8-arm',
      variants: [
        variant({ id: 'fp32', default: true }),
        variant({ id: 'int8-arm', installed: true }),
      ],
    });
    expect(pickPreselectedVariant(e)).toBe('int8-arm');
  });

  it('falls back to the default variant', () => {
    const e = entry({
      variants: [variant({ id: 'a' }), variant({ id: 'fp32', default: true })],
    });
    expect(pickPreselectedVariant(e)).toBe('fp32');
  });

  it('falls back to the first compatible variant when there is no default', () => {
    const e = entry({
      variants: [
        variant({ id: 'blocked', compatible: false }),
        variant({ id: 'ok', compatible: true }),
      ],
    });
    expect(pickPreselectedVariant(e)).toBe('ok');
  });

  it('falls back to the first variant when none is default or compatible', () => {
    const e = entry({
      variants: [
        variant({ id: 'first', compatible: false }),
        variant({ id: 'second', compatible: false }),
      ],
    });
    expect(pickPreselectedVariant(e)).toBe('first');
  });

  it('ignores a stale recommended id that is not in the variant list', () => {
    const e = entry({
      recommendedVariantId: 'gone',
      variants: [variant({ id: 'fp32', default: true })],
    });
    expect(pickPreselectedVariant(e)).toBe('fp32');
  });
});

describe('reasonKey', () => {
  it.each([
    ['backend.recommended', 'analysis.gallery.reasons.backendRecommended'],
    ['backend.supported', 'analysis.gallery.reasons.backendSupported'],
    ['precision.fp16_native', 'analysis.gallery.reasons.precisionFp16Native'],
    ['ram.constrained_fit', 'analysis.gallery.reasons.ramConstrainedFit'],
    ['variant.legacy', 'analysis.gallery.reasons.variantLegacy'],
    ['arch.unsupported', 'analysis.gallery.reasons.archUnsupported'],
    ['ram.insufficient', 'analysis.gallery.reasons.ramInsufficient'],
    ['hardware.excluded', 'analysis.gallery.reasons.hardwareExcluded'],
  ])('maps %s to %s', (code, key) => {
    expect(reasonKey(code)).toBe(key);
  });
});

describe('variantLabel', () => {
  it('uppercases the precision', () => {
    expect(variantLabel(variant({ id: 'fp32', precision: 'fp32' }))).toBe('FP32');
  });

  it('disambiguates variants that share a precision by the non-precision id part', () => {
    expect(variantLabel(variant({ id: 'no-dft-fp32', precision: 'fp32' }))).toBe('FP32 (no-dft)');
    expect(variantLabel(variant({ id: 'int8-arm', precision: 'int8' }))).toBe('INT8 (arm)');
  });

  it('appends the region when set', () => {
    expect(variantLabel(variant({ id: 'fp16', precision: 'fp16', region: 'Finland' }))).toBe(
      'FP16 (Finland)'
    );
  });

  it('falls back to the id when precision is absent', () => {
    expect(variantLabel(variant({ id: 'custom' }))).toBe('custom');
  });
});

describe('translateReason', () => {
  it('returns the translation when the reason code maps to a real key', () => {
    expect(translateReason('backend.recommended', undefined, 'raw')).toBe('Best for your hardware');
  });

  it('returns the fallback when the reason code has no translation', () => {
    expect(translateReason('some.unmapped_code', undefined, 'raw-fallback')).toBe('raw-fallback');
  });

  it('passes interpolation args through to t', () => {
    // regionMatched has a translation, so the translated branch is taken; the mock
    // ignores args, but this asserts args are accepted without changing the result.
    expect(translateReason('region.matched', { region: 'Finland' }, 'raw')).toBe(
      'Matched to your region'
    );
  });
});

describe('topReasons', () => {
  const reason = (code: string): VariantReason => ({ code });

  it('returns an empty array for no reasons', () => {
    expect(topReasons(undefined)).toEqual([]);
    expect(topReasons([])).toEqual([]);
  });

  it('localizes a single reason', () => {
    expect(topReasons([reason('backend.recommended')])).toEqual(['Best for your hardware']);
  });

  it('surfaces the region reason that sits at index 1, not just the headline', () => {
    expect(topReasons([reason('backend.recommended'), reason('region.matched')])).toEqual([
      'Best for your hardware',
      'Matched to your region',
    ]);
  });

  it('caps at the limit and falls back to the raw code for unmapped reasons', () => {
    expect(
      topReasons([reason('backend.recommended'), reason('region.matched'), reason('extra.one')])
    ).toEqual(['Best for your hardware', 'Matched to your region']);
  });
});
