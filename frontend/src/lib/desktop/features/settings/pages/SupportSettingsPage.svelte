<!--
  Support Settings Page Component

  Purpose: Support dump generation and diagnostic features for BirdNET-Go.
  Note: Telemetry settings have been moved to the Main Settings General tab.

  Features:
  - Support dump generation with customizable options (via shared SupportDumpCard)
  - Upload to Sentry or download locally options
  - User message inclusion for context

  Props: None - This is a page component that uses global settings stores

  @component
-->
<script lang="ts">
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import { Wrench } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import SupportDumpCard from '$lib/desktop/components/ui/SupportDumpCard.svelte';

  let activeTab = $state('diagnostics');

  let tabs = $derived<TabDefinition[]>([
    {
      id: 'diagnostics',
      label: t('settings.support.sections.diagnostics.title'),
      icon: Wrench,
      content: diagnosticsTabContent,
      hasChanges: false,
    },
  ]);
</script>

{#snippet diagnosticsTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('settings.support.sections.diagnostics.title')}
      description={t('settings.support.sections.diagnostics.description')}
      defaultOpen={true}
    >
      <SupportDumpCard />
    </SettingsSection>
  </div>
{/snippet}

<main
  class="settings-page-content"
  aria-label={t('settings.support.sections.diagnostics.description')}
>
  <SettingsTabs {tabs} bind:activeTab />
</main>
