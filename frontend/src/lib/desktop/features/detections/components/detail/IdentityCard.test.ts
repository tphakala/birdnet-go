import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import IdentityCard from './IdentityCard.svelte';
import type { ImageAttribution } from '$lib/types/detection.types';
import type { DetectionDetailData } from './detailTypes';

vi.mock('$lib/desktop/components/data/ConfidenceCircle.svelte');
vi.mock('$lib/desktop/components/ui/VerificationBadges.svelte');
vi.mock('$lib/desktop/features/dashboard/components/SourceBadge.svelte');

const detection: DetectionDetailData = {
  id: 7,
  date: '2026-11-13',
  time: '23:27:37',
  beginTime: '2026-11-13T23:27:29+02:00',
  endTime: '2026-11-13T23:27:56+02:00',
  speciesCode: 'dog',
  scientificName: 'Canis familiaris',
  commonName: 'Dog',
  confidence: 0.77,
  verified: 'correct',
  locked: false,
};

function credit(authorName: string, licenseName: string, licenseURL = '') {
  const imageAttribution: ImageAttribution = {
    authorName,
    authorURL: '',
    licenseName,
    licenseURL,
    sourceProvider: 'wikipedia',
  };
  const { container } = render(IdentityCard, {
    props: {
      detection,
      displayName: 'Dog',
      imageAttribution,
      predictionCount: 0,
      onShowPredictions: vi.fn(),
    },
  });
  return container.querySelector('.dd-identity-credit');
}

describe('IdentityCard photo credit', () => {
  afterEach(() => cleanup());

  it('shows no credit when the provider knows neither author nor licence', () => {
    expect(credit('Unknown', 'Unknown')).toBeNull();
  });

  it('leaves out an unknown licence and its separator', () => {
    expect(credit('Jane Doe', 'Unknown')?.textContent.trim()).toBe('Jane Doe');
  });

  it('links the licence only to a web address', () => {
    expect(credit('Jane Doe', 'CC BY 4.0', 'javascript:alert(1)')?.querySelector('a')).toBeNull();
    cleanup();
    expect(
      credit('Jane Doe', 'CC BY 4.0', 'https://creativecommons.org/licenses/by/4.0/')
        ?.querySelector('a')
        ?.getAttribute('href')
    ).toBe('https://creativecommons.org/licenses/by/4.0/');
  });
});
