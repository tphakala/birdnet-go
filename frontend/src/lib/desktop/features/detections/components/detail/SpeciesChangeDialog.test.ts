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

  it('reports a save to its own detection and leaves the next detection\u2019s dialog open', async () => {
    const onClose = vi.fn();
    const onChanged = vi.fn();
    const { rerender } = render(SpeciesChangeDialog, {
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

    await rerender({ detection: { ...detection, id: 8 } });
    answerPut?.(json({}));
    await waitFor(() => {
      expect(onChanged).toHaveBeenCalledWith(7);
    });
    expect(onClose).not.toHaveBeenCalled();
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
    // What is saved is the last pick, from whichever list it came.
    await fireEvent.click(
      screen.getByRole('button', { name: 'detections.detail.species.confirmEmpty' })
    );
    await waitFor(() => {
      expect(puts).toEqual([JSON.stringify({ scientificName: 'Turdus pilaris' })]);
    });
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
  });

  it('shows a viewer who cannot change the species plain rows and the reason', () => {
    renderDialog('detections.detail.review.loginRequired');

    expect(screen.queryAllByRole('radio')).toHaveLength(0);
    expect(screen.queryByRole('searchbox')).toBeNull();
    expect(screen.getByText(/Song Thrush/)).toBeInTheDocument();
    expect(screen.getByText('detections.detail.review.loginRequired')).toBeInTheDocument();
  });
});
