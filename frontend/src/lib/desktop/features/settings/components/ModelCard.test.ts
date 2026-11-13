import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import type { ComponentProps } from 'svelte';
import { fireEvent, within } from '@testing-library/svelte';
import { renderTyped, screen } from '../../../../../test/render-helpers';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import { t } from '$lib/i18n';
import type { CatalogVariant } from '$lib/types/models';
import { CHANNEL_PREVIEW } from '$lib/utils/variantSelection';
import {
  catalogEntry as entry,
  downloadProgress as progress,
} from '../../../../../test/fixtures/catalog';
import { stubLayout } from '../../../../../test/layout-helpers';
import ModelCard from './ModelCard.svelte';

const PAUSED_ID = 'gallery-action-status';
const LARGE_SPECIES_COUNT = 12012;
const ONE_MIB = 1024 * 1024;

function variant(overrides: Partial<CatalogVariant> = {}): CatalogVariant {
  return {
    id: 'v-global',
    speciesCount: 6522,
    default: true,
    installed: false,
    sizeBytes: 1_000_000,
    compatible: true,
    recommended: false,
    hardwareClass: 'amd64Cpu',
    ...overrides,
  };
}

const REGION_NAMES: ReadonlyMap<string, string> = new Map([['eu', 'Europe']]);

type CardProps = ComponentProps<typeof ModelCard>;

function renderCard(props: Partial<CardProps> & Pick<CardProps, 'entry' | 'mode'>) {
  const onAction = vi.fn();
  const result = renderTyped(ModelCard, {
    props: { regionNames: REGION_NAMES, pausedReasonId: PAUSED_ID, onAction, ...props },
  });
  return { ...result, onAction };
}

function button(action: string, name = 'Test Bird Model'): HTMLElement {
  return screen.getByRole('button', { name: `${action} ${name}` });
}

// A card notice (preview or incompatible), found by its data-tone attribute
function notice(): HTMLElement {
  const found = document.querySelector('[data-tone]');
  if (!(found instanceof HTMLElement)) throw new Error('the card shows no notice');
  return found;
}

