<!--
  About Page Component
  
  Purpose: Displays information about BirdNET-Go including overview, key features,
  credits, technology stack, license, and version information.
  
  Features:
  - Product overview and description
  - Key features list with icons
  - Original BirdNET team credits
  - Technology stack display
  - License information
  - Version and build date display (when available)
  
  Props: None - This is a standalone page component
  
  Usage:
  This component is rendered as a page view in the main application router.
  It provides static content with internationalization support via the t() function.
  
  @component
-->
<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { systemIcons } from '$lib/utils/icons';
  import { t } from '$lib/i18n';

  // TypeScript interface for version settings
  interface VersionSettings {
    version: string;
    buildDate: string;
  }

  // PERFORMANCE OPTIMIZATION: Removed unnecessary $effect() that called empty fetchVersionInfo()
  // In Svelte 5, $effect() runs on every component mount and when dependencies change.
  // Calling empty functions creates unnecessary reactive overhead.

  // Settings data (version info) - will use fallback translations for now
  let settings = $state<VersionSettings>({
    version: '',
    buildDate: '',
  });
</script>

<div class="col-span-12 space-y-4">
  <!-- Header with Logo -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body items-center text-center">
      <div
        class="w-36 h-36 rounded-full bg-gradient-to-b from-base-200 to-base-100 flex items-center justify-center p-1 ring-1 ring-base-content/10"
      >
        <img
          src="/assets/images/BirdNET-Go-logo.webp"
          alt={t('about.logoAlt')}
          class="w-full h-full object-contain rounded-full"
          style:transform="scale(1)"
        />
      </div>
      <div class="mt-4">
        <h1 class="text-4xl font-bold">{t('about.title')}</h1>
        <p class="text-base-content/70 text-lg mt-2">
          {t('about.subtitle')}
        </p>
      </div>
    </div>
  </div>

  <!-- Overview Section -->
  <Card title={t('about.overview')} className="bg-base-100 shadow-sm">
    <p>
      {t('about.overviewText')}
    </p>
  </Card>

  <!-- BirdNET Project Section -->
  <Card title={t('about.birdnetProject')} className="bg-base-100 shadow-sm">
    <p>
      {t('about.birdnetDescription')}
    </p>

    <p class="text-xl font-medium mt-6">{t('about.developedBy')}</p>
    <p>
      {t('about.developersText')}
    </p>
    <ul class="list-none pl-0 gap-2 mt-4 about-developers-grid">
      <li class="flex items-center gap-2">
        <!-- PERFORMANCE OPTIMIZATION: Using centralized systemIcons.user instead of custom UserIcon component -->
        <!-- Reduces bundle size and follows project's established icon pattern -->
        {@html systemIcons.user}
        Stefan Kahl
      </li>
      <li class="flex items-center gap-2">
        {@html systemIcons.user}
        Connor Wood
      </li>
      <li class="flex items-center gap-2">
        {@html systemIcons.user}
        Maximilian Eibl
      </li>
      <li class="flex items-center gap-2">
        {@html systemIcons.user}
        Holger Klinck
      </li>
    </ul>

    <div class="mt-6 flex justify-center">
      <a
        href="https://github.com/birdnet-team/BirdNET-Analyzer"
        class="btn btn-primary btn-outline gap-2"
        target="_blank"
        rel="noopener noreferrer"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="w-5 h-5"
          viewBox="0 0 24 24"
          fill="currentColor"
        >
          <path
            d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"
          />
        </svg>
        {t('about.visitBirdnetAnalyzer')}
      </a>
    </div>
  </Card>

  <!-- Contributors Section -->
  <Card title={t('about.contributors')} className="bg-base-100 shadow-sm">
    <p>
      {t('about.contributorsText')}
    </p>

    <p class="text-xl font-medium mt-6">{t('about.mainDeveloper')}</p>
    <p class="flex items-center gap-2 mt-2">
      {@html systemIcons.user}
      <a href="https://github.com/tphakala" class="btn btn-ghost btn-sm justify-start normal-case"
        >Tomi P. Hakala</a
      >
    </p>

    <p class="text-xl font-medium mt-6">{t('about.githubContributors')}</p>
    <p class="mt-2 text-base-content/70">
      {t('about.contributorsNote')}
    </p>
    <div class="gap-2 mt-4 about-contributors-grid">
      <a href="https://github.com/aav7fl" class="btn btn-ghost btn-sm justify-start normal-case"
        >@aav7fl</a
      >
      <a href="https://github.com/farski" class="btn btn-ghost btn-sm justify-start normal-case"
        >@farski</a
      >
      <a href="https://github.com/florisre" class="btn btn-ghost btn-sm justify-start normal-case"
        >@florisre</a
      >
      <a href="https://github.com/Fotguedes" class="btn btn-ghost btn-sm justify-start normal-case"
        >@Fotguedes</a
      >
      <a
        href="https://github.com/geekworldtour"
        class="btn btn-ghost btn-sm justify-start normal-case">@geekworldtour</a
      >
      <a href="https://github.com/isZumpo" class="btn btn-ghost btn-sm justify-start normal-case"
        >@isZumpo</a
      >
      <a href="https://github.com/janvrska" class="btn btn-ghost btn-sm justify-start normal-case"
        >@janvrska</a
      >
      <a href="https://github.com/jkrauska" class="btn btn-ghost btn-sm justify-start normal-case"
        >@jkrauska</a
      >
      <a href="https://github.com/LeoColman" class="btn btn-ghost btn-sm justify-start normal-case"
        >@LeoColman</a
      >
      <a
        href="https://github.com/PeteLawrence"
        class="btn btn-ghost btn-sm justify-start normal-case">@PeteLawrence</a
      >
      <a href="https://github.com/petterip" class="btn btn-ghost btn-sm justify-start normal-case"
        >@petterip</a
      >
      <a href="https://github.com/Phaeton" class="btn btn-ghost btn-sm justify-start normal-case"
        >@Phaeton</a
      >
      <a href="https://github.com/PovilasID" class="btn btn-ghost btn-sm justify-start normal-case"
        >@PovilasID</a
      >
      <a href="https://github.com/twt--" class="btn btn-ghost btn-sm justify-start normal-case"
        >@twt--</a
      >
      <a href="https://github.com/xconverge" class="btn btn-ghost btn-sm justify-start normal-case"
        >@xconverge</a
      >
    </div>

    <div class="mt-6 bg-base-200 rounded-lg p-4">
      <p class="text-xl font-medium">{t('about.communityAcknowledgment')}</p>
      <p class="mt-2">
        {t('about.communityText')}
      </p>
      <ul class="mt-2 list-none pl-0 space-y-1">
        <li class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5 text-success"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
              clip-rule="evenodd"
            />
          </svg>
          {t('about.bugReports')}
        </li>
        <li class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5 text-success"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
              clip-rule="evenodd"
            />
          </svg>
          {t('about.featureSuggestions')}
        </li>
        <li class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5 text-success"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
              clip-rule="evenodd"
            />
          </svg>
          {t('about.testing')}
        </li>
        <li class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5 text-success"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
              clip-rule="evenodd"
            />
          </svg>
          {t('about.documentation')}
        </li>
      </ul>
    </div>
  </Card>

  <!-- Additional Credits Section -->
  <Card title={t('about.additionalCredits')} className="bg-base-100 shadow-sm">
    <p class="text-xl font-medium">{t('about.birdnetPiProject')}</p>
    <p class="mt-2">
      {t('about.birdnetPiDescription')}
    </p>
    <div class="flex gap-2 mt-4">
      <a href="https://github.com/mcguirepr89/BirdNET-Pi" class="btn btn-sm btn-outline gap-2">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="w-4 h-4"
          viewBox="0 0 24 24"
          fill="currentColor"
        >
          <path
            d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"
          />
        </svg>
        {t('about.visitBirdnetPi')}
      </a>
    </div>

    <p class="text-xl font-medium mt-6">{t('about.labelTranslations')}</p>
    <p class="mt-2">{t('about.labelTranslationsBy')}</p>
    <div class="flex gap-2 mt-4">
      <a href="https://github.com/patlevin" class="btn btn-sm btn-outline gap-2">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="w-4 h-4"
          viewBox="0 0 24 24"
          fill="currentColor"
        >
          <path
            d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.30.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"
          />
        </svg>
        {t('about.patrickLevinGithub')}
      </a>
    </div>
  </Card>

  <!-- Version Information -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body">
      <h2 class="card-title">{t('about.versionInformation')}</h2>
      <div class="space-y-4">
        <p class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z"
              clip-rule="evenodd"
            />
          </svg>
          {t('about.currentVersion')}:
          <span class="font-mono">{settings.version || t('about.developmentBuild')}</span>
        </p>
        <p class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M10 18a8 8 0 100-16 8 8 0 000 16zm1-12a1 1 0 10-2 0v4a1 1 0 00.293.707l2.828 2.829a1 1 0 101.415-1.415L11 9.586V6z"
              clip-rule="evenodd"
            />
          </svg>
          {t('about.buildDate')}:
          <span class="font-mono">{settings.buildDate || t('about.unknown')}</span>
        </p>
      </div>
    </div>
  </div>

  <!-- License Information -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body">
      <h2 class="card-title">{t('about.licenseInformation')}</h2>
      <div class="space-y-4">
        <p>
          {t('about.licenseText')}
          <a
            href="https://creativecommons.org/licenses/by-nc-sa/4.0/"
            class="link link-primary"
            target="_blank"
            rel="noopener noreferrer">{t('about.ccLicense')}</a
          >.
        </p>
        <div class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M4 4a2 2 0 012-2h4.586A2 2 0 0112 2.586L15.414 6A2 2 0 0116 7.414V16a2 2 0 01-2 2H6a2 2 0 01-2-2V4z"
              clip-rule="evenodd"
            />
          </svg>
          <span>{t('about.licenseDescription')}</span>
        </div>
        <div class="flex items-center gap-2">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="w-5 h-5"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M10 2c-1.716 0-3.408.106-5.07.31C3.806 2.45 3 3.414 3 4.517V17.25a.75.75 0 001.075.676L10 15.082l5.925 2.844A.75.75 0 0017 17.25V4.517c0-1.103-.806-2.068-1.93-2.207A41.403 41.403 0 0010 2z"
              clip-rule="evenodd"
            />
          </svg>
          <a
            href="/assets/LICENSES.md"
            class="link link-primary"
            target="_blank"
            rel="noopener noreferrer">{t('about.dependencyLicenses')}</a
          >
        </div>
      </div>
    </div>
  </div>
</div>

<style>
  .about-developers-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .about-developers-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .about-contributors-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .about-contributors-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }
</style>
