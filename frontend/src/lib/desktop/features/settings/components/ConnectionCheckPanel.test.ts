import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import type { ConnectionCheckResult, ConnectionStatus } from '$lib/utils/settingsApi';
import ConnectionCheckPanel from './ConnectionCheckPanel.svelte';

// Show t() parameters so the tests can see which address a message names.
const originalT = vi.mocked(t).getMockImplementation();

const STATUSES: ConnectionStatus[] = [
  'direct',
  'trusted_proxy',
  'trusted_proxy_unverified',
  'untrusted_public_proxy',
  'untrusted_proxy',
  'untrusted_proxy_unverifiable',
];

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

interface PanelProps {
  result: ConnectionCheckResult | null;
  loading: boolean;
  error: string | null;
  stale: boolean;
  peerCoveredBy: string | null;
  cloudflareEnabled: boolean;
  disabled: boolean;
  onRecheck: () => void;
  onTrust: () => void;
  onTrustWithCloudflare: () => void;
}

function renderPanel(overrides: Partial<PanelProps> = {}) {
  const props: PanelProps = {
    result: result(),
    loading: false,
    error: null,
    stale: false,
    peerCoveredBy: null,
    cloudflareEnabled: false,
    disabled: false,
    onRecheck: vi.fn(),
    onTrust: vi.fn(),
    onTrustWithCloudflare: vi.fn(),
    ...overrides,
  };
  return { props, ...render(ConnectionCheckPanel, { props }) };
}

