<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '$lib/i18n';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { Scale, Target, Radio } from '@lucide/svelte';
  import RadioCardGroup from '$lib/desktop/components/ui/RadioCardGroup.svelte';
  import type { RadioCardOption } from '$lib/desktop/components/ui/RadioCardGroup.types';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import type { WizardStepProps } from '../types';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

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
      id: 'high-sensitivity',
      titleKey: 'wizard.steps.detection.highSensitivity',
      descKey: 'wizard.steps.detection.highSensitivityDesc',
      threshold: 0.6,
      icon: Radio,
    },
    {
      id: 'balanced',
      titleKey: 'wizard.steps.detection.balanced',
      descKey: 'wizard.steps.detection.balancedDesc',
      threshold: 0.7,
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
  ];

  /** Thresholds closer than this are the same stored value (float noise from YAML). */
  const THRESHOLD_EPSILON = 1e-6;

  const INTRO_ID = 'wizard-detection-intro';

  const sameThreshold = (a: number, b: number) => Math.abs(a - b) < THRESHOLD_EPSILON;

  // What the server holds, not the form copy, read once so the first render is
  // already right. The wizard opens only after settings load.
  // The threshold the server holds as far as this step knows; moves after a save.
  const initialThreshold = get(settingsStore).originalData?.birdnet?.threshold;
  let savedThreshold = $state(initialThreshold);

  const matchingPreset = (threshold: number | undefined) =>
    threshold === undefined ? undefined : presets.find(p => sameThreshold(p.threshold, threshold));

  // A stored value that matches no preset (a hand edit, an older wizard) checks
  // no card; the intro paragraph states the value instead of the generic text.
  const storedNoMatch = $derived(savedThreshold !== undefined && !matchingPreset(savedThreshold));

  let selectedId = $state<string | null>(matchingPreset(initialThreshold)?.id ?? null);

  const presetOptions = $derived<RadioCardOption[]>(
    presets.map(p => ({
      value: p.id,
      label: t(p.titleKey),
      description: t(p.descKey),
      detail: `${t('wizard.steps.detection.threshold')}: ${p.threshold}`,
      icon: p.icon,
      badge: p.recommended ? t('wizard.steps.detection.balancedRecommended') : undefined,
    }))
  );

  $effect(() => {
    untrack(() => onValidChange?.(true));
  });

  function selectOption(id: string) {
    selectedId = id;
  }

  // Next, Back and Done await the commit; it never runs on Skip or Leave setup.
  onMount(() => registerLeaveHandler?.(commit));

  // Save the step's pick when the wizard leaves it with Next, Back or Done.
  // Nothing is sent unless the pick differs from what is stored, so passing
  // through the step untouched never rewrites the configuration, and a failed
  // save is resent by the next leave.
  async function commit(): Promise<void> {
    const option = presets.find(o => o.id === selectedId);
    if (!option) return;
    if (savedThreshold !== undefined && sameThreshold(option.threshold, savedThreshold)) return;
    await settingsActions.saveSection('birdnet', { threshold: option.threshold });
    savedThreshold = option.threshold;
  }
</script>

<div class="space-y-5">
  <p id={INTRO_ID} class="text-sm text-[var(--color-base-content)]">
    {storedNoMatch
      ? t('wizard.steps.detection.descriptionStored', { threshold: savedThreshold })
      : t('wizard.steps.detection.description')}
  </p>

  <RadioCardGroup
    options={presetOptions}
    value={selectedId}
    onChange={selectOption}
    aria-label={t('wizard.steps.detection.title')}
    aria-describedby={storedNoMatch ? INTRO_ID : undefined}
  />

  <SettingsNote className="mt-0">
    <p>{t('wizard.steps.detection.fpFilterNote')}</p>
  </SettingsNote>
</div>