beforeEach(() => {
  // t is a shared mock: drop the calls earlier tests made, keep its implementation
  vi.mocked(t).mockClear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ModelCard', () => {
  describe('footer by state', () => {
    it('offers an enabled Install on a compatible available entry', () => {
      renderCard({ entry: entry(), mode: 'available' });

      const install = button('analysis.gallery.install');
      expect(install).toBeEnabled();
      expect(install).not.toHaveAttribute('aria-disabled');
      expect(document.querySelector('[data-tone]')).not.toBeInTheDocument();
    });

    it('blocks Install on an incompatible available entry and explains why by the notice', () => {
      renderCard({ entry: entry({ compatible: false }), mode: 'available' });

      const install = button('analysis.gallery.install');
      expect(install).toBeEnabled();
      expect(install).toHaveAttribute('aria-disabled', 'true');
      expect(notice()).toHaveTextContent('analysis.gallery.entryIncompatible');
      expect(install).toHaveAccessibleDescription('analysis.gallery.entryIncompatible');
    });

    it('offers Reinstall and Remove on an installed entry', () => {
      renderCard({ entry: entry({ installed: true }), mode: 'installed' });

      expect(button('analysis.gallery.reinstall')).toBeEnabled();
      expect(button('analysis.gallery.remove')).toBeEnabled();
      expect(screen.queryByText('analysis.gallery.builtIn')).not.toBeInTheDocument();
    });

    it('shows the built-in pill and no Reinstall or Remove on the permanent entry', () => {
      renderCard({ entry: entry({ installed: true, permanent: true }), mode: 'installed' });

      expect(screen.getByText('analysis.gallery.builtIn')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /reinstall|remove/ })).not.toBeInTheDocument();
    });

    it('adds Optimize and the faster-build note when a better variant exists', () => {
      renderCard({
        entry: entry({ installed: true, permanent: true }),
        mode: 'installed',
        optimizeAvailable: true,
      });

      expect(button('analysis.gallery.optimize.swap')).toBeEnabled();
      expect(screen.getByText('analysis.gallery.optimize.badgeTitle')).toBeInTheDocument();
    });

    it('ignores an offer on an Available card, which only installs', () => {
      renderCard({ entry: entry(), mode: 'available', optimizeAvailable: true });

      expect(button('analysis.gallery.install')).toBeEnabled();
      expect(
        screen.queryByRole('button', { name: /analysis\.gallery\.optimize\.swap/ })
      ).not.toBeInTheDocument();
      expect(screen.queryByText('analysis.gallery.optimize.badgeTitle')).not.toBeInTheDocument();
    });

    it('shows neither Optimize nor the note without an offer', () => {
      renderCard({ entry: entry({ installed: true }), mode: 'installed' });

      expect(
        screen.queryByRole('button', { name: /analysis\.gallery\.optimize\.swap/ })
      ).not.toBeInTheDocument();
      expect(screen.queryByText('analysis.gallery.optimize.badgeTitle')).not.toBeInTheDocument();
    });
  });

  describe('actions', () => {
    it.each([
      ['available', 'analysis.gallery.install', 'install', {}],
      ['installed', 'analysis.gallery.optimize.swap', 'install', { optimizeAvailable: true }],
      ['installed', 'analysis.gallery.reinstall', 'reinstall', {}],
      ['installed', 'analysis.gallery.remove', 'remove', {}],
    ] as const)('%s card: %s emits %s', async (mode, label, action, extra) => {
      const model = entry({ installed: mode === 'installed' });
      const { onAction } = renderCard({ entry: model, mode, ...extra });

      await fireEvent.click(button(label));
      expect(onAction).toHaveBeenCalledExactlyOnceWith(action, model);
    });

    it.each([
      ['available', ['analysis.gallery.install']],
      [
        'installed',
        ['analysis.gallery.optimize.swap', 'analysis.gallery.reinstall', 'analysis.gallery.remove'],
      ],
    ] as const)('pauses every %s action while another card runs one', async (mode, labels) => {
      const { onAction } = renderCard({
        entry: entry({ installed: mode === 'installed' }),
        mode,
        optimizeAvailable: true,
        actionInFlight: true,
        busy: null,
      });

      for (const label of labels) {
        const paused = button(label);
        expect(paused).toBeEnabled();
        expect(paused).toHaveAttribute('aria-disabled', 'true');
        expect(paused).toHaveAttribute('aria-describedby', PAUSED_ID);
        await fireEvent.click(paused);
      }
      expect(onAction).not.toHaveBeenCalled();
    });

    it('runs its own action and pauses the others when only busy is passed', async () => {
      const { onAction } = renderCard({
        entry: entry({ installed: true }),
        mode: 'installed',
        busy: 'reinstall',
      });

      const reinstall = button('analysis.gallery.reinstalling');
      expect(reinstall).toBeEnabled();
      expect(reinstall).toHaveAttribute('aria-disabled', 'true');
      expect(reinstall).toHaveAttribute('aria-describedby', PAUSED_ID);
      expect(reinstall).toHaveTextContent('analysis.gallery.reinstalling');
      await fireEvent.click(reinstall);

      const remove = button('analysis.gallery.remove');
      expect(remove).toHaveAttribute('aria-disabled', 'true');
      await fireEvent.click(remove);
      expect(onAction).not.toHaveBeenCalled();
    });

    it('keeps an incompatible Install blocked by the notice, never by the paused reason', async () => {
      const { onAction } = renderCard({
        entry: entry({ compatible: false }),
        mode: 'available',
        actionInFlight: true,
      });

      const install = button('analysis.gallery.install');
      expect(install).toBeEnabled();
      expect(install).toHaveAttribute('aria-disabled', 'true');
      expect(install).not.toHaveAttribute('aria-describedby', PAUSED_ID);
      expect(install).not.toHaveAttribute('title');
      await fireEvent.click(install);
      expect(onAction).not.toHaveBeenCalled();
    });

    it.each([
      ['available', 'install', 'analysis.gallery.installing'],
      ['installed', 'remove', 'analysis.gallery.removing'],
    ] as const)(
      'a %s card running %s blocks that button, names it by its running label and ignores clicks',
      async (mode, busy, running) => {
        const { onAction } = renderCard({
          entry: entry({ installed: mode === 'installed' }),
          mode,
          busy,
        });

        const own = button(running);
        expect(own).toBeEnabled();
        expect(own).toHaveAttribute('aria-disabled', 'true');
        expect(own).toHaveAttribute('aria-describedby', PAUSED_ID);
        expect(own).toHaveTextContent(running);
        await fireEvent.click(own);
        expect(onAction).not.toHaveBeenCalled();
      }
    );

    it('titles a paused button with the reason', () => {
      renderCard({ entry: entry(), mode: 'available', actionInFlight: true });

      expect(button('analysis.gallery.install')).toHaveAttribute(
        'title',
        'analysis.gallery.actionInProgress'
      );
    });

    it('labels a running Optimize as applying', () => {
      renderCard({
        entry: entry({ installed: true }),
        mode: 'installed',
        optimizeAvailable: true,
        busy: 'install',
      });

      const optimize = button('analysis.gallery.optimize.applying');
      expect(optimize).toHaveAttribute('aria-disabled', 'true');
      expect(optimize).toHaveTextContent('analysis.gallery.optimize.applying');
    });
  });

  describe('progress', () => {
    it('shows a named progress bar with the file counter while installing', () => {
      renderCard({ entry: entry(), mode: 'available', busy: 'install', progress: progress() });

      const bar = screen.getByRole('progressbar');
      expect(bar).toHaveAttribute('aria-valuenow', '50');
      expect(bar).toHaveAccessibleName(/analysis\.gallery\.progress\.downloading Test Bird Model/);
      expect(screen.getByText(/\(1\/2\)/)).toBeInTheDocument();
    });

    it('shows the downloaded and total bytes while downloading', () => {
      renderCard({
        entry: entry(),
        mode: 'available',
        busy: 'install',
        progress: progress({ downloadedBytes: ONE_MIB, totalBytes: 2 * ONE_MIB }),
      });

      expect(screen.getByText('1 MB / 2 MB')).toBeInTheDocument();
    });

    it('reports 0 percent and no byte counts when the total size is unknown', () => {
      renderCard({
        entry: entry(),
        mode: 'available',
        busy: 'install',
        progress: progress({ totalBytes: 0, downloadedBytes: 5 }),
      });

      expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '0');
      expect(screen.queryByText(/Bytes/)).not.toBeInTheDocument();
    });

    it('shows the file counter and byte counts only while downloading', () => {
      renderCard({
        entry: entry(),
        mode: 'available',
        busy: 'install',
        progress: progress({ status: 'verifying' }),
      });

      expect(screen.getByRole('progressbar')).toHaveAccessibleName(
        /analysis\.gallery\.progress\.verifying/
      );
      expect(screen.queryByText(/\(1\/2\)/)).not.toBeInTheDocument();
      expect(screen.queryByText(/ \/ /)).not.toBeInTheDocument();
    });

    it('omits the file counter for a single file', () => {
      renderCard({
        entry: entry(),
        mode: 'available',
        busy: 'install',
        progress: progress({ totalFiles: 1 }),
      });

      expect(screen.queryByText(/\(1\/1\)/)).not.toBeInTheDocument();
    });

    it.each([
      ['install', 'available', 'analysis.gallery.progress.complete'],
      ['reinstall', 'installed', 'analysis.gallery.reinstallComplete'],
    ] as const)('a completed %s says %s', (busy, mode, text) => {
      renderCard({
        entry: entry({ installed: mode === 'installed' }),
        mode,
        busy,
        progress: progress({ status: 'complete' }),
      });

      expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
      expect(screen.getByText(text, { selector: 'p' })).toBeInTheDocument();
    });
  });

  describe('preview', () => {
    it.each(['available', 'installed'] as const)(
      'shows the pill and build label on a %s card',
      mode => {
        renderCard({
          entry: entry({ channel: CHANNEL_PREVIEW, buildLabel: 'preview3.1', installed: true }),
          mode,
        });

        expect(screen.getByText('analysis.gallery.preview.badge')).toBeInTheDocument();
        expect(vi.mocked(t)).toHaveBeenCalledWith('analysis.gallery.preview.buildLabel', {
          version: '1.0',
          build: 'preview3.1',
        });
      }
    );

    it('adds the preview notice on the Available tab only', () => {
      const preview = entry({ channel: CHANNEL_PREVIEW });
      const { unmount } = renderCard({ entry: preview, mode: 'available' });
      expect(screen.getByRole('note')).toHaveTextContent('analysis.gallery.preview.cardNotice');
      unmount();

      renderCard({ entry: { ...preview, installed: true }, mode: 'installed' });
      expect(screen.queryByRole('note')).not.toBeInTheDocument();
    });
  });

  describe('license', () => {
    it.each([
      [true, 'analysis.gallery.license.commercialUseAllowed'],
      [false, 'analysis.gallery.license.nonCommercialOnly'],
    ])('commercialUse %s shows the license and its meaning as text', (commercialUse, meaning) => {
      renderCard({ entry: entry({ commercialUse, license: 'LIC-ID' }), mode: 'available' });

      expect(screen.getByText('LIC-ID')).toBeInTheDocument();
      expect(screen.getByText(meaning)).toBeInTheDocument();
    });
  });

  describe('variants', () => {
    const installedVariant = variant({
      id: 'v-eu',
      region: 'eu',
      speciesCount: 700,
      hardwareClass: 'gpuNvidia',
    });
    const recommendedVariant = variant({ id: 'v-global', speciesCount: 6522 });
    const withVariants = entry({
      installed: true,
      variants: [installedVariant, recommendedVariant],
      installedVariantId: 'v-eu',
      recommendedVariantId: 'v-global',
    });

    function detailsValue(term: string): HTMLElement {
      const dt = screen.getByText(term, { selector: 'dt' });
      const dd = dt.nextElementSibling;
      if (!(dd instanceof HTMLElement)) throw new Error(`no value after ${term}`);
      return dd;
    }

    it('describes the installed variant on the Installed tab', () => {
      renderCard({ entry: withVariants, mode: 'installed' });

      expect(detailsValue('analysis.gallery.regionLabel')).toHaveTextContent('Europe');
      expect(detailsValue('analysis.gallery.hardwareLabel')).toHaveTextContent(
        'analysis.gallery.hardware.gpuNvidia'
      );
      expect(vi.mocked(t)).toHaveBeenCalledWith('analysis.gallery.species', { count: '700' });
    });

    it('shows the raw region slug when its name is not known', () => {
      renderCard({ entry: withVariants, mode: 'installed', regionNames: new Map() });

      expect(detailsValue('analysis.gallery.regionLabel').textContent.trim()).toBe('eu');
    });

    it('falls back to the entry region for a variant without a region', () => {
      renderCard({
        entry: entry({
          region: 'Europe',
          variants: [variant({ id: 'fp32', default: true })],
          recommendedVariantId: 'fp32',
        }),
        mode: 'available',
      });

      expect(detailsValue('analysis.gallery.regionLabel')).toHaveTextContent('Europe');
    });

    it('describes the recommended variant on the Available tab', () => {
      renderCard({ entry: withVariants, mode: 'available' });

      expect(detailsValue('analysis.gallery.regionLabel')).toHaveTextContent(
        'analysis.gallery.regionGlobal'
      );
      expect(detailsValue('analysis.gallery.hardwareLabel')).toHaveTextContent(
        'analysis.gallery.hardware.amd64Cpu'
      );
    });

    it('falls back to the default variant without a recommendation', () => {
      renderCard({
        entry: entry({
          variants: [
            variant({ id: 'v-eu', region: 'eu', default: false, hardwareClass: 'gpuNvidia' }),
            variant({ id: 'v-default', default: true, hardwareClass: 'arm64Cpu' }),
          ],
        }),
        mode: 'available',
      });

      expect(detailsValue('analysis.gallery.hardwareLabel')).toHaveTextContent(
        'analysis.gallery.hardware.arm64Cpu'
      );
    });

    it('shows a placeholder hardware value when the installed variant is gone', () => {
      renderCard({
        entry: entry({
          installed: true,
          variants: [recommendedVariant],
          installedVariantId: 'v-gone',
        }),
        mode: 'installed',
      });

      expect(detailsValue('analysis.gallery.hardwareLabel')).toHaveTextContent(/^-$/);
    });

    it('shows a flat entry region and no hardware row', () => {
      renderCard({ entry: entry({ region: 'Finland' }), mode: 'available' });

      expect(detailsValue('analysis.gallery.regionLabel')).toHaveTextContent('Finland');
      expect(screen.queryByText('analysis.gallery.hardwareLabel')).not.toBeInTheDocument();
    });

    it('omits the region row of a flat entry without a region', () => {
      renderCard({ entry: entry({ region: '' }), mode: 'available' });

      expect(screen.queryByText('analysis.gallery.regionLabel')).not.toBeInTheDocument();
    });
  });

  it('groups the digits of a large species count', () => {
    renderCard({ entry: entry({ speciesCount: LARGE_SPECIES_COUNT }), mode: 'available' });

    expect(vi.mocked(t)).toHaveBeenCalledWith('analysis.gallery.species', {
      count: expect.stringMatching(/^12\D012$/),
    });
  });

  describe('author', () => {
    it('links to the upstream page in a new tab and says so', () => {
      renderCard({
        entry: entry({ upstreamUrl: 'https://example.org/model' }),
        mode: 'available',
      });

      const link = screen.getByRole('link');
      expect(link).toHaveAttribute('href', 'https://example.org/model');
      expect(link).toHaveAttribute('target', '_blank');
      expect(link).toHaveAttribute('rel', 'noopener noreferrer');
      expect(link).toHaveAccessibleName(/^Tester.*common\.aria\.opensInNewTab/);
    });

    it('shows the author as plain text without an upstream page', () => {
      renderCard({ entry: entry(), mode: 'available' });

      expect(screen.getByText('Tester')).toBeInTheDocument();
      expect(screen.queryByRole('link')).not.toBeInTheDocument();
    });

    it.each(['javascript:alert(1)', 'data:text/html,hi'])(
      'does not link an upstream URL with an unsafe scheme (%s)',
      upstreamUrl => {
        renderCard({ entry: entry({ upstreamUrl }), mode: 'available' });

        expect(screen.queryByRole('link')).not.toBeInTheDocument();
        expect(screen.getByText('Tester')).toBeInTheDocument();
      }
    );
  });

  it('keeps a long description in full and names its toggle after the model', () => {
    const description =
      'Bat classifier for full-band recordings. Needs a 192 kHz input to hear the calls.';
    stubLayout(description);
    renderCard({ entry: entry({ description }), mode: 'available' });

    expect(screen.getByText(/192 kHz/)).toHaveTextContent(description);
    expect(screen.getByRole('button', { name: /^common\.ui\.showMore/ })).toHaveAccessibleName(
      'common.ui.showMore Test Bird Model'
    );
  });

  it.each([
    ['bird', true, true],
    ['geomodel', true, false],
    ['bird', false, false],
  ] as const)(
    'a %s entry with hasGeomodel %s shows the geomodel pill: %s',
    (category, has, shown) => {
      renderCard({ entry: entry({ category, hasGeomodel: has }), mode: 'available' });

      expect(screen.queryByText('analysis.gallery.geomodelBadge') !== null).toBe(shown);
    }
  );

  it.each([
    ['installed', 'error'],
    ['available', 'warning'],
  ] as const)('a %s card shows the incompatibility in the %s tone', (mode, tone) => {
    renderCard({ entry: entry({ installed: mode === 'installed', compatible: false }), mode });

    expect(notice()).toHaveAttribute('data-tone', tone);
    // The notice keeps its status role
    expect(notice()).toHaveAttribute('role', 'status');
  });

  it('shows the incompatibility on an installed card too', () => {
    renderCard({
      entry: entry({ installed: true, compatible: false }),
      mode: 'installed',
    });

    expect(notice()).toHaveTextContent('analysis.gallery.entryIncompatible');
  });
});

describe('ModelCard Accessibility', () => {
  it('is an article named by the model', () => {
    renderCard({ entry: entry(), mode: 'available' });

    expect(screen.getByRole('article', { name: 'Test Bird Model' })).toBeInTheDocument();
  });

  it('has no violations as an available card', async () => {
    const { container } = renderCard({
      entry: entry({ channel: CHANNEL_PREVIEW, upstreamUrl: 'https://example.org' }),
      mode: 'available',
      busy: 'install',
      progress: progress(),
    });

    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });

  it('has no violations as an installed card with paused actions', async () => {
    const { container } = renderCard({
      entry: entry({ installed: true, hasGeomodel: true }),
      mode: 'installed',
      optimizeAvailable: true,
      actionInFlight: true,
    });
    // The paused buttons point at the page's status line; stand one in for it
    const status = document.createElement('p');
    status.id = PAUSED_ID;
    status.textContent = 'busy';
    container.append(status);

    const article = within(container).getByRole('article');
    expect(article).toBeInTheDocument();
    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });
});
