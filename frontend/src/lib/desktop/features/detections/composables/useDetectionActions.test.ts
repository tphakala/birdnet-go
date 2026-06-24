/**
 * Tests for useDetectionActions.
 *
 * Common mocks (logger, i18n, toast) come from src/test/setup.ts.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';

vi.mock('$lib/utils/api', () => ({
  fetchWithCSRF: vi.fn(),
}));
vi.mock('$lib/utils/reviewDetection', () => ({
  setDetectionVerification: vi.fn(),
}));
vi.mock('$lib/stores/navigation.svelte', () => ({
  navigation: { navigate: vi.fn() },
}));

import { useDetectionActions } from './useDetectionActions.svelte';
import { fetchWithCSRF } from '$lib/utils/api';
import { toastActions } from '$lib/stores/toast';
import type { Detection } from '$lib/types/detection.types';

const mockFetch = vi.mocked(fetchWithCSRF);

function detection(overrides: Partial<Detection>): Detection {
  return { id: 1, commonName: 'House Sparrow', locked: false, ...overrides } as Detection;
}

describe('useDetectionActions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetch.mockReset();
  });

  it('writes the server is_excluded (true) into onToggleExclusion', async () => {
    const onToggleExclusion = vi.fn();
    const actions = useDetectionActions({
      onRefresh: vi.fn(),
      isSpeciesExcluded: () => false,
      onToggleExclusion,
    });
    mockFetch.mockResolvedValue({
      common_name: 'House Sparrow',
      action: 'added',
      is_excluded: true,
    });

    actions.handleToggleSpecies(detection({}));
    await actions.confirmModalConfig.onConfirm();

    expect(onToggleExclusion).toHaveBeenCalledWith('House Sparrow', true);
  });

  it('trusts the server is_excluded=false even when the optimistic guess would differ', async () => {
    const onToggleExclusion = vi.fn();
    const actions = useDetectionActions({
      onRefresh: vi.fn(),
      // local view says "not excluded" -> optimistic negation would be true,
      // but the server reports it ended up not excluded.
      isSpeciesExcluded: () => false,
      onToggleExclusion,
    });
    mockFetch.mockResolvedValue({
      common_name: 'House Sparrow',
      action: 'removed',
      is_excluded: false,
    });

    actions.handleToggleSpecies(detection({}));
    await actions.confirmModalConfig.onConfirm();

    expect(onToggleExclusion).toHaveBeenCalledWith('House Sparrow', false);
  });

  it('still refreshes without updating the set when the toggle returns an empty body', async () => {
    const onRefresh = vi.fn();
    const onToggleExclusion = vi.fn();
    const actions = useDetectionActions({
      onRefresh,
      isSpeciesExcluded: () => false,
      onToggleExclusion,
    });
    // 204 / zero-length responses surface as null from fetchWithCSRF.
    mockFetch.mockResolvedValue(null);

    actions.handleToggleSpecies(detection({}));
    await actions.confirmModalConfig.onConfirm();

    expect(onToggleExclusion).not.toHaveBeenCalled();
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('shows an error toast when the ignore toggle fails', async () => {
    const actions = useDetectionActions({
      onRefresh: vi.fn(),
      isSpeciesExcluded: () => false,
      onToggleExclusion: vi.fn(),
    });
    mockFetch.mockRejectedValue(new Error('network down'));

    actions.handleToggleSpecies(detection({}));
    await actions.confirmModalConfig.onConfirm();

    expect(toastActions.error).toHaveBeenCalledTimes(1);
  });

  it('locks using the value snapshotted at modal-open, not the live prop', async () => {
    const actions = useDetectionActions({
      onRefresh: vi.fn(),
      isSpeciesExcluded: () => false,
      onToggleExclusion: vi.fn(),
    });
    mockFetch.mockResolvedValue({});
    const det = detection({ id: 5, locked: false });

    actions.handleToggleLock(det);
    // Background refresh swaps the underlying object's locked state.
    det.locked = true;
    await actions.confirmModalConfig.onConfirm();

    expect(mockFetch).toHaveBeenCalledWith(
      '/api/v2/detections/5/lock',
      expect.objectContaining({ body: JSON.stringify({ locked: true }) })
    );
  });

  it('deletes the id snapshotted at modal-open', async () => {
    const onRefresh = vi.fn();
    const actions = useDetectionActions({
      onRefresh,
      isSpeciesExcluded: () => false,
      onToggleExclusion: vi.fn(),
    });
    mockFetch.mockResolvedValue({});
    const det = detection({ id: 7 });

    actions.handleDelete(det);
    await actions.confirmModalConfig.onConfirm();

    expect(mockFetch).toHaveBeenCalledWith(
      '/api/v2/detections/7',
      expect.objectContaining({ method: 'DELETE' })
    );
    expect(onRefresh).toHaveBeenCalled();
  });

  it('shows an error toast when a delete request fails', async () => {
    const onRefresh = vi.fn();
    const actions = useDetectionActions({
      onRefresh,
      isSpeciesExcluded: () => false,
      onToggleExclusion: vi.fn(),
    });
    mockFetch.mockRejectedValue(new Error('network down'));

    actions.handleDelete(detection({ id: 9 }));
    await actions.confirmModalConfig.onConfirm();

    expect(toastActions.error).toHaveBeenCalledTimes(1);
    expect(onRefresh).not.toHaveBeenCalled();
  });
});
