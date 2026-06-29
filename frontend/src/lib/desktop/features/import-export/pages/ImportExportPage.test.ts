import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import ImportExportPage from './ImportExportPage.svelte';

// Mock the wizard component so we don't need to test its internals here.
// Must return a valid Svelte 5 component (a function).
vi.mock('../components/BirdNetPiImportWizard.svelte', () => ({
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

  it('shows the BirdNET-Pi import card title and description', () => {
    render(ImportExportPage);
    expect(screen.getByText('system.importExport.birdnetPi.cardTitle')).toBeInTheDocument();
    expect(screen.getByText('system.importExport.birdnetPi.cardDescription')).toBeInTheDocument();
  });

  it('shows the BirdNET-Pi start button', () => {
    render(ImportExportPage);
    const button = screen.getByRole('button', {
      name: /system.importExport.birdnetPi.startButton/,
    });
    expect(button).toBeInTheDocument();
    expect(button).not.toBeDisabled();
  });

  it('shows export section as coming soon', () => {
    render(ImportExportPage);
    expect(screen.getByText('system.importExport.comingSoon')).toBeInTheDocument();
  });

  it('export button is disabled', () => {
    render(ImportExportPage);
    const exportButton = screen.getByRole('button', {
      name: /system.importExport.export.startButton/,
    });
    expect(exportButton).toBeDisabled();
  });

  it('export disabled button has a visible reason', () => {
    render(ImportExportPage);
    // The disabled reason text should appear exactly once in the DOM
    const reasons = screen.getAllByText('system.importExport.export.disabledReason');
    expect(reasons).toHaveLength(1);
  });

  it('export disabled button has aria-describedby pointing to the reason', () => {
    render(ImportExportPage);
    const exportButton = screen.getByRole('button', {
      name: /system.importExport.export.startButton/,
    });
    expect(exportButton).toHaveAttribute('aria-describedby', 'export-disabled-reason');
  });

  it('wizard is not shown initially', async () => {
    const { default: WizardMock } = await import('../components/BirdNetPiImportWizard.svelte');
    render(ImportExportPage);
    // The wizard mock component should not have been called before clicking start
    expect(vi.mocked(WizardMock)).not.toHaveBeenCalled();
  });

  it('clicking start import button opens the wizard', async () => {
    const { default: WizardMock } = await import('../components/BirdNetPiImportWizard.svelte');
    render(ImportExportPage);
    const startButton = screen.getByRole('button', {
      name: /system.importExport.birdnetPi.startButton/,
    });
    await fireEvent.click(startButton);
    // The wizard component should have been mounted after clicking start
    expect(vi.mocked(WizardMock)).toHaveBeenCalled();
  });
});
