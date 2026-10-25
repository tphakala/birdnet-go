<!--
  Audio Equalizer Settings Component
  
  Purpose: Manages audio filter/equalizer configuration for BirdNET-Go
  
  Features:
  - Enable/disable equalizer
  - Add/remove audio filters (LowPass, HighPass)
  - Configure filter parameters (frequency, Q factor, passes)
  - Dynamic parameter inputs based on filter type from backend
  - Real-time frequency response visualization
  
  Props:
  - equalizerSettings: Object containing equalizer enabled state and filters array
  - disabled: Boolean to disable all inputs
  - onUpdate: Callback function to update equalizer settings
  - sampleRate: Sample rate the response preview is calculated at (optional;
    omit to use the server's configured rate)
  
  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import LowPassIcon from '$lib/desktop/components/ui/LowPassIcon.svelte';
  import HighPassIcon from '$lib/desktop/components/ui/HighPassIcon.svelte';
  import BandRejectIcon from '$lib/desktop/components/ui/BandRejectIcon.svelte';
  import FilterResponseGraph from './FilterResponseGraph.svelte';
  import EqualizerNumberInput, {
    eqLabelClasses,
    EQ_PARAMETER_CELL_CLASS,
    type EqRowVariant,
  } from './EqualizerNumberInput.svelte';
  import { untrack } from 'svelte';
  import { SvelteMap } from 'svelte/reactivity';
  import { safeGet, safeArrayAccess } from '$lib/utils/security';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { getCsrfToken } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { generateId } from '$lib/utils/uuid';
  import {
    DEFAULT_EQ_GAIN_DB,
    DEFAULT_EQ_Q,
    DEFAULT_EQ_WIDTH_HZ,
    EQ_DB_PER_PASS,
    EQ_PASSES_OFF,
    eqInputStep,
    type EqFieldNotice,
    type EqNumericParameter,
  } from '$lib/utils/audio/equalizer';
  import type { EqualizerFilterType } from '$lib/stores/settings';
  import fallbackEqFilterConfig from './eqFilterFallbackConfig.json';

  const logger = loggers.settings;

  // Attenuation options for filter passes (0 passes means the filter is off)
  let attenuationOptions = $derived([
    { value: '0', label: t('settings.audio.audioFilters.attenuationLevels.off') },
    { value: '1', label: t('settings.audio.audioFilters.attenuationLevels.12db') },
    { value: '2', label: t('settings.audio.audioFilters.attenuationLevels.24db') },
    { value: '3', label: t('settings.audio.audioFilters.attenuationLevels.36db') },
    { value: '4', label: t('settings.audio.audioFilters.attenuationLevels.48db') },
  ]);

  // The attenuation options for a stored passes value. A passes value the list does not
  // offer (the backend accepts more than the editor offers) gets an option of its own, so
  // the dropdown shows the stored value instead of an empty selection.
  function attenuationOptionsFor(passes: number) {
    if (!Number.isInteger(passes) || attenuationOptions.some(o => o.value === String(passes))) {
      return attenuationOptions;
    }
    return [
      ...attenuationOptions,
      {
        value: String(passes),
        label: t('settings.audio.audioFilters.attenuationLevels.custom', {
          db: passes * EQ_DB_PER_PASS,
        }),
      },
    ];
  }

  interface FilterParameter {
    Name: string;
    Label: string;
    Type: string;
    Unit?: string;
    Min: number;
    Max: number;
    Default: number;
    Tooltip?: string;
  }

  interface FilterTypeConfig {
    Parameters: FilterParameter[];
    Tooltip?: string;
  }

  // Fallback configuration used when API fails or returns invalid data. It is a
  // copy of the backend table, kept identical by a Go test.
  const FALLBACK_EQ_FILTER_CONFIG: Record<string, FilterTypeConfig> = fallbackEqFilterConfig;

  interface Filter {
    type: EqualizerFilterType;
    frequency: number;
    q?: number;
    width?: number;
    gain?: number;
    passes?: number;
  }

  // Separate type for the new filter form which can have empty type before selection
  interface NewFilterForm {
    type: EqualizerFilterType | '';
    frequency: number;
    q?: number;
    width?: number;
    gain?: number;
    passes?: number;
  }

  interface EqualizerSettings {
    enabled: boolean;
    filters: Filter[];
  }

  interface Props {
    equalizerSettings: EqualizerSettings;
    disabled?: boolean;
    onUpdate: (_updatedSettings: EqualizerSettings) => void;
    /** Sample rate the preview is calculated at; omit to use the server's configured rate. */
    sampleRate?: number;
  }

  let { equalizerSettings, disabled = false, onUpdate, sampleRate }: Props = $props();

  // Load filter config from backend
  let eqFilterConfig = $state<Record<string, FilterTypeConfig>>({});
  let loadingConfig = $state(true);

  // Frequency of the blank form before a type is picked; picking a type sets its default
  const UNSELECTED_FREQUENCY_HZ = 0;

  // Blank form for a new filter; new filters start Off (0 passes)
  function createEmptyFilter(): NewFilterForm {
    return {
      type: '',
      frequency: UNSELECTED_FREQUENCY_HZ,
      q: DEFAULT_EQ_Q,
      width: DEFAULT_EQ_WIDTH_HZ,
      gain: DEFAULT_EQ_GAIN_DB,
      passes: EQ_PASSES_OFF,
    };
  }

  // New filter state for adding filters (uses NewFilterForm to allow empty type)
  let newFilter = $state<NewFilterForm>(createEmptyFilter());

  // Map filter types to their icon components
  const filterIconMap = {
    LowPass: LowPassIcon,
    HighPass: HighPassIcon,
    BandReject: BandRejectIcon,
  };

  // Filter type options derived from config - with icons
  let filterTypeOptions = $derived.by(() => {
    const placeholder = {
      value: '',
      label: t('settings.audio.audioFilters.selectFilterType'),
    };
    const typeOptions = Object.keys(eqFilterConfig).map(filterType => ({
      value: filterType,
      label: filterType,
      icon: filterIconMap[filterType as keyof typeof filterIconMap],
    }));
    return [placeholder, ...typeOptions];
  });

  // Load filter configuration from backend on mount with cleanup
  let abortController: AbortController | null = null;

  $effect(() => {
    loadFilterConfig();

    return () => {
      // Cleanup: abort any pending request
      if (abortController) {
        abortController.abort();
      }
    };
  });

  async function loadFilterConfig() {
    loadingConfig = true;

    // Create new abort controller for this request
    abortController = new AbortController();

    try {
      // Build headers, only including CSRF token if available
      const headers: Record<string, string> = {};
      const csrfToken = getCsrfToken();
      if (csrfToken) {
        headers['X-CSRF-Token'] = csrfToken;
      }

      const response = await fetch(buildAppUrl('/api/v2/system/audio/equalizer/config'), {
        headers,
        // Revalidate: a table cached by an older release would clamp new limits.
        cache: 'no-cache',
        signal: abortController.signal,
      });

      if (!response.ok) {
        // If API doesn't exist, use fallback config
        eqFilterConfig = { ...FALLBACK_EQ_FILTER_CONFIG };
        return;
      }

      // Parse JSON with error handling
      let data;
      try {
        data = await response.json();
      } catch (parseError) {
        logger.error('Error parsing filter config JSON:', parseError);
        eqFilterConfig = { ...FALLBACK_EQ_FILTER_CONFIG };
        return;
      }

      eqFilterConfig = data || { ...FALLBACK_EQ_FILTER_CONFIG };
    } catch (error) {
      // Don't log abort errors as they're expected during cleanup
      if (error instanceof Error && error.name !== 'AbortError') {
        logger.error('Error loading filter config:', error);
      }
      // Use fallback config
      eqFilterConfig = { ...FALLBACK_EQ_FILTER_CONFIG };
    } finally {
      loadingConfig = false;
      abortController = null;
    }
  }

  // Get filter parameters for a given filter type
  function getEqFilterParameters(filterType: string): FilterParameter[] {
    const config = safeGet(eqFilterConfig, filterType);
    return config?.Parameters || [];
  }

  // The numeric parameter a table entry describes, or undefined for Passes and unknown names
  function numericParameter(param: FilterParameter): EqNumericParameter | undefined {
    switch (param.Name.toLowerCase()) {
      case 'frequency':
        return 'Frequency';
      case 'q':
        return 'Q';
      case 'width':
        return 'Width';
      case 'gain':
        return 'Gain';
    }
    return undefined;
  }

  // Translated label of a filter parameter. The table's own English label is the
  // fallback for a parameter the editor has no translation for.
  function parameterLabel(filterType: string, param: FilterParameter): string {
    switch (param.Name.toLowerCase()) {
      case 'frequency':
        return filterType === 'BandReject'
          ? t('settings.audio.audioFilters.centerFrequency')
          : t('settings.audio.audioFilters.cutoffFrequency');
      case 'q':
        return t('settings.audio.audioFilters.qFactor');
      case 'width':
        return t('settings.audio.audioFilters.bandwidth');
      case 'gain':
        return t('settings.audio.audioFilters.gain');
      case 'passes':
        return t('settings.audio.audioFilters.attenuation');
    }
    return param.Label;
  }

  // Stored value of a numeric parameter in a filter or in the new-filter form
  function parameterValue(
    filter: Filter | NewFilterForm,
    name: EqNumericParameter
  ): number | undefined {
    switch (name) {
      case 'Frequency':
        return filter.frequency;
      case 'Q':
        return filter.q;
      case 'Width':
        return filter.width;
      case 'Gain':
        return filter.gain;
    }
  }

  // Store a committed numeric parameter in the new-filter form
  function setNewFilterParameter(name: EqNumericParameter, value: number) {
    switch (name) {
      case 'Frequency':
        newFilter = { ...newFilter, frequency: value };
        break;
      case 'Q':
        newFilter = { ...newFilter, q: value };
        break;
      case 'Width':
        newFilter = { ...newFilter, width: value };
        break;
      case 'Gain':
        newFilter = { ...newFilter, gain: value };
        break;
    }
  }

  // Number of filter rows. Part of the parameter cell keys: rows are keyed by position, so
  // after a row is removed a cell would otherwise show the next filter. Any change in the
  // count remounts every cell, and the effect below drops every filter row's notice.
  const filterCount = $derived((equalizerSettings.filters || []).length);

  // Row a notice belongs to: a filter row by its index, or the new-filter row
  type NoticeRow = number | 'new';

  // The latest notice in a row, the filter type it was raised for and the field it is about
  interface RowNotice extends EqFieldNotice {
    type: string;
    param: string;
  }

  // Each row shows only its latest notice, in a band the row reserves (see noticeBand)
  const rowNotices = new SvelteMap<NoticeRow, RowNotice>();

  $effect(() => {
    void filterCount;
    untrack(() => {
      for (const row of [...rowNotices.keys()]) {
        if (row !== 'new') rowNotices.delete(row);
      }
    });
  });

  // Record a field's notice, or withdraw it when the row's notice is that field's
  function setFieldNotice(
    row: NoticeRow,
    type: string,
    param: string,
    notice: EqFieldNotice | null
  ) {
    if (notice) {
      rowNotices.set(row, { ...notice, type, param });
      return;
    }
    const current = rowNotices.get(row);
    if (current?.type === type && current.param === param) rowNotices.delete(row);
  }

  // The notice a row shows, if it was raised for the row's current filter type
  function rowNotice(row: NoticeRow, type: string): RowNotice | undefined {
    const notice = rowNotices.get(row);
    return notice?.type === type ? notice : undefined;
  }

  function noticeId(row: NoticeRow): string {
    return `${editorId}-row-${row}-notice`;
  }

  function noticeText(notice: EqFieldNotice): string {
    switch (notice.kind) {
      case 'min':
        return t('components.forms.numberField.adjustedToMinimum', { value: notice.value });
      case 'max':
        return t('components.forms.numberField.adjustedToMaximum', { value: notice.value });
      case 'empty':
        // The field above shows the kept value, so the notice does not repeat it
        return t('settings.audio.audioFilters.emptyEntryRestored');
    }
  }

  // Longest notice a row's numeric fields can show, used to reserve the row's notice band.
  // Clamp notices name a bound and the kept notice names no value, so these are all the
  // notices the row can show.
  function reserveText(params: FilterParameter[]): string {
    let longest = '';
    for (const param of params) {
      if (!numericParameter(param)) continue;
      const candidates = [
        noticeText({ kind: 'min', value: param.Min }),
        noticeText({ kind: 'max', value: param.Max }),
        noticeText({ kind: 'empty', value: param.Default }),
      ];
      for (const text of candidates) {
        if (text.length > longest.length) longest = text;
      }
    }
    return longest;
  }

  // Column of a parameter within the notice band: the band spans the parameter columns,
  // so the first parameter is column 1. Literal class names so Tailwind generates them.
  function bandColumnClass(params: FilterParameter[], param: string | undefined): string {
    switch (params.findIndex(p => p.Name === param)) {
      case 1:
        return 'md:col-start-2';
      case 2:
        return 'md:col-start-3';
      case 3:
        return 'md:col-start-4';
      default:
        return 'md:col-start-1';
    }
  }

  // The rightmost numeric parameter: the narrowest start a notice can have, where the
  // reserved text is measured so that a notice of any field fits in the same height
  function lastNumericParameter(params: FilterParameter[]): string | undefined {
    let last: string | undefined;
    for (const param of params) {
      if (numericParameter(param)) last = param.Name;
    }
    return last;
  }

  // Handle a click on a label whose control is a button: focus the control without the
  // click the browser would otherwise forward to it
  function focusInsteadOfActivating(event: MouseEvent, controlId: string) {
    event.preventDefault();
    document.getElementById(controlId)?.focus();
  }

  // Id prefix of this editor instance, for the <label for> of each attenuation dropdown
  // (the editor mounts several times on a page)
  const editorId = generateId('eq-editor');
  function passesId(row: number | 'new'): string {
    return `${editorId}-passes-${row}`;
  }

  // Add a new filter
  function addNewFilter() {
    if (!newFilter.type) return;

    // After the type check above, we know type is not empty - create a proper Filter
    const filterToAdd: Filter = {
      type: newFilter.type as EqualizerFilterType,
      frequency: newFilter.frequency,
      q: newFilter.q,
      width: newFilter.width,
      gain: newFilter.gain,
      passes: newFilter.passes,
    };

    const filters = [...(equalizerSettings.filters || []), filterToAdd];
    onUpdate({ ...equalizerSettings, filters });

    // Reset new filter form
    newFilter = createEmptyFilter();
    rowNotices.delete('new');
  }

  // Remove a filter by index
  function removeFilter(index: number) {
    const filters = equalizerSettings.filters.filter((_, i) => i !== index);
    onUpdate({ ...equalizerSettings, filters });
  }

  // Update a specific parameter of a filter
  function updateFilterParameter(index: number, paramName: string, value: unknown) {
    const filters = [...equalizerSettings.filters];
    const currentFilter = safeArrayAccess(filters, index);
    if (!currentFilter) return;

    const updatedFilter = { ...currentFilter };
    const normalizedParamName = paramName.toLowerCase();

    // Safe property assignment - whitelist allowed parameters
    const allowedParams = ['frequency', 'q', 'width', 'gain', 'passes'];
    if (!allowedParams.includes(normalizedParamName)) return;

    // Get parameter configuration for validation
    const filterParams = getEqFilterParameters(currentFilter.type);
    const paramConfig = filterParams.find(p => p.Name.toLowerCase() === normalizedParamName);

    // Validate and clamp numeric inputs
    let validatedValue = value;

    if (
      normalizedParamName === 'frequency' ||
      normalizedParamName === 'q' ||
      normalizedParamName === 'width' ||
      normalizedParamName === 'gain' ||
      normalizedParamName === 'passes'
    ) {
      const numericValue = Number(value);

      // Return if invalid number
      if (isNaN(numericValue)) return;

      // Passes is a whole number; the dropdown only emits an offered value or the stored one
      if (normalizedParamName === 'passes') {
        validatedValue = Math.round(numericValue);
      } else if (paramConfig) {
        validatedValue = Math.max(paramConfig.Min, Math.min(paramConfig.Max, numericValue));
      } else {
        validatedValue = numericValue;
      }
    }

    // Apply validated value with explicit property assignment
    switch (normalizedParamName) {
      case 'frequency':
        updatedFilter.frequency = validatedValue as number;
        break;
      case 'q':
        updatedFilter.q = validatedValue as number;
        break;
      case 'width':
        updatedFilter.width = validatedValue as number;
        break;
      case 'gain':
        updatedFilter.gain = validatedValue as number;
        break;
      case 'passes':
        updatedFilter.passes = validatedValue as number;
        break;
    }

    filters.splice(index, 1, updatedFilter);
    onUpdate({ ...equalizerSettings, filters });
  }

  // Set default values when filter type is selected
  function getFilterDefaults(filterType: EqualizerFilterType | '') {
    if (!filterType) {
      newFilter = createEmptyFilter();
      return;
    }

    const parameters = getEqFilterParameters(filterType);
    const updatedFilter: NewFilterForm = { ...createEmptyFilter(), type: filterType };

    // Apply parameter defaults with explicit property assignments
    parameters.forEach(param => {
      const paramName = param.Name.toLowerCase();
      switch (paramName) {
        case 'frequency':
          updatedFilter.frequency = param.Default;
          break;
        case 'q':
          updatedFilter.q = param.Default;
          break;
        case 'width':
          updatedFilter.width = param.Default;
          break;
        case 'gain':
          updatedFilter.gain = param.Default;
          break;
        case 'passes':
          updatedFilter.passes = param.Default;
          break;
      }
    });

    newFilter = updatedFilter;
  }

  // Handle equalizer enabled/disabled
  function handleEqualizerToggle(enabled: boolean) {
    onUpdate({ ...equalizerSettings, enabled });
  }
</script>

<div class="space-y-4">
  <Checkbox
    checked={equalizerSettings.enabled}
    label={t('settings.audio.audioFilters.enableEqualizer')}
    helpText={t('settings.audio.audioFilters.enableEqualizerHelp')}
    {disabled}
    onchange={handleEqualizerToggle}
  />

  {#if equalizerSettings.enabled && !loadingConfig}
    <!-- Filter Response Visualization - Always visible to show current state -->
    <div class="mb-6">
      <h3 class="text-sm font-medium mb-2">
        {t('settings.audio.audioFilters.frequencyResponse')}
      </h3>
      <FilterResponseGraph
        equalizer={{
          enabled: equalizerSettings.enabled,
          filters: equalizerSettings.filters ?? [],
        }}
        {sampleRate}
      />
    </div>

    <!-- One parameter cell of a filter row: a number input, or the attenuation dropdown -->
    {#snippet parameterCell(
      filterType: string,
      param: FilterParameter,
      variant: EqRowVariant,
      row: number | 'new',
      filter: Filter | NewFilterForm,
      commitNumeric: (_name: EqNumericParameter, _value: number) => void,
      commitPasses: (_passes: number) => void
    )}
      {@const numeric = numericParameter(param)}
      {@const unitSuffix = param.Unit ? ` (${param.Unit})` : ''}
      {#if param.Name.toLowerCase() === 'passes'}
        <div class={EQ_PARAMETER_CELL_CLASS}>
          <!-- A label click would also click the dropdown trigger and open the list; it only
               moves focus there, as a label click does for the number inputs. A label is never
               focused, so keyboard users reach the dropdown itself and need no key handler. -->
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
          <label
            class={eqLabelClasses(variant).label}
            for={passesId(row)}
            onclick={event => focusInsteadOfActivating(event, passesId(row))}
          >
            <span class={eqLabelClasses(variant).text}>
              {parameterLabel(filterType, param)}{unitSuffix}
            </span>
          </label>
          <SelectDropdown
            id={passesId(row)}
            value={String(filter.passes ?? EQ_PASSES_OFF)}
            options={attenuationOptionsFor(filter.passes ?? EQ_PASSES_OFF)}
            onChange={value => {
              const passes = Number(value);
              if (Number.isInteger(passes)) commitPasses(passes);
            }}
            {disabled}
            groupBy={false}
            menuSize="sm"
          />
        </div>
      {:else if numeric}
        <EqualizerNumberInput
          label={parameterLabel(filterType, param)}
          unit={param.Unit}
          value={parameterValue(filter, numeric) ?? param.Default}
          min={param.Min}
          max={param.Max}
          step={eqInputStep(numeric)}
          {disabled}
          {variant}
          noticeId={rowNotice(row, filterType)?.param === param.Name ? noticeId(row) : undefined}
          onCommit={value => commitNumeric(numeric, value)}
          onNotice={notice => setFieldNotice(row, filterType, param.Name, notice)}
        />
      {/if}
    {/snippet}

    <!-- The notice band of a row: the third track of the row grid, spanning the parameter
         columns as a column subgrid. An invisible copy of the longest notice the row can
         show, placed at the narrowest column a notice can start in, sets its height for
         good, so a notice appearing or going away never moves a control. The visible
         notice starts under the field it is about and runs to the end of the row. Below
         md the band is a block after the row's fields. -->
    {#snippet noticeBand(row: NoticeRow, filterType: string, params: FilterParameter[])}
      {@const notice = rowNotice(row, filterType)}
      <div class="grid md:col-start-2 md:col-span-4 md:row-start-3 md:grid-cols-subgrid">
        <p
          data-eq-notice-reserve
          aria-hidden="true"
          class="help-text invisible pt-1 col-start-1 row-start-1 md:col-end-5 {bandColumnClass(
            params,
            lastNumericParameter(params)
          )}"
        >
          {reserveText(params)}
        </p>
        <p
          id={noticeId(row)}
          role="status"
          class="help-text pt-1 col-start-1 row-start-1 md:col-end-5 {bandColumnClass(
            params,
            notice?.param
          )}"
        >
          {notice ? noticeText(notice) : ''}
        </p>
      </div>
    {/snippet}

    <div class="space-y-4">
      <!-- Existing filters -->
      {#each equalizerSettings.filters || [] as filter, index (index)}
        {@const filterParams = getEqFilterParameters(filter.type)}
        <div
          class="grid grid-cols-1 md:grid-cols-5 gap-4 md:gap-y-0 items-end p-4 bg-[var(--color-base-200)] rounded-lg border border-[var(--color-base-300)]"
        >
          <!-- Filter Type Display -->
          <div class="flex items-end md:row-span-2 md:self-end">
            <button
              type="button"
              class="btn btn-sm w-full pointer-events-none bg-[var(--color-base-300)] border-[var(--color-base-300)] gap-2"
            >
              {#if filter.type === 'LowPass'}
                <LowPassIcon class="size-4" />
              {:else if filter.type === 'HighPass'}
                <HighPassIcon class="size-4" />
              {:else if filter.type === 'BandReject'}
                <BandRejectIcon class="size-4" />
              {/if}
              <span class="font-medium">{filter.type}</span>
            </button>
          </div>

          <!-- Dynamic parameters based on filter type, remounted when the filter count or
               this row's type changes (see filterCount) -->
          {#each filterParams as param (`${filterCount}-${filter.type}-${param.Name}`)}
            {@render parameterCell(
              filter.type,
              param,
              'edit',
              index,
              filter,
              (name, value) => updateFilterParameter(index, name, value),
              passes => updateFilterParameter(index, param.Name, passes)
            )}
          {/each}

          {@render noticeBand(index, filter.type, filterParams)}

          <!-- Remove button -->
          <div class="flex items-end md:justify-end md:col-start-5 md:row-span-2 md:self-end">
            <button
              type="button"
              class="btn btn-error btn-sm w-full md:w-24"
              onclick={() => removeFilter(index)}
              {disabled}
            >
              {t('settings.audio.audioFilters.remove')}
            </button>
          </div>
        </div>
      {/each}

      <!-- Add new filter section -->
      <div class="grid grid-cols-1 md:grid-cols-5 gap-4 md:gap-y-0 items-end mt-6">
        <!-- New Filter Type -->
        <div class="flex flex-col md:row-span-2 md:self-end">
          <SelectDropdown
            value={newFilter.type}
            label={t('settings.audio.audioFilters.newFilterType')}
            options={filterTypeOptions}
            onChange={value => {
              newFilter.type = value as EqualizerFilterType | '';
              getFilterDefaults(newFilter.type);
              rowNotices.delete('new');
            }}
            {disabled}
            groupBy={false}
            menuSize="sm"
          />
        </div>

        <!-- New Audio Filter Parameters, keyed by type so a type switch starts every
             field afresh -->
        {#if newFilter.type}
          {#each getEqFilterParameters(newFilter.type) as param (`${newFilter.type}-${param.Name}`)}
            {@render parameterCell(
              newFilter.type,
              param,
              'new',
              'new',
              newFilter,
              setNewFilterParameter,
              passes => (newFilter = { ...newFilter, passes })
            )}
          {/each}

          {@render noticeBand('new', newFilter.type, getEqFilterParameters(newFilter.type))}
        {/if}

        <!-- Add new filter button -->
        <div class="flex flex-col md:row-span-2 md:self-end">
          <div class="label">
            <span class="label-text">&nbsp;</span>
          </div>
          <button
            type="button"
            onclick={addNewFilter}
            class="btn btn-primary btn-sm w-24"
            disabled={!newFilter.type || disabled}
          >
            {t('settings.audio.audioFilters.addFilter')}
          </button>
        </div>
      </div>
    </div>
  {:else if equalizerSettings.enabled && loadingConfig}
    <div class="flex justify-center p-4">
      <span class="loading loading-spinner loading-sm"></span>
    </div>
  {/if}
</div>
