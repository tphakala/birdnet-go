import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import SpeciesChangeDialog from './SpeciesChangeDialog.svelte';
import { t } from '$lib/i18n';
import { toastActions } from '$lib/stores/toast';
import type { AlternatePrediction, DetectionDetailData } from './detailTypes';

const detection: DetectionDetailData = {
  id: 7,
  date: '2026-11-14',
  time: '02:57:03',
  beginTime: '2026-11-14T02:57:00+02:00',
  endTime: '2026-11-14T02:57:03+02:00',
  speciesCode: 'eurbla',
  scientificName: 'Turdus merula',
  commonName: 'Blackbird',
  confidence: 0.87,
  verified: 'unverified',
  locked: false,
};

const alternates: AlternatePrediction[] = [
  { scientificName: 'Turdus philomelos', commonName: 'Song Thrush', confidence: 0.4 },
  { scientificName: 'Turdus iliacus', commonName: 'Redwing', confidence: 0.2 },
];

// The range filter's species, served to every test.
const rangeSpecies = [
  { scientificName: 'Turdus merula', commonName: 'Blackbird' },
  { scientificName: 'Turdus philomelos', commonName: 'Song Thrush' },
  { scientificName: 'Turdus pilaris', commonName: 'Fieldfare' },
  { scientificName: 'Turdus viscivorus', commonName: 'Mistle Thrush' },
];

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function renderDialog(blockedReason = '') {
  const onChanged = vi.fn();
  render(SpeciesChangeDialog, {
    props: {
      isOpen: true,
      detection,
      speciesName: 'Blackbird',
      alternates,
      predictionModel: 'BirdNET 2.4',
      blockedReason,
      onClose: vi.fn(),
      onChanged,
    },
  });
  return { onChanged };
}

const radio = (name: string) => screen.getByRole('radio', { name: new RegExp(name) });

