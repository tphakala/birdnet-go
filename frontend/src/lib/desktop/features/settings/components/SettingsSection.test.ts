import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import SettingsSection from './SettingsSection.svelte';

describe('SettingsSection', () => {
  it('renders with basic props', () => {
    render(SettingsSection, {
      props: {
        title: 'Test Section',
        description: 'Test description',
      },
    });

    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Test Section');
    expect(screen.getByText('Test description')).toBeInTheDocument();
  });

  it('uses explicit hasChanges prop when provided', () => {
    render(SettingsSection, {
      props: {
        title: 'Test Section',
        hasChanges: true,
      },
    });

    expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
  });

  it('shows hasChanges false when explicitly set to false', () => {
    render(SettingsSection, {
      props: {
        title: 'Test Section',
        hasChanges: false,
      },
    });

    expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
  });

  it('detects changes using originalData and currentData when hasChanges not provided', () => {
    const originalData = { test: 'original' };
    const currentData = { test: 'modified' };

    render(SettingsSection, {
      props: {
        title: 'Test Section',
        originalData,
        currentData,
      },
    });

    expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
  });

  it('returns false when originalData and currentData are identical', () => {
    const data = { test: 'same' };

    render(SettingsSection, {
      props: {
        title: 'Test Section',
        originalData: data,
        currentData: data,
      },
    });

    expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
  });

  it('returns false when originalData or currentData is missing', () => {
    render(SettingsSection, {
      props: {
        title: 'Test Section',
        originalData: { test: 'data' },
        // currentData is missing
      },
    });

    expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
  });

  it('prioritizes explicit hasChanges over automatic detection', () => {
    const originalData = { test: 'original' };
    const currentData = { test: 'modified' };

    render(SettingsSection, {
      props: {
        title: 'Test Section',
        hasChanges: false, // Explicit false
        originalData,
        currentData,
        // Even though data differs, explicit hasChanges should win
      },
    });

    expect(screen.queryByRole('status', { name: 'Settings changed' })).not.toBeInTheDocument();
  });

  it('handles complex nested data changes', () => {
    const originalData = {
      audio: {
        source: 'default',
        export: { enabled: false },
      },
    };

    const currentData = {
      audio: {
        source: 'default',
        export: { enabled: true },
      },
    };

    render(SettingsSection, {
      props: {
        title: 'Audio Settings',
        originalData,
        currentData,
      },
    });

    expect(screen.getByRole('status', { name: 'Settings changed' })).toBeInTheDocument();
  });

  it('passes through other props to CollapsibleCard', () => {
    render(SettingsSection, {
      props: {
        title: 'Test Section',
        description: 'Test description',
        defaultOpen: false,
        className: 'custom-class',
      },
    });

    // These would be tested more thoroughly in integration tests
    // Here we just verify the component renders without errors
    const container = screen.getByRole('heading', { level: 3 }).closest('.collapse');
    expect(container).toHaveClass('custom-class');
  });
});
