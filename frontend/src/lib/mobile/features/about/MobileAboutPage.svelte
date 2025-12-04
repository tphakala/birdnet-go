<script lang="ts">
  import { t } from '$lib/i18n';
  import { systemIcons, alertIconsSvg } from '$lib/utils/icons';
</script>

<div class="flex flex-col gap-4 p-4 pb-20">
  <!-- Header with Logo -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body items-center py-8 text-center">
      <div
        class="flex h-24 w-24 items-center justify-center rounded-full bg-gradient-to-b from-base-200 to-base-100 p-1 ring-1 ring-base-content/10"
      >
        <img
          src="/assets/images/BirdNET-Go-logo.webp"
          alt={t('about.logoAlt')}
          class="h-full w-full rounded-full object-contain"
        />
      </div>
      <h1 class="mt-4 text-2xl font-bold">{t('about.title')}</h1>
      <p class="text-sm text-base-content/70">
        {t('about.subtitle')}
      </p>
    </div>
  </div>

  <!-- Overview -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4">
      <h2 class="mb-2 font-semibold">{t('about.overview')}</h2>
      <p class="text-sm text-base-content/80">
        {t('about.overviewText')}
      </p>

      <div class="mt-4 rounded-lg bg-base-200 p-3">
        <p class="flex items-center justify-center gap-2 text-center text-xs text-base-content/70">
          <span class="text-yellow-500">
            {@html systemIcons.star}
          </span>
          Star us on GitHub to support development!
        </p>
      </div>

      <a
        href="https://github.com/tphakala/birdnet-go"
        class="btn btn-primary btn-sm mt-4 gap-2"
        target="_blank"
        rel="noopener noreferrer"
      >
        {@html systemIcons.github}
        View on GitHub
      </a>
    </div>
  </div>

  <!-- BirdNET Credits -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4">
      <h2 class="mb-2 font-semibold">{t('about.birdnetProject')}</h2>
      <p class="text-sm text-base-content/80">
        {t('about.birdnetDescription')}
      </p>

      <p class="mt-4 text-sm font-medium">{t('about.developedBy')}</p>
      <div class="mt-2 grid grid-cols-2 gap-2">
        <div class="flex items-center gap-2 text-sm">
          {@html systemIcons.user}
          Stefan Kahl
        </div>
        <div class="flex items-center gap-2 text-sm">
          {@html systemIcons.user}
          Connor Wood
        </div>
        <div class="flex items-center gap-2 text-sm">
          {@html systemIcons.user}
          Maximilian Eibl
        </div>
        <div class="flex items-center gap-2 text-sm">
          {@html systemIcons.user}
          Holger Klinck
        </div>
      </div>
    </div>
  </div>

  <!-- Contributors -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4">
      <h2 class="mb-2 font-semibold">{t('about.contributors')}</h2>

      <p class="text-sm font-medium">{t('about.mainDeveloper')}</p>
      <a href="https://github.com/tphakala" class="btn btn-ghost btn-sm mt-1 justify-start">
        {@html systemIcons.user}
        Tomi P. Hakala
      </a>

      <div class="mt-4 rounded-lg bg-base-200 p-3">
        <p class="text-sm font-medium">{t('about.communityAcknowledgment')}</p>
        <ul class="mt-2 space-y-1">
          <li class="flex items-center gap-2 text-sm">
            <span class="h-4 w-4 text-success">{@html alertIconsSvg.success}</span>
            {t('about.bugReports')}
          </li>
          <li class="flex items-center gap-2 text-sm">
            <span class="h-4 w-4 text-success">{@html alertIconsSvg.success}</span>
            {t('about.featureSuggestions')}
          </li>
          <li class="flex items-center gap-2 text-sm">
            <span class="h-4 w-4 text-success">{@html alertIconsSvg.success}</span>
            {t('about.testing')}
          </li>
        </ul>
      </div>
    </div>
  </div>

  <!-- License -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body p-4">
      <h2 class="mb-2 font-semibold">{t('about.licenseInformation')}</h2>
      <p class="text-sm text-base-content/80">
        {t('about.licenseText')}
        <a
          href="https://creativecommons.org/licenses/by-nc-sa/4.0/"
          class="link link-primary"
          target="_blank"
          rel="noopener noreferrer"
        >
          {t('about.ccLicense')}
        </a>.
      </p>
    </div>
  </div>

  <!-- External Links -->
  <div class="flex flex-wrap justify-center gap-2">
    <a
      href="https://github.com/tphakala/birdnet-go"
      class="btn btn-outline btn-sm gap-2"
      target="_blank"
      rel="noopener noreferrer"
    >
      {@html systemIcons.github}
      GitHub
    </a>
    <a
      href="https://discord.gg/gcSCFGUtsd"
      class="btn btn-outline btn-sm gap-2"
      target="_blank"
      rel="noopener noreferrer"
    >
      {@html systemIcons.globe}
      Discord
    </a>
  </div>
</div>
