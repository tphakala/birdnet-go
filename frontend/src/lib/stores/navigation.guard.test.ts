import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { navigation } from './navigation.svelte';
import { unsavedChanges } from './unsavedChanges.svelte';
import { resetBasePath, setBasePath } from '$lib/utils/urlHelpers';

const START = '/ui/start';
const MIDDLE = '/ui/middle';
const GUARDED = '/ui/guarded';
const ELSEWHERE = '/ui/elsewhere';

describe('navigation with an unsaved-changes guard', () => {
  const originalLocation = window.location;
  let dirty = true;
  let unregister: (() => void) | undefined;
  const pageListener = vi.fn<(event: PopStateEvent) => void>();
  let popWaiters: Array<(applied: boolean) => void> = [];

  // Mirrors App.svelte: the capture listener asks the store and swallows a held pop.
  const appPopListener = (event: PopStateEvent) => {
    const applied = navigation.handlePopState();
    if (!applied) event.stopImmediatePropagation();
    popWaiters.shift()?.(applied);
  };

  function nextPop(): Promise<boolean> {
    return new Promise(resolve => popWaiters.push(resolve));
  }

  beforeEach(() => {
    // Shared test setup stubs window.location; use JSDOM's history-aware location.
    Object.defineProperty(window, 'location', { value: document.location, configurable: true });
    setBasePath('');
    dirty = true;
    popWaiters = [];
    pageListener.mockClear();
    window.addEventListener('popstate', appPopListener, { capture: true });
    window.addEventListener('popstate', pageListener);
  });

  afterEach(() => {
    unregister?.();
    unsavedChanges.resetForTesting();
    window.removeEventListener('popstate', appPopListener, { capture: true });
    window.removeEventListener('popstate', pageListener);
    resetBasePath();
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
  });

  /** Builds [START, GUARDED] (or with MIDDLE) and registers a dirty guard on the last entry. */
  async function arrive(...paths: string[]) {
    navigation.redirect(paths[0]);
    for (const path of paths.slice(1)) await navigation.navigate(path);
    unregister = unsavedChanges.register({ isDirty: () => dirty });
  }

  describe('navigate and redirect', () => {
    it('holds navigate until Leave and then applies the target', async () => {
      await arrive(START, GUARDED);
      const lengthBefore = window.history.length;

      const decision = navigation.navigate(ELSEWHERE);
      expect(unsavedChanges.prompting).toBe(true);
      expect(navigation.currentPath).toBe(GUARDED);
      expect(window.location.pathname).toBe(GUARDED);
      expect(window.history.length).toBe(lengthBefore);

      unsavedChanges.leave();
      await expect(decision).resolves.toBe(true);
      expect(navigation.currentPath).toBe(ELSEWHERE);
      expect(window.location.pathname).toBe(ELSEWHERE);
      expect(window.history.length).toBe(lengthBefore + 1);
    });

    it('leaves everything as it was on Stay', async () => {
      await arrive(START, GUARDED);
      const lengthBefore = window.history.length;

      const decision = navigation.navigate(ELSEWHERE);
      unsavedChanges.stay();

      await expect(decision).resolves.toBe(false);
      expect(navigation.currentPath).toBe(GUARDED);
      expect(window.location.pathname).toBe(GUARDED);
      expect(window.history.length).toBe(lengthBefore);
    });

    it('applies a query-only navigate synchronously while dirty', async () => {
      await arrive(START, GUARDED);
      void navigation.navigate(`${GUARDED}?x=1`);
      expect(unsavedChanges.prompting).toBe(false);
      expect(navigation.currentSearch).toBe('?x=1');
      expect(window.location.search).toBe('?x=1');
    });

    it('holds redirect the same way, and does not hold a follow-up redirect after Leave', async () => {
      await arrive(START, GUARDED);

      const decision = navigation.redirect(ELSEWHERE);
      expect(navigation.currentPath).toBe(GUARDED);
      unsavedChanges.leave();
      await expect(decision).resolves.toBe(true);
      expect(navigation.currentPath).toBe(ELSEWHERE);

      // Routing redirects the old page away in the same transition (App.svelte retired routes).
      void navigation.redirect(MIDDLE);
      expect(unsavedChanges.prompting).toBe(false);
      expect(navigation.currentPath).toBe(MIDDLE);
    });

    it('drops an allowed navigate while a prompt is open', async () => {
      await arrive(START, GUARDED);
      const decision = navigation.navigate(ELSEWHERE);
      const lengthBefore = window.history.length;

      await expect(navigation.navigate(`${GUARDED}?x=1`)).resolves.toBe(false);

      expect(window.history.length).toBe(lengthBefore);
      expect(window.location.search).toBe('');
      unsavedChanges.stay();
      await decision;
    });
  });

  describe('Back and Forward', () => {
    it('holds Back, restores the guarded URL, and lands on the popped entry on Leave', async () => {
      await arrive(START, GUARDED);

      const held = nextPop();
      window.history.back();
      await expect(held).resolves.toBe(false);

      expect(unsavedChanges.prompting).toBe(true);
      expect(window.location.pathname).toBe(GUARDED);
      expect(navigation.currentPath).toBe(GUARDED);
      expect(pageListener).not.toHaveBeenCalled();

      const landed = nextPop();
      unsavedChanges.leave();
      await expect(landed).resolves.toBe(true);
      expect(window.location.pathname).toBe(START);
      expect(navigation.currentPath).toBe(START);
    });

    it('restores the page URL with its query, hash and proxy prefix after a held Back', async () => {
      setBasePath('/proxy');
      await arrive(START, `${GUARDED}?tab=one#frag`);

      const held = nextPop();
      window.history.back();
      await held;

      expect(window.location.pathname).toBe(`/proxy${GUARDED}`);
      expect(window.location.search).toBe('?tab=one');
      expect(window.location.hash).toBe('#frag');
      expect(navigation.currentSearch).toBe('?tab=one');
    });

    it('restores a query a page wrote itself once the store was told', async () => {
      await arrive(START, GUARDED);
      // A page rewrites its own query outside the store, then reports it.
      window.history.replaceState(null, '', `${GUARDED}?date=2026-01-02`);
      navigation.syncFromLocation();

      const held = nextPop();
      window.history.back();
      await held;

      expect(window.location.pathname).toBe(GUARDED);
      expect(window.location.search).toBe('?date=2026-01-02');
      expect(navigation.currentSearch).toBe('?date=2026-01-02');
    });

    it('keeps the page on Stay after Back', async () => {
      await arrive(START, GUARDED);

      const held = nextPop();
      window.history.back();
      await held;
      unsavedChanges.stay();

      expect(window.location.pathname).toBe(GUARDED);
      expect(navigation.currentPath).toBe(GUARDED);
    });

    it('holds Forward and lands on the popped entry on Leave', async () => {
      await arrive(START, GUARDED);
      dirty = false;
      const back = nextPop();
      window.history.back();
      await back;
      expect(navigation.currentPath).toBe(START);

      dirty = true;
      const held = nextPop();
      window.history.forward();
      await expect(held).resolves.toBe(false);
      expect(window.location.pathname).toBe(START);

      const landed = nextPop();
      unsavedChanges.leave();
      await landed;
      expect(window.location.pathname).toBe(GUARDED);
      expect(navigation.currentPath).toBe(GUARDED);
    });

    it('answers an open prompt with Stay on a second Back and prompts for the new target', async () => {
      await arrive(START, MIDDLE, GUARDED);
      const request = vi.spyOn(unsavedChanges, 'request');

      const first = nextPop();
      window.history.back();
      await first;
      expect(unsavedChanges.prompting).toBe(true);
      const firstDecision: Promise<boolean> | undefined = request.mock.results[0]?.value;

      const second = nextPop();
      window.history.back();
      await expect(second).resolves.toBe(false);
      // The first prompt was answered with Stay, and the second pop opened its own prompt.
      await expect(Promise.race([firstDecision, Promise.resolve('open')])).resolves.toBe(false);
      expect(request).toHaveBeenCalledTimes(2);
      expect(unsavedChanges.prompting).toBe(true);
      expect(window.location.pathname).toBe(GUARDED);

      const landed = nextPop();
      unsavedChanges.leave();
      await landed;
      expect(window.location.pathname).toBe(MIDDLE);
      expect(navigation.currentPath).toBe(MIDDLE);
    });
  });
});
