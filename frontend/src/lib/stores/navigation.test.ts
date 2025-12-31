// frontend/src/lib/stores/navigation.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { navigation, createNavigation } from './navigation.svelte';

describe('navigation store', () => {
  beforeEach(() => {
    // Mock history.pushState
    vi.spyOn(window.history, 'pushState').mockImplementation(() => {});
  });

  describe('navigate', () => {
    it('should update currentPath', () => {
      const nav = createNavigation();
      nav.navigate('/ui/settings');
      expect(nav.currentPath).toBe('/ui/settings');
    });

    it('should call history.pushState', () => {
      const nav = createNavigation();
      nav.navigate('/ui/analytics');
      expect(window.history.pushState).toHaveBeenCalledWith({}, '', '/ui/analytics');
    });

    it('should normalize paths without /ui/ prefix', () => {
      const nav = createNavigation();
      nav.navigate('/settings');
      expect(nav.currentPath).toBe('/ui/settings');
    });

    it('should handle root path', () => {
      const nav = createNavigation();
      nav.navigate('/');
      expect(nav.currentPath).toBe('/ui/dashboard');
    });
  });

  describe('handlePopState', () => {
    it('should update currentPath from window.location', () => {
      const nav = createNavigation();
      // Simulate browser back by setting window.location.pathname
      Object.defineProperty(window, 'location', {
        value: { pathname: '/ui/about' },
        writable: true,
        configurable: true,
      });
      nav.handlePopState();
      expect(nav.currentPath).toBe('/ui/about');
    });
  });
});
