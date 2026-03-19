# Onboarding Wizard — Design Spec

**Date:** 2026-03-19
**Issue:** [#887](https://github.com/tphakala/birdnet-go/issues/887)
**Depends on:** PR #2418 (wizard dialog infrastructure — merged)

## Overview

First-time setup wizard that guides new BirdNET-Go users through essential configuration. Six steps, each saving settings independently so partial completion is preserved. Built on the wizard dialog infrastructure from PR #2418.

## Step Structure

| # | Step | Component | Saves to | Validity |
|---|------|-----------|----------|----------|
| 1 | Welcome | `WelcomeStep.svelte` | nothing | always valid |
| 2 | Location & Language | `LocationLanguageStep.svelte` | `node` (lat/lon), `birdnet` (locale), `realtime.dashboard` (UI locale) | always valid (defaults work) |
| 3 | Audio Source | `AudioSourceStep.svelte` | `audio` (capture source or RTSP URL) | valid when source selected |
| 4 | Detection Threshold | `DetectionStep.svelte` | `birdnet` (threshold) | always valid (preset pre-selected) |
| 5 | Privacy & Integration | `IntegrationStep.svelte` | `realtime` (privacy filter, birdweather, telemetry) | always valid |
| 6 | Responsible Use | `ResponsibleUseStep.svelte` | nothing | valid when "I understand" checked |

All step components live in `frontend/src/lib/desktop/features/wizard/steps/`.

## Step Details

### Step 1: Welcome

Static presentation. No settings saved.

- BirdNET-Go logo (existing asset)
- Welcome heading: "Welcome to BirdNET-Go!"
- Brief description of what the application does (real-time bird sound identification)
- Credit line: BirdNET Analyzer by Cornell Lab of Ornithology and Chemnitz University of Technology
- Call to action: "Let's set up your bird monitoring station"

The wizard's Next button advances to step 2.

### Step 2: Location & Language

Three configuration groups in a single step.

**UI Language:**
- Reuse existing `LanguageSelector` component (13 locales)
- Changing UI language immediately updates the wizard text

**Species Name Language:**
- New `SelectDropdown` populated from the 40+ BirdNET locales defined in `internal/conf/locale.go`
- Independent from UI language (e.g., UI in Finnish, species names in English)
- Saved to `birdnet.locale` — will support hot-reload (no restart needed)

**Location:**
- Latitude/longitude `NumberField` inputs
- "Use my location" button using browser Geolocation API (`navigator.geolocation`)
- Interactive MapLibre map:
  - Click anywhere on the map to set coordinates
  - Marker shows current position
  - Map pans/zooms when number fields change
  - Number fields update when map is clicked
  - Built on existing `mapConfig.ts` (OpenStreetMap tiles)
  - New `LocationPickerMap.svelte` component

**Save behavior:** Calls `settingsActions.updateSection()` for each affected section when the user navigates away from this step (next or back).

### Step 3: Audio Source

Single audio source configuration. Additional sources can be added later in Settings.

**Source type toggle** — radio buttons:
- Microphone/Soundcard (default)
- RTSP Stream

**Soundcard mode:**
- `SelectDropdown` populated from `GET /api/v2/system/audio/devices`
- Shows device name for each available capture device
- Loading state while devices are fetched

**RTSP mode:**
- `TextInput` for stream URL
- Placeholder: `rtsp://user:password@host:port/stream`

**Validation:** Step is valid when a source is selected (soundcard chosen or RTSP URL non-empty).

### Step 4: Detection Threshold

Three preset cards in a radio-button group. One is pre-selected (Balanced).

| Preset | Threshold | Description |
|--------|-----------|-------------|
| Balanced (recommended) | 0.8 | Good balance of accuracy and sensitivity |
| High Accuracy | 0.9 | Fewer false positives, may miss some birds |
| High Sensitivity | 0.6 | More detections, more false positives |

Each card shows the preset name, threshold value, and a one-line description.

Below the presets, a note: "Once your system is running and proven, you can enable the false positive filter in Settings to further reduce incorrect detections."

### Step 5: Privacy & Integration

Three independent settings, each with a toggle and brief explanation.

**Privacy Filter** (default: ON):
- Checkbox: "Enable privacy filter"
- Description: "Filters out detections when human voices are detected near the microphone"
- Saves to `realtime.privacyFilter.enabled`

**BirdWeather** (default: OFF):
- Checkbox: "Share detections with BirdWeather"
- Description: "Contribute your bird detections to the BirdWeather community network"
- When enabled, shows `TextInput` for BirdWeather Station ID
- Saves to `realtime.birdweather`

**Error Reporting** (default: OFF):
- Checkbox: "Send anonymous error reports"
- Description: "Help improve BirdNET-Go by sending anonymous error reports when something goes wrong"
- Saves to `realtime.telemetry.enabled`

### Step 6: Responsible Use

Full-text presentation about AI-assisted bird identification. No settings saved.

Content covers:
- BirdNET-Go uses AI-assisted identification — results are probabilistic, not definitive
- Review and verify detections before sharing observations
- Use confidence scores as a guide, not absolute truth
- Cross-reference with field guides and local expertise
- Consider seasonal patterns and habitat suitability
- When contributing to citizen science: only submit verified observations, include notes about confirmation method

**Validation:** "I understand" checkbox must be checked to enable the Done button. This uses the `onValidChange` callback to gate wizard completion.

## New Components

### `LocationPickerMap.svelte`

Interactive MapLibre map for setting station coordinates.

**Props:**
- `latitude: number` — current latitude
- `longitude: number` — current longitude
- `onLocationChange: (lat: number, lon: number) => void` — callback when location changes

**Behavior:**
- Renders a MapLibre GL map using `mapConfig.ts` styles
- Displays a draggable marker at the current coordinates
- Click on map: moves marker, fires `onLocationChange`
- Drag marker: fires `onLocationChange` on drag end
- Smart initial zoom: zoomed out (3) when coordinates are 0,0; zoomed in (11) otherwise
- Responsive sizing within the wizard modal

**Dependencies:** `maplibre-gl` (already in project), `mapConfig.ts` (existing)

### Step Components (6 files)

Each step component implements the `WizardStepProps` interface:

```typescript
interface WizardStepProps {
  onValidChange?: (valid: boolean) => void;
}
```

Steps that need validation call `onValidChange(false)` on mount and `onValidChange(true)` when their condition is met.

## Registry Integration

In `wizardRegistry.ts`, populate the currently empty `onboardingSteps` array:

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
  // ... remaining 4 steps
];
```

## i18n

All wizard text uses translation keys under `wizard.steps.*`. New keys added to all 13 locale files (`da`, `de`, `en`, `es`, `fi`, `fr`, `it`, `lv`, `nl`, `pl`, `pt`, `sk`, `sv`).

English is the source of truth. Other locales get English text as placeholder — community can translate later.

Key structure:
```
wizard.steps.welcome.title
wizard.steps.welcome.heading
wizard.steps.welcome.description
wizard.steps.welcome.credit
wizard.steps.welcome.cta
wizard.steps.locationLanguage.title
wizard.steps.locationLanguage.uiLanguage
wizard.steps.locationLanguage.speciesLanguage
...
```

## Settings Save Strategy

Each step saves independently when the user navigates away (next or back). This is implemented by having each step component track its local state and call `settingsActions.updateSection()` in an `onDestroy` or via a save callback.

Pattern:
1. Step mounts, loads current settings from the store
2. User makes changes (local `$state` variables)
3. On step transition (next/back/skip), step saves changes via settings API
4. If save fails, show a toast error but don't block navigation

## No Backend Changes

All required API endpoints already exist:
- `GET /api/v2/app/config` — wizard state detection (PR #2418)
- `POST /api/v2/app/wizard/dismiss` — mark wizard completed (PR #2418)
- `GET /api/v2/system/audio/devices` — audio device list
- `POST /api/v2/settings/*` — settings updates (existing)

## File Structure

```
frontend/src/lib/desktop/features/wizard/
├── steps/
│   ├── WelcomeStep.svelte
│   ├── LocationLanguageStep.svelte
│   ├── AudioSourceStep.svelte
│   ├── DetectionStep.svelte
│   ├── IntegrationStep.svelte
│   └── ResponsibleUseStep.svelte
├── components/
│   └── LocationPickerMap.svelte
├── WizardDialog.svelte          (existing)
├── WizardProgressBar.svelte     (existing)
├── WizardContentRenderer.svelte (existing)
├── wizardState.svelte.ts        (existing)
├── wizardRegistry.ts            (existing — populate onboardingSteps)
└── types.ts                     (existing)
```

## Testing

- Unit tests for each step component (render, validation, save behavior)
- Unit test for `LocationPickerMap` (coordinate updates, callbacks)
- Update `wizardRegistry.test.ts` to cover populated onboarding steps
- Existing wizard state machine tests remain valid (no changes to state logic)

## Out of Scope

- BirdNET-Pi import (no foundation exists)
- Interactive UI tour (Shepherd.js — separate feature)
- Multiple audio source configuration (use Settings page)
- RTSP stream testing/preview in wizard
- Advanced detection settings (sensitivity, overlap, deep detection)
- Mobile layout (desktop-only UI per project guidelines)
