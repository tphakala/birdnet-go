import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import LiveStreamPage from './LiveStreamPage.svelte';

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class {
    onmessage: unknown = null;
    onerror: unknown = null;
    close() {}
  },
}));

vi.mock('$lib/stores/appState.svelte', () => ({
  hasLiveAudioAccess: () => true,
  appState: { csrfToken: 'token', audioExportEnabled: false },
  getCsrfToken: () => 'token',
}));

describe('LiveStreamPage accessibility', () => {
  it('names the audio source dropdown independently of its placeholder or selection', () => {
    render(LiveStreamPage);

    const trigger = screen.getByRole('combobox', { name: 'spectrogram.page.sourceLabel' });
    expect(trigger).toHaveAttribute('aria-haspopup', 'listbox');
  });
});
