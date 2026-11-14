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

// The range list is loaded once per page (module cache), so every test serves the same one.
const rangeSpecies = [
  { scientificName: 'Turdus merula', commonName: 'Blackbird' },
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
          reviewCounts: true,
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

  it('says the legacy database cannot change a species when the save answers 409', async () => {
    renderDialog();
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => expect(puts).toHaveLength(1));
    answerPut?.(json({ error: 'conflict' }, 409));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'detections.detail.species.legacyDatabase'
    );
  });
});

describe('SpeciesChangeDialog choosing', () => {
  const puts: string[] = [];
  let summaryRequests = 0;

  beforeEach(() => {
    puts.length = 0;
    summaryRequests = 0;
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
        if (url.includes('/analytics/species/summary')) {
          summaryRequests += 1;
          return Promise.resolve(json([]));
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

  // Moving a detection to another species changes two species' all-time totals, so the rows
  // the page kept before the change are out of date.
  it('makes the page ask for the all-time species totals again after a change', async () => {
    const { loadSpeciesSummary, forgetSpeciesSummary } = await import('./speciesSummary');
    forgetSpeciesSummary();
    await loadSpeciesSummary('Turdus merula');
    await loadSpeciesSummary('Turdus merula');
    expect(summaryRequests).toBe(1);

    const { onChanged } = renderDialog();
    await fireEvent.click(radio('Song Thrush'));
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => expect(onChanged).toHaveBeenCalled());

    await loadSpeciesSummary('Turdus merula');
    expect(summaryRequests).toBe(2);
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
