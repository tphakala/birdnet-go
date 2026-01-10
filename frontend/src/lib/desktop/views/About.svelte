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
  import { Star, Github, Globe, Info, Clock, FileText, CircleCheck, User } from '@lucide/svelte';
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
    <div class="card-body flex flex-col items-center text-center">
      <div
        class="w-36 h-36 rounded-full bg-gradient-to-b from-[var(--surface-200)] to-[var(--color-base-100)] flex items-center justify-center p-1 border border-[var(--border-100)]"
      >
        <img
          src="/ui/assets/BirdNET-Go-logo.webp"
          alt={t('about.logoAlt')}
          class="w-full h-full object-contain rounded-full"
        />
      </div>
      <div class="mt-4">
        <h1 class="text-4xl font-bold">{t('about.title')}</h1>
        <p class="text-base-content opacity-70 text-lg mt-2">
          {t('about.subtitle')}
        </p>
      </div>
    </div>
  </div>

  <!-- About BirdNET-Go Section -->
  <Card title={t('about.overview')} className="bg-base-100 shadow-sm">
    <p>
      {t('about.overviewText')}
    </p>

    <div class="mt-4 p-4 bg-base-200 rounded-lg">
      <p
        class="text-sm text-base-content opacity-80 text-center flex items-center justify-center gap-2"
      >
        <span class="text-yellow-500">
          <Star class="size-5" />
        </span>
        {t('about.githubStarPrompt')}
      </p>
    </div>

    <div class="mt-4 flex justify-center">
      <a
        href="https://github.com/tphakala/birdnet-go"
        class="btn btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label={t('about.visitGithubAriaLabel')}
      >
        <Github class="size-5" />
        {t('about.viewOnGithub')}
      </a>
    </div>
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
        <User class="size-5" />
        Stefan Kahl
      </li>
      <li class="flex items-center gap-2">
        <User class="size-5" />
        Connor Wood
      </li>
      <li class="flex items-center gap-2">
        <User class="size-5" />
        Maximilian Eibl
      </li>
      <li class="flex items-center gap-2">
        <User class="size-5" />
        Holger Klinck
      </li>
    </ul>

    <div class="mt-6 flex justify-center">
      <a
        href="https://github.com/birdnet-team/BirdNET-Analyzer"
        class="btn btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label={t('about.visitBirdnetAnalyzerAriaLabel')}
      >
        <Github class="size-5" />
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
      <User class="size-5" />
      <a href="https://github.com/tphakala" class="btn btn-ghost btn-sm justify-start normal-case"
        >Tomi P. Hakala</a
      >
    </p>

    <p class="text-xl font-medium mt-6">{t('about.githubContributors')}</p>
    <p class="mt-2 text-base-content opacity-70">
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
          <div class="w-5 h-5 text-success">
            <CircleCheck class="size-5" />
          </div>
          {t('about.bugReports')}
        </li>
        <li class="flex items-center gap-2">
          <div class="w-5 h-5 text-success">
            <CircleCheck class="size-5" />
          </div>
          {t('about.featureSuggestions')}
        </li>
        <li class="flex items-center gap-2">
          <div class="w-5 h-5 text-success">
            <CircleCheck class="size-5" />
          </div>
          {t('about.testing')}
        </li>
        <li class="flex items-center gap-2">
          <div class="w-5 h-5 text-success">
            <CircleCheck class="size-5" />
          </div>
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
      <a
        href="https://github.com/mcguirepr89/BirdNET-Pi"
        class="btn btn-sm btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label="Visit BirdNET-Pi GitHub repository"
      >
        <Github class="size-5" />
        {t('about.visitBirdnetPi')}
      </a>
    </div>

    <p class="text-xl font-medium mt-6">{t('about.labelTranslations')}</p>
    <p class="mt-2">{t('about.labelTranslationsBy')}</p>
    <div class="flex gap-2 mt-4">
      <a
        href="https://github.com/patlevin"
        class="btn btn-sm btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label="Visit Patrick Levin's GitHub profile"
      >
        <Github class="size-5" />
        {t('about.patrickLevinGithub')}
      </a>
    </div>

    <p class="text-xl font-medium mt-6">{t('about.taxonomyDataTitle')}</p>
    <p class="mt-2">
      {t('about.taxonomyDataIntro')}
    </p>
    <div class="mt-2 space-y-1">
      <p class="text-sm">
        <span class="font-medium">{t('about.source')}:</span>
        {t('about.ebirdApiV2')}
      </p>
      <p class="text-sm">
        <span class="font-medium">{t('about.copyright')}:</span> © Cornell Lab of Ornithology
      </p>
      <p class="text-sm">
        <span class="font-medium">{t('about.coverage')}:</span>
        {t('about.taxonomyCoverage')}
      </p>
    </div>
    <div class="flex gap-2 mt-4">
      <a
        href="https://ebird.org"
        class="btn btn-sm btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label={t('about.visitEbird')}
      >
        <Globe class="size-5" />
        {t('about.ebirdOrg')}
      </a>
      <a
        href="https://ebird.org/science/use-ebird-data/the-ebird-taxonomy"
        class="btn btn-sm btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label={t('about.learnEbirdTaxonomy')}
      >
        <FileText class="size-5" />
        {t('common.buttons.learnMore')}
      </a>
    </div>

    <p class="text-xl font-medium mt-6">{t('about.avicommonsTitle')}</p>
    <p class="mt-2">
      {t('about.avicommonsDescription')}
    </p>
    <p class="mt-2 flex items-center gap-2">
      <User class="size-5" />
      Adam Jackson
    </p>
    <div class="flex gap-2 mt-4">
      <a
        href="https://avicommons.org"
        class="btn btn-sm btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label="Visit Avicommons website"
      >
        <Globe class="size-5" />
        avicommons.org
      </a>
      <a
        href="https://github.com/rawcomposition/avicommons"
        class="btn btn-sm btn-outline-primary gap-2"
        target="_blank"
        rel="noopener noreferrer"
        aria-label={t('about.visitAvicommonsGithub')}
      >
        <Github class="size-5" />
        {t('common.labels.github')}
      </a>
    </div>
  </Card>

  <!-- Version Information -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body">
      <h2 class="card-title">{t('about.versionInformation')}</h2>
      <div class="space-y-4">
        <p class="flex items-center gap-2">
          <Info class="size-5" />
          {t('about.currentVersion')}:
          <span class="font-mono">{settings.version || t('about.developmentBuild')}</span>
        </p>
        <p class="flex items-center gap-2">
          <Clock class="size-5" />
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
          <FileText class="size-5" />
          <span>{t('about.licenseDescription')}</span>
        </div>
        <div class="flex items-center gap-2">
          <FileText class="size-5" />
          <a
            href="/ui/assets/LICENSES.md"
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
