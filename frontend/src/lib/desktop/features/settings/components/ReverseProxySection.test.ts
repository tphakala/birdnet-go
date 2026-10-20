import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { t } from '$lib/i18n';
import { settingsAPI, type ConnectionCheckResult } from '$lib/utils/settingsApi';
import { navigation } from '$lib/stores/navigation.svelte';
import { deferred } from '../../../../../test/async-helpers';
import ReverseProxySection from './ReverseProxySection.svelte';

const getConnection = vi.mocked(settingsAPI.security.getConnection);
const originalGetConnection = getConnection.getMockImplementation();
const originalT = vi.mocked(t).getMockImplementation();

function result(overrides: Partial<ConnectionCheckResult> = {}): ConnectionCheckResult {
  return {
    status: 'untrusted_proxy',
    peerAddress: '172.18.0.1',
    hasForwardedHeaders: true,
    peerIsTrustedProxy: false,
    verifiedClientAddress: '',
    peerIsCloudflareEdge: false,
    cloudflarePresetWouldVerify: false,
    ...overrides,
  };
}

interface SectionProps {
  trustedProxies: string[];
  savedTrustedProxies: string[];
  dataLoaded: boolean;
  disabled: boolean;
  hasChanges: boolean;
  onUpdate: (list: string[]) => void;
  draft?: string;
}

function renderSection(overrides: Partial<SectionProps> = {}) {
  const props: SectionProps = {
    trustedProxies: [],
    savedTrustedProxies: [],
    dataLoaded: true,
    disabled: false,
    hasChanges: false,
    onUpdate: vi.fn(),
    ...overrides,
  };
  return { props, ...render(ReverseProxySection, { props }) };
}

function rows(): HTMLElement[] {
  return within(screen.getByRole('list', { name: /reverseProxy\.listLabel/ })).getAllByRole(
    'listitem'
  );
}

function cloudflareCheckbox(): HTMLInputElement {
  return screen.getByRole<HTMLInputElement>('checkbox', { name: /reverseProxy\.cloudflareLabel/ });
}

