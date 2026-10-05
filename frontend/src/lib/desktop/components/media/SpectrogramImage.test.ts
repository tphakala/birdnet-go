/**
 * Tests for SpectrogramImage: the image-only view of a spectrogram whose audio
 * clip was removed by retention.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, screen, fireEvent } from '../../../../test/render-helpers';
import SpectrogramImage from './SpectrogramImage.svelte';

describe('SpectrogramImage', () => {
  const imageTest = createComponentTestFactory(SpectrogramImage);

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it.each([
    ['md', true, '/api/v2/spectrogram/42?size=md&raw=true'],
    ['lg', false, '/api/v2/spectrogram/42?size=lg'],
    ['xl', true, '/api/v2/spectrogram/42?size=xl&raw=true'],
  ] as const)('requests the %s size with raw=%s', (size, raw, expectedPath) => {
    imageTest.render({ detectionId: '42', size, raw });

    const img = screen.getByRole('img');
    expect(img.getAttribute('src')).toContain(expectedPath);
    expect(img.getAttribute('loading')).toBe('lazy');
  });

  it('shows the unavailable state when the image fails to load, without further requests', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    imageTest.render({ detectionId: '42', size: 'md', raw: true });

    await fireEvent.error(screen.getByRole('img'));

    expect(screen.queryByRole('img')).toBeNull();
    expect(screen.getByText(/unavailable/i)).toBeTruthy();
    const urls = fetchSpy.mock.calls.map(call => String(call[0]));
    expect(urls.some(url => url.includes('/status') || url.includes('/generate'))).toBe(false);
    fetchSpy.mockRestore();
  });

  it('retries with a different detection after a failed load', async () => {
    const { rerender } = imageTest.render({ detectionId: '42', size: 'md', raw: true });
    await fireEvent.error(screen.getByRole('img'));
    expect(screen.queryByRole('img')).toBeNull();

    await rerender({ detectionId: '43', size: 'md', raw: true });

    expect(screen.getByRole('img').getAttribute('src')).toContain('/api/v2/spectrogram/43?');
  });

  it('uses the given alt text', () => {
    imageTest.render({ detectionId: '42', size: 'md', raw: true, alt: 'Spectrogram for Robin' });

    expect(screen.getByRole('img', { name: 'Spectrogram for Robin' })).toBeTruthy();
  });
});
