import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/svelte';
import SampleRateBadge from './SampleRateBadge.svelte';
import { renderTyped } from '../../../../test/render-helpers';

const MIN_KEY = 'settings.audio.soundCards.compatibility.minSampleRate';
const RECOMMENDED_KEY = 'settings.audio.soundCards.compatibility.recommendedSampleRate';

describe('SampleRateBadge', () => {
  it('names the minimum when the rate is below it, and not the recommendation', () => {
    renderTyped(SampleRateBadge, {
      props: { sampleRate: 48000, minRate: 96000, recommendedRate: 256000 },
    });
    expect(screen.getByText(new RegExp(MIN_KEY))).toBeInTheDocument();
    expect(screen.queryByText(new RegExp(RECOMMENDED_KEY))).toBeNull();
  });

  it('names the recommendation when the rate meets the minimum but not the recommendation', () => {
    renderTyped(SampleRateBadge, {
      props: { sampleRate: 96000, minRate: 96000, recommendedRate: 256000 },
    });
    expect(screen.getByText(new RegExp(RECOMMENDED_KEY))).toBeInTheDocument();
  });

  it('shows nothing when the rate meets the recommendation or the model states no rates', () => {
    const { container, unmount } = renderTyped(SampleRateBadge, {
      props: { sampleRate: 256000, minRate: 96000, recommendedRate: 256000 },
    });
    expect(container.textContent.trim()).toBe('');
    unmount();
    const none = renderTyped(SampleRateBadge, { props: { sampleRate: 8000 } });
    expect(none.container.textContent.trim()).toBe('');
  });
});