describe('SpeciesChangeDialog saving', () => {
  let answerPut: ((_response: Response) => void) | undefined;
  const puts: string[] = [];

  beforeEach(() => {
    puts.length = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (init?.method === 'PUT') {
          puts.push(String(init.body));
          return new Promise<Response>(resolve => {
            answerPut = resolve;
          });
        }
        if (url.includes('/range/species/list')) {
          return Promise.resolve(json({ species: rangeSpecies }));
        }
        return Promise.resolve(json({}));
      })
    );
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(toastActions.success).mockClear();
    vi.mocked(t).mockClear();
  });

  // Regression: the toast named whatever species was picked when the save answered, not
  // the one that was sent.
  it('names the species it saved even when another is picked during the save', async () => {
    const { onChanged } = renderDialog();
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await fireEvent.click(radio('Redwing'));

    answerPut?.(json({}));
    await waitFor(() => {
      expect(onChanged).toHaveBeenCalled();
    });

    expect(puts).toEqual([JSON.stringify({ scientificName: 'Turdus philomelos' })]);
    expect(vi.mocked(toastActions.success)).toHaveBeenCalledOnce();
    expect(vi.mocked(t)).toHaveBeenCalledWith('detections.detail.species.changed', {
      species: 'Song Thrush',
    });
    expect(vi.mocked(t)).not.toHaveBeenCalledWith('detections.detail.species.changed', {
      species: 'Redwing',
    });
  });

  // The page keys the dialog by detection, so stepping away destroys the instance while a
  // save may still be out. Its onClose would close the next detection's dialog.
  describe('after the dialog was destroyed by a step', () => {
    async function destroyedDuringSave() {
      const onClose = vi.fn();
      const onChanged = vi.fn();
      const { unmount } = render(SpeciesChangeDialog, {
        props: {
          isOpen: true,
          detection,
          speciesName: 'Blackbird',
          alternates,
          predictionModel: 'BirdNET 2.4',
          blockedReason: '',
          onClose,
          onChanged,
        },
      });
      await fireEvent.click(radio('Song Thrush'));
      await fireEvent.click(
        screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
      );
      await waitFor(() => expect(puts).toHaveLength(1));
      unmount();
      return { onClose, onChanged };
    }

    it('reports the save to its own detection and does not close the next dialog', async () => {
      const { onClose, onChanged } = await destroyedDuringSave();
      answerPut?.(json({}));

      await waitFor(() => {
        expect(onChanged).toHaveBeenCalledWith({
          detectionId: 7,
          scientificName: 'Turdus merula',
          changedTo: 'Turdus philomelos',
          speciesStats: true,
        });
      });
      expect(onClose).not.toHaveBeenCalled();
    });

    it('reports a failed save as a toast, since the dialog that would show it is gone', async () => {
      vi.mocked(toastActions.error).mockClear();
      const { onChanged } = await destroyedDuringSave();
      answerPut?.(json({}, 500));

      await waitFor(() => {
        expect(vi.mocked(toastActions.error)).toHaveBeenCalledWith(
          'detections.detail.species.changeFailed'
        );
      });
      expect(onChanged).not.toHaveBeenCalled();
    });
  });

  // A change moves the detection between two species, so both species' statistics change,
  // whichever detection the page shows by the time the save answers.
  it('reports both species of a saved change while the dialog is still open', async () => {
    const { onChanged } = renderDialog();
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    answerPut?.(json({}));

    await waitFor(() => {
      expect(onChanged).toHaveBeenCalledWith({
        detectionId: 7,
        scientificName: 'Turdus merula',
        changedTo: 'Turdus philomelos',
        speciesStats: true,
      });
    });
  });

  // The dialog closes after a change, so a reload of the detection that then fails is
  // reported where the user can see it.
  it('reports a failed reload after a saved change as a toast, once', async () => {
    vi.mocked(toastActions.error).mockClear();
    const onChanged = vi.fn(() => Promise.resolve(false));
    render(SpeciesChangeDialog, {
      props: {
        isOpen: true,
        detection,
        speciesName: 'Blackbird',
        alternates,
        predictionModel: 'BirdNET 2.4',
        blockedReason: '',
        onClose: vi.fn(),
        onChanged,
      },
    });
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    answerPut?.(json({}));

    await waitFor(() => {
      expect(vi.mocked(toastActions.error)).toHaveBeenCalledWith(
        'detections.detail.review.reloadFailed'
      );
    });
    expect(vi.mocked(toastActions.error)).toHaveBeenCalledOnce();
  });

  it('reports a failed save as a toast when the dialog was closed while it saved', async () => {
    vi.mocked(toastActions.error).mockClear();
    const props = {
      isOpen: true,
      detection,
      speciesName: 'Blackbird',
      alternates,
      predictionModel: 'BirdNET 2.4',
      blockedReason: '',
      onClose: vi.fn(),
      onChanged: vi.fn(),
    };
    const { rerender } = render(SpeciesChangeDialog, { props });
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    await rerender({ ...props, isOpen: false });
    answerPut?.(json({}, 500));

    await waitFor(() => {
      expect(vi.mocked(toastActions.error)).toHaveBeenCalledWith(
        'detections.detail.species.changeFailed'
      );
    });
  });

  it('says why the species cannot be changed when the save answers 409 (locked or legacy database)', async () => {
    renderDialog();
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    answerPut?.(json({ error: 'conflict' }, 409));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'detections.detail.species.conflict'
    );
  });
});

