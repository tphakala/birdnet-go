import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import DetectionRow from './DetectionRow.svelte';
import type { Detection } from '$lib/types/detection.types';
import { navigation } from '$lib/stores/navigation.svelte';

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
