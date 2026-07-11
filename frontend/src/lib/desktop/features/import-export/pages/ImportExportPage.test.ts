import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import ImportExportPage from './ImportExportPage.svelte';

// Mock the wizard and activity card so we don't test their internals here.
// Must return a valid Svelte 5 component (a function).
vi.mock('../components/BirdNetPiImportWizard.svelte', () => ({
  default: vi.fn(() => null),
}));
vi.mock('../components/ImportActivityCard.svelte', () => ({
  default: vi.fn(() => null),
}));

describe('ImportExportPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders import and export section headings', () => {
    render(ImportExportPage);
    expect(screen.getByText('system.importExport.import.sectionTitle')).toBeInTheDocument();
    expect(screen.getByText('system.importExport.export.sectionTitle')).toBeInTheDocument();
  });

  it('shows the BirdNET-Pi source row with title and description', () => {
    render(ImportExportPage);
    expect(screen.getByText('system.importExport.birdnetPi.cardTitle')).toBeInTheDocument();
    expect(screen.getByText('system.importExport.birdnetPi.cardDescription')).toBeInTheDocument();
  });

  it('shows the BirdNET-Pi start button enabled', () => {
    render(ImportExportPage);
    const button = screen.getByRole('button', {
      name: /system.importExport.birdnetPi.startButton/,
    });
    expect(button).toBeInTheDocument();
    expect(button).not.toBeDisabled();
  });

  it('marks the BirdNET-Pi source as experimental', () => {
    render(ImportExportPage);
    expect(screen.getByText('system.importExport.experimental')).toBeInTheDocument();
    expect(
      screen.getByText('system.importExport.birdnetPi.experimentalNotice')
    ).toBeInTheDocument();
  });

  it('shows planned sources as coming soon without action buttons', () => {
    render(ImportExportPage);
    // birds.db upload and detections export are both planned
    expect(screen.getByText('system.importExport.birdsDbUpload.cardTitle')).toBeInTheDocument();
    expect(screen.getByText('system.importExport.export.cardTitle')).toBeInTheDocument();
    expect(screen.getAllByText('system.importExport.comingSoon')).toHaveLength(2);
    // The only button rendered by this page component (the activity card is
    // mocked) is the BirdNET-Pi import action.
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(1);
    expect(buttons[0]).toHaveAccessibleName(
      expect.stringContaining('system.importExport.birdnetPi.startButton')
    );
  });

  it('renders the import activity card and wires onOpenWizard to the wizard', async () => {
    const { default: ActivityMock } = await import('../components/ImportActivityCard.svelte');
    const { default: WizardMock } = await import('../components/BirdNetPiImportWizard.svelte');
    render(ImportExportPage);
    expect(vi.mocked(ActivityMock)).toHaveBeenCalled();
    const props = vi.mocked(ActivityMock).mock.calls[0]?.[1] as
      { refreshSignal?: number; onOpenWizard?: () => void } | undefined;
    expect(props?.refreshSignal).toBe(0);
    expect(vi.mocked(WizardMock)).not.toHaveBeenCalled();
    // The card's callback must actually open the wizard.
    props?.onOpenWizard?.();
    await Promise.resolve();
    expect(vi.mocked(WizardMock)).toHaveBeenCalled();
  });

  it('wizard is not shown initially', async () => {
    const { default: WizardMock } = await import('../components/BirdNetPiImportWizard.svelte');
    render(ImportExportPage);
    expect(vi.mocked(WizardMock)).not.toHaveBeenCalled();
  });

  it('clicking start import button opens the wizard', async () => {
    const { default: WizardMock } = await import('../components/BirdNetPiImportWizard.svelte');
    render(ImportExportPage);
    const startButton = screen.getByRole('button', {
      name: /system.importExport.birdnetPi.startButton/,
    });
    await fireEvent.click(startButton);
    expect(vi.mocked(WizardMock)).toHaveBeenCalled();
  });

  it('wizard start and close both bump the activity refresh signal', async () => {
    const { default: WizardMock } = await import('../components/BirdNetPiImportWizard.svelte');
    const { default: ActivityMock } = await import('../components/ImportActivityCard.svelte');
    render(ImportExportPage);
    const activityProps = vi.mocked(ActivityMock).mock.calls[0]?.[1] as {
      refreshSignal?: number;
    };
    expect(activityProps.refreshSignal).toBe(0);

    const startButton = screen.getByRole('button', {
      name: /system.importExport.birdnetPi.startButton/,
    });
    await fireEvent.click(startButton);
    const wizardProps = vi.mocked(WizardMock).mock.calls[0]?.[1] as {
      onClose?: () => void;
      onImportStarted?: () => void;
    };

    // A job started inside the wizard refreshes the card immediately, so it
    // picks up the running import behind the open modal.
    wizardProps.onImportStarted?.();
    // Props are live getters in Svelte 5; the mocked child sees the new value.
    expect(activityProps.refreshSignal).toBe(1);

    wizardProps.onClose?.();
    expect(activityProps.refreshSignal).toBe(2);
  });
});
