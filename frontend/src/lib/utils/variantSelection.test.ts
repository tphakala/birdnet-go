import { describe, it, expect, vi } from 'vitest';
import {
  pickPreselectedVariant,
  familyRegionSlug,
  reasonKey,
  variantLabel,
  translateReason,
  topReasons,
  normalizeRegionMode,
  variantHardwareClass,
  variantHardwareLabel,
  optimizeOffers,
  buildRecordFor,
  targetBuildNotes,
  CHANNEL_STABLE,
  HARDWARE_CLASS,
} from './variantSelection';
import type {
  CatalogBuild,
  CatalogEntry,
  CatalogVariant,
  RegionFamily,
  VariantReason,
} from '$lib/types/models';

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

  // A low-RAM host where the global build does not fit: the server recommends
  // nothing, and only regional slices (smaller) are compatible.
  const lowRamRegional = () =>
    entry({
      variants: [
        variant({ id: 'fp32', default: true, compatible: false }),
        variant({ id: 'fp32@africa', region: 'africa' }),
        variant({ id: 'fp32@nordic', region: 'nordic' }),
      ],
    });

  it('never preselects a slice of another region when nothing is recommended', () => {
    expect(pickPreselectedVariant(lowRamRegional(), 'nordic')).toBe('fp32@nordic');
  });

  it('preselects the blocked default rather than a slice when no region is active', () => {
    expect(pickPreselectedVariant(lowRamRegional(), '')).toBe('fp32');
  });

  it('preselects the blocked default when the active region slice does not fit either', () => {
    const e = entry({
      variants: [
        variant({ id: 'fp32', default: true, compatible: false }),
        variant({ id: 'fp32@africa', region: 'africa' }),
        variant({ id: 'fp32@nordic', region: 'nordic', compatible: false }),
      ],
    });
    expect(pickPreselectedVariant(e, 'nordic')).toBe('fp32');
  });

  it('keeps an installed slice of another region preselected', () => {
    const e = lowRamRegional();
    e.installedVariantId = 'fp32@africa';
    expect(pickPreselectedVariant(e, 'nordic')).toBe('fp32@africa');
  });

  it('falls back to the first variant of an entry made only of other regions slices', () => {
    const e = entry({
      variants: [
        variant({ id: 'fp32@africa', region: 'africa', compatible: false }),
        variant({ id: 'fp32@iberia', region: 'iberia' }),
      ],
    });
    expect(pickPreselectedVariant(e, 'nordic')).toBe('fp32@africa');
  });

  it('prefers the first global or active region variant over an earlier slice of another region when none of them runs', () => {
    const e = entry({
      variants: [
        variant({ id: 'fp32@africa', region: 'africa' }),
        variant({ id: 'int8', compatible: false }),
        variant({ id: 'fp32@nordic', region: 'nordic', compatible: false }),
      ],
    });
    expect(pickPreselectedVariant(e, 'nordic')).toBe('int8');
  });
});

