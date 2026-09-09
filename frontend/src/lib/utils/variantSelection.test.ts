import { describe, it, expect, vi } from 'vitest';
import {
  pickPreselectedVariant,
  reasonKey,
  variantLabel,
  translateReason,
  topReasons,
  normalizeRegionMode,
  variantHardwareClass,
  variantHardwareLabel,
  optimizeOffers,
  CHANNEL_STABLE,
  HARDWARE_CLASS,
} from './variantSelection';
import type { CatalogEntry, CatalogVariant, VariantReason } from '$lib/types/models';

// Controlled i18n mock: only these two keys have a "translation"; every other key
// echoes back, which is how the real t() signals an untranslated key. This lets us
// exercise both the translated branch and the fallback branch of translateReason.
// The mock t is a spy (declared via vi.hoisted so it exists before the hoisted
// vi.mock runs) so a test can assert the interpolation args are forwarded to t,
// not merely that the result is unchanged. It still echoes an unmapped key, which
// is how the real t() signals an untranslated key.
const { tSpy } = vi.hoisted(() => ({
  tSpy: vi.fn((key: string, _args?: Record<string, string>) => {
    const dict: Record<string, string> = {
      'analysis.gallery.reasons.backendRecommended': 'Best for your hardware',
      'analysis.gallery.reasons.regionMatched': 'Matched to your region',
    };
    // eslint-disable-next-line security/detect-object-injection -- test mock, key is a controlled literal
    return dict[key] ?? key;
  }),
}));

