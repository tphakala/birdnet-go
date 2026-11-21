import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import SupportDumpCard from './SupportDumpCard.svelte';
import { api } from '$lib/utils/api';
import { unsavedChanges, type NavigationTarget } from '$lib/stores/unsavedChanges.svelte';

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  api: {
    get: vi.fn(() => Promise.resolve({ systemID: 'sys-1' })),
    post: vi.fn(),
  },
}));

const FROM: NavigationTarget = { path: ['', 'ui', 'settings', 'support'].join('/'), search: '' };
const TO: NavigationTarget = { path: ['', 'ui', 'dashboard'].join('/'), search: '' };
const MESSAGE_LABEL = 'settings.support.supportReport.userMessage.labelOptional';
const UPLOAD_LABEL = 'settings.support.supportReport.uploadOption.labelWithRequirement';
const DOWNLOAD_BUTTON = /settings\.support\.supportReport\.generateButton\.download/;
const DOWNLOAD_URL = '/api/v2/support/download/dump-1';
const DOWNLOAD_DELAY_MS = 500;

describe('SupportDumpCard unsaved-changes guard', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    vi.mocked(api.post).mockResolvedValue({ success: true, download_url: DOWNLOAD_URL });
  });

  afterEach(() => {
    cleanup();
    unsavedChanges.resetForTesting();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  async function typeMessage(text: string) {
    render(SupportDumpCard);
    await fireEvent.input(await screen.findByLabelText(MESSAGE_LABEL), { target: { value: text } });
  }

  async function generateLocalDownload() {
    // Without the upload the card builds a local download, and no issue number is needed.
    await fireEvent.click(screen.getByLabelText(UPLOAD_LABEL));
    await fireEvent.click(screen.getByRole('button', { name: DOWNLOAD_BUTTON }));
    await waitFor(() => expect(api.post).toHaveBeenCalled());
  }

  it('blocks a path change while a message is typed and none has been submitted', async () => {
    render(SupportDumpCard);
    await screen.findByLabelText(MESSAGE_LABEL);
    expect(unsavedChanges.blocks(TO, FROM)).toBe(false);

    await fireEvent.input(screen.getByLabelText(MESSAGE_LABEL), { target: { value: 'it broke' } });

    expect(unsavedChanges.blocks(TO, FROM)).toBe(true);
  });

  it('stops blocking once the message went out with a successful generate', async () => {
    await typeMessage('it broke');

    await generateLocalDownload();

    await waitFor(() => expect(unsavedChanges.blocks(TO, FROM)).toBe(false));
  });

  it('blocks again when the message is edited after a generate', async () => {
    await typeMessage('it broke');
    await generateLocalDownload();
    await waitFor(() => expect(unsavedChanges.blocks(TO, FROM)).toBe(false));

    await fireEvent.input(screen.getByLabelText(MESSAGE_LABEL), {
      target: { value: 'it broke again' },
    });

    expect(unsavedChanges.blocks(TO, FROM)).toBe(true);
  });

  it('downloads a local dump through an anchor and does not navigate the page', async () => {
    const hrefBefore = window.location.href;
    const clicked: Array<{ href: string | null; download: boolean }> = [];
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      clicked.push({ href: this.getAttribute('href'), download: this.hasAttribute('download') });
    });
    await typeMessage('');

    await generateLocalDownload();
    await vi.advanceTimersByTimeAsync(DOWNLOAD_DELAY_MS);

    expect(clicked).toEqual([{ href: DOWNLOAD_URL, download: true }]);
    expect(window.location.href).toBe(hrefBefore);
  });
});
