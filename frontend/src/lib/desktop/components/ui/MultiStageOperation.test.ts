import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import MultiStageOperation from './MultiStageOperation.svelte';
import type { Stage } from './MultiStageOperation.types';
import type { ComponentProps } from 'svelte';

// Helper function to render MultiStageOperation with proper typing
const renderMultiStageOperation = (
  props: Partial<ComponentProps<typeof MultiStageOperation>> & { stages: Stage[] }
) => {
  return render(MultiStageOperation, { props });
};

describe('MultiStageOperation', () => {
  const mockStages: Stage[] = [
    {
      id: 'stage1',
      title: 'Preparation',
      description: 'Setting up the environment',
      status: 'completed',
    },
    {
      id: 'stage2',
      title: 'Processing',
      description: 'Processing data',
      status: 'in_progress',
      progress: 45,
      message: 'Processing 45 of 100 items',
    },
    {
      id: 'stage3',
      title: 'Validation',
      description: 'Validating results',
      status: 'pending',
    },
    {
      id: 'stage4',
      title: 'Cleanup',
      status: 'pending',
    },
  ];

  it('renders all stages', () => {
    renderMultiStageOperation({ stages: mockStages });

    expect(screen.getByText('Preparation')).toBeInTheDocument();
    expect(screen.getByText('Processing')).toBeInTheDocument();
    expect(screen.getByText('Validation')).toBeInTheDocument();
    expect(screen.getByText('Cleanup')).toBeInTheDocument();
  });

  it('shows stage descriptions', () => {
    renderMultiStageOperation({ stages: mockStages });

    expect(screen.getByText('Setting up the environment')).toBeInTheDocument();
    expect(screen.getByText('Processing data')).toBeInTheDocument();
    expect(screen.getByText('Validating results')).toBeInTheDocument();
  });

  it('displays progress for in_progress stage', () => {
    renderMultiStageOperation({ stages: mockStages });

    expect(screen.getByText('Processing 45 of 100 items')).toBeInTheDocument();
    expect(screen.getByText('45%')).toBeInTheDocument();
  });

  it('shows overall progress', () => {
    renderMultiStageOperation({ stages: mockStages });

    expect(screen.getByText('Overall Progress')).toBeInTheDocument();
    expect(screen.getByText('25%')).toBeInTheDocument(); // 1 of 4 completed
  });

  it('hides overall progress when showProgress is false', () => {
    renderMultiStageOperation({ stages: mockStages, showProgress: false });

    expect(screen.queryByText('Overall Progress')).not.toBeInTheDocument();
  });

  it('renders error state', () => {
    const stagesWithError: Stage[] = [
      ...mockStages.slice(0, 2),
      {
        id: 'stage3',
        title: 'Validation',
        status: 'error',
        error: 'Validation failed: Invalid data format',
      },
    ];

    renderMultiStageOperation({ stages: stagesWithError });

    expect(screen.getByText('Validation failed: Invalid data format')).toBeInTheDocument();
  });

  it('renders skipped state', () => {
    const stagesWithSkipped: Stage[] = [
      ...mockStages.slice(0, 2),
      {
        id: 'stage3',
        title: 'Optional Step',
        status: 'skipped',
        message: 'Skipped due to configuration',
      },
    ];

    renderMultiStageOperation({ stages: stagesWithSkipped });

    expect(screen.getByText('Skipped due to configuration')).toBeInTheDocument();
  });

  it('handles stage click when callback provided', async () => {
    const onStageClick = vi.fn();

    renderMultiStageOperation({ stages: mockStages, onStageClick });

    await fireEvent.click(screen.getByText('Processing'));

    expect(onStageClick).toHaveBeenCalledWith('stage2');
  });

  it('does not handle clicks when no callback provided', async () => {
    renderMultiStageOperation({ stages: mockStages });

    // Should not throw error
    const processingStage = screen.getByText('Processing');
    await fireEvent.click(processingStage);

    // Verify the element still exists and wasn't affected
    expect(processingStage).toBeInTheDocument();
  });

  it('renders compact variant', () => {
    renderMultiStageOperation({ stages: mockStages, variant: 'compact' });

    // Compact variant should not show descriptions
    expect(screen.queryByText('Setting up the environment')).not.toBeInTheDocument();
    // But should show titles
    expect(screen.getByText('Preparation')).toBeInTheDocument();
  });

  it('renders timeline variant', () => {
    renderMultiStageOperation({ stages: mockStages, variant: 'timeline' });

    // Timeline should show all content
    expect(screen.getByText('Preparation')).toBeInTheDocument();
    expect(screen.getByText('Setting up the environment')).toBeInTheDocument();
  });

  it('shows in_progress stage styling', () => {
    renderMultiStageOperation({
      stages: mockStages,
    });

    // The in_progress stage (Processing) should have specific styling
    const processingStage = screen.getByText('Processing').closest('.card');
    expect(processingStage).toBeInTheDocument();
  });

  it('shows step numbers in default variant', () => {
    renderMultiStageOperation({ stages: mockStages });

    expect(screen.getByText('Step 1 of 4')).toBeInTheDocument();
    expect(screen.getByText('Step 2 of 4')).toBeInTheDocument();
    expect(screen.getByText('Step 3 of 4')).toBeInTheDocument();
    expect(screen.getByText('Step 4 of 4')).toBeInTheDocument();
  });

  it('calculates overall progress excluding skipped stages', () => {
    const stagesWithSkipped: Stage[] = [
      { id: '1', title: 'Stage 1', status: 'completed' },
      { id: '2', title: 'Stage 2', status: 'completed' },
      { id: '3', title: 'Stage 3', status: 'skipped' },
      { id: '4', title: 'Stage 4', status: 'pending' },
    ];

    renderMultiStageOperation({ stages: stagesWithSkipped });

    // 2 completed out of 3 non-skipped = 67%
    expect(screen.getByText('67%')).toBeInTheDocument();
  });

  it('handles empty stages array', () => {
    renderMultiStageOperation({ stages: [] });

    expect(screen.getByText('0%')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    renderMultiStageOperation({
      stages: mockStages,
      className: 'custom-class',
    });

    expect(document.querySelector('.multi-stage-operation.custom-class')).toBeInTheDocument();
  });
});