vi.mock('$lib/i18n', () => ({
  t: tSpy,
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
    channel: CHANNEL_STABLE,
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

  it('strips the @region suffix from the id so it does not leak into the descriptor', () => {
    // Regression: a regional id like "int8-arm@nordic" must not render the
    // "@nordic" suffix inside the descriptor as "arm@nordic".
    expect(variantLabel(variant({ id: 'fp32@nordic', precision: 'fp32', region: 'nordic' }))).toBe(
      'FP32 (nordic)'
    );
    expect(
      variantLabel(
        variant({ id: 'int8-arm@southern-africa', precision: 'int8', region: 'southern-africa' })
      )
    ).toBe('INT8 (arm) (southern-africa)');
    expect(
      variantLabel(variant({ id: 'no-dft-fp32@iberia', precision: 'fp32', region: 'iberia' }))
    ).toBe('FP32 (no-dft) (iberia)');
  });

  it('falls back to the id when precision is absent', () => {
    expect(variantLabel(variant({ id: 'custom' }))).toBe('custom');
  });

  it('resolves the region display name from the map when provided', () => {
    const names = new Map([['nordic', 'Nordic and Baltic']]);
    expect(
      variantLabel(variant({ id: 'fp32@nordic', precision: 'fp32', region: 'nordic' }), names)
    ).toBe('FP32 (Nordic and Baltic)');
  });

  it('falls back to the raw slug when the map lacks the region or is absent', () => {
    const names = new Map([['iberia', 'Iberia']]);
    // Slug not in the map -> raw slug.
    expect(
      variantLabel(variant({ id: 'fp32@nordic', precision: 'fp32', region: 'nordic' }), names)
    ).toBe('FP32 (nordic)');
    // No map at all -> raw slug, unchanged from the single-argument behavior.
    expect(variantLabel(variant({ id: 'fp32@nordic', precision: 'fp32', region: 'nordic' }))).toBe(
      'FP32 (nordic)'
    );
  });

  it('returns the localized built-in label for a BuiltIn variant, not the raw id', () => {
    expect(variantLabel(variant({ id: 'builtin', builtIn: true }))).toBe(
      'analysis.gallery.builtIn'
    );
  });
});

describe('variantHardwareClass', () => {
  it('classifies the BuiltIn baseline as built-in', () => {
    expect(variantHardwareClass(variant({ id: 'builtin', builtIn: true }))).toBe('builtIn');
  });

  it('maps cuda/tensorrt backends to a discrete GPU', () => {
    const cuda = variant({
      id: 'fp32',
      reasons: [{ code: 'backend.recommended', args: { backend: 'cuda' } }],
    });
    const trt = variant({
      id: 'fp32',
      reasons: [{ code: 'backend.recommended', args: { backend: 'tensorrt' } }],
    });
    expect(variantHardwareClass(cuda)).toBe(HARDWARE_CLASS.gpuNvidia);
    expect(variantHardwareClass(trt)).toBe(HARDWARE_CLASS.gpuNvidia);
  });

  it('maps the openvino-gpu backend to an Intel GPU', () => {
    const ov = variant({
      id: 'fp32',
      reasons: [{ code: 'backend.recommended', args: { backend: 'openvino-gpu' } }],
    });
    expect(variantHardwareClass(ov)).toBe(HARDWARE_CLASS.gpuIntel);
  });

  it('prefers the recommended backend reason over any other backend reason', () => {
    const v = variant({
      id: 'fp32',
      reasons: [
        { code: 'backend.available', args: { backend: 'onnxruntime-cpu' } },
        { code: 'backend.recommended', args: { backend: 'cuda' } },
      ],
    });
    expect(variantHardwareClass(v)).toBe(HARDWARE_CLASS.gpuNvidia);
  });

  it('falls back to the id when no backend reason is present, using only the arm token', () => {
    // An unauthenticated request carries no reasons: an "arm" id marks an ARM CPU
    // build, everything else a generic CPU. Precision alone is NOT used: an INT8 id
    // without "arm" (e.g. a future x86 INT8 build) must not be mislabeled ARM.
    expect(variantHardwareClass(variant({ id: 'int8-arm-dfttrunc', precision: 'int8' }))).toBe(
      HARDWARE_CLASS.armCpu
    );
    expect(variantHardwareClass(variant({ id: 'int8-x86', precision: 'int8' }))).toBe(
      HARDWARE_CLASS.cpu
    );
    expect(variantHardwareClass(variant({ id: 'fp32-dfttrunc', precision: 'fp32' }))).toBe(
      HARDWARE_CLASS.cpu
    );
  });
});

describe('variantHardwareLabel', () => {
  it('uses the built-in label for the baseline and the hardware key otherwise', () => {
    expect(variantHardwareLabel(variant({ id: 'builtin', builtIn: true }))).toBe(
      'analysis.gallery.builtIn'
    );
    expect(
      variantHardwareLabel(
        variant({
          id: 'fp32',
          reasons: [{ code: 'backend.recommended', args: { backend: 'cuda' } }],
        })
      )
    ).toBe(`analysis.gallery.hardware.${HARDWARE_CLASS.gpuNvidia}`);
    expect(variantHardwareLabel(variant({ id: 'fp32-dfttrunc', precision: 'fp32' }))).toBe(
      `analysis.gallery.hardware.${HARDWARE_CLASS.cpu}`
    );
  });

  it('prefers the server-computed hardwareClass token over the client-derived class', () => {
    // The id/reasons would derive a generic cpu, but the arch-explicit server token wins.
    expect(variantHardwareLabel(variant({ id: 'fp32', hardwareClass: 'amd64Cpu' }))).toBe(
      `analysis.gallery.hardware.${HARDWARE_CLASS.amd64Cpu}`
    );
    // Server token wins even when the reasons point at a GPU backend.
    expect(
      variantHardwareLabel(
        variant({
          id: 'fp16',
          hardwareClass: 'arm64Cpu',
          reasons: [{ code: 'backend.recommended', args: { backend: 'cuda' } }],
        })
      )
    ).toBe(`analysis.gallery.hardware.${HARDWARE_CLASS.arm64Cpu}`);
  });

  it('falls back to the client-derived class when the server omits the token', () => {
    expect(variantHardwareLabel(variant({ id: 'int8-arm', precision: 'int8' }))).toBe(
      `analysis.gallery.hardware.${HARDWARE_CLASS.armCpu}`
    );
  });
});

describe('optimizeOffers', () => {
  // A permanent-style entry: installed on its BuiltIn baseline, with a faster DFT
  // build recommended for this host.
  function v24Entry(overrides: Partial<CatalogEntry> = {}): CatalogEntry {
    return entry({
      id: 'birdnet-v2.4',
      installed: true,
      permanent: true,
      installedVariantId: 'builtin',
      recommendedVariantId: 'fp32-dfttrunc',
      variants: [
        variant({ id: 'builtin', builtIn: true, default: true, installed: true }),
        variant({
          id: 'fp32-dfttrunc',
          precision: 'fp32',
          compatible: true,
          recommended: true,
          reasons: [{ code: 'backend.recommended', args: { backend: 'onnxruntime-cpu' } }],
        }),
      ],
      ...overrides,
    });
  }

  it('offers a swap when the recommended variant differs from the installed one', () => {
    const offers = optimizeOffers([v24Entry()]);
    expect(offers).toHaveLength(1);
    expect(offers[0].entry.id).toBe('birdnet-v2.4');
    expect(offers[0].from?.id).toBe('builtin');
    expect(offers[0].to.id).toBe('fp32-dfttrunc');
    expect(offers[0].reasons.length).toBeGreaterThan(0);
  });

  it('makes no offer when the installed variant is already the recommended one', () => {
    const e = v24Entry({ installedVariantId: 'fp32-dfttrunc' });
    expect(optimizeOffers([e])).toHaveLength(0);
  });

  it('makes no offer when the recommended variant is incompatible', () => {
    const e = v24Entry();
    const rec = e.variants?.find(v => v.id === 'fp32-dfttrunc');
    if (rec) rec.compatible = false;
    expect(optimizeOffers([e])).toHaveLength(0);
  });

  it('makes no offer for a flat (variant-less) entry', () => {
    const flat = entry({ id: 'geo', installed: true, recommendedVariantId: undefined });
    expect(optimizeOffers([flat])).toHaveLength(0);
  });

  it('makes no offer when there is no recommendation (e.g. an unauthenticated request)', () => {
    const e = v24Entry({ recommendedVariantId: undefined });
    expect(optimizeOffers([e])).toHaveLength(0);
  });

  it('makes no offer for an entry that is not installed', () => {
    const e = v24Entry({ installed: false });
    expect(optimizeOffers([e])).toHaveLength(0);
  });

  it('still offers a swap when the installed variant was dropped from the catalog', () => {
    // The installed variant id no longer matches any catalog variant (deprecated and
    // removed): the offer must still surface so the user moves off the dead variant,
    // with a null `from` (its label is unavailable).
    const e = v24Entry({ installedVariantId: 'legacy-gone' });
    const offers = optimizeOffers([e]);
    expect(offers).toHaveLength(1);
    expect(offers[0].from).toBeNull();
    expect(offers[0].to.id).toBe('fp32-dfttrunc');
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
    tSpy.mockClear();
    // regionMatched has a translation, so the translated branch is taken. Assert
    // the args object reaches t verbatim, not just that the result is unchanged.
    expect(translateReason('region.matched', { region: 'Finland' }, 'raw')).toBe(
      'Matched to your region'
    );
    expect(tSpy).toHaveBeenCalledWith('analysis.gallery.reasons.regionMatched', {
      region: 'Finland',
    });
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

describe('normalizeRegionMode', () => {
  it('collapses empty string, null and undefined to the automatic mode', () => {
    // '', null and undefined all mean "automatic" (the Go ModelRegion omitempty
    // field), so they must normalize identically or the settings dirty-check would
    // flag a logical no-op as an edit.
    expect(normalizeRegionMode('')).toBe('auto');
    expect(normalizeRegionMode(undefined)).toBe('auto');
    expect(normalizeRegionMode(null)).toBe('auto');
  });

  it('passes a concrete region mode through unchanged', () => {
    expect(normalizeRegionMode('auto')).toBe('auto');
    expect(normalizeRegionMode('global')).toBe('global');
    expect(normalizeRegionMode('nordic')).toBe('nordic');
  });
});
