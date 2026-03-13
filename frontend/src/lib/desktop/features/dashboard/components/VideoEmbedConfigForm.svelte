<!--
  Configuration form for the Video Embed dashboard element.
  @component
-->
<script lang="ts">
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { t } from '$lib/i18n';
  import type { VideoEmbedConfig } from '$lib/stores/settings';

  interface Props {
    config: VideoEmbedConfig;
    onUpdate: (_config: VideoEmbedConfig) => void;
  }

  let { config, onUpdate }: Props = $props();
</script>

<div class="space-y-4">
  <TextInput
    id="video-url"
    value={config.url}
    label={t('dashboard.videoEmbed.youtubeUrl')}
    placeholder={t('dashboard.videoEmbed.youtubeUrlPlaceholder')}
    helpText={t('dashboard.videoEmbed.youtubeUrlHelp')}
    onchange={value => onUpdate({ ...config, url: value })}
  />

  <TextInput
    id="video-title"
    value={config.title}
    label={t('dashboard.videoEmbed.titleLabel')}
    placeholder={t('dashboard.videoEmbed.titlePlaceholder')}
    onchange={value => onUpdate({ ...config, title: value })}
  />
</div>