describe('SpeciesChangeDialog choosing', () => {
  const puts: string[] = [];

  beforeEach(() => {
    puts.length = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (init?.method === 'PUT') {
          puts.push(String(init.body));
          return Promise.resolve(json({}));
        }
        if (url.includes('/range/species/list')) {
          return Promise.resolve(json({ species: rangeSpecies }));
        }
        return Promise.resolve(json({}));
      })
    );
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  async function search(query: string) {
    await fireEvent.input(screen.getByRole('searchbox'), { target: { value: query } });
  }

  it('keeps one species picked across the predictions and the search results', async () => {
    renderDialog();
    await fireEvent.click(radio('Song Thrush'));
    await search('Fieldfare');
    await fireEvent.click(await screen.findByRole('radio', { name: /Fieldfare/ }));

    expect(screen.getAllByRole('radio', { checked: true })).toHaveLength(1);
    expect(radio('Fieldfare')).toBeChecked();
    // What the dialog itself marks as picked: one row across both lists, not only the
    // browser's radio group (which unchecks the others whatever the component holds).
    const marked = document.querySelectorAll('.dd-species-option-active');
    expect(marked).toHaveLength(1);
    expect(marked[0].textContent).toContain('Fieldfare');
    // What is saved is the last pick, from whichever list it came.
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => {
      expect(puts).toEqual([JSON.stringify({ scientificName: 'Turdus pilaris' })]);
    });
  });

  // The radio is visually hidden, so the browser scrolls its 1 px box and not its row into
  // view: arrowing through a long list left the checked row out of sight.
  it('scrolls the row of a focused radio into view', async () => {
    const scrolled: Element[] = [];
    const scrollIntoView = vi.fn(function (this: Element) {
      scrolled.push(this);
    });
    const prototype = window.HTMLElement.prototype;
    const original = Object.getOwnPropertyDescriptor(prototype, 'scrollIntoView');
    Object.defineProperty(prototype, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoView,
    });
    try {
      renderDialog();
      await fireEvent.focus(radio('Redwing'));

      expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
      expect(scrolled.at(-1)?.textContent).toContain('Redwing');
      expect(scrolled.at(-1)?.tagName).toBe('LABEL');
    } finally {
      if (original) Object.defineProperty(prototype, 'scrollIntoView', original);
      else Reflect.deleteProperty(prototype, 'scrollIntoView');
    }
  });

  // The range filter's list changes with the settings, which apply without a page reload.
  it('reads the range filter list again at each opening', async () => {
    const props = {
      isOpen: true,
      detection,
      speciesName: 'Blackbird',
      alternates,
      predictionModel: 'BirdNET 2.4',
      blockedReason: '',
      onClose: vi.fn(),
      onChanged: vi.fn(),
    };
    const rangeRequests = () =>
      vi.mocked(fetch).mock.calls.filter(([input]) => String(input).includes('/range/species/list'))
        .length;
    const { rerender } = render(SpeciesChangeDialog, { props });
    await waitFor(() => expect(rangeRequests()).toBe(1));

    await rerender({ ...props, isOpen: false });
    await rerender({ ...props, isOpen: true });

    await waitFor(() => expect(rangeRequests()).toBe(2));
  });

  it('does not list a predicted species again among the search results', async () => {
    renderDialog();
    await search('Turdus');

    await screen.findByRole('radio', { name: /Fieldfare/ });
    // Song Thrush is a prediction and also in the catalog: one row, in the predictions list.
    expect(screen.getAllByRole('radio', { name: /Song Thrush/ })).toHaveLength(1);
  });

  it('never offers the detection\u2019s own species in the results', async () => {
    renderDialog();
    await search('Turdus');

    await screen.findByRole('radio', { name: /Fieldfare/ });
    expect(screen.queryByRole('radio', { name: /Blackbird/ })).toBeNull();
  });

  it('searches only from two characters on', async () => {
    renderDialog();
    await screen.findByText('detections.detail.species.searchHintRange');
    await search('F');

    expect(screen.getByText('detections.detail.species.searchHintRange')).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: /Fieldfare/ })).toBeNull();

    await search('Fi');
    expect(await screen.findByRole('radio', { name: /Fieldfare/ })).toBeInTheDocument();
  });

  it('saves nothing until a species is picked', async () => {
    renderDialog();
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );

    expect(puts).toEqual([]);
    // The click is ignored, not turned into a failed save.
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows a viewer who cannot change the species plain rows and the reason', () => {
    renderDialog('detections.detail.review.loginRequired');

    expect(screen.queryAllByRole('radio')).toHaveLength(0);
    expect(screen.queryByRole('searchbox')).toBeNull();
    expect(screen.getByText(/Song Thrush/)).toBeInTheDocument();
    expect(screen.getByText('detections.detail.review.loginRequired')).toBeInTheDocument();
  });
});