const trustButton = () => screen.queryByRole('button', { name: /check\.trust "?\{"peer"/ });
const trustWithCloudflareButton = () =>
  screen.queryByRole('button', { name: /check\.trustWithCloudflare/ });

describe('ConnectionCheckPanel', () => {
  beforeEach(() => {
    vi.mocked(t).mockImplementation((key: string, params?: Record<string, unknown>) =>
      params ? `${key} ${JSON.stringify(params)}` : key
    );
  });

  afterEach(() => {
    vi.mocked(t).mockReset();
    if (originalT) vi.mocked(t).mockImplementation(originalT);
  });

  it.each(STATUSES)('renders the title and body for %s', status => {
    renderPanel({ result: result({ status, verifiedClientAddress: '192.168.1.20' }) });

    // The status region repeats the title for screen readers; the visible box needs its own
    const visibleTitles = screen
      .getAllByText(new RegExp(`reverseProxy\\.status\\.${status}\\.title`))
      .filter(el => !el.closest('[role="status"]'));
    expect(visibleTitles).toHaveLength(1);
    expect(
      screen.getByText(new RegExp(`reverseProxy\\.status\\.${status}\\.body`))
    ).toBeInTheDocument();
  });

  it('shows the Trust button with the address and the self-trust warning only for untrusted_proxy', () => {
    renderPanel();

    const button = trustButton();
    expect(button).not.toBeNull();
    expect(button?.textContent).toContain('172.18.0.1');
    expect(screen.getByText(/check\.trustWarning/)).toBeInTheDocument();
  });

  it.each(STATUSES.filter(status => status !== 'untrusted_proxy'))(
    'offers no Trust button for %s',
    status => {
      renderPanel({ result: result({ status }) });
      expect(trustButton()).toBeNull();
    }
  );

  it('calls onTrust when Trust is clicked', async () => {
    const { props } = renderPanel();
    const button = trustButton();
    if (!button) throw new Error('Trust button missing');

    await fireEvent.click(button);

    expect(props.onTrust).toHaveBeenCalledTimes(1);
  });

  it('moves focus to the added note after Trust', async () => {
    // The page updates the form list synchronously inside onTrust, as the store does
    let rerenderPanel: (props: { peerCoveredBy: string | null }) => Promise<void> = async () => {};
    const { rerender } = renderPanel({
      onTrust: () => void rerenderPanel({ peerCoveredBy: '172.18.0.1' }),
    });
    rerenderPanel = rerender;
    const button = trustButton();
    if (!button) throw new Error('Trust button missing');
    button.focus();

    await fireEvent.click(button);

    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByText('settings.security.reverseProxy.check.added')
      )
    );
  });

  it('replaces the Trust button with the added note once the peer is in the form list', () => {
    renderPanel({ peerCoveredBy: '172.18.0.1' });

    expect(trustButton()).toBeNull();
    expect(screen.getByText('settings.security.reverseProxy.check.added')).toBeInTheDocument();
  });

  it.each(['untrusted_proxy_unverifiable', 'trusted_proxy_unverified'] as const)(
    'offers Trust with Cloudflare for %s when the preset would verify',
    async status => {
      const { props } = renderPanel({
        result: result({ status, cloudflarePresetWouldVerify: true }),
      });

      expect(screen.getByText(/check\.cloudflareInFront/)).toBeInTheDocument();
      const button = trustWithCloudflareButton();
      if (!button) throw new Error('Trust with Cloudflare button missing');
      await fireEvent.click(button);

      expect(props.onTrustWithCloudflare).toHaveBeenCalledTimes(1);
    }
  );

  it('shows only the Cloudflare fix when the preset would verify', () => {
    for (const status of ['untrusted_proxy_unverifiable', 'trusted_proxy_unverified'] as const) {
      const { unmount } = renderPanel({
        result: result({ status, cloudflarePresetWouldVerify: true }),
      });

      expect(screen.getByText(/check\.cloudflareInFront/)).toBeInTheDocument();
      expect(trustWithCloudflareButton()).not.toBeNull();
      expect(screen.queryByText(new RegExp(`status\\.${status}\\.body`))).toBeNull();
      expect(screen.queryByText(/status\.untrusted_proxy_unverifiable\.fix/)).toBeNull();
      expect(screen.queryByText(/check\.selfClientCause/)).toBeNull();
      expect(screen.queryByText(/status\.trusted_proxy_unverified\.cause/)).toBeNull();
      expect(screen.queryByText(/check\.chainHint/)).toBeNull();
      unmount();

      // Without the preset fix the generic chain hint is shown
      const plain = renderPanel({ result: result({ status, cloudflarePresetWouldVerify: false }) });
      expect(screen.getByText(/check\.chainHint/)).toBeInTheDocument();
      plain.unmount();
    }
  });

  it('offers Trust with Cloudflare for a listed peer until the preset is on, then shows the note', async () => {
    const unverified = result({
      status: 'trusted_proxy_unverified',
      cloudflarePresetWouldVerify: true,
    });
    const { props, rerender } = renderPanel({
      result: unverified,
      peerCoveredBy: '172.18.0.1',
      cloudflareEnabled: false,
    });

    // The peer is already listed, so only the preset is missing
    expect(screen.queryByText('settings.security.reverseProxy.check.added')).toBeNull();
    const button = trustWithCloudflareButton();
    if (!button) throw new Error('Trust with Cloudflare button missing');
    await fireEvent.click(button);
    expect(props.onTrustWithCloudflare).toHaveBeenCalledTimes(1);

    await rerender({ cloudflareEnabled: true });
    expect(trustWithCloudflareButton()).toBeNull();
    expect(screen.getByText('settings.security.reverseProxy.check.added')).toBeInTheDocument();
  });

  it('keeps the Trust with Cloudflare button when only the preset is on and the peer is not listed', () => {
    renderPanel({
      result: result({ status: 'trusted_proxy_unverified', cloudflarePresetWouldVerify: true }),
      peerCoveredBy: null,
      cloudflareEnabled: true,
    });

    expect(trustWithCloudflareButton()).not.toBeNull();
    expect(screen.queryByText('settings.security.reverseProxy.check.added')).toBeNull();
  });

  it('shows the added note instead of Trust with Cloudflare when both are in the form', () => {
    renderPanel({
      result: result({ status: 'untrusted_proxy_unverifiable', cloudflarePresetWouldVerify: true }),
      peerCoveredBy: '172.18.0.1',
      cloudflareEnabled: true,
    });

    expect(trustWithCloudflareButton()).toBeNull();
    expect(screen.getByText('settings.security.reverseProxy.check.added')).toBeInTheDocument();
  });

  it('names the covering range instead of offering Trust when a listed range holds the peer', () => {
    renderPanel({ peerCoveredBy: '172.18.0.0/16' });

    expect(trustButton()).toBeNull();
    expect(screen.getByText(/check\.coveredBy/)).toHaveTextContent('172.18.0.0/16');
    expect(screen.queryByText('settings.security.reverseProxy.check.added')).toBeNull();
  });

  it.each(['untrusted_proxy_unverifiable', 'trusted_proxy_unverified'] as const)(
    'lists a proxy forwarding its own address as a cause for %s',
    status => {
      renderPanel({ result: result({ status }) });
      expect(
        screen.getByText('settings.security.reverseProxy.check.selfClientCause')
      ).toBeInTheDocument();
    }
  );

  it('mentions proxy chains for the unverified and unverifiable statuses', () => {
    renderPanel({ result: result({ status: 'untrusted_proxy_unverifiable' }) });
    expect(screen.getByText(/check\.chainHint/)).toBeInTheDocument();
  });

  it('points to the Cloudflare checkbox for a Cloudflare edge while the preset is off', () => {
    renderPanel({
      result: result({ status: 'untrusted_public_proxy', peerIsCloudflareEdge: true }),
    });
    expect(screen.getByText(/check\.cloudflareEdge/)).toBeInTheDocument();
  });

  it('does not point to the Cloudflare checkbox when the preset is already on', () => {
    renderPanel({
      result: result({ status: 'untrusted_public_proxy', peerIsCloudflareEdge: true }),
      cloudflareEnabled: true,
    });
    expect(screen.queryByText(/check\.cloudflareEdge/)).toBeNull();
  });

  it('does not point to the Cloudflare checkbox for a public proxy outside Cloudflare', () => {
    renderPanel({ result: result({ status: 'untrusted_public_proxy' }) });
    expect(screen.queryByText(/check\.cloudflareEdge/)).toBeNull();
  });

  it('describes a direct connection without an address when the peer is unknown', () => {
    renderPanel({
      result: result({ status: 'direct', peerAddress: '', hasForwardedHeaders: false }),
    });

    expect(
      screen.getByText('settings.security.reverseProxy.status.direct.bodyNoPeer')
    ).toBeInTheDocument();
    expect(screen.queryByText(/status\.direct\.hint/)).toBeNull();
  });

  it('shows the error with a way to check again and no status', async () => {
    const { props } = renderPanel({ result: null, error: 'Network error' });

    expect(screen.getByText('Network error')).toBeInTheDocument();
    expect(
      screen.getAllByText('settings.security.reverseProxy.check.error').length
    ).toBeGreaterThan(0);
    expect(screen.queryByText(/reverseProxy\.status\./)).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /check\.recheck/ }));
    expect(props.onRecheck).toHaveBeenCalledTimes(1);
  });

  it('shows the fallback error sentence once', () => {
    const fallback = 'settings.security.reverseProxy.check.error';
    renderPanel({ result: null, error: fallback });

    // The alert region repeats the title for screen readers; the visible box shows it once
    const visible = screen.getAllByText(fallback).filter(el => !el.closest('[role="alert"]'));
    expect(visible).toHaveLength(1);
  });

  it('announces a failed check in the alert region and not in the status region', () => {
    renderPanel({ result: null, error: 'Network error' });

    // Exactly the title: the visible box, which also shows the detail, is not the alert
    expect(screen.getByRole('alert').textContent).toBe(
      'settings.security.reverseProxy.check.error'
    );
    expect(screen.getByRole('status')).not.toHaveTextContent(
      'settings.security.reverseProxy.check.error'
    );
    // The visible box is not a live region, so the failure is announced once
    expect(screen.getAllByRole('alert')).toHaveLength(1);
  });

  it('shows the loading state instead of an earlier result', () => {
    renderPanel({ loading: true });

    // The status region repeats the text for screen readers; the visible loading line needs its own
    const visible = screen
      .getAllByText('settings.security.reverseProxy.check.checking')
      .filter(el => !el.closest('[role="status"]'));
    expect(visible).toHaveLength(1);
    expect(screen.queryByText(/reverseProxy\.status\./)).toBeNull();
    expect(screen.getByRole('button', { name: /check\.recheck/ })).toHaveAttribute(
      'aria-disabled',
      'true'
    );
  });

  it('shows the stale note only while the form list differs from the saved list', async () => {
    const { rerender } = renderPanel({ stale: false });
    expect(screen.queryByText('settings.security.reverseProxy.check.stale')).toBeNull();

    await rerender({ stale: true });

    expect(screen.getByText('settings.security.reverseProxy.check.stale')).toBeInTheDocument();
  });

  it('disables the Trust button while settings are saving', () => {
    renderPanel({ disabled: true });
    expect(trustButton()).toBeDisabled();
  });
});
