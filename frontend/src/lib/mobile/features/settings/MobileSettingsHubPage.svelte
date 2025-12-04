<script lang="ts">
  import { t } from '$lib/i18n';
  import { systemIcons } from '$lib/utils/icons';
  import SettingsNavCard from '../../components/settings/SettingsNavCard.svelte';

  interface Props {
    onNavigate?: (_section: string) => void;
  }

  let { onNavigate }: Props = $props();

  const settingsSections = [
    {
      id: 'main',
      title: t('settings.main.title'),
      description: t('settings.main.description'),
      iconHtml: systemIcons.home,
      iconClassName: 'bg-primary/20 text-primary',
    },
    {
      id: 'ui',
      title: t('settings.ui.title'),
      description: t('settings.ui.description'),
      iconHtml: systemIcons.sunIcon,
      iconClassName: 'bg-secondary/20 text-secondary',
    },
    {
      id: 'audio',
      title: t('settings.audio.title'),
      description: t('settings.audio.description'),
      iconHtml: systemIcons.settingsGear,
      iconClassName: 'bg-accent/20 text-accent',
    },
    {
      id: 'filters',
      title: t('settings.filters.title'),
      description: t('settings.filters.description'),
      iconHtml: systemIcons.list,
      iconClassName: 'bg-info/20 text-info',
    },
    {
      id: 'integrations',
      title: t('settings.integrations.title'),
      description: t('settings.integrations.description'),
      iconHtml: systemIcons.globe,
      iconClassName: 'bg-success/20 text-success',
    },
    {
      id: 'security',
      title: t('settings.security.title'),
      description: t('settings.security.description'),
      iconHtml: systemIcons.shield,
      iconClassName: 'bg-warning/20 text-warning',
    },
    {
      id: 'species',
      title: t('settings.species.title'),
      description: t('settings.species.description'),
      iconHtml: systemIcons.bird,
      iconClassName: 'bg-error/20 text-error',
    },
    {
      id: 'notifications',
      title: t('settings.notifications.title'),
      description: t('settings.notifications.description'),
      iconHtml: systemIcons.bell,
      iconClassName: 'bg-primary/20 text-primary',
    },
    {
      id: 'support',
      title: t('settings.support.title'),
      description: t('settings.support.description'),
      iconHtml: systemIcons.help,
      iconClassName: 'bg-neutral/20 text-neutral',
    },
  ];

  function handleNavigate(sectionId: string) {
    onNavigate?.(sectionId);
  }
</script>

<div class="flex flex-col gap-2 p-4">
  {#each settingsSections as section (section.id)}
    <SettingsNavCard
      title={section.title}
      description={section.description}
      iconHtml={section.iconHtml}
      iconClassName={section.iconClassName}
      onClick={() => handleNavigate(section.id)}
    />
  {/each}
</div>