describe('familyRegionSlug', () => {
  const family = (catalogId: string, slug: string): RegionFamily => ({
    catalogId,
    repo: 'r',
    installed: false,
    installedVariantRegion: '',
    resolved: { slug, source: slug ? 'auto' : 'global', ambiguous: false },
  });
  // BirdNET v3.0 ships no central-india tile; its own table resolves the same
  // coordinates to south-asia-peninsular.
  const v30 = entry({
    id: 'v30',
    variants: [
      variant({ id: 'fp32' }),
      variant({ id: 'fp32@south-asia-peninsular', region: 'south-asia-peninsular' }),
    ],
  });
  const perch = entry({
    id: 'perch',
    variants: [variant({ id: 'g' }), variant({ id: 'b2@central-india', region: 'central-india' })],
  });
  const families = [family('v30', 'south-asia-peninsular'), family('perch', 'central-india')];

  it('keeps the active slug for a family that ships that tile', () => {
    expect(familyRegionSlug(perch, 'central-india', families)).toBe('central-india');
  });

  it('uses the family own resolution when it lacks the active tile', () => {
    expect(familyRegionSlug(v30, 'central-india', families)).toBe('south-asia-peninsular');
  });

  it('is the global model when the family resolves to none', () => {
    expect(familyRegionSlug(v30, 'central-india', [family('v30', '')])).toBe('');
  });

  it('keeps the active slug for an entry without a family record', () => {
    expect(familyRegionSlug(v30, 'central-india', undefined)).toBe('central-india');
    expect(familyRegionSlug(v30, 'central-india', [family('perch', 'central-india')])).toBe(
      'central-india'
    );
  });

  it('stays unscoped when no region is active', () => {
    expect(familyRegionSlug(v30, '', families)).toBe('');
  });

  it('lets the preselection fall back to the family own tile', () => {
    const bare = entry({
      id: 'v30',
      variants: [
        variant({ id: 'fp32@south-asia-peninsular', region: 'south-asia-peninsular' }),
        variant({ id: 'fp32@other', region: 'other', default: true }),
      ],
    });
    expect(pickPreselectedVariant(bare, familyRegionSlug(bare, 'central-india', families))).toBe(
      'fp32@south-asia-peninsular'
    );
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

  it('makes no offer for an entry superseded by a visible successor', () => {
    // The superseded notice already tells the user to move to the successor, so the
    // entry gets no prompt to switch builds of itself. The API omits supersededBy
    // when the successor is hidden, so the field is the visibility signal.
    const e = v24Entry({ supersededBy: { id: 'birdnet-plus', name: 'BirdNET+' } });
    expect(optimizeOffers([e])).toHaveLength(0);
  });

  it('makes no offer for an entry whose installed build has a newer release to update to', () => {
    // The update notice and the card Update action cover this entry, so it gets
    // no Optimize offer on top of them (one model, one notice and one card action).
    const e = v24Entry({
      update: {
        variantId: 'fp32-dfttrunc',
        fromVersion: '3.0',
        toVersion: '3.0.1',
        fromBuild: 1,
        toBuild: 1,
      },
    });
    expect(optimizeOffers([e])).toHaveLength(0);
  });

  it('keeps offering later entries after skipping a superseded one', () => {
    const superseded = v24Entry({
      id: 'old-model',
      supersededBy: { id: 'birdnet-plus', name: 'BirdNET+' },
    });
    const offers = optimizeOffers([superseded, v24Entry()]);
    expect(offers.map(o => o.entry.id)).toEqual(['birdnet-v2.4']);
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

describe('buildRecordFor', () => {
  const build1: CatalogBuild = { version: '2', build: 1, date: '2026-07-01', reason: 'First.' };
  const build2: CatalogBuild = { version: '2', build: 2, date: '2026-11-21', notes: ['Second.'] };
  const notes = entry({ version: '2', builds: [build1, build2] });

  it('resolves a variant without a build to build 1 of its release', () => {
    expect(buildRecordFor(notes, variant({ id: 'a', version: '2' }))).toBe(build1);
  });

  it('takes the entry release when the variant carries none', () => {
    expect(buildRecordFor(notes, variant({ id: 'a', build: 2 }))).toBe(build2);
  });

  it('finds no record for another release or a build without one', () => {
    expect(buildRecordFor(notes, variant({ id: 'a', version: '1', build: 2 }))).toBeNull();
    expect(buildRecordFor(notes, variant({ id: 'a', version: '2', build: 3 }))).toBeNull();
  });
});

describe('targetBuildNotes', () => {
  const build2: CatalogBuild = { version: '2', build: 2, date: '2026-11-21', notes: ['Second.'] };
  const installedB1 = variant({ id: 'x@nordic', version: '2', build: 1, installed: true });
  const nordicB2 = variant({ id: 'x-b2@nordic', version: '2', build: 2 });
  const alpineB2 = variant({ id: 'x-b2@alps', version: '2', build: 2 });

  it('returns the notes of a different build the swap installs', () => {
    const e = entry({
      installed: true,
      installedVariantId: installedB1.id,
      builds: [build2],
      variants: [nordicB2, installedB1],
    });
    expect(targetBuildNotes(e, nordicB2.id)).toBe(build2);
  });

  it('returns null for a fresh install, the installed build itself and a target without notes', () => {
    const fresh = entry({ builds: [build2], variants: [nordicB2] });
    expect(targetBuildNotes(fresh, nordicB2.id)).toBeNull();

    // Swapping between two regions of the installed build brings nothing new.
    const sameBuild = entry({
      installed: true,
      installedVariantId: nordicB2.id,
      builds: [build2],
      variants: [nordicB2, alpineB2],
    });
    expect(targetBuildNotes(sameBuild, alpineB2.id)).toBeNull();

    const noNotes = entry({
      installed: true,
      installedVariantId: installedB1.id,
      variants: [nordicB2, installedB1],
    });
    expect(targetBuildNotes(noNotes, nordicB2.id)).toBeNull();
  });
});
