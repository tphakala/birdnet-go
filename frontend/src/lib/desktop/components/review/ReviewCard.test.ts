import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import ReviewCardTestWrapper from './ReviewCard.test.svelte';

// Mock the i18n module
vi.mock('$lib/i18n', () => ({
  t: (key: string) => key,
}));

// Mock the api module
vi.mock('$lib/utils/api', () => ({
  fetchWithCSRF: vi.fn(),
}));

describe('ReviewCard', () => {
  describe('comment section', () => {
    it('should stay open when user types in comment textarea (issue #1683)', async () => {
      const user = userEvent.setup();

      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(ReviewCardTestWrapper as any);

      // Find and click the button to expand the comment section
      const expandButton = screen.getByRole('button', {
        name: /common\.review\.form\.addComment/i,
      });
      await user.click(expandButton);

      // Wait for the textarea to appear
      const textarea = await waitFor(() => screen.getByRole('textbox'));
      expect(textarea).toBeInTheDocument();

      // Type in the textarea
      await user.type(textarea, 'Test comment');

      // The textarea should still be visible after typing
      // This is the bug: the comment section collapses on first keystroke
      expect(textarea).toBeInTheDocument();
      expect(textarea).toHaveValue('Test comment');

      // The expand button should now show "Hide Comment" (indicating section is still open)
      const hideButton = screen.getByRole('button', {
        name: /common\.review\.form\.hideComment/i,
      });
      expect(hideButton).toBeInTheDocument();
    });

    it('should preserve comment when user expands section with empty detection comments', async () => {
      const user = userEvent.setup();

      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(ReviewCardTestWrapper as any, {
        props: {
          detection: {
            id: 1,
            date: '2024-12-30',
            time: '12:00:00',
            source: 'test',
            beginTime: '12:00:00',
            endTime: '12:00:03',
            speciesCode: 'test',
            scientificName: 'Testus birdus',
            commonName: 'Test Bird',
            confidence: 0.95,
            verified: 'unverified',
            locked: false,
            comments: [], // Empty comments
          },
        },
      });

      // Expand the comment section
      const expandButton = screen.getByRole('button', {
        name: /common\.review\.form\.addComment/i,
      });
      await user.click(expandButton);

      // Wait for textarea and type
      const textarea = await waitFor(() => screen.getByRole('textbox'));
      await user.type(textarea, 'My new comment');

      // After typing, textarea should still have the value
      expect(textarea).toHaveValue('My new comment');

      // Section should still be expanded (textarea visible)
      expect(screen.getByRole('textbox')).toBeInTheDocument();
    });
  });
});
