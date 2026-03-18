import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import DetectionRow from './DetectionRow.svelte';
import type { Detection } from '$lib/types/detection.types';
import { navigation } from '$lib/stores/navigation.svelte';
import { fetchWithCSRF } from '$lib/utils/api';
import { toastActions } from '$lib/stores/toast';

// Mock the navigation store
vi.mock('$lib/stores/navigation.svelte', () => ({
  navigation: {
    currentPath: '/ui/detections',
    navigate: vi.fn(),
    handlePopState: vi.fn(),
  },
}));

// Mock fetchWithCSRF
vi.mock('$lib/utils/api', () => ({
  fetchWithCSRF: vi.fn(),
}));

// Create a mock detection for testing
function createMockDetection(overrides: Partial<Detection> = {}): Detection {
  return {
    id: 123,
    date: '2024-01-15',
    time: '10:30:00',
    commonName: 'American Robin',
    scientificName: 'Turdus migratorius',
    confidence: 0.85,
    locked: false,
    sourceType: 'microphone',
    sourceName: 'default',
    clipName: 'clip_001.wav',
    spectrogramPath: '/spectrograms/clip_001.png',
    ...overrides,
  } as Detection;
}

describe('DetectionRow navigation tests', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('navigates to review tab when review action is clicked', async () => {
    const detection = createMockDetection({ id: 456 });

    render(DetectionRow, {
      props: {
        detection,
      },
    });

    // Open the action menu
    const menuButton = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(menuButton);

    // Click review action
    const reviewButton = screen.getByRole('menuitem', { name: /review detection/i });
    await fireEvent.click(reviewButton);

    // Verify navigation was called with correct URL including query parameter
    expect(navigation.navigate).toHaveBeenCalledWith('/ui/detections/456?tab=review');
  });

  it('provides handleReview callback that navigates with query params', async () => {
    // This test verifies the pattern: navigation.navigate is called with ?tab=review
    // which requires the navigation store to properly separate pathname from query
    const detection = createMockDetection({ id: 999 });

    render(DetectionRow, {
      props: {
        detection,
      },
    });

    const menuButton = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(menuButton);

    const reviewButton = screen.getByRole('menuitem', { name: /review detection/i });
    await fireEvent.click(reviewButton);

    // The fix ensures query params don't break routing in App.svelte
    expect(navigation.navigate).toHaveBeenCalledWith('/ui/detections/999?tab=review');
  });

  it('uses correct detection ID in review navigation', async () => {
    const detection = createMockDetection({ id: 257651 });

    render(DetectionRow, {
      props: {
        detection,
      },
    });

    const menuButton = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(menuButton);

    const reviewButton = screen.getByRole('menuitem', { name: /review detection/i });
    await fireEvent.click(reviewButton);

    // Verify the exact URL format matches what App.svelte handleRouting expects
    expect(navigation.navigate).toHaveBeenCalledWith('/ui/detections/257651?tab=review');
  });
});

describe('DetectionRow error notification tests', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('shows error toast when delete action fails', async () => {
    vi.mocked(fetchWithCSRF).mockRejectedValueOnce(new Error('Network error'));
    const detection = createMockDetection({ id: 100 });

    render(DetectionRow, { props: { detection } });

    // Open action menu and click delete
    const menuButton = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(menuButton);
    const deleteButton = screen.getByRole('menuitem', { name: /delete detection/i });
    await fireEvent.click(deleteButton);

    // Confirm the modal
    const confirmButton = await screen.findByRole('button', { name: /delete/i });
    await fireEvent.click(confirmButton);

    await waitFor(() => {
      expect(toastActions.error).toHaveBeenCalledTimes(1);
    });
  });

  it('shows error toast when toggle lock fails', async () => {
    vi.mocked(fetchWithCSRF).mockRejectedValueOnce(new Error('Server error'));
    const detection = createMockDetection({ id: 200, locked: false });

    render(DetectionRow, { props: { detection } });

    // Open action menu and click lock
    const menuButton = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(menuButton);
    const lockButton = screen.getByRole('menuitem', { name: /lock detection/i });
    await fireEvent.click(lockButton);

    // Confirm the modal
    const confirmButton = await screen.findByRole('button', { name: /confirm/i });
    await fireEvent.click(confirmButton);

    await waitFor(() => {
      expect(toastActions.error).toHaveBeenCalledTimes(1);
    });
  });

  it('shows error toast when toggle species exclusion fails', async () => {
    vi.mocked(fetchWithCSRF).mockRejectedValueOnce(new Error('Server error'));
    const detection = createMockDetection({ id: 300 });

    render(DetectionRow, { props: { detection } });

    // Open action menu and click ignore species
    const menuButton = screen.getByRole('button', { name: /actions menu/i });
    await fireEvent.click(menuButton);
    const ignoreButton = screen.getByRole('menuitem', { name: /ignore species/i });
    await fireEvent.click(ignoreButton);

    // Confirm the modal
    const confirmButton = await screen.findByRole('button', { name: /confirm/i });
    await fireEvent.click(confirmButton);

    await waitFor(() => {
      expect(toastActions.error).toHaveBeenCalledTimes(1);
    });
  });
});
