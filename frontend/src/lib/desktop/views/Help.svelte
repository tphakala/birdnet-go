<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import {
    Bug,
    MessageCircleQuestion,
    ExternalLink,
    FileText,
    Globe,
    LifeBuoy,
  } from '@lucide/svelte';
  import GithubIcon from '$lib/desktop/components/ui/GithubIcon.svelte';
  import { t } from '$lib/i18n';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { appState } from '$lib/stores/appState.svelte';
  import { LICENSE_URL } from '$lib/utils/externalUrls';
</script>

<div class="col-span-12 space-y-4">
  <!-- Page Header -->
  <Card className="bg-[var(--color-base-100)] shadow-sm">
    <div class="flex flex-col items-center text-center">
      <div
        class="w-20 h-20 rounded-full bg-gradient-to-b from-[var(--surface-200)] to-[var(--color-base-100)] flex items-center justify-center border border-[var(--border-100)]"
      >
        <LifeBuoy class="size-10 text-[var(--color-primary)]" />
      </div>
      <div class="mt-3">
        <h1 class="text-3xl font-bold">{t('help.title')}</h1>
        <p class="text-[var(--color-base-content)] opacity-70 text-base mt-2">
          {t('help.subtitle')}
        </p>
      </div>
    </div>
  </Card>

  <!-- Card 1: Report a Bug -->
  <Card title={t('navigation.reportBug')} className="bg-[var(--color-base-100)] shadow-sm">
    <p class="text-[var(--color-base-content)] opacity-80">
      {t('help.reportBug.description')}
    </p>

    <div class="mt-4">
      <button
        onclick={() => navigation.navigate('/ui/help/report-bug')}
        class="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-md transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:bg-[var(--color-primary-hover)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2"
      >
        <Bug class="size-4" />
        {t('navigation.reportBug')}
      </button>
    </div>
  </Card>

  <!-- Card 2: Ask a Question -->
  <Card title={t('navigation.askQuestion')} className="bg-[var(--color-base-100)] shadow-sm">
    <p class="text-[var(--color-base-content)] opacity-80">
      {t('help.askQuestion.description')}
    </p>

    <div class="mt-4">
      <a
        href={appState.projectLinks.discussionsUrl}
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-md transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:bg-[var(--color-primary-hover)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2"
        aria-label={t('navigation.askQuestionAriaLabel')}
      >
        <MessageCircleQuestion class="size-4" />
        {t('navigation.askQuestion')}
        <ExternalLink class="size-3 opacity-60" />
      </a>
    </div>
  </Card>

  <!-- Card 3: Quick Links -->
  <Card title={t('help.quickLinks.title')} className="bg-[var(--color-base-100)] shadow-sm">
    <div class="space-y-3">
      <a
        href={appState.projectLinks.repoUrl}
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors duration-150 hover:bg-[var(--color-base-200)]"
        aria-label={t('navigation.viewOnGithubAriaLabel')}
      >
        <GithubIcon class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
        <span class="flex-1">{t('navigation.github')}</span>
        <ExternalLink class="size-3.5 text-[var(--color-base-content)]/40" />
      </a>
      <a
        href={appState.projectLinks.releasesUrl}
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors duration-150 hover:bg-[var(--color-base-200)]"
      >
        <FileText class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
        <span class="flex-1">{t('help.quickLinks.releases')}</span>
        <ExternalLink class="size-3.5 text-[var(--color-base-content)]/40" />
      </a>
      <a
        href={LICENSE_URL}
        target="_blank"
        rel="noopener noreferrer"
        class="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors duration-150 hover:bg-[var(--color-base-200)]"
      >
        <Globe class="size-5 shrink-0 text-[var(--color-base-content)]/70" />
        <span class="flex-1">{t('about.licenseInformation')}</span>
        <ExternalLink class="size-3.5 text-[var(--color-base-content)]/40" />
      </a>
    </div>
  </Card>
</div>
