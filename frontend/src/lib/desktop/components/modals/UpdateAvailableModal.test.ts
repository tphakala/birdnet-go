/**
 * Tests for UpdateAvailableModal.
 *
 * The i18n mock in src/test/setup.ts returns the key unchanged, so labels here
 * equal their translation keys.
 */

import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/svelte';
import UpdateAvailableModal from './UpdateAvailableModal.svelte';
import type { UpdateInfo } from '$lib/types/update';

const baseInfo: UpdateInfo = {
  updateAvailable: true,
  latestVersion: 'v0.7.1',
  latestName: 'v0.7.1 release',
  releasedAt: '2026-07-08T12:00:00Z',
  channel: 'stable',
  notes: 'Fixed a notable bug',
  releaseURL: 'https://example.test/release',
  critical: false,
};

afterEach(cleanup);

describe('UpdateAvailableModal', () => {
  it('renders the release title, changelog, and a link to the release when open', () => {
    render(UpdateAvailableModal, {
      props: { isOpen: true, info: baseInfo, currentVersion: 'v0.7.0', onClose: () => {} },
    });

    expect(screen.getByText('v0.7.1 release')).toBeInTheDocument();
    expect(screen.getByText('Fixed a notable bug')).toBeInTheDocument();

    const link = screen.getByRole('link', { name: /viewRelease/i });
    expect(link).toHaveAttribute('href', 'https://example.test/release');
    // The manifest link was intentionally removed.
    expect(screen.queryByText(/viewManifest/i)).not.toBeInTheDocument();
  });

  it('shows the critical banner only for critical updates', () => {
    render(UpdateAvailableModal, {
      props: {
        isOpen: true,
        info: { ...baseInfo, critical: true },
        currentVersion: 'v0.7.0',
        onClose: () => {},
      },
    });

    expect(screen.getByRole('alert')).toBeInTheDocument();
  });
});
