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
  import FilterResponseGraph from './FilterResponseGraph.svelte';
  import { safeGet, safeArrayAccess } from '$lib/utils/security';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.settings;

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
    type: string;
    frequency: number;
    q?: number;
    gain?: number;
    passes?: number;
    [key: string]: any;
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

  // New filter state for adding filters
  let newFilter = $state<Filter>({
    type: '',
    frequency: 0,
    q: 0.707,
    gain: 0,
    passes: 1, // Default to 12dB attenuation
  });

  // Load filter configuration from backend on mount
  $effect(() => {
    loadFilterConfig();
  });

  async function loadFilterConfig() {
    loadingConfig = true;

    try {
      const csrfToken =
        (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute(
          'content'
        ) || '';

      const response = await fetch('/api/v2/system/audio/equalizer/config', {
        headers: { 'X-CSRF-Token': csrfToken },
      });

      if (!response.ok) {
        // If API doesn't exist, use fallback config
        eqFilterConfig = {
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
        };
      } else {
        const data = await response.json();
        eqFilterConfig = data || {};
      }
    } catch (error) {
      logger.error('Error loading filter config:', error);
      // Use fallback config
      eqFilterConfig = {
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
      };
    } finally {
      loadingConfig = false;
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

    const filterToAdd = { ...newFilter };
    // Remove empty id if it exists
    if (!filterToAdd.id) delete filterToAdd.id;

    // Ensure HP/LP filters use Butterworth Q factor
    if (filterToAdd.type === 'HighPass' || filterToAdd.type === 'LowPass') {
      filterToAdd.q = 0.707;
    }

    const filters = [...(equalizerSettings.filters || []), filterToAdd];
    onUpdate({ ...equalizerSettings, filters });

    // Reset new filter form
    newFilter = { type: '', frequency: 0, q: 0.707, gain: 0, passes: 1 };
  }

  // Remove a filter by index
  function removeFilter(index: number) {
    const filters = equalizerSettings.filters.filter((_, i) => i !== index);
    onUpdate({ ...equalizerSettings, filters });
  }

  // Update a specific parameter of a filter
  function updateFilterParameter(index: number, paramName: string, value: any) {
    const filters = [...equalizerSettings.filters];
    const currentFilter = safeArrayAccess(filters, index);
    if (!currentFilter) return;

    const updatedFilter = { ...currentFilter };
    const normalizedParamName = paramName.toLowerCase();

    // Safe property assignment - whitelist allowed parameters
    const allowedParams = ['frequency', 'q', 'gain', 'passes'];
    if (allowedParams.includes(normalizedParamName)) {
      // eslint-disable-next-line security/detect-object-injection -- safe with whitelist
      updatedFilter[normalizedParamName] = value;
    }

    filters.splice(index, 1, updatedFilter);
    onUpdate({ ...equalizerSettings, filters });
  }

  // Set default values when filter type is selected
  function getFilterDefaults(filterType: string) {
    if (!filterType) {
      newFilter = { type: '', frequency: 0, q: 0.707, gain: 0, passes: 1 };
      return;
    }

    const parameters = getEqFilterParameters(filterType);
    const updatedFilter: Filter = {
      type: filterType,
      frequency: 0,
      q: 0.707,
      gain: 0,
      passes: 1, // Default to 12dB attenuation
    };

    parameters.forEach(param => {
      const paramName = param.Name.toLowerCase();
      // Safe property assignment - whitelist allowed parameters
      const allowedParams = ['frequency', 'q', 'gain', 'passes'];
      if (allowedParams.includes(paramName)) {
        // eslint-disable-next-line security/detect-object-injection -- safe with whitelist
        updatedFilter[paramName] = param.Default;
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
      {#each equalizerSettings.filters || [] as filter, index}
        {@const filterParams = getEqFilterParameters(filter.type)}
        <div
          class="grid grid-cols-1 md:grid-cols-5 gap-4 items-end p-4 bg-base-200 rounded-lg border border-base-300"
        >
          <!-- Filter Type Display -->
          <div class="flex items-end">
            <button
              type="button"
              class="btn btn-sm w-full pointer-events-none bg-base-300 border-base-300"
            >
              <span class="font-medium">{filter.type} Filter</span>
            </button>
          </div>

          <!-- Dynamic parameters based on filter type -->
          {#each filterParams as param}
            <!-- Skip Q factor for HP/LP filters - always use Butterworth (Q=0.707) -->
            {#if !(param.Name === 'Q' && (filter.type === 'HighPass' || filter.type === 'LowPass'))}
              <div class="flex flex-col">
                <div class="label pt-0">
                  <span class="label-text-alt">
                    {param.Label}{param.Unit ? ` (${param.Unit})` : ''}
                  </span>
                </div>
                {#if param.Label === 'Attenuation'}
                  <!-- Select for Passes/Attenuation -->
                  <select
                    value={String(
                      filter[param.Name.toLowerCase()] ?? filter[param.Name] ?? param.Default ?? 1
                    )}
                    onchange={e =>
                      updateFilterParameter(index, param.Name, parseInt(e.currentTarget.value))}
                    class="select select-bordered select-sm w-full"
                    {disabled}
                  >
                    <option value="0">0dB</option>
                    <option value="1">12dB</option>
                    <option value="2">24dB</option>
                    <option value="3">36dB</option>
                    <option value="4">48dB</option>
                  </select>
                {:else}
                  <!-- Input for other parameters -->
                  <input
                    value={filter[param.Name.toLowerCase()] ?? filter[param.Name] ?? param.Default}
                    oninput={e =>
                      updateFilterParameter(index, param.Name, parseFloat(e.currentTarget.value))}
                    type="number"
                    min={param.Min}
                    max={param.Max}
                    step={param.Type === 'float' || param.Name === 'Q' ? 0.1 : 1}
                    class="input input-bordered input-sm w-full"
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
          <label class="label" for="new-filter-type">
            <span class="label-text">{t('settings.audio.audioFilters.newFilterType')}</span>
          </label>
          <select
            id="new-filter-type"
            bind:value={newFilter.type}
            onchange={() => getFilterDefaults(newFilter.type)}
            class="select select-bordered select-sm w-full"
            {disabled}
          >
            <option value="">{t('settings.audio.audioFilters.selectFilterType')}</option>
            {#each Object.keys(eqFilterConfig) as filterType}
              <option value={filterType}>{filterType}</option>
            {/each}
          </select>
        </div>

        <!-- New Audio Filter Parameters -->
        {#if newFilter.type}
          {#each getEqFilterParameters(newFilter.type) as param}
            <!-- Skip Q factor for HP/LP filters - always use Butterworth (Q=0.707) -->
            {#if !(param.Name === 'Q' && (newFilter.type === 'HighPass' || newFilter.type === 'LowPass'))}
              <div class="flex flex-col">
                <div class="label">
                  <span class="label-text">
                    {param.Label}{param.Unit ? ` (${param.Unit})` : ''}
                  </span>
                </div>
                {#if param.Label === 'Attenuation'}
                  <!-- Select for Passes/Attenuation -->
                  <select
                    value={String(newFilter[param.Name.toLowerCase()] ?? 0)}
                    onchange={e => {
                      newFilter[param.Name.toLowerCase()] = parseInt(e.currentTarget.value);
                    }}
                    class="select select-bordered select-sm w-full"
                    {disabled}
                  >
                    <option value="0">0dB</option>
                    <option value="1">12dB</option>
                    <option value="2">24dB</option>
                    <option value="3">36dB</option>
                    <option value="4">48dB</option>
                  </select>
                {:else}
                  <!-- Input for other parameters -->
                  <input
                    bind:value={newFilter[param.Name.toLowerCase()]}
                    type="number"
                    step={param.Type === 'float' || param.Name === 'Q' ? 0.1 : 1}
                    min={param.Min}
                    max={param.Max}
                    class="input input-bordered input-sm w-full"
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