describe('ReverseProxySection', () => {
  beforeEach(() => {
    vi.mocked(t).mockImplementation((key: string, params?: Record<string, unknown>) =>
      params ? `${key} ${JSON.stringify(params)}` : key
    );
    getConnection.mockResolvedValue(result());
  });

  afterEach(() => {
    vi.mocked(t).mockReset();
    if (originalT) vi.mocked(t).mockImplementation(originalT);
    getConnection.mockReset();
    if (originalGetConnection) getConnection.mockImplementation(originalGetConnection);
    window.location.hash = '';
    vi.restoreAllMocks();
  });

  it('lists the addresses without the Cloudflare token and checks the box for any spelling', () => {
    const list = ['10.0.0.1', 'CLOUDFLARE', '172.18.0.0/16'];
    renderSection({ trustedProxies: list, savedTrustedProxies: list });

    expect(rows().map(row => row.querySelector('.font-mono')?.textContent)).toEqual([
      '10.0.0.1',
      '172.18.0.0/16',
    ]);
    expect(cloudflareCheckbox().checked).toBe(true);
  });

  it('names Cloudflare in the empty state when only the preset is saved', () => {
    renderSection({ trustedProxies: ['cloudflare'], savedTrustedProxies: ['cloudflare'] });

    expect(
      screen.getByText('settings.security.reverseProxy.emptyStateCloudflareOnly')
    ).toBeInTheDocument();
    expect(screen.queryByText('settings.security.reverseProxy.emptyState')).toBeNull();
  });

  it('keeps the plain empty state when the preset is off', () => {
    renderSection();

    expect(screen.getByText('settings.security.reverseProxy.emptyState')).toBeInTheDocument();
  });

  it('turns the preset off and back on without touching the addresses', async () => {
    const saved = ['10.0.0.1', 'Cloudflare', '10.0.0.2'];
    const onUpdate = vi.fn();
    const { rerender } = renderSection({
      trustedProxies: saved,
      savedTrustedProxies: saved,
      onUpdate,
    });

    await fireEvent.click(cloudflareCheckbox());
    expect(onUpdate).toHaveBeenLastCalledWith(['10.0.0.1', '10.0.0.2']);

    await rerender({ trustedProxies: ['10.0.0.1', '10.0.0.2'] });
    await fireEvent.click(cloudflareCheckbox());
    expect(onUpdate).toHaveBeenLastCalledWith(saved);
  });

  it('keeps the Cloudflare token when an address is added', async () => {
    const saved = ['cloudflare'];
    const onUpdate = vi.fn();
    renderSection({ trustedProxies: saved, savedTrustedProxies: saved, onUpdate });

    const field = screen.getByRole('textbox', { name: /reverseProxy\.listLabel/ });
    await fireEvent.input(field, { target: { value: '172.18.0.1' } });
    await fireEvent.keyDown(field, { key: 'Enter' });

    expect(onUpdate).toHaveBeenLastCalledWith(['cloudflare', '172.18.0.1']);
  });

  it('marks ranges, and only valid ranges, with the Range badge', () => {
    const list = [
      '10.0.0.0/8',
      '10.0.0.1',
      '10.0.0.2/32',
      '::1/128',
      'nonsense/8',
      '2001:db8::/32',
    ];
    renderSection({ trustedProxies: list, savedTrustedProxies: list });

    const badged = rows().map(
      row => within(row).queryByText('settings.security.reverseProxy.rangeBadge') !== null
    );
    expect(badged).toEqual([true, false, false, false, false, true]);
  });

  it('waits for the settings to load before the first check, then checks once', async () => {
    const { rerender } = renderSection({ dataLoaded: false });
    await tick();
    expect(getConnection).not.toHaveBeenCalled();

    await rerender({ dataLoaded: true });

    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(1));
  });

  it('checks again when the saved list changes but not when only the form list changes', async () => {
    const { rerender } = renderSection();
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(1));

    await rerender({ trustedProxies: ['172.18.0.1'] });
    await tick();
    expect(getConnection).toHaveBeenCalledTimes(1);

    await rerender({ savedTrustedProxies: ['172.18.0.1'] });
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(2));
  });

  it('adds the peer with Trust, and hides Trust once the peer is in the form list', async () => {
    const onUpdate = vi.fn();
    const { rerender } = renderSection({ trustedProxies: ['10.0.0.1'], onUpdate });
    const trust = await screen.findByRole('button', { name: /check\.trust \{"peer"/ });

    await fireEvent.click(trust);
    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1', '172.18.0.1']);

    await rerender({ trustedProxies: ['10.0.0.1', '172.18.0.1'] });
    expect(screen.queryByRole('button', { name: /check\.trust \{"peer"/ })).toBeNull();
    expect(screen.getByText('settings.security.reverseProxy.check.added')).toBeInTheDocument();
  });

  it('treats a typed /32 of the peer as listed, but not an invalid row', async () => {
    const { rerender } = renderSection({ trustedProxies: ['172.18.0.1/32'] });
    await screen.findByText(/status\.untrusted_proxy\.body/);
    expect(screen.queryByRole('button', { name: /check\.trust \{"peer"/ })).toBeNull();

    await rerender({ trustedProxies: ['172.18.0.1/99'] });
    expect(screen.getByRole('button', { name: /check\.trust \{"peer"/ })).toBeInTheDocument();
  });

  it('adds the peer and the preset with Trust and Cloudflare', async () => {
    getConnection.mockResolvedValue(
      result({ status: 'untrusted_proxy_unverifiable', cloudflarePresetWouldVerify: true })
    );
    const onUpdate = vi.fn();
    renderSection({ trustedProxies: ['10.0.0.1'], savedTrustedProxies: ['10.0.0.1'], onUpdate });

    await fireEvent.click(
      await screen.findByRole('button', { name: /check\.trustWithCloudflare/ })
    );

    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.1', '172.18.0.1', 'cloudflare']);
  });

  it('lists the peer once when Trust and Cloudflare is used with the peer already listed', async () => {
    getConnection.mockResolvedValue(
      result({ status: 'trusted_proxy_unverified', cloudflarePresetWouldVerify: true })
    );
    const onUpdate = vi.fn();
    renderSection({
      trustedProxies: ['172.18.0.1'],
      savedTrustedProxies: ['172.18.0.1'],
      onUpdate,
    });

    await fireEvent.click(
      await screen.findByRole('button', { name: /check\.trustWithCloudflare/ })
    );

    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(onUpdate).toHaveBeenCalledWith(['172.18.0.1', 'cloudflare']);
  });

  it('shows the stale note while the card has unsaved changes', async () => {
    const { rerender } = renderSection({
      trustedProxies: ['10.0.0.1'],
      savedTrustedProxies: ['10.0.0.1'],
    });
    await screen.findByText(/status\.untrusted_proxy\.body/);
    expect(screen.queryByText('settings.security.reverseProxy.check.stale')).toBeNull();

    await rerender({ trustedProxies: [], hasChanges: true });

    expect(screen.getByText('settings.security.reverseProxy.check.stale')).toBeInTheDocument();
  });

  it('shows the error and no status when the check fails', async () => {
    getConnection.mockRejectedValue(new Error('Network error'));
    renderSection();

    // The alert region repeats the title for screen readers, so it appears twice
    const titles = await screen.findAllByText('settings.security.reverseProxy.check.error');
    expect(titles.length).toBeGreaterThan(0);
    expect(screen.getByText('Network error')).toBeInTheDocument();
    expect(screen.queryByText(/reverseProxy\.status\./)).toBeNull();
  });

  it('shows the newer result when an older request resolves last', async () => {
    const first = deferred<ConnectionCheckResult>();
    const second = deferred<ConnectionCheckResult>();
    getConnection.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { rerender } = renderSection();
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(1));

    await rerender({ savedTrustedProxies: ['172.18.0.1'] });
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(2));

    second.resolve(result({ status: 'trusted_proxy', verifiedClientAddress: '192.168.1.20' }));
    await screen.findByText(/status\.trusted_proxy\.body/);
    first.resolve(result({ status: 'untrusted_proxy' }));
    await tick();
    await tick();

    expect(screen.getByText(/status\.trusted_proxy\.body/)).toBeInTheDocument();
    expect(screen.queryByText(/status\.untrusted_proxy\.body/)).toBeNull();
  });

  it('keeps the newer result when an older request fails last', async () => {
    const first = deferred<ConnectionCheckResult>();
    const second = deferred<ConnectionCheckResult>();
    getConnection.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { rerender } = renderSection();
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(1));
    await rerender({ savedTrustedProxies: ['172.18.0.1'] });
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(2));

    second.resolve(result({ status: 'trusted_proxy', verifiedClientAddress: '192.168.1.20' }));
    await screen.findByText(/status\.trusted_proxy\.body/);
    first.reject(new Error('Old failure'));
    await tick();
    await tick();

    expect(screen.getByText(/status\.trusted_proxy\.body/)).toBeInTheDocument();
    expect(screen.queryByText('Old failure')).toBeNull();
  });

  it('keeps loading while the newest request runs when an older one finishes', async () => {
    const first = deferred<ConnectionCheckResult>();
    const second = deferred<ConnectionCheckResult>();
    getConnection.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { rerender } = renderSection();
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(1));
    await rerender({ savedTrustedProxies: ['172.18.0.1'] });
    await waitFor(() => expect(getConnection).toHaveBeenCalledTimes(2));

    first.resolve(result({ status: 'untrusted_proxy' }));
    await tick();
    await tick();

    expect(screen.getByRole('button', { name: /check\.recheck/ })).toHaveAttribute(
      'aria-disabled',
      'true'
    );
    expect(screen.queryByText(/status\.untrusted_proxy\.body/)).toBeNull();
  });

  it('shows a draft passed in, so it survives the card being remounted', () => {
    renderSection({ draft: '10.0.0.9' });

    expect(
      screen.getByRole<HTMLInputElement>('textbox', { name: /reverseProxy\.listLabel/ }).value
    ).toBe('10.0.0.9');
  });

  it('hides Trust when a valid listed range already covers the peer', async () => {
    renderSection({ trustedProxies: ['172.18.0.0/16'] });

    expect(await screen.findByText(/check\.coveredBy/)).toHaveTextContent('172.18.0.0/16');
    expect(screen.queryByRole('button', { name: /check\.trust \{"peer"/ })).toBeNull();
  });

  it('focuses the card and drops the hash when opened through the cross-link', async () => {
    const redirect = vi.spyOn(navigation, 'redirect').mockImplementation(() => {});
    // jsdom has no scrollIntoView
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      value: scrollIntoView,
      configurable: true,
      writable: true,
    });
    window.location.hash = '#reverse-proxy';

    try {
      renderSection();

      await waitFor(() => expect(document.activeElement?.id).toBe('reverse-proxy'));
      expect(scrollIntoView).toHaveBeenCalled();
      expect(redirect).toHaveBeenCalledWith('/');
    } finally {
      Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
    }
  });

  it('leaves focus alone without the hash', async () => {
    const redirect = vi.spyOn(navigation, 'redirect').mockImplementation(() => {});

    renderSection();
    await tick();

    expect(document.activeElement?.id).not.toBe('reverse-proxy');
    expect(redirect).not.toHaveBeenCalled();
  });
});
