import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { screen, waitFor, fireEvent, cleanup } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../test/render-helpers';
import DetectionDetail from './DetectionDetail.svelte';
import type { Detection } from '$lib/types/detection.types';
import { get } from 'svelte/store';
import { settingsStore, settingsActions } from '$lib/stores/settings';

// Heavy / context-dependent children are irrelevant to the guide-panel toggle.
vi.mock('$lib/desktop/components/media/AudioPlayer.svelte');
vi.mock('$lib/desktop/components/data/ConfidenceCircle.svelte');
vi.mock('$lib/desktop/components/data/WeatherDetails.svelte');
vi.mock('$lib/desktop/features/dashboard/components/SourceBadge.svelte');
vi.mock('$lib/desktop/components/ui/VerificationBadges.svelte');

// SpeciesComparison is intentionally NOT mocked: its real close button drives the
// parent's onclose, which is the behavior under test. Its data calls go through
// $lib/utils/api, mocked here to resolve immediately so it mounts cleanly.
vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn((url: string) => {
      if (url.includes('/similar')) {
        return Promise.resolve({ scientific_name: '', genus: '', similar: [] });
      }
      // guide (and any other read): minimal valid guide shape.
      return Promise.resolve({
        scientific_name: '',
        common_name: '',
        description: 'A bird.',
        quality: 'full',
        features: { notes: false, enrichments: true, similar_species: true },
        source: { provider: '', url: '', license: '', license_url: '' },
        partial: false,
        cached_at: '',
      });
    }),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
}));

const detailTest = createComponentTestFactory(DetectionDetail);

const COMPARISON_CLOSE = '[data-testid="species-comparison-close"]';
const REOPEN_KEY = 'analytics.species.similar.show';

function makeDetection(overrides: Partial<Detection> = {}): Detection {
  return {
    id: 1,
    date: '2024-01-01',
    time: '10:00:00',
    timestamp: '2024-01-01T10:00:00Z',
    beginTime: '2024-01-01T10:00:00Z',
    endTime: '2024-01-01T10:00:03Z',
    speciesCode: 'spc',
    scientificName: 'Turdus merula',
    commonName: 'Common Blackbird',
    confidence: 0.9,
    verified: 'unverified',
    locked: false,
    ...overrides,
  };
}

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response;
}

// enableGuide publishes a dashboard config with the guide + similar-species panel
// on but notes off, so only SpeciesComparison renders in the guide section. It
// spreads the existing (default) dashboard so the required Dashboard fields are
// preserved, overriding only speciesGuide.
function enableGuide(): void {
  const dashboard = get(settingsStore).formData.realtime?.dashboard;
  if (!dashboard) {
    throw new Error('default dashboard settings missing');
  }
  settingsActions.updateSection('realtime', {
    dashboard: {
      ...dashboard,
      speciesGuide: {
        enabled: true,
        enableWikipedia: false,
        showNotes: false,
        showEnrichments: true,
        showSimilarSpecies: true,
      },
    },
  });
}

describe('DetectionDetail species guide panel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/v2/detections/')) {
          return Promise.resolve(jsonResponse(makeDetection()));
        }
        return Promise.resolve(jsonResponse({}));
      })
    );
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    settingsActions.resetAllSettings();
  });

  // Closing the comparison must not be a dead end: a reopen affordance appears and
  // re-expands the panel. Regression guard for the close-button-with-no-reopen bug.
  it('reopens the comparison after it is closed', async () => {
    enableGuide();
    const { container } = detailTest.render({ detectionId: 'det-1' });

    // The comparison renders once the detection loads (its close button is present).
    const closeBtn = await waitFor(
      () => {
        const b = container.querySelector(COMPARISON_CLOSE);
        if (!b) throw new Error('comparison not mounted yet');
        return b as HTMLElement;
      },
      { timeout: 5000 }
    );

    // Close it: the comparison collapses and the reopen affordance appears.
    await fireEvent.click(closeBtn);
    const reopenBtn = await screen.findByText(REOPEN_KEY, {}, { timeout: 5000 });
    expect(container.querySelector(COMPARISON_CLOSE)).toBeNull();

    // Reopen it: the comparison comes back.
    await fireEvent.click(reopenBtn);
    await waitFor(
      () => {
        expect(container.querySelector(COMPARISON_CLOSE)).not.toBeNull();
      },
      { timeout: 5000 }
    );
  });
});
