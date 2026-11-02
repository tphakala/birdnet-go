import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../test/render-helpers';
import LegacyCleanupCard from './LegacyCleanupCard.svelte';
import type { LegacyStatus } from '$lib/types/legacy';

const legacyStatus = {
  exists: true,
  can_cleanup: true,
  reason: '',
  size_bytes: 1024,
  total_records: 10,
  last_modified: null,
  location: '/data/birdnet-legacy.db',
  tables: [],
} satisfies LegacyStatus;

function renderCard(cleanupState: string, cleanupError = '') {
  return renderTyped(LegacyCleanupCard, {
    props: {
      status: legacyStatus,
      cleanupState,
      cleanupError,
      cleanupSpaceReclaimed: 0,
      isLoading: false,
      onDelete: () => {},
    },
  });
}

describe('LegacyCleanupCard', () => {
  afterEach(() => {
    cleanup();
  });

  it('shows a failed cleanup as one titled alert with the server message', () => {
    renderCard('failed', 'Legacy database is still in use');

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(t('system.database.legacy.cleanup.failed'));
    expect(alert).toHaveTextContent('Legacy database is still in use');
    expect(alert).not.toHaveAttribute('aria-live');
  });

  it('shows no alert before the cleanup has failed', () => {
    renderCard('idle');

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
