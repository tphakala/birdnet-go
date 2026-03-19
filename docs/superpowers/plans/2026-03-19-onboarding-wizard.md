# Onboarding Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a six-step first-time setup wizard that guides new BirdNET-Go users through location, language, audio, detection, privacy, and responsible use configuration.

**Architecture:** Six async-loaded Svelte 5 step components registered in the existing wizard registry. Each step saves settings independently via the settings store + API on unmount. A new `LocationPickerMap` component provides interactive map-based coordinate selection.

**Tech Stack:** Svelte 5 (runes), TypeScript, Tailwind v4.1, MapLibre GL JS, Vitest

**Spec:** `docs/superpowers/specs/2026-03-19-onboarding-wizard-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `frontend/src/lib/desktop/features/wizard/wizardRegistry.ts` | Modify | Populate `onboardingSteps` array |
| `frontend/src/lib/desktop/features/wizard/steps/WelcomeStep.svelte` | Create | Welcome screen with logo and credits |
| `frontend/src/lib/desktop/features/wizard/steps/LocationLanguageStep.svelte` | Create | Location picker, UI locale, species locale |
| `frontend/src/lib/desktop/features/wizard/components/LocationPickerMap.svelte` | Create | Interactive MapLibre click-to-set map |
| `frontend/src/lib/desktop/features/wizard/steps/AudioSourceStep.svelte` | Create | Soundcard/RTSP source selection |
| `frontend/src/lib/desktop/features/wizard/steps/DetectionStep.svelte` | Create | Threshold preset cards |
| `frontend/src/lib/desktop/features/wizard/steps/IntegrationStep.svelte` | Create | Privacy filter, BirdWeather, Sentry |
| `frontend/src/lib/desktop/features/wizard/steps/ResponsibleUseStep.svelte` | Create | AI guidelines + "I understand" gate |
| `frontend/static/messages/en.json` | Modify | English i18n keys for all steps |
| `frontend/static/messages/{da,de,es,fi,fr,it,lv,nl,pl,pt,sk,sv}.json` | Modify | Copy English keys as placeholders |
| `frontend/src/lib/desktop/features/wizard/wizardRegistry.test.ts` | Modify | Test populated onboarding steps |

---

### Task 1: i18n Keys

Add all wizard step translation keys to all 13 locale files. Do this first so all subsequent components can reference keys immediately.

**Files:**
- Modify: `frontend/static/messages/en.json`
- Modify: `frontend/static/messages/{da,de,es,fi,fr,it,lv,nl,pl,pt,sk,sv}.json`

- [ ] **Step 1: Add English keys to `en.json`**

Add these keys inside the existing `"wizard"` object (which already has `skip`, `back`, `next`, `done`, `progress`, `progressLabel`):

```json
"steps": {
  "welcome": {
    "title": "Welcome",
    "heading": "Welcome to BirdNET-Go!",
    "description": "BirdNET-Go is a real-time bird sound identification system. It listens to audio from your microphone or audio stream and uses the BirdNET AI model to identify bird species.",
    "credit": "Powered by the BirdNET Analyzer developed by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology and Chemnitz University of Technology.",
    "cta": "Let's set up your bird monitoring station."
  },
  "locationLanguage": {
    "title": "Location & Language",
    "uiLanguageLabel": "Interface Language",
    "uiLanguageHelp": "Language used for the web interface",
    "speciesLanguageLabel": "Species Name Language",
    "speciesLanguageHelp": "Language used for bird species names in detections",
    "locationLabel": "Station Location",
    "locationHelp": "Your location helps filter species to those likely in your area",
    "latitudeLabel": "Latitude",
    "longitudeLabel": "Longitude",
    "useMyLocation": "Use my location",
    "locationDetected": "Location detected",
    "locationError": "Could not detect location. Please enter coordinates manually.",
    "locationDenied": "Location access denied. Please enter coordinates manually."
  },
  "audioSource": {
    "title": "Audio Source",
    "sourceTypeLabel": "Source Type",
    "soundcard": "Microphone / Soundcard",
    "rtspStream": "RTSP Stream",
    "deviceLabel": "Audio Device",
    "deviceLoading": "Loading audio devices...",
    "noDevicesFound": "No audio devices detected. Try configuring an RTSP stream instead.",
    "rtspUrlLabel": "Stream URL",
    "rtspUrlPlaceholder": "rtsp://user:password@host:port/stream",
    "rtspUrlHelp": "Enter the URL of your RTSP audio stream",
    "additionalSourcesHint": "You can add more audio sources later in Settings."
  },
  "detection": {
    "title": "Detection Threshold",
    "description": "Choose how strict the bird identification should be. You can adjust this later in Settings.",
    "balanced": "Balanced",
    "balancedDesc": "Good balance of accuracy and sensitivity",
    "balancedRecommended": "Recommended",
    "highAccuracy": "High Accuracy",
    "highAccuracyDesc": "Fewer false positives, may miss some birds",
    "highSensitivity": "High Sensitivity",
    "highSensitivityDesc": "More detections, more false positives",
    "threshold": "Confidence threshold",
    "fpFilterNote": "Once your system is running and proven, you can enable the false positive filter in Settings to further reduce incorrect detections."
  },
  "integration": {
    "title": "Privacy & Integration",
    "privacyFilterLabel": "Enable privacy filter",
    "privacyFilterHelp": "Filters out detections when human voices are detected near the microphone",
    "birdweatherLabel": "Share detections with BirdWeather",
    "birdweatherHelp": "Contribute your bird detections to the BirdWeather community network",
    "birdweatherIdLabel": "BirdWeather Station ID",
    "birdweatherIdPlaceholder": "Enter your BirdWeather station ID",
    "errorReportingLabel": "Send anonymous error reports",
    "errorReportingHelp": "Help improve BirdNET-Go by sending anonymous error reports when something goes wrong"
  },
  "responsibleUse": {
    "title": "Using BirdNET-Go Responsibly",
    "intro": "BirdNET-Go is a powerful tool that enhances your birding experience through AI-assisted identification. To ensure the best outcomes for both birding and conservation:",
    "point1": "Review and verify detections before sharing observations",
    "point2": "Use the confidence scores as a guide, not absolute truth",
    "point3": "Cross-reference with field guides and local expertise",
    "point4": "Consider seasonal patterns and habitat suitability",
    "citizenScienceHeading": "When contributing to citizen science:",
    "citizenPoint1": "Only submit observations you have personally verified",
    "citizenPoint2": "Include notes about visual or behavioral confirmation",
    "citizenPoint3": "Use AI detections as a starting point for your own observations",
    "outro": "This approach ensures data quality for scientific research while maximizing your learning and enjoyment of birds.",
    "acknowledge": "I understand"
  }
}
```

- [ ] **Step 2: Copy English keys to all other locale files**

For each of the 12 non-English locale files (`da.json`, `de.json`, `es.json`, `fi.json`, `fr.json`, `it.json`, `lv.json`, `nl.json`, `pl.json`, `pt.json`, `sk.json`, `sv.json`), add the identical `"steps"` block inside their existing `"wizard"` object. The English text serves as placeholder until community translates.

- [ ] **Step 3: Verify formatting**

Run: `cd frontend && npx prettier --check static/messages/*.json`

Fix any formatting issues: `npx prettier --write static/messages/*.json`

- [ ] **Step 4: Commit**

```bash
git add frontend/static/messages/*.json
git commit -m "feat(wizard): add i18n keys for onboarding wizard steps"
```

---

### Task 2: WelcomeStep Component

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/steps/WelcomeStep.svelte`

- [ ] **Step 1: Create WelcomeStep component**

```svelte
<script lang="ts">
  import { t } from '$lib/i18n';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  // Welcome step is always valid
  $effect(() => {
    onValidChange?.(true);
  });
</script>

<div class="flex flex-col items-center gap-6 py-4 text-center">
  <img
    src="/ui/assets/BirdNET-Go-logo.webp"
    alt={t('about.logoAlt')}
    class="h-24 w-24 rounded-2xl shadow-lg"
  />

  <div class="space-y-3">
    <h2 class="text-2xl font-bold text-[var(--color-base-content)]">
      {t('wizard.steps.welcome.heading')}
    </h2>

    <p class="mx-auto max-w-md text-sm leading-relaxed text-[var(--color-base-content)] opacity-70">
      {t('wizard.steps.welcome.description')}
    </p>
  </div>

  <p class="mx-auto max-w-md text-xs leading-relaxed text-[var(--color-base-content)] opacity-50">
    {t('wizard.steps.welcome.credit')}
  </p>

  <p class="text-sm font-medium text-[var(--color-base-content)]">
    {t('wizard.steps.welcome.cta')}
  </p>
</div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

Expected: No errors related to WelcomeStep.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/steps/WelcomeStep.svelte
git commit -m "feat(wizard): add WelcomeStep component"
```

---

### Task 3: LocationPickerMap Component

Interactive MapLibre map for setting coordinates by clicking.

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/components/LocationPickerMap.svelte`

**Reference files:**
- `frontend/src/lib/desktop/features/settings/utils/mapConfig.ts` — map style/config
- `frontend/src/lib/desktop/features/dashboard/components/BannerLocationMap.svelte` — existing MapLibre usage pattern

- [ ] **Step 1: Create LocationPickerMap component**

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import { createMapStyle, MAP_CONFIG } from '$lib/desktop/features/settings/utils/mapConfig';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('LocationPickerMap');

  interface Props {
    latitude: number;
    longitude: number;
    onLocationChange: (lat: number, lon: number) => void;
  }

  let { latitude, longitude, onLocationChange }: Props = $props();

  let mapContainer = $state<HTMLDivElement>();
  let map = $state<import('maplibre-gl').Map | undefined>();
  let marker = $state<import('maplibre-gl').Marker | undefined>();

  // Use lower zoom than the standard WORLD_VIEW_ZOOM for the wizard picker
  // so users can see the full world map and click their approximate area
  const PICKER_WORLD_ZOOM = 3;

  function getZoom(): number {
    return latitude !== 0 || longitude !== 0 ? MAP_CONFIG.DEFAULT_ZOOM : PICKER_WORLD_ZOOM;
  }

  onMount(() => {
    if (!mapContainer) return;

    let mounted = true;

    import('maplibre-gl').then(maplibregl => {
      if (!mounted || !mapContainer) return;

      const mapInstance = new maplibregl.Map({
        container: mapContainer,
        style: createMapStyle(),
        center: [longitude, latitude],
        zoom: getZoom(),
        minZoom: MAP_CONFIG.MIN_ZOOM,
        maxZoom: MAP_CONFIG.MAX_ZOOM,
        pitchWithRotate: MAP_CONFIG.PITCH_WITH_ROTATE,
        touchZoomRotate: MAP_CONFIG.TOUCH_ZOOM_ROTATE,
        fadeDuration: MAP_CONFIG.FADE_DURATION,
      });

      const markerInstance = new maplibregl.Marker({ draggable: true })
        .setLngLat([longitude, latitude])
        .addTo(mapInstance);

      markerInstance.on('dragend', () => {
        const lngLat = markerInstance.getLngLat();
        onLocationChange(
          Math.round(lngLat.lat * 10000) / 10000,
          Math.round(lngLat.lng * 10000) / 10000
        );
      });

      mapInstance.on('click', (e: import('maplibre-gl').MapMouseEvent) => {
        const { lat, lng } = e.lngLat;
        const roundedLat = Math.round(lat * 10000) / 10000;
        const roundedLng = Math.round(lng * 10000) / 10000;
        markerInstance.setLngLat([roundedLng, roundedLat]);
        onLocationChange(roundedLat, roundedLng);
      });

      map = mapInstance;
      marker = markerInstance;
    }).catch(err => {
      logger.error('Failed to load MapLibre', err);
    });

    return () => {
      mounted = false;
      map?.remove();
    };
  });

  // Sync marker and map center when coordinates change externally (e.g., number fields, geolocation)
  $effect(() => {
    if (!map || !marker) return;
    const currentPos = marker.getLngLat();
    if (
      Math.abs(currentPos.lat - latitude) > 0.0001 ||
      Math.abs(currentPos.lng - longitude) > 0.0001
    ) {
      marker.setLngLat([longitude, latitude]);
      map.flyTo({
        center: [longitude, latitude],
        zoom: getZoom(),
        duration: 500,
      });
    }
  });
</script>

<div
  bind:this={mapContainer}
  class="h-48 w-full overflow-hidden rounded-lg border border-[var(--border-200)]"
></div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/components/LocationPickerMap.svelte
git commit -m "feat(wizard): add LocationPickerMap interactive map component"
```

---

### Task 4: LocationLanguageStep Component

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/steps/LocationLanguageStep.svelte`

**Reference files:**
- `frontend/src/lib/desktop/components/ui/LanguageSelector.svelte` — UI locale selector
- `frontend/src/lib/desktop/features/settings/pages/MainSettingsPage.svelte:522-540` — BirdNET locale loading pattern
- `frontend/src/lib/stores/settings.ts` — `BirdNetSettings` interface (lat, lon, locale)

- [ ] **Step 1: Create LocationLanguageStep component**

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import LanguageSelector from '$lib/desktop/components/ui/LanguageSelector.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import LocationPickerMap from '../components/LocationPickerMap.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { MapPin } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('LocationLanguageStep');

  let { onValidChange }: WizardStepProps = $props();

  // Local state
  let latitude = $state(0);
  let longitude = $state(0);
  let speciesLocale = $state('en');
  let localesLoading = $state(true);
  let localeOptions = $state<Array<{ value: string; label: string }>>([]);
  let geolocating = $state(false);
  let hasGeolocation = $state(false);

  // Always valid — defaults are fine
  $effect(() => {
    onValidChange?.(true);
  });

  onMount(() => {
    // Check geolocation availability (safe for SSR/test)
    hasGeolocation = typeof navigator !== 'undefined' && !!navigator.geolocation;

    // Load current settings
    const store = get(settingsStore);
    if (store?.formData?.birdnet) {
      latitude = store.formData.birdnet.latitude ?? 0;
      longitude = store.formData.birdnet.longitude ?? 0;
      speciesLocale = store.formData.birdnet.locale ?? 'en';
    }

    // Load BirdNET locale options from API
    api
      .get<Record<string, string>>('/api/v2/settings/locales')
      .then(data => {
        localeOptions = Object.entries(data ?? {}).map(([value, label]) => ({
          value,
          label: label as string,
        }));
      })
      .catch(() => {
        localeOptions = [{ value: 'en', label: 'English' }];
      })
      .finally(() => {
        localesLoading = false;
      });
  });

  function handleLocationChange(lat: number, lon: number) {
    latitude = lat;
    longitude = lon;
  }

  function handleGeolocation() {
    if (!hasGeolocation) return;
    geolocating = true;
    navigator.geolocation.getCurrentPosition(
      position => {
        latitude = Math.round(position.coords.latitude * 10000) / 10000;
        longitude = Math.round(position.coords.longitude * 10000) / 10000;
        geolocating = false;
      },
      error => {
        logger.error('Geolocation failed', error);
        geolocating = false;
      },
      { enableHighAccuracy: true, timeout: 10000 }
    );
  }

  // Save on unmount
  $effect(() => {
    return () => {
      settingsActions.updateSection('birdnet', {
        latitude,
        longitude,
        locale: speciesLocale,
      });
      settingsActions.saveSettings().catch(() => {
        // Toast error handled by settings store
      });
    };
  });
</script>

<div class="space-y-5">
  <!-- UI Language -->
  <div>
    <label class="mb-1 block text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.locationLanguage.uiLanguageLabel')}
    </label>
    <p class="mb-2 text-xs text-[var(--color-base-content)] opacity-60">
      {t('wizard.steps.locationLanguage.uiLanguageHelp')}
    </p>
    <LanguageSelector />
  </div>

  <!-- Species Language -->
  <div>
    <label class="mb-1 block text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.locationLanguage.speciesLanguageLabel')}
    </label>
    <p class="mb-2 text-xs text-[var(--color-base-content)] opacity-60">
      {t('wizard.steps.locationLanguage.speciesLanguageHelp')}
    </p>
    <SelectDropdown
      options={localeOptions}
      value={speciesLocale}
      searchable={true}
      disabled={localesLoading}
      onChange={value => {
        if (typeof value === 'string') speciesLocale = value;
      }}
    />
  </div>

  <!-- Location -->
  <div>
    <div class="mb-2 flex items-center justify-between">
      <div>
        <label class="block text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.locationLanguage.locationLabel')}
        </label>
        <p class="text-xs text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.locationLanguage.locationHelp')}
        </p>
      </div>
      {#if hasGeolocation}
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-[var(--radius-field)] border border-[var(--border-200)] bg-transparent px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--hover-overlay)] disabled:opacity-50"
          onclick={handleGeolocation}
          disabled={geolocating}
        >
          <MapPin class="size-3.5" />
          {t('wizard.steps.locationLanguage.useMyLocation')}
        </button>
      {/if}
    </div>

    <div class="mb-3 grid grid-cols-2 gap-3">
      <NumberField
        label={t('wizard.steps.locationLanguage.latitudeLabel')}
        value={latitude}
        min={-90}
        max={90}
        step={0.0001}
        onUpdate={value => { latitude = value; }}
      />
      <NumberField
        label={t('wizard.steps.locationLanguage.longitudeLabel')}
        value={longitude}
        min={-180}
        max={180}
        step={0.0001}
        onUpdate={value => { longitude = value; }}
      />
    </div>

    <LocationPickerMap {latitude} {longitude} onLocationChange={handleLocationChange} />
  </div>
</div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/steps/LocationLanguageStep.svelte
git commit -m "feat(wizard): add LocationLanguageStep with map picker and locale selection"
```

---

### Task 5: AudioSourceStep Component

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/steps/AudioSourceStep.svelte`

**Reference files:**
- `frontend/src/lib/desktop/features/settings/pages/AudioSettingsPage.svelte` — device loading pattern
- `frontend/src/lib/stores/settings.ts` — `AudioSettings.source`, `RTSPSettings.streams[]`, `StreamConfig`

- [ ] **Step 1: Create AudioSourceStep component**

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions } from '$lib/stores/settings';
  import { Mic, Video } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import { getLogger } from '$lib/utils/logger';

  const logger = getLogger('AudioSourceStep');

  let { onValidChange }: WizardStepProps = $props();

  type SourceType = 'soundcard' | 'rtsp';

  let sourceType = $state<SourceType>('soundcard');
  let selectedDevice = $state('');
  let rtspUrl = $state('');
  let devices = $state<Array<{ value: string; label: string }>>([]);
  let devicesLoading = $state(true);

  // Validation
  let isValid = $derived(
    sourceType === 'soundcard' ? selectedDevice !== '' : rtspUrl.trim() !== ''
  );

  $effect(() => {
    onValidChange?.(isValid);
  });

  // Load audio devices on mount (one-time fetch)
  onMount(() => {
    api
      .get<Array<{ name: string; index: number; id: string }>>('/api/v2/system/audio/devices')
      .then(data => {
        devices = (data ?? []).map(d => ({
          value: d.name,
          label: d.name,
        }));
        // Auto-switch to RTSP if no devices found
        if (devices.length === 0) {
          sourceType = 'rtsp';
        }
      })
      .catch(err => {
        logger.error('Failed to load audio devices', err);
        devices = [];
        sourceType = 'rtsp';
      })
      .finally(() => {
        devicesLoading = false;
      });
  });

  // Save on unmount
  $effect(() => {
    return () => {
      if (sourceType === 'soundcard' && selectedDevice) {
        settingsActions.updateSection('realtime', {
          audio: { source: selectedDevice },
        });
      } else if (sourceType === 'rtsp' && rtspUrl.trim()) {
        settingsActions.updateSection('realtime', {
          rtsp: {
            streams: [
              {
                name: 'Stream 1',
                url: rtspUrl.trim(),
                type: 'rtsp',
                transport: 'tcp',
              },
            ],
          },
        });
      }
      settingsActions.saveSettings().catch(() => {});
    };
  });
</script>

<div class="space-y-5">
  <!-- Source type selector -->
  <div>
    <label class="mb-2 block text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.audioSource.sourceTypeLabel')}
    </label>
    <div class="grid grid-cols-2 gap-3">
      <button
        type="button"
        class="flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-colors {sourceType === 'soundcard'
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => { sourceType = 'soundcard'; }}
      >
        <Mic class="size-5 shrink-0 text-[var(--color-base-content)]" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.audioSource.soundcard')}
        </span>
      </button>

      <button
        type="button"
        class="flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-colors {sourceType === 'rtsp'
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => { sourceType = 'rtsp'; }}
      >
        <Video class="size-5 shrink-0 text-[var(--color-base-content)]" />
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.audioSource.rtspStream')}
        </span>
      </button>
    </div>
  </div>

  <!-- Soundcard selector -->
  {#if sourceType === 'soundcard'}
    <div>
      <label class="mb-1 block text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.audioSource.deviceLabel')}
      </label>
      {#if devicesLoading}
        <p class="text-sm text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.audioSource.deviceLoading')}
        </p>
      {:else if devices.length === 0}
        <p class="rounded-lg bg-[var(--color-info)]/10 p-3 text-sm text-[var(--color-info)]">
          {t('wizard.steps.audioSource.noDevicesFound')}
        </p>
      {:else}
        <SelectDropdown
          options={devices}
          value={selectedDevice}
          searchable={true}
          onChange={value => {
            if (typeof value === 'string') selectedDevice = value;
          }}
        />
      {/if}
    </div>
  {/if}

  <!-- RTSP URL input -->
  {#if sourceType === 'rtsp'}
    <div>
      <label class="mb-1 block text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.audioSource.rtspUrlLabel')}
      </label>
      <p class="mb-2 text-xs text-[var(--color-base-content)] opacity-60">
        {t('wizard.steps.audioSource.rtspUrlHelp')}
      </p>
      <TextInput
        bind:value={rtspUrl}
        placeholder={t('wizard.steps.audioSource.rtspUrlPlaceholder')}
      />
    </div>
  {/if}

  <p class="text-xs text-[var(--color-base-content)] opacity-50">
    {t('wizard.steps.audioSource.additionalSourcesHint')}
  </p>
</div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/steps/AudioSourceStep.svelte
git commit -m "feat(wizard): add AudioSourceStep with soundcard/RTSP selection"
```

---

### Task 6: DetectionStep Component

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/steps/DetectionStep.svelte`

- [ ] **Step 1: Create DetectionStep component**

```svelte
<script lang="ts">
  import { t } from '$lib/i18n';
  import { settingsActions } from '$lib/stores/settings';
  import { Scale, Target, Radio } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  interface Preset {
    id: string;
    titleKey: string;
    descKey: string;
    threshold: number;
    icon: typeof Scale;
    recommended?: boolean;
  }

  const presets: Preset[] = [
    {
      id: 'balanced',
      titleKey: 'wizard.steps.detection.balanced',
      descKey: 'wizard.steps.detection.balancedDesc',
      threshold: 0.8,
      icon: Scale,
      recommended: true,
    },
    {
      id: 'high-accuracy',
      titleKey: 'wizard.steps.detection.highAccuracy',
      descKey: 'wizard.steps.detection.highAccuracyDesc',
      threshold: 0.9,
      icon: Target,
    },
    {
      id: 'high-sensitivity',
      titleKey: 'wizard.steps.detection.highSensitivity',
      descKey: 'wizard.steps.detection.highSensitivityDesc',
      threshold: 0.6,
      icon: Radio,
    },
  ];

  let selectedPreset = $state('balanced');

  // Always valid — a preset is always selected
  $effect(() => {
    onValidChange?.(true);
  });

  // Save on unmount
  $effect(() => {
    return () => {
      const preset = presets.find(p => p.id === selectedPreset);
      if (preset) {
        settingsActions.updateSection('birdnet', {
          threshold: preset.threshold,
        });
        settingsActions.saveSettings().catch(() => {});
      }
    };
  });
</script>

<div class="space-y-4">
  <p class="text-sm text-[var(--color-base-content)] opacity-70">
    {t('wizard.steps.detection.description')}
  </p>

  <div class="space-y-3">
    {#each presets as preset (preset.id)}
      {@const PresetIcon = preset.icon}
      <button
        type="button"
        class="flex w-full items-start gap-3 rounded-lg border-2 p-4 text-left transition-colors {selectedPreset === preset.id
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)]/5'
          : 'border-[var(--border-200)] hover:border-[var(--border-300)]'}"
        onclick={() => { selectedPreset = preset.id; }}
      >
        <PresetIcon class="mt-0.5 size-5 shrink-0 text-[var(--color-base-content)]" />
        <div class="flex-1">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-[var(--color-base-content)]">
              {t(preset.titleKey)}
            </span>
            {#if preset.recommended}
              <span class="rounded-full bg-[var(--color-primary)]/10 px-2 py-0.5 text-xs font-medium text-[var(--color-primary)]">
                {t('wizard.steps.detection.balancedRecommended')}
              </span>
            {/if}
          </div>
          <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
            {t(preset.descKey)}
          </p>
          <p class="mt-1 text-xs font-mono text-[var(--color-base-content)] opacity-40">
            {t('wizard.steps.detection.threshold')}: {preset.threshold}
          </p>
        </div>
      </button>
    {/each}
  </div>

  <p class="rounded-lg bg-[var(--color-info)]/10 p-3 text-xs text-[var(--color-info)]">
    {t('wizard.steps.detection.fpFilterNote')}
  </p>
</div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/steps/DetectionStep.svelte
git commit -m "feat(wizard): add DetectionStep with threshold presets"
```

---

### Task 7: IntegrationStep Component

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/steps/IntegrationStep.svelte`

**Reference files:**
- `frontend/src/lib/stores/settings.ts` — `PrivacyFilterSettings`, `BirdWeatherSettings`, `SentrySettings`

- [ ] **Step 1: Create IntegrationStep component**

```svelte
<script lang="ts">
  import { t } from '$lib/i18n';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { settingsActions } from '$lib/stores/settings';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  let privacyEnabled = $state(true);
  let birdweatherEnabled = $state(false);
  let birdweatherId = $state('');
  let sentryEnabled = $state(false);

  // Always valid
  $effect(() => {
    onValidChange?.(true);
  });

  // Save on unmount
  $effect(() => {
    return () => {
      settingsActions.updateSection('realtime', {
        privacyFilter: { enabled: privacyEnabled },
        birdweather: {
          enabled: birdweatherEnabled,
          id: birdweatherId,
        },
      });
      settingsActions.updateSection('sentry', {
        enabled: sentryEnabled,
      });
      settingsActions.saveSettings().catch(() => {});
    };
  });
</script>

<div class="space-y-5">
  <!-- Privacy Filter -->
  <label class="flex cursor-pointer items-start gap-3">
    <input
      type="checkbox"
      bind:checked={privacyEnabled}
      class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
    />
    <div>
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.privacyFilterLabel')}
      </span>
      <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
        {t('wizard.steps.integration.privacyFilterHelp')}
      </p>
    </div>
  </label>

  <hr class="border-[var(--border-200)]" />

  <!-- BirdWeather -->
  <div>
    <label class="flex cursor-pointer items-start gap-3">
      <input
        type="checkbox"
        bind:checked={birdweatherEnabled}
        class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
      />
      <div>
        <span class="text-sm font-medium text-[var(--color-base-content)]">
          {t('wizard.steps.integration.birdweatherLabel')}
        </span>
        <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
          {t('wizard.steps.integration.birdweatherHelp')}
        </p>
      </div>
    </label>
    {#if birdweatherEnabled}
      <div class="ml-7 mt-2">
        <TextInput
          bind:value={birdweatherId}
          placeholder={t('wizard.steps.integration.birdweatherIdPlaceholder')}
        />
      </div>
    {/if}
  </div>

  <hr class="border-[var(--border-200)]" />

  <!-- Error Reporting -->
  <label class="flex cursor-pointer items-start gap-3">
    <input
      type="checkbox"
      bind:checked={sentryEnabled}
      class="mt-0.5 size-4 shrink-0 accent-[var(--color-primary)]"
    />
    <div>
      <span class="text-sm font-medium text-[var(--color-base-content)]">
        {t('wizard.steps.integration.errorReportingLabel')}
      </span>
      <p class="mt-0.5 text-xs text-[var(--color-base-content)] opacity-60">
        {t('wizard.steps.integration.errorReportingHelp')}
      </p>
    </div>
  </label>
</div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/steps/IntegrationStep.svelte
git commit -m "feat(wizard): add IntegrationStep with privacy, BirdWeather, and Sentry"
```

---

### Task 8: ResponsibleUseStep Component

**Files:**
- Create: `frontend/src/lib/desktop/features/wizard/steps/ResponsibleUseStep.svelte`

- [ ] **Step 1: Create ResponsibleUseStep component**

```svelte
<script lang="ts">
  import { t } from '$lib/i18n';
  import type { WizardStepProps } from '../types';

  let { onValidChange }: WizardStepProps = $props();

  let acknowledged = $state(false);

  // Valid only when acknowledged
  $effect(() => {
    onValidChange?.(acknowledged);
  });
</script>

<div class="space-y-4">
  <p class="text-sm leading-relaxed text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.intro')}
  </p>

  <ul class="list-disc space-y-2 pl-5 text-sm text-[var(--color-base-content)] opacity-80">
    <li>{t('wizard.steps.responsibleUse.point1')}</li>
    <li>{t('wizard.steps.responsibleUse.point2')}</li>
    <li>{t('wizard.steps.responsibleUse.point3')}</li>
    <li>{t('wizard.steps.responsibleUse.point4')}</li>
  </ul>

  <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
    {t('wizard.steps.responsibleUse.citizenScienceHeading')}
  </h4>

  <ul class="list-disc space-y-2 pl-5 text-sm text-[var(--color-base-content)] opacity-80">
    <li>{t('wizard.steps.responsibleUse.citizenPoint1')}</li>
    <li>{t('wizard.steps.responsibleUse.citizenPoint2')}</li>
    <li>{t('wizard.steps.responsibleUse.citizenPoint3')}</li>
  </ul>

  <p class="text-sm leading-relaxed text-[var(--color-base-content)] opacity-70">
    {t('wizard.steps.responsibleUse.outro')}
  </p>

  <label class="mt-4 flex cursor-pointer items-center gap-3 rounded-lg border border-[var(--border-200)] p-3">
    <input
      type="checkbox"
      bind:checked={acknowledged}
      class="size-4 shrink-0 accent-[var(--color-primary)]"
    />
    <span class="text-sm font-medium text-[var(--color-base-content)]">
      {t('wizard.steps.responsibleUse.acknowledge')}
    </span>
  </label>
</div>
```

- [ ] **Step 2: Run svelte-check**

Run: `cd frontend && npx svelte-check --threshold error`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/steps/ResponsibleUseStep.svelte
git commit -m "feat(wizard): add ResponsibleUseStep with acknowledgment gate"
```

---

### Task 9: Wire Up Registry + Update Tests

Connect all step components to the wizard registry and update tests.

**Files:**
- Modify: `frontend/src/lib/desktop/features/wizard/wizardRegistry.ts`
- Modify: `frontend/src/lib/desktop/features/wizard/wizardRegistry.test.ts`

- [ ] **Step 1: Populate onboardingSteps in wizardRegistry.ts**

Replace the empty `onboardingSteps` array with:

```typescript
const onboardingSteps: WizardStep[] = [
  {
    id: 'welcome',
    type: 'component',
    titleKey: 'wizard.steps.welcome.title',
    component: () => import('./steps/WelcomeStep.svelte'),
  },
  {
    id: 'location-language',
    type: 'component',
    titleKey: 'wizard.steps.locationLanguage.title',
    component: () => import('./steps/LocationLanguageStep.svelte'),
  },
  {
    id: 'audio-source',
    type: 'component',
    titleKey: 'wizard.steps.audioSource.title',
    component: () => import('./steps/AudioSourceStep.svelte'),
  },
  {
    id: 'detection',
    type: 'component',
    titleKey: 'wizard.steps.detection.title',
    component: () => import('./steps/DetectionStep.svelte'),
  },
  {
    id: 'integration',
    type: 'component',
    titleKey: 'wizard.steps.integration.title',
    component: () => import('./steps/IntegrationStep.svelte'),
  },
  {
    id: 'responsible-use',
    type: 'component',
    titleKey: 'wizard.steps.responsibleUse.title',
    component: () => import('./steps/ResponsibleUseStep.svelte'),
  },
];
```

- [ ] **Step 2: Update wizardRegistry.test.ts**

First, update the existing test that asserts onboarding returns an empty array — it will now return 6 steps:

```typescript
// REPLACE the existing test:
//   it('returns empty array when no steps are registered', ...)
// WITH:
it('returns 6 steps for onboarding flow', () => {
  const steps = getStepsForFlow('onboarding');
  expect(steps).toHaveLength(6);
});
```

Then add these additional tests after existing tests:

```typescript
describe('onboarding flow', () => {
  it('all onboarding steps are component type', () => {
    const steps = getStepsForFlow('onboarding');
    for (const step of steps) {
      expect(step.type).toBe('component');
    }
  });

  it('onboarding steps have unique IDs', () => {
    const steps = getStepsForFlow('onboarding');
    const ids = steps.map(s => s.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it('first onboarding step is welcome', () => {
    const steps = getStepsForFlow('onboarding');
    expect(steps[0].id).toBe('welcome');
  });

  it('last onboarding step is responsible-use', () => {
    const steps = getStepsForFlow('onboarding');
    expect(steps[steps.length - 1].id).toBe('responsible-use');
  });
});
```

- [ ] **Step 3: Run tests**

Run: `cd frontend && npx vitest run src/lib/desktop/features/wizard/wizardRegistry.test.ts`

Expected: All tests pass, including the 5 new onboarding tests.

- [ ] **Step 4: Run full check**

Run: `cd frontend && npm run check:all`

Expected: No errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/desktop/features/wizard/wizardRegistry.ts frontend/src/lib/desktop/features/wizard/wizardRegistry.test.ts
git commit -m "feat(wizard): wire onboarding steps into registry and add tests"
```

---

### Task 10: Final Validation

**Files:** None (validation only)

- [ ] **Step 1: Run full frontend quality check**

Run: `cd frontend && npm run check:all`

Expected: Zero errors, zero warnings from svelte-check, ESLint, Prettier.

- [ ] **Step 2: Run all wizard tests**

Run: `cd frontend && npx vitest run src/lib/desktop/features/wizard/`

Expected: All wizard tests pass (existing state machine tests + new registry tests).

- [ ] **Step 3: Run Go linter** (no Go changes expected, but verify)

Run: `golangci-lint run -v`

Expected: No new issues.

- [ ] **Step 4: Add component smoke tests if time permits**

The spec calls for unit tests for each step component. At minimum, the registry tests from Task 9 validate step registration. For deeper coverage, add render tests for `DetectionStep` and `ResponsibleUseStep` (simplest components, no API calls) to verify they render and their validation callbacks work. Component tests for steps with API calls (LocationLanguageStep, AudioSourceStep, IntegrationStep) require mocking the settings store and API — these can be added as a follow-up.

- [ ] **Step 6: Validate Svelte components with MCP autofixer**

Run the Svelte autofixer (`mcp__svelte__svelte-autofixer`) on each new component to check for Svelte 5 best practice issues. Fix any issues found.

- [ ] **Step 7: Format all changed files**

Run: `cd frontend && npx prettier --write src/lib/desktop/features/wizard/`

- [ ] **Step 8: Final commit if any formatting changes**

```bash
git add -A && git commit -m "style: format wizard components"
```
