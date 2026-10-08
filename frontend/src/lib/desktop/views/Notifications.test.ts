import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import { removeStoredValue } from '$lib/utils/storage';
import Notifications from './Notifications.svelte';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn().mockResolvedValue({ notifications: [], total: 0 }),
    post: vi.fn().mockResolvedValue({}),
    put: vi.fn().mockResolvedValue({}),
    delete: vi.fn().mockResolvedValue({}),
  },
}));

describe('Notifications filter dropdown names', () => {
  beforeEach(() => {
    // Only the view mode this view persists; other keys belong to other tests or the setup
    removeStoredValue('notifications-view-mode');
  });

  it.each([
    ['notifications.aria.filterByStatus', 'notifications.filters.allStatus'],
    ['notifications.aria.filterByType', 'notifications.filters.allTypes'],
    ['notifications.aria.filterByPriority', 'notifications.filters.allPriorities'],
  ])('names the filter %s independently of the selected value', async (name, shownValue) => {
    render(Notifications);

    await waitFor(() => expect(screen.getByRole('button', { name })).toBeInTheDocument());
    expect(screen.getByRole('button', { name })).toHaveTextContent(shownValue);
  });
});
