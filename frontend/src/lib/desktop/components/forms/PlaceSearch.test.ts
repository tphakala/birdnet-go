import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import PlaceSearch from './PlaceSearch.svelte';
import {
  PHOTON_SITE_URL,
  PLACE_SEARCH_DEBOUNCE_MS,
  type PlaceResult,
} from '$lib/utils/placeSearch';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import { OPTION_HIGHLIGHT_BG_CLASS, OPTION_HIGHLIGHT_OUTLINE_CLASS } from './SelectDropdown.styles';
import { PLACE_ERROR_CLASS, PLACE_OPTION_DETAIL_CLASS } from './PlaceSearch.styles';

const LABEL = 'components.locationMap.search.label';
const SUBMIT = 'components.locationMap.search.submit';
const DISCLOSURE = 'components.locationMap.search.disclosure';
const OPEN_PHOTON = 'components.locationMap.search.openPhoton';
const NO_RESULTS = 'components.locationMap.search.noResults';
const ERROR_NETWORK = 'components.locationMap.search.errorNetwork';
const ERROR_RATE_LIMITED = 'components.locationMap.search.errorRateLimited';
const ERROR_UNAVAILABLE = 'components.locationMap.search.errorUnavailable';
const SEARCHING = 'components.locationMap.search.searching';
const RESULTS = 'components.locationMap.search.results';

const originalFetch = globalThis.fetch;

function feature(osmId: number, name: string, longitude: number, latitude: number) {
  return {
    type: 'Feature',
    properties: { osm_type: 'R', osm_id: osmId, name, country: 'Finland' },
    geometry: { type: 'Point', coordinates: [longitude, latitude] },
  };
}

const HELSINKI = feature(34914, 'Helsinki', 24.9435408, 60.1666204);
const HELSINGBORG = feature(1, 'Helsingborg', 12.6945, 56.0465);
const HELSINGFORS = feature(2, 'Helsingfors', 24.9384, 60.1699);
const HELSINKI_PLACE: PlaceResult = {
  id: 'R:34914',
  name: 'Helsinki',
  detail: 'Finland',
  latitude: 60.1666204,
  longitude: 24.9435408,
};

