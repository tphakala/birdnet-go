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
    EQ_OFFERED_MAX_PASSES,
    EQ_PASSES_OFF,
    eqInputStep,
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
    id?: string;
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
  // after a row is removed a cell would otherwise show the next filter and keep that
  // field's notice. Any change in the count remounts every cell, which also drops the
  // notices of rows that did not move.
  const filterCount = $derived((equalizerSettings.filters || []).length);

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

      // For passes, ensure integer and clamp to bounds
      if (normalizedParamName === 'passes') {
        const intValue = Math.round(numericValue);
        validatedValue = Math.max(EQ_PASSES_OFF, Math.min(EQ_OFFERED_MAX_PASSES, intValue));
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
          <label class={eqLabelClasses(variant).label} for={passesId(row)}>
            <span class={eqLabelClasses(variant).text}>
              {parameterLabel(filterType, param)}{unitSuffix}
            </span>
          </label>
          <SelectDropdown
            id={passesId(row)}
            value={String(filter.passes ?? EQ_PASSES_OFF)}
            options={attenuationOptions}
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
          onCommit={value => commitNumeric(numeric, value)}
        />
      {/if}
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
