import { describe, it, expect, afterEach } from 'vitest';
import * as Sentry from '@sentry/browser';
import { initSentry, shutdownSentry } from './sentry';

const CONFIG = { dsn: 'https://public@example.invalid/1', systemId: 'sys-1', version: '1.0.0' };
const CLOSE_TIMEOUT_MS = 1000;

const storedBreadcrumbs = () => Sentry.getIsolationScope().getScopeData().breadcrumbs;

// Runs against the real SDK (no mock) so a change in how the client reads `enabled`,
// or in how close() and re-init behave, is caught here.
describe('shutdownSentry', () => {
  afterEach(async () => {
    await Sentry.close(CLOSE_TIMEOUT_MS);
  });

  it('disables the client synchronously, clears breadcrumbs, and allows a fresh init', async () => {
    initSentry(CONFIG);
    const first = Sentry.getClient();
    expect(Sentry.isEnabled()).toBe(true);
    Sentry.addBreadcrumb({ message: 'before opt-out' });
    expect(storedBreadcrumbs()).not.toHaveLength(0);

    const closing = shutdownSentry();
    // Same turn: close() alone would still report enabled until its flush finishes.
    expect(Sentry.isEnabled()).toBe(false);
    expect(storedBreadcrumbs()).toHaveLength(0);
    await closing;

    initSentry(CONFIG);
    expect(Sentry.isEnabled()).toBe(true);
    expect(Sentry.getClient()).not.toBe(first);
  });

  it('starts a new client without breadcrumbs recorded while stopped', async () => {
    initSentry(CONFIG);
    await shutdownSentry();

    // The stopped client stays bound, so the SDK keeps recording breadcrumbs.
    Sentry.addBreadcrumb({ message: 'while stopped' });
    expect(storedBreadcrumbs()).toHaveLength(1);

    initSentry(CONFIG);
    expect(storedBreadcrumbs()).toHaveLength(0);
  });
});
