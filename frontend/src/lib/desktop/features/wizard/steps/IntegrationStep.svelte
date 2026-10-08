<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { t, type TranslationKey } from '$lib/i18n';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import { settingsActions, settingsStore } from '$lib/stores/settings';
  import { get } from 'svelte/store';
  import { ShieldCheck, Cloud, HeartHandshake } from '@lucide/svelte';
  import type { WizardStepProps } from '../types';
  import type { SettingsSectionPayloads } from '$lib/utils/settingsApi';
  import { generateId } from '$lib/utils/uuid';
  import { isBirdweatherToken } from '$lib/utils/birdweather';

  let { onValidChange, registerLeaveHandler }: WizardStepProps = $props();

  const TOKEN_FIELD_ID = generateId('wizard-birdweather-token');
  // The token is a plain text input, not a masked one: a password input makes browsers
  // offer to save it as a site password after Next and fill a saved password into it,
  // which would pass the format check and be saved as the station id. 'off' keeps
  // autofill away from it.
  const TOKEN_AUTOCOMPLETE = 'off';

  // Read synchronously so the first validity report already reflects the saved settings
  const store = get(settingsStore);
  const initial = {
    privacyEnabled: store?.formData?.realtime?.privacyFilter?.enabled ?? true,
    birdweatherEnabled: store?.formData?.realtime?.birdweather?.enabled ?? false,
    birdweatherId: store?.formData?.realtime?.birdweather?.id ?? '',
    sentryEnabled: store?.formData?.sentry?.enabled ?? false,
  };

  let privacyEnabled = $state(initial.privacyEnabled);
  let birdweatherEnabled = $state(initial.birdweatherEnabled);
  let birdweatherId = $state(initial.birdweatherId);
  let sentryEnabled = $state(initial.sentryEnabled);

  /** Sections this step saves, in save order: BirdWeather first because its
   * token is the value most likely to be rejected, so a failure there writes
   * nothing. */
  const SECTIONS = ['birdweather', 'privacyfilter', 'sentry'] as const;
  type IntegrationSection = (typeof SECTIONS)[number];

  // The token last known to be on the server (trimmed). While BirdWeather is off
  // a malformed token is not sent: it stays in the field, and the server keeps
  // this one.
  let savedTokenId = (store?.formData?.realtime?.birdweather?.id ?? '').trim();

  // The token is sent trimmed: the server does not trim it, so this is the string
  // the validation below accepts.
  function currentPayloads(): { [S in IntegrationSection]: SettingsSectionPayloads[S] } {
    const typedId = birdweatherId.trim();
    const keepSavedId = !birdweatherEnabled && typedId !== '' && !isBirdweatherToken(typedId);
    return {
      birdweather: { enabled: birdweatherEnabled, id: keepSavedId ? savedTokenId : typedId },
      privacyfilter: { enabled: privacyEnabled },
      sentry: { enabled: sentryEnabled },
    };
  }

  // JSON of what each section last held on the server (as far as this step
  // knows): the values read on open, then the values of each successful save.
  // The commit sends only sections that differ, so a retry after a partial
  // failure repeats only the sections that did not succeed.
  const loaded = currentPayloads();
  let savedJson: Record<IntegrationSection, string> = {
    birdweather: JSON.stringify(loaded.birdweather),
    privacyfilter: JSON.stringify(loaded.privacyfilter),
    sentry: JSON.stringify(loaded.sentry),
  };

  let blockedReason = $derived.by((): TranslationKey | undefined => {
    if (!birdweatherEnabled) return undefined;
    if (birdweatherId.trim() === '') return 'wizard.steps.integration.reasons.enterToken';
    return isBirdweatherToken(birdweatherId)
      ? undefined
      : 'wizard.steps.integration.reasons.tokenFormat';
  });
  let isValid = $derived(blockedReason === undefined);

  // Set when the user leaves the token field and on open for a saved token; cleared
  // by every edit. Display only: Next's reason does not depend on it.
  let tokenLeft = $state(initial.birdweatherId.trim() !== '');
  let showTokenError = $derived(tokenLeft && blockedReason !== undefined);

  function onTokenUpdate(value: string) {
    birdweatherId = value;
    tokenLeft = false;
  }

  function onTokenBlur() {
    tokenLeft = true;
  }

  $effect(() => {
    // Primitives, so the effect runs only when one of them changes
    const valid = isValid;
    const why = blockedReason;
    untrack(() => onValidChange?.(valid, why));
  });

  // Next, Back and Done await the commit; it never runs on Skip or Leave setup.
  onMount(() => registerLeaveHandler?.(commit));

  // Set when the step unmounts, so a commit still in flight sends no further sections.
  let left = false;
  onDestroy(() => {
    left = true;
  });

  // Save the step's edits when the wizard leaves it with Next, Back or Done.
  // Each changed section is its own request, in SECTIONS order. If Skip closes
  // the wizard while one section is saving, that request completes but the
  // remaining sections are not sent.
  async function commit(): Promise<void> {
    const next = currentPayloads();
    for (const section of SECTIONS) {
      if (left) return;
      // Back runs this on an invalid step too: the invalid BirdWeather section
      // stays unsent and unsaved, the valid sections are saved.
      if (section === 'birdweather' && !isValid) continue;
      // eslint-disable-next-line security/detect-object-injection -- section is a member of SECTIONS
      const body = next[section];
      const json = JSON.stringify(body);
      // eslint-disable-next-line security/detect-object-injection -- section is a member of SECTIONS
      if (json === savedJson[section]) continue;
      await settingsActions.saveSection(section, body);
      // eslint-disable-next-line security/detect-object-injection -- section is a member of SECTIONS
      savedJson[section] = json;
      if (section === 'birdweather') savedTokenId = next.birdweather.id ?? '';
    }
  }
</script>

<div class="space-y-3">
  <ToggleField
    variant="card"
    icon={ShieldCheck}
    label={t('wizard.steps.integration.privacyFilterLabel')}
    description={t('wizard.steps.integration.privacyFilterHelp')}
    value={privacyEnabled}
    onUpdate={value => (privacyEnabled = value)}
  />

  <ToggleField
    variant="card"
    icon={Cloud}
    label={t('wizard.steps.integration.birdweatherLabel')}
    description={t('wizard.steps.integration.birdweatherHelp')}
    value={birdweatherEnabled}
    onUpdate={value => (birdweatherEnabled = value)}
  />
  <!-- A sibling after the BirdWeather card, never inside it: clicks in the field must not
       toggle BirdWeather. The field keeps two error lines reserved, so showing the error
       does not move the card below. -->
  {#if birdweatherEnabled}
    <div class="ml-12 mt-[-0.25rem]">
      <TextInput
        id={TOKEN_FIELD_ID}
        label={t('settings.integration.birdweather.token.label')}
        value={birdweatherId}
        oninput={onTokenUpdate}
        onblur={onTokenBlur}
        error={showTokenError && blockedReason ? t(blockedReason) : undefined}
        reserveErrorSpace
        autocomplete={TOKEN_AUTOCOMPLETE}
      />
    </div>
  {/if}

  <ToggleField
    variant="card"
    icon={HeartHandshake}
    label={t('wizard.steps.integration.errorReportingLabel')}
    description={t('wizard.steps.integration.errorReportingHelp')}
    value={sentryEnabled}
    onUpdate={value => (sentryEnabled = value)}
  />
</div>
