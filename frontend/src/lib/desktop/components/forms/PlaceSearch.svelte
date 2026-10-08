<!--
  PlaceSearch Component

  Purpose: Find a place by name and report its coordinates. Used by LocationMap
  (its `placeSearch` prop) to move the station pin to a town or address.

  Features:
  - WAI-ARIA combobox with a listbox popup: the focus stays in the input,
    ArrowUp/ArrowDown move the active option (aria-activedescendant), Enter picks,
    Escape closes the list and then clears the input
  - Searches Photon (photon.komoot.io, OpenStreetMap data) from the browser, as typed
    after a pause once there are at least PLACE_SEARCH_MIN_AUTOCOMPLETE_LENGTH
    characters, and at once on Enter or the search button for any query
  - The previous request is aborted when a new search starts or the input changes,
    so a late response is never shown
  - States: idle, searching, results, no results, unreachable/rate limited/failing
    service. An always-rendered sr-only status region announces them
  - A line under the field says where searches are sent

  Props:
  - onSelect: called with the chosen place
  - disabled: disables the input and button, drops the results and cancels a pending search
  - className: additional classes for the root

  @component
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import { CircleAlert, ExternalLink, Search } from '@lucide/svelte';
  import { getLocale, t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import { createDebounce } from '$lib/utils/debounce';
  import { safeGet } from '$lib/utils/security';
  import { generateId } from '$lib/utils/uuid';
  import {
    PHOTON_SITE_URL,
    PLACE_SEARCH_DEBOUNCE_MS,
    PLACE_SEARCH_MIN_AUTOCOMPLETE_LENGTH,
    searchPlaces,
    type PlaceResult,
    type PlaceSearchFailure,
  } from '$lib/utils/placeSearch';
  import Button from '$lib/desktop/components/ui/Button.svelte';
  import { getOptionStateClasses } from './SelectDropdown.styles';
  import {
    PLACE_DISCLOSURE_CLASS,
    PLACE_DISCLOSURE_ICON_CLASS,
    PLACE_ERROR_CLASS,
    PLACE_MESSAGE_CLASS,
    PLACE_OPTION_CLASS,
    PLACE_OPTION_DETAIL_CLASS,
  } from './PlaceSearch.styles';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';

  interface Props {
    /** Called with the place the user picked. */
    onSelect: (_place: PlaceResult) => void;
    /** Disable the input and button, drop the results and cancel a pending search. */
    disabled?: boolean;
    className?: string;
  }

  let { onSelect, disabled = false, className = '' }: Props = $props();

  const inputId = generateId('place-search-input');
  const listboxId = generateId('place-search-list');
  const disclosureId = generateId('place-search-disclosure');
  const messageId = generateId('place-search-message');

  type Phase = 'idle' | 'searching' | 'done' | 'failed';

  let inputElement: HTMLInputElement | undefined = $state();
  let query = $state('');
  let phase = $state<Phase>('idle');
  let results = $state<PlaceResult[]>([]);
  let failure = $state<PlaceSearchFailure | null>(null);
  let listOpen = $state(false);
  let activeIndex = $state(-1);

  // Each search takes the next number; only the latest one may show its outcome.
  let sequence = 0;
  let controller: AbortController | null = null;

  const errorKeys = {
    network: 'components.locationMap.search.errorNetwork',
    rateLimited: 'components.locationMap.search.errorRateLimited',
    unavailable: 'components.locationMap.search.errorUnavailable',
    timeout: 'components.locationMap.search.errorUnavailable',
    invalid: 'components.locationMap.search.errorUnavailable',
  } as const satisfies Record<PlaceSearchFailure, string>;

  let message = $derived.by(() => {
    if (phase === 'searching') return t('components.locationMap.search.searching');
    if (phase === 'failed' && failure) return t(safeGet(errorKeys, failure, errorKeys.unavailable));
    if (phase === 'done' && results.length === 0) {
      return t('components.locationMap.search.noResults');
    }
    return '';
  });
  let isFailure = $derived(phase === 'failed' && failure !== null);
  // What the polite status region says: progress, or how many places were found. A failure
  // goes to the alert region instead.
  let announcement = $derived(
    phase === 'done' && results.length > 0
      ? t('components.locationMap.search.results', { count: results.length })
      : isFailure
        ? ''
        : message
  );
  let alertText = $derived(isFailure ? message : '');
  let describedBy = $derived(message ? `${disclosureId} ${messageId}` : disclosureId);
  let activePlace = $derived(listOpen && activeIndex >= 0 ? results.at(activeIndex) : undefined);
  let activeOptionId = $derived(activePlace ? optionId(activePlace) : undefined);

  /** Kind label and place detail on the second line of an option. */
  function detailText(place: PlaceResult): string {
    const kind = place.kind ? t(`components.locationMap.search.kind.${place.kind}`) : '';
    return [kind, place.detail].filter(Boolean).join(', ');
  }

  function optionId(place: PlaceResult): string {
    return `${listboxId}-${place.id.replace(/[^A-Za-z0-9_-]/g, '_')}`;
  }

  /** Forget any search in progress; its outcome will be dropped. */
  function invalidateSearch() {
    sequence++;
    controller?.abort();
    controller = null;
  }

  function resetResults() {
    phase = 'idle';
    failure = null;
    results = [];
    listOpen = false;
    activeIndex = -1;
  }

  async function runSearch(text: string) {
    invalidateSearch();
    const current = sequence;
    const ownController = new AbortController();
    controller = ownController;

    resetResults();
    phase = 'searching';

    const outcome = await searchPlaces(text, {
      locale: getLocale(),
      signal: ownController.signal,
    });
    if (current !== sequence) return;
    controller = null;

    if (outcome.status === 'ok') {
      results = outcome.results;
      phase = 'done';
      // A list for an input that lost focus meanwhile could not be driven or dismissed.
      listOpen = outcome.results.length > 0 && document.activeElement === inputElement;
    } else if (outcome.status === 'error') {
      failure = outcome.reason;
      phase = 'failed';
    } else {
      resetResults();
    }
  }

  const autocomplete = createDebounce((text: string) => {
    void runSearch(text);
  }, PLACE_SEARCH_DEBOUNCE_MS);

  function handleInput(event: Event) {
    const target = event.currentTarget;
    if (!(target instanceof HTMLInputElement)) return;
    query = target.value;

    // Whatever was running or shown belongs to the previous text.
    autocomplete.cancel();
    invalidateSearch();
    resetResults();

    const text = query.trim();
    if (text.length >= PLACE_SEARCH_MIN_AUTOCOMPLETE_LENGTH) {
      autocomplete(text);
    }
  }

  /** Search now, for any non-empty query. */
  function searchNow() {
    const text = query.trim();
    if (text === '') return;
    autocomplete.cancel();
    void runSearch(text);
  }

  // The button took focus from the input; give it back so the results can be driven
  // from the keyboard.
  function searchFromButton() {
    inputElement?.focus();
    searchNow();
  }

  function selectPlace(place: PlaceResult) {
    autocomplete.cancel();
    invalidateSearch();
    query = place.name;
    resetResults();
    onSelect(place);
  }

  function moveActive(step: 1 | -1) {
    if (results.length === 0) return;
    if (!listOpen) {
      listOpen = true;
    }
    const last = results.length - 1;
    if (step === 1) {
      activeIndex = activeIndex >= last ? 0 : activeIndex + 1;
    } else {
      activeIndex = activeIndex <= 0 ? last : activeIndex - 1;
    }
  }

  // Native listener rather than a Svelte `onkeydown`: Svelte delegates keydown to the
  // document, where stopPropagation could no longer keep Escape from a surrounding
  // modal, which would treat it as its own close request.
  function handleKeydown(event: KeyboardEvent) {
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowUp':
        if (results.length === 0) return;
        event.preventDefault();
        event.stopPropagation();
        moveActive(event.key === 'ArrowDown' ? 1 : -1);
        break;
      case 'Enter': {
        event.preventDefault();
        event.stopPropagation();
        if (activePlace) {
          selectPlace(activePlace);
        } else {
          searchNow();
        }
        break;
      }
      case 'Escape':
        if (listOpen) {
          event.preventDefault();
          event.stopPropagation();
          listOpen = false;
          activeIndex = -1;
        } else if (query !== '') {
          event.preventDefault();
          event.stopPropagation();
          autocomplete.cancel();
          invalidateSearch();
          query = '';
          resetResults();
        }
        break;
    }
  }

  $effect(() => {
    const element = inputElement;
    if (!element) return;
    element.addEventListener('keydown', handleKeydown);
    return () => element.removeEventListener('keydown', handleKeydown);
  });

  // A control the page disabled starts nothing and keeps no old results.
  $effect(() => {
    if (!disabled) return;
    autocomplete.cancel();
    invalidateSearch();
    resetResults();
  });

  onDestroy(() => {
    autocomplete.cancel();
    invalidateSearch();
  });
