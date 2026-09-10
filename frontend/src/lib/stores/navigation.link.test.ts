import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { handleAppLinkClick, navigation } from './navigation.svelte';
import { resetBasePath, setBasePath } from '$lib/utils/urlHelpers';

describe('application links', () => {
  const originalLocation = window.location;

  beforeEach(() => {
    // Shared test setup stubs window.location; use JSDOM's history-aware location.
    Object.defineProperty(window, 'location', { value: document.location, configurable: true });
    setBasePath('');
    navigation.redirect('/ui/settings/security?tab=oauth');
    vi.spyOn(navigation, 'navigate');
  });

  afterEach(() => {
    document.body.replaceChildren();
    vi.restoreAllMocks();
    resetBasePath();
    Object.defineProperty(window, 'location', { value: originalLocation, configurable: true });
  });

  function clickLink(
    href: string,
    options: MouseEventInit = {},
    attributes: Record<string, string> = {},
    alreadyPrevented = false
  ) {
    const anchor = document.createElement('a');
    anchor.href = href;
    for (const [name, value] of Object.entries(attributes)) anchor.setAttribute(name, value);
    anchor.addEventListener('click', handleAppLinkClick);
    let intercepted = false;
    anchor.addEventListener('click', event => {
      intercepted = event.defaultPrevented;
      // JSDOM does not implement native navigation.
      event.preventDefault();
    });
    document.body.append(anchor);
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, ...options });
    if (alreadyPrevented) event.preventDefault();
    anchor.dispatchEvent(event);
    return intercepted;
  }

  it('routes same-page query and fragment changes without reloading', () => {
    expect(clickLink('/ui/settings/security?tab=server#configuration')).toBe(true);
    expect(navigation.navigate).toHaveBeenCalledWith(
      '/ui/settings/security?tab=server#configuration'
    );
    expect(navigation.currentPath).toBe('/ui/settings/security');
    expect(navigation.currentSearch).toBe('?tab=server');
    expect(window.location.hash).toBe('#configuration');
  });

  it('preserves reverse-proxy prefixes exactly once', () => {
    setBasePath('/birdnet');
    expect(clickLink('/birdnet/ui/settings/main?tab=location')).toBe(true);
    expect(navigation.currentPath).toBe('/ui/settings/main');
    expect(window.location.pathname).toBe('/birdnet/ui/settings/main');
    expect(window.location.search).toBe('?tab=location');
  });

  it.each<MouseEventInit>([
    { ctrlKey: true },
    { metaKey: true },
    { shiftKey: true },
    { altKey: true },
    { button: 1 },
  ])('preserves native behavior for modified clicks: %j', options => {
    expect(clickLink('/ui/settings/main?tab=location', options)).toBe(false);
    expect(navigation.navigate).not.toHaveBeenCalled();
  });

  it.each<{ href: string; attributes: Record<string, string> }>([
    { href: '/ui/settings/main?tab=location', attributes: { target: '_blank' } },
    { href: '/ui/settings/main?tab=location', attributes: { download: '' } },
    { href: 'https://example.org/ui/settings/main?tab=location', attributes: {} },
    { href: '/api/v2/settings', attributes: {} },
  ])('leaves explicit targets and non-app URLs native: %j', ({ href, attributes }) => {
    expect(clickLink(href, {}, attributes)).toBe(false);
    expect(navigation.navigate).not.toHaveBeenCalled();
  });

  it('respects a previously prevented event', () => {
    clickLink('/ui/settings/main?tab=location', {}, {}, true);
    expect(navigation.navigate).not.toHaveBeenCalled();
  });
});
