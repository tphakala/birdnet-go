import { describe, it, expect } from 'vitest';
import * as Sentry from '@sentry/browser';
import { initSentry, shutdownSentry } from './sentry';

// Runs against the real SDK (no mock) so a change in how the client reads `enabled`,
// or in how close() and re-init behave, is caught here.
describe('shutdownSentry', () => {
  it('disables the client synchronously, clears breadcrumbs, and allows a fresh init', async () => {
    const config = { dsn: 'https://public@example.invalid/1', systemId: 'sys-1', version: '1.0.0' };

    initSentry(config);
    const first = Sentry.getClient();
    expect(Sentry.isEnabled()).toBe(true);
    Sentry.addBreadcrumb({ message: 'before opt-out' });
    expect(Sentry.getIsolationScope().getScopeData().breadcrumbs).not.toHaveLength(0);

    const closing = shutdownSentry();
    // Same turn: close() alone would still report enabled until its flush finishes.
    expect(Sentry.isEnabled()).toBe(false);
    expect(Sentry.getIsolationScope().getScopeData().breadcrumbs).toHaveLength(0);
    await closing;

    initSentry(config);
    expect(Sentry.isEnabled()).toBe(true);
    expect(Sentry.getClient()).not.toBe(first);
  });
});
