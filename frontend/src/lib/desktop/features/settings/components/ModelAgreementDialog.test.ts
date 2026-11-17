import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import ModelAgreementDialog from './ModelAgreementDialog.svelte';
import type { AgreementDocument } from '$lib/types/models';
import { ApiError } from '$lib/utils/api';

vi.mock('$lib/utils/modelsApi', () => ({
  fetchModelDocument: vi.fn(),
}));

import { fetchModelDocument } from '$lib/utils/modelsApi';

const policy: AgreementDocument = { repo: 'org/model', kind: 'acceptable_use', sha256: 'aaa' };
const terms: AgreementDocument = { repo: 'org/model', kind: 'future_kind', sha256: 'bbb' };

// jsdom does not implement the modal dialog methods.
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
});

function renderDialog(documents: AgreementDocument[]) {
  const onAgree = vi.fn();
  const onCancel = vi.fn();
  const props = { open: true, modelName: 'Test Model', documents, onAgree, onCancel };
  const { rerender } = render(ModelAgreementDialog, { props });
  const setOpen = (open: boolean) => rerender({ ...props, open });
  return { onAgree, onCancel, setOpen };
}

function loadedDocument(sha256: string, text: string) {
  return { repo: 'org/model', kind: 'acceptable_use', requiresAgreement: true, sha256, text };
}

function dialogElement(): HTMLDialogElement {
  const dialog = document.querySelector('dialog');
  if (!dialog) throw new Error('no dialog');
  return dialog;
}

const agreeButton = () =>
  screen.getByRole('button', { name: 'analysis.gallery.agreement.agreeAndContinue' });

