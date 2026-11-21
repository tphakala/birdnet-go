import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, waitFor } from '@testing-library/svelte';

// A page restored from the back/forward cache keeps state that assumed it was going away (closed
// streams, a stopped heartbeat, a closed popout, one-way navigation flags), so App reloads it.
vi.mock('./lib/desktop/layouts/RootLayout.svelte', () => ({ default: () => {} }));
vi.mock('./lib/desktop/features/dashboard/pages/DashboardPage.svelte', () => ({
  default: () => {},
}));
vi.mock('./lib/desktop/features/wizard/WizardDialog.svelte', () => ({ default: () => {} }));
vi.mock('./lib/stores/sseNotifications', () => ({ sseNotifications: {} }));

vi.mock('./lib/stores/appState.svelte', async importOriginal => {
  const original = await importOriginal<typeof import('./lib/stores/appState.svelte')>();
  return {
    ...original,
    initApp: vi.fn(async () => {
      Object.assign(original.appState.security, {
        enabled: false,
        accessAllowed: true,
        privateMode: false,
      });
      original.appState.initialized = true;
      original.appState.loading = false;
      return true;
    }),
  };
});

import App from './App.svelte';
import { unsavedChanges } from './lib/stores/unsavedChanges.svelte';

describe('App page restore', () => {
  let unregister: (() => void) | undefined;

  beforeEach(() => {
    vi.mocked(window.location.reload).mockClear();
    unregister = unsavedChanges.register({ isDirty: () => true });
  });

  afterEach(() => {
    unregister?.();
    unsavedChanges.resetForTesting();
  });

  it('reloads a page restored from the back/forward cache, past the leave prompt', async () => {
    render(App);

    window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));

    await waitFor(() => expect(window.location.reload).toHaveBeenCalledTimes(1));
    const unload = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(false);
  });

  it('does not reload on an ordinary page show', async () => {
    render(App);

    window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: false }));
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(window.location.reload).not.toHaveBeenCalled();
  });
});
