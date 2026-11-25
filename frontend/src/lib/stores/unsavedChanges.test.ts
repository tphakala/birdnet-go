import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  createUnsavedChanges,
  samePathAndQuery,
  type UnsavedChanges,
  type NavigationTarget,
} from './unsavedChanges.svelte';

const A: NavigationTarget = { path: '/ui/a', search: '' };
const B: NavigationTarget = { path: '/ui/b', search: '' };
const AQuery: NavigationTarget = { path: '/ui/a', search: '?tab=x' };

function firePageUnload(): Event {
  const event = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(event);
  return event;
}

describe('unsavedChanges registry', () => {
  let registry: UnsavedChanges;
  const cleanups: Array<() => void> = [];

  function register(
    isDirty: () => boolean,
    allows?: (to: NavigationTarget, from: NavigationTarget) => boolean
  ) {
    const off = registry.register({ isDirty, allows });
    cleanups.push(off);
    return off;
  }

  beforeEach(() => {
    registry = createUnsavedChanges();
  });

  afterEach(() => {
    registry.resetForTesting();
    for (const off of cleanups.splice(0)) off();
  });

  describe('blocks', () => {
    it('never blocks while clean', () => {
      register(() => false);
      expect(registry.blocks(B, A)).toBe(false);
    });

    it('blocks a path change while dirty and allows same-path and query-only moves', () => {
      register(() => true);
      expect(registry.blocks(B, A)).toBe(true);
      expect(registry.blocks(AQuery, A)).toBe(false);
      expect(registry.blocks(A, A)).toBe(false);
    });

    it('blocks a query change when the guard uses samePathAndQuery', () => {
      register(() => true, samePathAndQuery);
      expect(registry.blocks(AQuery, A)).toBe(true);
      expect(registry.blocks(A, A)).toBe(false);
    });

    it('honours a custom allows predicate', () => {
      register(
        () => true,
        to => to.path.startsWith('/ui/a')
      );
      expect(registry.blocks({ path: '/ui/a/sub', search: '' }, A)).toBe(false);
      expect(registry.blocks(B, A)).toBe(true);
    });

    it('reads isDirty at decision time and stops blocking after unregister', () => {
      let dirty = false;
      const off = register(() => dirty);
      expect(registry.blocks(B, A)).toBe(false);
      dirty = true;
      expect(registry.blocks(B, A)).toBe(true);
      off();
      expect(registry.blocks(B, A)).toBe(false);
    });
  });

  describe('request', () => {
    it('returns null when nothing blocks', () => {
      register(() => false);
      expect(registry.request(B, A)).toBeNull();
      expect(registry.prompting).toBe(false);
    });

    it('resolves true on leave, then stays released away from the leaving path only', async () => {
      register(() => true);
      const decision = registry.request(B, A);
      expect(decision).not.toBeNull();
      expect(registry.prompting).toBe(true);
      registry.leave();
      await expect(decision).resolves.toBe(true);
      expect(registry.prompting).toBe(false);
      // On its way out: a follow-up move from the new path does not ask again.
      expect(registry.blocks({ path: '/ui/c', search: '' }, B)).toBe(false);
      // Back on the path it left from, it protects again.
      expect(registry.blocks(B, A)).toBe(true);
    });

    it('resolves false on stay and keeps blocking', async () => {
      register(() => true);
      const decision = registry.request(B, A);
      registry.stay();
      await expect(decision).resolves.toBe(false);
      expect(registry.prompting).toBe(false);
      expect(registry.blocks(B, A)).toBe(true);
    });

    it('resolves false for every move while a prompt is open and keeps the first prompt open', async () => {
      register(() => true);
      const first = registry.request(B, A);
      await expect(registry.request(B, A)).resolves.toBe(false);
      await expect(registry.request(AQuery, A)).resolves.toBe(false);
      expect(registry.prompting).toBe(true);
      registry.leave();
      await expect(first).resolves.toBe(true);
    });
  });

  describe('confirmDiscard', () => {
    it('resolves true without prompting when clean', async () => {
      register(() => false);
      await expect(registry.confirmDiscard()).resolves.toBe(true);
      expect(registry.prompting).toBe(false);
    });

    it('prompts when dirty, resolves false on stay and true on leave', async () => {
      register(() => true);
      const stayed = registry.confirmDiscard();
      expect(registry.prompting).toBe(true);
      registry.stay();
      await expect(stayed).resolves.toBe(false);
      const left = registry.confirmDiscard();
      registry.leave();
      await expect(left).resolves.toBe(true);
    });

    it('releases nothing after leave', async () => {
      register(() => true);
      const left = registry.confirmDiscard();
      registry.leave();
      await left;
      expect(registry.blocks(B, A)).toBe(true);
    });

    it('resolves false while another prompt is open', async () => {
      register(() => true);
      void registry.request(B, A);
      await expect(registry.confirmDiscard()).resolves.toBe(false);
    });
  });

  describe('beforeunload', () => {
    it('is prevented while dirty', () => {
      register(() => true);
      expect(firePageUnload().defaultPrevented).toBe(true);
    });

    it('is not prevented while clean', () => {
      register(() => false);
      expect(firePageUnload().defaultPrevented).toBe(false);
    });

    it('is not prevented after the last registration is removed', () => {
      const off = register(() => true);
      off();
      expect(firePageUnload().defaultPrevented).toBe(false);
    });

    it('is not prevented after allowUnload', () => {
      register(() => true);
      registry.allowUnload();
      expect(firePageUnload().defaultPrevented).toBe(false);
    });
  });
});
