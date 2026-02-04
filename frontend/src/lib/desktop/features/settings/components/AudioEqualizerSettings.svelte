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
  
  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import LowPassIcon from '$lib/desktop/components/ui/LowPassIcon.svelte';
  import HighPassIcon from '$lib/desktop/components/ui/HighPassIcon.svelte';
  import BandRejectIcon from '$lib/desktop/components/ui/BandRejectIcon.svelte';
  import FilterResponseGraph from './FilterResponseGraph.svelte';
  import { safeGet, safeArrayAccess } from '$lib/utils/security';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { getCsrfToken } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import type { EqualizerFilterType } from '$lib/stores/settings';

  const logger = loggers.settings;

  // Attenuation options for filter passes
  const attenuationOptions = [
    { value: '0', label: '0dB' },
    { value: '1', label: '12dB' },
    { value: '2', label: '24dB' },
    { value: '3', label: '36dB' },
    { value: '4', label: '48dB' },
  ];

  // Fallback configuration used when API fails or returns invalid data
  const FALLBACK_EQ_FILTER_CONFIG = {
    LowPass: {
      Parameters: [
        {
          Name: 'Frequency',
          Label: 'Cutoff Frequency',
          Type: 'number',
          Unit: 'Hz',
          Min: 20,
          Max: 20000,
          Default: 15000,
        },
        { Name: 'Q', Label: 'Q Factor', Type: 'number', Min: 0.1, Max: 10, Default: 0.707 },
        { Name: 'Passes', Label: 'Attenuation', Type: 'number', Min: 1, Max: 4, Default: 1 },
      ],
    },
    HighPass: {
      Parameters: [
        {
          Name: 'Frequency',
          Label: 'Cutoff Frequency',
          Type: 'number',
          Unit: 'Hz',
          Min: 20,
          Max: 20000,
          Default: 100,
        },
        { Name: 'Q', Label: 'Q Factor', Type: 'number', Min: 0.1, Max: 10, Default: 0.707 },
        { Name: 'Passes', Label: 'Attenuation', Type: 'number', Min: 1, Max: 4, Default: 1 },
      ],
    },
    BandReject: {
      Parameters: [
        {
          Name: 'Frequency',
          Label: 'Center Frequency',
          Type: 'number',
          Unit: 'Hz',
          Min: 20,
          Max: 20000,
          Default: 1000,
        },
        {
          Name: 'Width',
          Label: 'Bandwidth',
          Type: 'number',
          Unit: 'Hz',
          Min: 1,
          Max: 10000,
          Default: 100,
        },
        { Name: 'Passes', Label: 'Attenuation', Type: 'number', Min: 1, Max: 4, Default: 1 },
      ],
    },
  };

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
  }

  let { equalizerSettings, disabled = false, onUpdate }: Props = $props();

  // Load filter config from backend
  let eqFilterConfig = $state<Record<string, FilterTypeConfig>>({});
  let loadingConfig = $state(true);

  // New filter state for adding filters (uses NewFilterForm to allow empty type)
  let newFilter = $state<NewFilterForm>({
    type: '',
    frequency: 0,
    q: 0.707,
    width: 100,
    gain: 0,
    passes: 1, // Default to 12dB attenuation
  });

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

    // Ensure HP/LP filters use Butterworth Q factor
    if (filterToAdd.type === 'HighPass' || filterToAdd.type === 'LowPass') {
      filterToAdd.q = 0.707;
    }

    const filters = [...(equalizerSettings.filters || []), filterToAdd];
    onUpdate({ ...equalizerSettings, filters });

    // Reset new filter form
    newFilter = { type: '', frequency: 0, q: 0.707, width: 100, gain: 0, passes: 1 };
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
        validatedValue = Math.max(0, Math.min(4, intValue));
      } else if (paramConfig) {
        // Use parameter bounds if available, otherwise use sensible defaults
        const min = paramConfig.Min ?? 0;
        const max = paramConfig.Max ?? (normalizedParamName === 'frequency' ? 20000 : 10);
        validatedValue = Math.max(min, Math.min(max, numericValue));
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
      newFilter = { type: '', frequency: 0, q: 0.707, width: 100, gain: 0, passes: 1 };
      return;
    }

    const parameters = getEqFilterParameters(filterType);
    const updatedFilter: NewFilterForm = {
      type: filterType,
      frequency: 0,
      q: 0.707,
      width: 100,
      gain: 0,
      passes: 1, // Default to 12dB attenuation
    };

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

    // Force Q to 0.707 (Butterworth) for HP/LP filters
    if (filterType === 'HighPass' || filterType === 'LowPass') {
      updatedFilter.q = 0.707;
    }

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
      <FilterResponseGraph filters={equalizerSettings.filters || []} />
    </div>

    <div class="space-y-4">
      <!-- Existing filters -->
      {#each equalizerSettings.filters || [] as filter, index (index)}
        {@const filterParams = getEqFilterParameters(filter.type)}
        <div
          class="grid grid-cols-1 md:grid-cols-5 gap-4 items-end p-4 bg-base-200 rounded-lg border border-base-300"
        >
          <!-- Filter Type Display -->
          <div class="flex items-end">
            <button
              type="button"
              class="btn btn-sm w-full pointer-events-none bg-base-300 border-base-300 gap-2"
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

          <!-- Dynamic parameters based on filter type -->
          {#each filterParams as param (param.Name)}
            <!-- Skip Q factor for HP/LP filters - always use Butterworth (Q=0.707) -->
            {#if !(param.Name === 'Q' && (filter.type === 'HighPass' || filter.type === 'LowPass'))}
              <div class="flex flex-col">
                <div class="label pt-0">
                  <span class="label-text-alt">
                    {param.Label}{param.Unit ? ` (${param.Unit})` : ''}
                  </span>
                </div>
                {#if param.Name.toLowerCase() === 'passes'}
                  <!-- Select for Passes/Attenuation -->
                  <SelectDropdown
                    value={String(filter.passes ?? param.Default ?? 1)}
                    options={attenuationOptions}
                    onChange={value =>
                      updateFilterParameter(index, param.Name, parseInt(value as string))}
                    {disabled}
                    groupBy={false}
                    menuSize="sm"
                  />
                {:else if param.Name.toLowerCase() === 'frequency'}
                  <!-- Frequency input -->
                  <input
                    value={filter.frequency ?? param.Default}
                    oninput={e =>
                      updateFilterParameter(index, param.Name, parseFloat(e.currentTarget.value))}
                    type="number"
                    min={param.Min}
                    max={param.Max}
                    step="1"
                    class="input input-sm w-full"
                    {disabled}
                  />
                {:else if param.Name.toLowerCase() === 'q'}
                  <!-- Q factor input -->
                  <input
                    value={filter.q ?? param.Default}
                    oninput={e =>
                      updateFilterParameter(index, param.Name, parseFloat(e.currentTarget.value))}
                    type="number"
                    min={param.Min}
                    max={param.Max}
                    step="0.1"
                    class="input input-sm w-full"
                    {disabled}
                  />
                {:else if param.Name.toLowerCase() === 'width'}
                  <!-- Width (Bandwidth) input -->
                  <input
                    value={filter.width ?? param.Default}
                    oninput={e =>
                      updateFilterParameter(index, param.Name, parseFloat(e.currentTarget.value))}
                    type="number"
                    min={param.Min}
                    max={param.Max}
                    step="1"
                    class="input input-sm w-full"
                    {disabled}
                  />
                {:else if param.Name.toLowerCase() === 'gain'}
                  <!-- Gain input -->
                  <input
                    value={filter.gain ?? param.Default}
                    oninput={e =>
                      updateFilterParameter(index, param.Name, parseFloat(e.currentTarget.value))}
                    type="number"
                    min={param.Min}
                    max={param.Max}
                    step="0.1"
                    class="input input-sm w-full"
                    {disabled}
                  />
                {/if}
              </div>
            {/if}
          {/each}

          <!-- Remove button -->
          <div class="flex items-end md:justify-end md:col-start-5">
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
      <div class="grid grid-cols-1 md:grid-cols-5 gap-4 items-end mt-6">
        <!-- New Filter Type -->
        <div class="flex flex-col">
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

        <!-- New Audio Filter Parameters -->
        {#if newFilter.type}
          {#each getEqFilterParameters(newFilter.type) as param (param.Name)}
            <!-- Skip Q factor for HP/LP filters - always use Butterworth (Q=0.707) -->
            {#if !(param.Name === 'Q' && (newFilter.type === 'HighPass' || newFilter.type === 'LowPass'))}
              <div class="flex flex-col">
                <div class="label">
                  <span class="label-text">
                    {param.Label}{param.Unit ? ` (${param.Unit})` : ''}
                  </span>
                </div>
                {#if param.Name.toLowerCase() === 'passes'}
                  <!-- Select for Passes/Attenuation -->
                  <SelectDropdown
                    value={String(newFilter.passes ?? 1)}
                    options={attenuationOptions}
                    onChange={value => {
                      newFilter = { ...newFilter, passes: parseInt(value as string, 10) };
                    }}
                    {disabled}
                    groupBy={false}
                    menuSize="sm"
                  />
                {:else if param.Name.toLowerCase() === 'frequency'}
                  <!-- Frequency input -->
                  <input
                    value={newFilter.frequency}
                    oninput={e => {
                      const value = parseFloat(e.currentTarget.value) || 0;
                      newFilter = { ...newFilter, frequency: value };
                    }}
                    type="number"
                    step="1"
                    min={param.Min}
                    max={param.Max}
                    class="input input-sm w-full"
                    {disabled}
                  />
                {:else if param.Name.toLowerCase() === 'q'}
                  <!-- Q factor input -->
                  <input
                    value={newFilter.q ?? 0.707}
                    oninput={e => {
                      const value = parseFloat(e.currentTarget.value) || 0.707;
                      newFilter = { ...newFilter, q: value };
                    }}
                    type="number"
                    step="0.1"
                    min={param.Min}
                    max={param.Max}
                    class="input input-sm w-full"
                    {disabled}
                  />
                {:else if param.Name.toLowerCase() === 'width'}
                  <!-- Width (Bandwidth) input -->
                  <input
                    value={newFilter.width ?? 100}
                    oninput={e => {
                      const value = parseFloat(e.currentTarget.value) || 100;
                      newFilter = { ...newFilter, width: value };
                    }}
                    type="number"
                    step="1"
                    min={param.Min}
                    max={param.Max}
                    class="input input-sm w-full"
                    {disabled}
                  />
                {:else if param.Name.toLowerCase() === 'gain'}
                  <!-- Gain input -->
                  <input
                    value={newFilter.gain ?? 0}
                    oninput={e => {
                      const value = parseFloat(e.currentTarget.value) || 0;
                      newFilter = { ...newFilter, gain: value };
                    }}
                    type="number"
                    step="0.1"
                    min={param.Min}
                    max={param.Max}
                    class="input input-sm w-full"
                    {disabled}
                  />
                {/if}
              </div>
            {/if}
          {/each}
        {/if}

        <!-- Add new filter button -->
        <div class="flex flex-col">
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