function jsonResponse(...features: unknown[]): Response {
  return new Response(JSON.stringify({ type: 'FeatureCollection', features }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

function fetchedQueries(): (string | null)[] {
  return fetchMock.mock.calls.map(([input]) => new URL(String(input)).searchParams.get('q'));
}

/** A fetch call whose response the test resolves or rejects itself. */
function deferNextFetch() {
  let resolve!: (response: Response) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<Response>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  fetchMock.mockImplementationOnce(() => promise);
  return { resolve, reject };
}

function input(): HTMLInputElement {
  return screen.getByRole<HTMLInputElement>('combobox', { name: LABEL });
}

function type(value: string) {
  // Typing happens in the focused input, as in a browser.
  input().focus();
  return fireEvent.input(input(), { target: { value } });
}

function press(key: string) {
  return fireEvent.keyDown(input(), { key });
}

/** Let the debounce elapse and the pending search settle. */
async function settle() {
  await vi.advanceTimersByTimeAsync(PLACE_SEARCH_DEBOUNCE_MS);
}

async function searchFor(value: string) {
  await type(value);
  await settle();
}

let renderedContainer: HTMLElement;

function renderSearch(onSelect = vi.fn<(place: PlaceResult) => void>()) {
  renderedContainer = render(PlaceSearch, { props: { onSelect } }).container;
  return onSelect;
}

describe('PlaceSearch', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    fetchMock = vi.fn<typeof fetch>();
    globalThis.fetch = fetchMock;
  });

  afterEach(() => {
    cleanup();
    globalThis.fetch = originalFetch;
    vi.useRealTimers();
  });

  describe('searching', () => {
    it('does not search for fewer than three typed characters', async () => {
      renderSearch();

      await searchFor('He');
      await vi.advanceTimersByTimeAsync(PLACE_SEARCH_DEBOUNCE_MS * 5);

      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('searches once after typing stops', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      renderSearch();

      await type('Hel');
      await vi.advanceTimersByTimeAsync(PLACE_SEARCH_DEBOUNCE_MS - 100);
      await type('Hels');
      await vi.advanceTimersByTimeAsync(PLACE_SEARCH_DEBOUNCE_MS - 100);
      await type('Helsinki');
      expect(fetchMock).not.toHaveBeenCalled();
      await settle();

      expect(fetchedQueries()).toEqual(['Helsinki']);
    });

    it('does not search for blanks around a short query', async () => {
      renderSearch();

      await searchFor('  a  ');

      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('searches at once on Enter even for a two-letter name', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      renderSearch();

      await type('Ii');
      await press('Enter');

      expect(fetchedQueries()).toEqual(['Ii']);
    });

    it('searches at once with the submit button', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      renderSearch();

      await type('Ii');
      await fireEvent.click(screen.getByRole('button', { name: SUBMIT }));

      expect(fetchedQueries()).toEqual(['Ii']);
    });

    it('does not search a blank query on Enter or with the submit button', async () => {
      renderSearch();

      await type('   ');
      await press('Enter');
      await fireEvent.click(screen.getByRole('button', { name: SUBMIT }));
      await vi.advanceTimersByTimeAsync(0);

      expect(fetchMock).not.toHaveBeenCalled();
      // No "searching" and no "nothing found" either: nothing was asked
      expect(screen.getByRole('status').textContent).toBe('');
    });

    it('does not search an empty query on Enter', async () => {
      renderSearch();

      await press('Enter');

      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('does not search again for the pending autocomplete after Enter', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      renderSearch();

      await type('Helsinki');
      await press('Enter');
      await settle();

      expect(fetchedQueries()).toEqual(['Helsinki']);
    });

    it('announces that it is searching', async () => {
      deferNextFetch();
      renderSearch();

      await searchFor('Helsinki');

      expect(screen.getByRole('status')).toHaveTextContent(SEARCHING);
    });
  });

  describe('results', () => {
    it('shows the results and announces how many were found', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      renderSearch();

      await searchFor('Hels');

      const options = screen.getAllByRole('option');
      expect(options).toHaveLength(2);
      expect(options[0]).toHaveTextContent('Helsinki');
      expect(options[0]).toHaveTextContent('Finland');
      expect(input()).toHaveAttribute('aria-expanded', 'true');
      expect(screen.getByRole('status')).toHaveTextContent(RESULTS);
    });

    it('selects a result with the keyboard', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      const onSelect = renderSearch();
      await searchFor('Hels');

      await press('ArrowDown');
      const [first, second] = screen.getAllByRole('option');
      expect(input()).toHaveAttribute('aria-activedescendant', first.id);
      expect(first).toHaveAttribute('aria-selected', 'true');

      await press('ArrowDown');
      expect(input()).toHaveAttribute('aria-activedescendant', second.id);
      await press('ArrowUp');
      expect(input()).toHaveAttribute('aria-activedescendant', first.id);

      await press('Enter');

      expect(onSelect).toHaveBeenCalledExactlyOnceWith(HELSINKI_PLACE);
      expect(input().value).toBe('Helsinki');
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(input()).toHaveAttribute('aria-expanded', 'false');
      // Picking does not search again
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it('wraps the active option around the list ends', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      renderSearch();
      await searchFor('Hels');

      await press('ArrowUp');
      const [first, second] = screen.getAllByRole('option');
      expect(input()).toHaveAttribute('aria-activedescendant', second.id);

      await press('ArrowDown');
      expect(input()).toHaveAttribute('aria-activedescendant', first.id);
    });

    it('selects a result with a mouse click on the focused input', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime.bind(vi) });
      const onSelect = renderSearch();
      await searchFor('Hels');

      // A real press on an option must not blur the input, which would close the list first
      await user.click(screen.getAllByRole('option')[0]);

      expect(onSelect).toHaveBeenCalledExactlyOnceWith(HELSINKI_PLACE);
    });

    it('selects a result with a click', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      const onSelect = renderSearch();
      await searchFor('Hels');

      await fireEvent.click(screen.getAllByRole('option')[0]);

      expect(onSelect).toHaveBeenCalledExactlyOnceWith(HELSINKI_PLACE);
    });

    it('keeps option ids tied to the result, not its position', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      renderSearch();
      await searchFor('Hels');
      const idOfHelsinki = screen.getAllByRole('option')[0].id;

      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINGBORG, HELSINKI)));
      await searchFor('Helsi');

      expect(screen.getAllByRole('option')[1].id).toBe(idOfHelsinki);
    });

    it('says so when nothing was found', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse()));
      renderSearch();

      await searchFor('Zzzzz');

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(screen.getAllByText(NO_RESULTS).length).toBeGreaterThan(0);
    });
  });

  describe('result presentation', () => {
    it('highlights the active option with the shared highlight and not with the info color', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      renderSearch();
      await searchFor('Hels');
      await press('ArrowDown');

      const [active, idle] = screen.getAllByRole('option');
      for (const token of [
        ...OPTION_HIGHLIGHT_BG_CLASS.split(' '),
        ...OPTION_HIGHLIGHT_OUTLINE_CLASS.split(' '),
      ]) {
        expect(active).toHaveClass(token);
        expect(idle).not.toHaveClass(token);
      }
      expect(active.className).not.toContain('--color-info');
    });

    it('shows the detail in the muted text color', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      renderSearch();
      await searchFor('Hels');

      expect(screen.getByText('Finland')).toHaveClass(PLACE_OPTION_DETAIL_CLASS);
    });

    it('separates the name from the detail in the accessible name of an option', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      renderSearch();
      await searchFor('Hels');

      // The comma is hidden text; jsdom puts spaces around inline elements, so allow them
      expect(screen.getByRole('option', { name: /^Helsinki\s*,\s*Finland$/ })).toBeInTheDocument();
    });

    it('labels the kind of a place so look-alike results can be told apart', async () => {
      const station = {
        ...HELSINKI,
        properties: {
          ...HELSINKI.properties,
          osm_id: 25474663,
          osm_type: 'N',
          osm_key: 'railway',
          osm_value: 'station',
        },
      };
      const city = {
        ...HELSINKI,
        properties: { ...HELSINKI.properties, osm_key: 'place', osm_value: 'city' },
      };
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(city, station, HELSINGBORG)));
      renderSearch();
      await searchFor('Hels');

      const [cityOption, stationOption, plainOption] = screen.getAllByRole('option');
      expect(cityOption).toHaveAccessibleName(
        /^Helsinki\s*,\s*components\.locationMap\.search\.kind\.city\s*,\s*Finland$/
      );
      expect(stationOption).toHaveAccessibleName(
        /^Helsinki\s*,\s*components\.locationMap\.search\.kind\.station\s*,\s*Finland$/
      );
      expect(plainOption).toHaveAccessibleName(/^Helsingborg\s*,\s*Finland$/);
    });
  });

  describe('failure display', () => {
    it('announces a failure in an always-rendered alert region and not in the status region', async () => {
      renderSearch();
      const alert = screen.getByRole('alert');
      expect(alert.textContent).toBe('');

      fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
      await searchFor('Helsinki');

      expect(screen.getByRole('alert')).toBe(alert);
      expect(alert).toHaveTextContent(ERROR_NETWORK);
      expect(screen.getByRole('status')).not.toHaveTextContent(ERROR_NETWORK);
    });

    it('clears the alert when the next search starts', async () => {
      renderSearch();
      fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
      await searchFor('Helsinki');
      expect(screen.getByRole('alert')).toHaveTextContent(ERROR_NETWORK);

      deferNextFetch();
      await press('Enter');

      expect(screen.getByRole('alert').textContent).toBe('');
    });

    it('does not use the alert region for a search without results', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse()));
      renderSearch();
      await searchFor('Zzzzz');

      expect(screen.getByRole('alert').textContent).toBe('');
      expect(screen.getByRole('status')).toHaveTextContent(NO_RESULTS);
    });

    it('styles a failure as an error with an icon and a search without results as plain text', async () => {
      renderSearch();
      fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
      await searchFor('Helsinki');

      const failure = screen
        .getAllByText(ERROR_NETWORK)
        .find(element => element.getAttribute('aria-hidden') === 'true');
      expect(failure).toHaveClass(PLACE_ERROR_CLASS);
      expect(failure?.querySelector('svg')).not.toBeNull();

      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse()));
      await type('Zzzzz');
      await settle();

      const empty = screen
        .getAllByText(NO_RESULTS)
        .find(element => element.getAttribute('aria-hidden') === 'true');
      expect(empty).not.toHaveClass(PLACE_ERROR_CLASS);
      expect(empty?.querySelector('svg')).toBeNull();
    });

    it('keeps the message line in the layout when it has no text', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse()));
      const { container } = render(PlaceSearch, { props: { onSelect: vi.fn() } });
      const line = container.querySelector('p[aria-hidden="true"]');
      expect(line).not.toBeNull();
      expect(line).toHaveClass('min-h-8');

      await searchFor('Zzzzz');

      expect(container.querySelector('p[aria-hidden="true"]')).toBe(line);
    });
  });

  describe('superseded searches', () => {
    it('shows no results when the input was cleared during a search', async () => {
      const pending = deferNextFetch();
      renderSearch();
      await searchFor('Helsinki');

      await type('');
      pending.resolve(jsonResponse(HELSINKI));
      await vi.advanceTimersByTimeAsync(0);

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(screen.getByRole('status')).not.toHaveTextContent(RESULTS);
    });

    it('shows no results when the input became shorter than three characters', async () => {
      const pending = deferNextFetch();
      renderSearch();
      await searchFor('Helsinki');

      await type('He');
      pending.resolve(jsonResponse(HELSINKI));
      await vi.advanceTimersByTimeAsync(0);

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    });

    it('drops the results on screen when the text changes', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      const onSelect = renderSearch();
      await searchFor('Hels');
      await press('ArrowDown');
      expect(screen.getAllByRole('option')).toHaveLength(2);

      await type('Helsin');

      // The old list is gone before anything is searched, so it cannot be clicked
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(input()).not.toHaveAttribute('aria-activedescendant');

      await press('Enter');

      // Enter searches the new text instead of picking the old active option
      expect(screen.getByRole('status')).not.toHaveTextContent(RESULTS);
      expect(onSelect).not.toHaveBeenCalled();
      expect(fetchedQueries().at(-1)).toBe('Helsin');
    });

    it('aborts the request of a search the input moved on from', async () => {
      deferNextFetch();
      renderSearch();
      await searchFor('Helsinki');
      const signal = fetchMock.mock.calls[0]?.[1]?.signal;

      await type('Helsingborg');

      expect(signal?.aborted).toBe(true);
    });

    it('ignores a response that arrives after a newer search', async () => {
      const older = deferNextFetch();
      renderSearch();
      await searchFor('Hel');

      const newer = deferNextFetch();
      await type('Helsingborg');
      await press('Enter');

      newer.resolve(jsonResponse(HELSINGBORG));
      await vi.advanceTimersByTimeAsync(0);
      older.resolve(jsonResponse(HELSINKI));
      await vi.advanceTimersByTimeAsync(0);

      const options = screen.getAllByRole('option');
      expect(options).toHaveLength(1);
      expect(options[0]).toHaveTextContent('Helsingborg');
    });

    it('ignores a response that arrives after the component was destroyed', async () => {
      const pending = deferNextFetch();
      renderSearch();
      await searchFor('Helsinki');
      const signal = fetchMock.mock.calls[0]?.[1]?.signal;

      cleanup();
      pending.resolve(jsonResponse(HELSINKI));
      await vi.advanceTimersByTimeAsync(0);

      expect(signal?.aborted).toBe(true);
    });
  });

  describe('failures', () => {
    it('shows a clear message when the service is unreachable and keeps the input usable', async () => {
      fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
      renderSearch();

      await searchFor('Helsinki');

      expect(screen.getAllByText(ERROR_NETWORK).length).toBeGreaterThan(0);
      expect(input()).toBeEnabled();

      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      await press('Enter');
      await vi.advanceTimersByTimeAsync(0);

      expect(screen.getAllByRole('option')).toHaveLength(1);
      expect(screen.queryByText(ERROR_NETWORK)).not.toBeInTheDocument();
    });

    it('explains a rate limit', async () => {
      fetchMock.mockResolvedValueOnce(new Response('{}', { status: 429 }));
      renderSearch();

      await searchFor('Helsinki');

      expect(screen.getAllByText(ERROR_RATE_LIMITED).length).toBeGreaterThan(0);
    });

    it('explains a service that does not respond properly', async () => {
      fetchMock.mockResolvedValueOnce(new Response('{}', { status: 503 }));
      renderSearch();

      await searchFor('Helsinki');

      expect(screen.getAllByText(ERROR_UNAVAILABLE).length).toBeGreaterThan(0);
    });
  });

  describe('Escape', () => {
    it('closes the results without reaching document listeners, then clears the input', async () => {
      const documentKeydown = vi.fn();
      document.addEventListener('keydown', documentKeydown);
      try {
        fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
        renderSearch();
        await searchFor('Helsinki');
        expect(screen.getByRole('listbox')).toBeInTheDocument();

        await press('Escape');
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
        expect(input().value).toBe('Helsinki');

        await press('Escape');
        expect(input().value).toBe('');
        expect(documentKeydown).not.toHaveBeenCalled();

        // Nothing left to close: the key goes on to the surrounding dialog
        await press('Escape');
        expect(documentKeydown).toHaveBeenCalledTimes(1);
      } finally {
        document.removeEventListener('keydown', documentKeydown);
      }
    });

    it('cancels a running search when it clears the input', async () => {
      deferNextFetch();
      renderSearch();
      await searchFor('Helsinki');
      const signal = fetchMock.mock.calls[0]?.[1]?.signal;

      await press('Escape');

      expect(signal?.aborted).toBe(true);
      expect(input().value).toBe('');
    });
  });

  describe('disabled', () => {
    it('disables the controls and hides the results while disabled', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      const onSelect = vi.fn<(place: PlaceResult) => void>();
      const { rerender } = render(PlaceSearch, { props: { onSelect } });
      await searchFor('Helsinki');
      expect(screen.getByRole('listbox')).toBeInTheDocument();

      await rerender({ onSelect, disabled: true });

      expect(input()).toBeDisabled();
      expect(screen.getByRole('button', { name: SUBMIT })).toBeDisabled();
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    });

    it('forgets the search when it is disabled, so enabling it again shows no old results', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      const onSelect = vi.fn<(place: PlaceResult) => void>();
      const { rerender } = render(PlaceSearch, { props: { onSelect } });
      await searchFor('Helsinki');
      expect(screen.getByRole('listbox')).toBeInTheDocument();

      await rerender({ onSelect, disabled: true });
      await rerender({ onSelect, disabled: false });

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(input()).toHaveAttribute('aria-expanded', 'false');
    });

    it('drops the answer of a request that was running when it was disabled', async () => {
      const pending = deferNextFetch();
      const onSelect = vi.fn<(place: PlaceResult) => void>();
      const { rerender } = render(PlaceSearch, { props: { onSelect } });
      await searchFor('Helsinki');

      await rerender({ onSelect, disabled: true });
      pending.resolve(jsonResponse(HELSINKI));
      await vi.advanceTimersByTimeAsync(0);
      await rerender({ onSelect, disabled: false });

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(screen.getByRole('status').textContent).toBe('');
    });

    it('does not start a pending search once it is disabled', async () => {
      const onSelect = vi.fn<(place: PlaceResult) => void>();
      const { rerender } = render(PlaceSearch, { props: { onSelect } });
      await type('Helsinki');

      await rerender({ onSelect, disabled: true });
      await settle();

      expect(fetchMock).not.toHaveBeenCalled();
    });
  });

  describe('focus', () => {
    it('does not open the list when the input lost focus before the results arrived', async () => {
      const pending = deferNextFetch();
      renderSearch();
      await searchFor('Helsinki');

      input().blur();
      pending.resolve(jsonResponse(HELSINKI));
      await vi.advanceTimersByTimeAsync(0);

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(input()).toHaveAttribute('aria-expanded', 'false');
    });

    it('opens the list with focus in the input after the search button was used', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI)));
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime.bind(vi) });
      renderSearch();
      await type('Ii');

      await user.click(screen.getByRole('button', { name: SUBMIT }));
      await vi.advanceTimersByTimeAsync(0);

      expect(screen.getByRole('listbox')).toBeInTheDocument();
      expect(document.activeElement).toBe(input());
    });

    it('closes the list when the input loses focus', async () => {
      fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
      renderSearch();
      await searchFor('Hels');
      expect(screen.getByRole('listbox')).toBeInTheDocument();

      input().blur();
      await vi.advanceTimersByTimeAsync(0);

      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(input()).toHaveAttribute('aria-expanded', 'false');
    });

    it('starts at the first option when ArrowDown reopens the list after a blur', async () => {
      fetchMock.mockImplementation(() =>
        Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG, HELSINGFORS))
      );
      renderSearch();
      await searchFor('Hels');
      await press('ArrowDown');
      await press('ArrowDown');

      input().blur();
      input().focus();
      await press('ArrowDown');

      const [first] = screen.getAllByRole('option');
      expect(input()).toHaveAttribute('aria-activedescendant', first.id);
    });
  });

  describe('privacy', () => {
    it('explains that searches go to Photon', () => {
      renderSearch();

      expect(screen.getByText(DISCLOSURE)).toBeInTheDocument();
      const link = screen.getByRole('link', { name: OPEN_PHOTON });
      expect(link).toHaveAttribute('href', PHOTON_SITE_URL);
      expect(link).toHaveAttribute('target', '_blank');
      expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    });

    it('links the disclosure to the input', () => {
      renderSearch();

      const describedBy = input().getAttribute('aria-describedby') ?? '';
      const disclosure = screen.getByText(DISCLOSURE);
      expect(describedBy.split(' ')).toContain(disclosure.id);
    });
  });
});

