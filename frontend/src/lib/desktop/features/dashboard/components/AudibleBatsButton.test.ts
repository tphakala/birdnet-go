import { describe, it, expect, afterEach, vi, beforeEach } from 'vitest';
import { cleanup, fireEvent, screen } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import AudibleBatsButton, { type AudibleBatsSettings } from './AudibleBatsButton.svelte';

const factory = createComponentTestFactory(AudibleBatsButton);

function renderButton(overrides: Record<string, unknown> = {}) {
  const onEnable = vi.fn();
  const onDisable = vi.fn();
  const result = factory.render({
    props: {
      active: false,
      generating: false,
      onEnable,
      onDisable,
      ...overrides,
    },
  });
  return { ...result, onEnable, onDisable };
}

// Open the popup by clicking the trigger button (the first button rendered).
async function openPopup(container: HTMLElement) {
  const trigger = container.querySelector('button');
  if (!trigger) throw new Error('expected a trigger button');
  await fireEvent.click(trigger);
}

describe('AudibleBatsButton', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it('opens the popup and shows the expansion options', async () => {
    const { container } = renderButton();
    await openPopup(container);

    // 5x is the default; all four factors are offered.
    for (const label of ['5×', '10×', '16×', '20×']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });

  it('calls onEnable with the default settings', async () => {
    const { container, onEnable } = renderButton();
    await openPopup(container);

    await fireEvent.click(screen.getByText('media.audio.audibleBats.enable'));

    expect(onEnable).toHaveBeenCalledTimes(1);
    const settings = onEnable.mock.calls[0][0] as AudibleBatsSettings;
    expect(settings.expansion).toBe(5);
  });

  it('calls onDisable when active and the primary action is clicked', async () => {
    const { container, onDisable } = renderButton({ active: true });
    await openPopup(container);

    await fireEvent.click(screen.getByText('media.audio.audibleBats.disable'));
    expect(onDisable).toHaveBeenCalledTimes(1);
  });

  it('disables the active derived copy when a setting changes', async () => {
    const { container, onDisable } = renderButton({ active: true });
    await openPopup(container);

    // Switching the expansion factor while active invalidates the derived copy.
    await fireEvent.click(screen.getByText('20×'));
    expect(onDisable).toHaveBeenCalledTimes(1);
  });

  it('persists the chosen time-expansion factor to localStorage', async () => {
    const { container, onEnable } = renderButton();
    await openPopup(container);
    await fireEvent.click(screen.getByText('20×'));
    await fireEvent.click(screen.getByText('media.audio.audibleBats.enable'));

    expect(onEnable).toHaveBeenCalled();
    const raw = localStorage.getItem('birdnet:audibleBats');
    expect(raw).not.toBeNull();
    const stored = JSON.parse(raw as string) as AudibleBatsSettings;
    expect(stored.expansion).toBe(20);
  });

  it('shows the generating label while generating', async () => {
    const { container } = renderButton({ generating: true });
    await openPopup(container);
    expect(screen.getByText('media.audio.audibleBats.generating')).toBeInTheDocument();
  });

  it('does not open the popup when disabled', async () => {
    const { container } = renderButton({ disabled: true });
    await openPopup(container);
    expect(screen.queryByText('media.audio.audibleBats.timeExpansion')).not.toBeInTheDocument();
  });

  it('shows the disabled reason as a tooltip and accessible description', () => {
    const { container } = renderButton({ disabled: true, disabledReason: 'Not available' });
    const trigger = container.querySelector('button');
    expect(trigger).toHaveAttribute('title', 'Not available');
    const describedBy = trigger?.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    expect(document.getElementById(describedBy as string)?.textContent).toBe('Not available');
  });
});
