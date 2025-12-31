/**
 * Helper test to create a notification for manual testing.
 */
import { describe, expect, it } from 'vitest';
import { apiCall } from './integration-setup';

describe('Test Helpers', () => {
  it('create notification for manual testing', async () => {
    const response = await apiCall('/notifications/test/new-species', {
      method: 'POST',
    });
    expect(response.ok).toBe(true);
    const notification = await response.json();
    console.log('Created notification:', notification.id);
  });
});