describe('ModelAgreementDialog', () => {
  beforeEach(() => {
    cleanup();
    vi.mocked(fetchModelDocument).mockReset();
  });

  it('agrees only once every document is loaded and ticked, with every sha256', async () => {
    vi.mocked(fetchModelDocument).mockImplementation(async sha => ({
      repo: 'org/model',
      kind: 'x',
      requiresAgreement: true,
      sha256: sha,
      text: `text of ${sha}`,
    }));
    const { onAgree } = renderDialog([policy, terms]);

    const boxes = await screen.findAllByRole('checkbox');
    expect(boxes).toHaveLength(2);
    expect(screen.getByText('text of aaa')).toBeInTheDocument();

    await fireEvent.click(agreeButton());
    expect(agreeButton()).toHaveAttribute('aria-disabled', 'true');
    expect(onAgree).not.toHaveBeenCalled();

    await fireEvent.click(boxes[0]);
    await fireEvent.click(agreeButton());
    expect(onAgree).not.toHaveBeenCalled();

    await fireEvent.click(boxes[1]);
    expect(agreeButton()).not.toHaveAttribute('aria-disabled');
    await fireEvent.click(agreeButton());
    expect(onAgree).toHaveBeenCalledWith(['aaa', 'bbb']);
  });

  it('offers Retry and no way to agree when a text cannot be verified', async () => {
    const unavailable = new ApiError(
      'the text could not be verified',
      502,
      new Response(null, { status: 502 })
    );
    vi.mocked(fetchModelDocument).mockRejectedValueOnce(unavailable).mockResolvedValueOnce({
      repo: 'org/model',
      kind: 'acceptable_use',
      requiresAgreement: true,
      sha256: 'aaa',
      text: 'policy text',
    });
    const { onAgree } = renderDialog([policy]);

    expect(await screen.findByText('the text could not be verified')).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(agreeButton()).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByText('analysis.gallery.agreement.unavailableBlocked')).toBeInTheDocument();
    await fireEvent.click(agreeButton());
    expect(onAgree).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: /common\.retry/ }));
    await waitFor(() => expect(screen.getByText('policy text')).toBeInTheDocument());
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  // A close event from the dialog's previous closing is delivered as a task, so it
  // can arrive after the parent already showed the dialog again for a new request.
  it('ignores a close event that arrives after the dialog opened again', async () => {
    vi.mocked(fetchModelDocument).mockReturnValue(new Promise(() => {}));
    const { onCancel } = renderDialog([policy]);
    const dialog = document.querySelector('dialog');
    if (!dialog) throw new Error('no dialog');
    expect(dialog.open).toBe(true);

    dialog.dispatchEvent(new Event('close'));

    expect(onCancel).not.toHaveBeenCalled();
  });

  // The skeleton holds the text's place, so the loaded box must not change height
  // and move the checkbox and buttons under the pointer.
  it('keeps the text box at the skeleton height', async () => {
    let release: (value: Awaited<ReturnType<typeof fetchModelDocument>>) => void = () => {};
    vi.mocked(fetchModelDocument).mockReturnValue(
      new Promise(resolve => {
        release = resolve;
      })
    );
    renderDialog([policy]);
    const heightOf = (el: Element | null | undefined) =>
      el?.getAttribute('class')?.match(/(?:^|\s)(h-\d+)(?:\s|$)/)?.[1];
    const skeletonBox = screen.getByText('analysis.gallery.documents.loading').parentElement;
    const skeletonHeight = heightOf(skeletonBox);
    expect(skeletonHeight).toBeDefined();

    release({
      repo: 'org/model',
      kind: 'acceptable_use',
      requiresAgreement: true,
      sha256: 'aaa',
      text: 'short',
    });

    const region = await screen.findByRole('region', {
      name: 'analysis.gallery.documents.textRegion',
    });
    expect(heightOf(region)).toBe(skeletonHeight);
  });

  it('reserves the checkbox row while the text loads', async () => {
    vi.mocked(fetchModelDocument).mockReturnValue(new Promise(() => {}));
    renderDialog([policy]);

    const row = document.querySelector('[data-agree-row]');
    expect(row).not.toBeNull();
    expect(row?.getAttribute('class')).toMatch(/(?:^|\s)min-h-\d+(?:\s|$)/);
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('keeps focus in the dialog after Retry', async () => {
    vi.mocked(fetchModelDocument)
      .mockRejectedValueOnce(
        new ApiError('the text could not be verified', 502, new Response(null, { status: 502 }))
      )
      .mockReturnValue(new Promise(() => {}));
    renderDialog([policy]);
    const retry = await screen.findByRole('button', { name: /common\.retry/ });
    retry.focus();

    await fireEvent.click(retry);

    await screen.findByText('analysis.gallery.documents.loading');
    const dialog = document.querySelector('dialog');
    expect(document.activeElement).not.toBe(document.body);
    expect(dialog?.contains(document.activeElement)).toBe(true);
  });

  it('cancels when the user closes the dialog, and not when the parent closes it', async () => {
    vi.mocked(fetchModelDocument).mockReturnValue(new Promise(() => {}));
    const { onCancel, setOpen } = renderDialog([policy]);

    // Escape: the browser closes the element while the parent still has it open.
    dialogElement().close();
    dialogElement().dispatchEvent(new Event('close'));
    expect(onCancel).toHaveBeenCalledTimes(1);

    // Agree or Cancel: the parent closes it first, so the late event is not a cancel.
    await setOpen(true);
    dialogElement().showModal();
    await setOpen(false);
    dialogElement().dispatchEvent(new Event('close'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('starts unticked and reloads the text when shown again', async () => {
    vi.mocked(fetchModelDocument).mockResolvedValue(loadedDocument('aaa', 'policy text'));
    const { setOpen } = renderDialog([policy]);
    await fireEvent.click(await screen.findByRole('checkbox'));
    expect(screen.getByRole('checkbox')).toBeChecked();

    await setOpen(false);
    await setOpen(true);

    expect(await screen.findByRole('checkbox')).not.toBeChecked();
    expect(fetchModelDocument).toHaveBeenCalledTimes(2);
    expect(agreeButton()).toHaveAttribute('aria-disabled', 'true');
  });

  it('shows markup in a document as text', async () => {
    vi.mocked(fetchModelDocument).mockResolvedValue(
      loadedDocument('aaa', '<b>bold</b> <img src=x onerror="alert(1)">')
    );
    renderDialog([policy]);

    expect(
      await screen.findByText('<b>bold</b> <img src=x onerror="alert(1)">')
    ).toBeInTheDocument();
    expect(dialogElement().querySelector('b, img')).toBeNull();
  });

  it('names a document kind it does not know generically', async () => {
    vi.mocked(fetchModelDocument).mockResolvedValue({
      repo: 'org/model',
      kind: 'future_kind',
      requiresAgreement: true,
      sha256: 'bbb',
      text: 'terms',
    });
    renderDialog([terms]);

    expect(
      await screen.findByRole('heading', { name: 'analysis.gallery.documents.genericKind' })
    ).toBeInTheDocument();
    expect(screen.queryByText('future_kind')).not.toBeInTheDocument();
  });
});