</script>

<div class={cn('relative', className)}>
  <label for={inputId} class="sr-only">{t('components.locationMap.search.label')}</label>
  <div class="flex gap-2">
    <div class="relative min-w-0 flex-1">
      <input
        bind:this={inputElement}
        id={inputId}
        type="search"
        value={query}
        {disabled}
        oninput={handleInput}
        onblur={() => {
          listOpen = false;
          activeIndex = -1;
        }}
        placeholder={t('components.locationMap.search.placeholder')}
        autocomplete="off"
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={listOpen}
        aria-controls={listboxId}
        aria-activedescendant={activeOptionId}
        aria-describedby={describedBy}
        class="input input-sm w-full pr-9"
      />
      {#if phase === 'searching'}
        <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center">
          <LoadingSpinner size="xs" aria-hidden="true" />
        </span>
      {/if}
      {#if listOpen && results.length > 0 && !disabled}
        <ul
          id={listboxId}
          role="listbox"
          aria-label={t('components.locationMap.search.label')}
          class="absolute left-0 right-0 top-full z-30 mt-1 max-h-60 overflow-y-auto rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] shadow-lg"
        >
          {#each results as place, index (place.id)}
            <!-- Keyboard use goes through the combobox input (aria-activedescendant), so the
                 options are neither focusable nor given key handlers. -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <li
              id={optionId(place)}
              role="option"
              aria-selected={index === activeIndex}
              class={cn(
                'cursor-pointer px-3 py-2 text-sm',
                PLACE_OPTION_CLASS,
                getOptionStateClasses({ selected: false, highlighted: index === activeIndex })
              )}
              onmousedown={event => event.preventDefault()}
              onclick={() => selectPlace(place)}
            >
              <!-- The hidden comma ends the name, so a screen reader does not run it into the detail. -->
              <span class="font-medium"
                >{place.name}{#if detailText(place)}<span class="sr-only">,</span>{/if}</span
              >
              {#if detailText(place)}
                <span class={cn('ml-1 text-xs', PLACE_OPTION_DETAIL_CLASS)}
                  >{detailText(place)}</span
                >
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
    <Button
      variant="default"
      size="md"
      className="shrink-0"
      aria-label={t('components.locationMap.search.submit')}
      {disabled}
      onclick={searchFromButton}
    >
      <Search class="size-4" aria-hidden="true" />
    </Button>
  </div>

  <!-- Always rendered; only its text changes, so changes are announced. -->
  <div class="sr-only" role="status" aria-atomic="true">{announcement}</div>

  <!-- Always rendered, like the status region, so a failure is announced when it appears. -->
  <div class="sr-only" role="alert" aria-atomic="true">{alertText}</div>

  <!-- Always rendered so a message appearing does not move what is below; the regions above
       announce it. -->
  <p
    id={messageId}
    class={cn(
      // Two lines reserved: a longer translation wraps at tablet width and must not move the map
      'mt-1 flex min-h-8 items-start gap-1.5 text-xs',
      isFailure ? PLACE_ERROR_CLASS : PLACE_MESSAGE_CLASS
    )}
    aria-hidden="true"
  >
    {#if isFailure}
      <CircleAlert class="size-3.5 shrink-0" />
    {/if}
    {message}
  </p>

  <p id={disclosureId} class={cn('mt-1 text-xs', PLACE_DISCLOSURE_CLASS)}>
    {t('components.locationMap.search.disclosure')}
    <a
      href={PHOTON_SITE_URL}
      target="_blank"
      rel="noopener noreferrer"
      class={cn(
        'ml-0.5 inline-flex align-middle hover:text-[var(--color-base-content)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)]',
        PLACE_DISCLOSURE_ICON_CLASS
      )}
      aria-label={t('components.locationMap.search.openPhoton')}
    >
      <ExternalLink class="size-3" aria-hidden="true" />
    </a>
  </p>
</div>
