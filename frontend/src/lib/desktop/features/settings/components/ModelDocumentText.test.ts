import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import ModelDocumentText from './ModelDocumentText.svelte';

vi.mock('$lib/utils/modelsApi', () => ({
  fetchModelDocument: vi.fn(),
}));

import { fetchModelDocument } from '$lib/utils/modelsApi';

function loadedDocument(sha256: string, text: string) {
  return { repo: 'org/model', kind: 'acceptable_use', requiresAgreement: true, sha256, text };
}

describe('ModelDocumentText', () => {
  beforeEach(() => {
    cleanup();
    vi.mocked(fetchModelDocument).mockReset();
  });

  it('shows the text of the current document when an earlier response arrives late', async () => {
    let releaseFirst: (value: ReturnType<typeof loadedDocument>) => void = () => {};
    vi.mocked(fetchModelDocument)
      .mockReturnValueOnce(
        new Promise(resolve => {
          releaseFirst = resolve;
        })
      )
      .mockResolvedValueOnce(loadedDocument('bbb', 'second text'));
    const { rerender } = render(ModelDocumentText, { props: { sha256: 'aaa', label: 'x' } });

    await rerender({ sha256: 'bbb', label: 'x' });
    expect(await screen.findByText('second text')).toBeInTheDocument();
    releaseFirst(loadedDocument('aaa', 'first text'));

    await new Promise(resolve => setTimeout(resolve, 0));
    expect(screen.queryByText('first text')).not.toBeInTheDocument();
    expect(screen.getByText('second text')).toBeInTheDocument();
  });

  it('keeps the current text when an earlier request fails late', async () => {
    let failFirst: (reason: unknown) => void = () => {};
    vi.mocked(fetchModelDocument)
      .mockReturnValueOnce(
        new Promise((_resolve, reject) => {
          failFirst = reject;
        })
      )
      .mockResolvedValueOnce(loadedDocument('bbb', 'second text'));
    const { rerender } = render(ModelDocumentText, { props: { sha256: 'aaa', label: 'x' } });

    await rerender({ sha256: 'bbb', label: 'x' });
    expect(await screen.findByText('second text')).toBeInTheDocument();
    failFirst(new Error('late failure'));

    await new Promise(resolve => setTimeout(resolve, 0));
    expect(screen.getByText('second text')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /common\.retry/ })).not.toBeInTheDocument();
  });

  it('explains an unavailable text when the request fails without a server reason', async () => {
    vi.mocked(fetchModelDocument).mockRejectedValue(new Error('Failed to fetch'));
    renderTyped(ModelDocumentText, { props: { sha256: 'aaa', label: 'x' } });

    expect(
      await screen.findByText('analysis.gallery.errors.documentUnavailable')
    ).toBeInTheDocument();
    expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
  });
});