describe('PlaceSearch Accessibility', () => {
  beforeEach(() => {
    fetchMock = vi.fn<typeof fetch>();
    globalThis.fetch = fetchMock;
  });

  afterEach(() => {
    cleanup();
    globalThis.fetch = originalFetch;
  });

  it('exposes a combobox, a listbox and options', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
    renderSearch();

    const combobox = input();
    expect(combobox).toHaveAttribute('aria-autocomplete', 'list');
    expect(combobox).toHaveAttribute('aria-expanded', 'false');
    expect(combobox).toHaveAttribute('autocomplete', 'off');

    combobox.focus();
    await fireEvent.input(combobox, { target: { value: 'Hels' } });
    await fireEvent.keyDown(combobox, { key: 'Enter' });
    const listbox = await screen.findByRole('listbox');

    expect(combobox).toHaveAttribute('aria-controls', listbox.id);
    expect(combobox).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getAllByRole('option')).toHaveLength(2);
  });

  it('keeps an always-rendered status region', () => {
    renderSearch();

    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('names the submit button', () => {
    renderSearch();

    expect(screen.getByRole('button', { name: SUBMIT })).toBeInTheDocument();
  });

  it('has no axe violations with the results open', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(HELSINKI, HELSINGBORG)));
    renderSearch();
    input().focus();
    await fireEvent.input(input(), { target: { value: 'Hels' } });
    await fireEvent.keyDown(input(), { key: 'Enter' });
    await screen.findByRole('listbox');

    await expect(expectNoA11yViolations(renderedContainer)).resolves.toBeUndefined();
  });
});
