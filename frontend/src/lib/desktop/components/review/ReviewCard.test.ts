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
  it('renders detected prediction before ranked alternative predictions when present', async () => {
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(ReviewCardTestWrapper as any, {
      props: {
        detection: {
          id: 1,
          date: '2024-12-30',
          time: '12:00:00',
          source: { id: 'test', type: 'unknown' },
          beginTime: '12:00:00',
          endTime: '12:00:03',
          speciesCode: 'amcro',
          scientificName: 'Corvus brachyrhynchos',
          commonName: 'American Crow',
          confidence: 0.95,
          verified: 'unverified',
          locked: false,
          comments: [],
          alternativePredictions: [
            {
              rank: 2,
              scientificName: 'Melanerpes carolinus',
              commonName: 'Red-bellied Woodpecker',
              speciesCode: 'RBWO',
              confidence: 0.86,
            },
            {
              rank: 3,
              scientificName: 'Cyanocitta cristata',
              commonName: 'Blue Jay',
              speciesCode: 'BLJA',
              confidence: 0.72,
            },
            {
              rank: 4,
              scientificName: 'Corvus ossifragus',
              commonName: 'Fish Crow',
              speciesCode: 'FICR',
              confidence: 0.64,
            },
            {
              rank: 5,
              scientificName: 'Corvus corax',
              commonName: 'Common Raven',
              speciesCode: 'CORA',
              confidence: 0.51,
            },
          ],
        },
      },
    });

    expect(screen.getByText('common.review.form.alternativePredictionsTitle')).toBeInTheDocument();
    expect(screen.getByText('American Crow')).toBeInTheDocument();
    expect(screen.getByText('Corvus brachyrhynchos')).toBeInTheDocument();
    expect(screen.getByText('common.review.form.detectedPredictionLabel')).toBeInTheDocument();
    expect(screen.getByText('95%')).toHaveClass('bg-[var(--color-success)]');
    expect(screen.getByText('Red-bellied Woodpecker')).toBeInTheDocument();
    expect(screen.getByText('Melanerpes carolinus')).toBeInTheDocument();
    expect(screen.getByText('86%')).toHaveClass(
      'bg-[color-mix(in_srgb,var(--color-success)_80%,var(--color-warning))]'
    );
    expect(screen.getByText('Blue Jay')).toBeInTheDocument();
    expect(screen.queryByText('Fish Crow')).not.toBeInTheDocument();
    expect(screen.queryByText('Common Raven')).not.toBeInTheDocument();

    const candidateNames = screen
      .getAllByText(/American Crow|Red-bellied Woodpecker|Blue Jay/)
      .map(element => element.textContent);
    expect(candidateNames).toEqual(['American Crow', 'Red-bellied Woodpecker', 'Blue Jay']);

    const showMoreButton = screen.getByRole('button', {
      name: /common\.ui\.showMore \(2\)/,
    });
    expect(showMoreButton).toHaveAttribute('aria-expanded', 'false');

    await user.click(showMoreButton);

    expect(screen.getByText('Fish Crow')).toBeInTheDocument();
    expect(screen.getByText('Corvus ossifragus')).toBeInTheDocument();
    expect(screen.getByText('Common Raven')).toBeInTheDocument();
    expect(screen.getByText('Corvus corax')).toBeInTheDocument();

    expect(
      screen.getByRole('button', {
        name: 'common.ui.showLess',
      })
    ).toHaveAttribute('aria-expanded', 'true');

    const expandedCandidateNames = screen
      .getAllByText(/American Crow|Red-bellied Woodpecker|Blue Jay|Fish Crow|Common Raven/)
      .map(element => element.textContent);
    expect(expandedCandidateNames).toEqual([
      'American Crow',
      'Red-bellied Woodpecker',
      'Blue Jay',
      'Fish Crow',
      'Common Raven',
    ]);
  });

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
