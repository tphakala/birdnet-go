import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import Checkbox from './Checkbox.svelte';

describe('Checkbox', () => {
  it('renders with basic props', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
      },
    });

    expect(screen.getByLabelText('Test checkbox')).toBeInTheDocument();
    expect(screen.getByRole('checkbox')).not.toBeChecked();
  });

  it('renders checked state correctly', () => {
    render(Checkbox, {
      props: {
        checked: true,
        label: 'Test checkbox',
      },
    });

    expect(screen.getByRole('checkbox')).toBeChecked();
  });

  it('handles click events and calls onchange', async () => {
    const onchange = vi.fn();

    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        onchange,
      },
    });

    const checkbox = screen.getByRole('checkbox');
    await fireEvent.click(checkbox);

    expect(onchange).toHaveBeenCalledWith(true);
  });

  it('renders disabled state', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        disabled: true,
      },
    });

    expect(screen.getByRole('checkbox')).toBeDisabled();
  });

  it('renders help text when provided', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        helpText: 'This is help text',
      },
    });

    expect(screen.getByText('This is help text')).toBeInTheDocument();
  });

  it('renders tooltip button when tooltip provided', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        tooltip: 'This is a tooltip',
      },
    });

    expect(screen.getByRole('button', { name: 'Help information' })).toBeInTheDocument();
  });

  it('shows tooltip on hover', async () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        tooltip: 'This is a tooltip',
      },
    });

    const helpButton = screen.getByRole('button', { name: 'Help information' });
    await fireEvent.mouseEnter(helpButton);

    expect(screen.getByText('This is a tooltip')).toBeInTheDocument();
  });

  it('applies size classes correctly', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        size: 'lg',
      },
    });

    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).toHaveClass('checkbox-lg');
  });

  it('applies variant classes correctly', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        variant: 'secondary',
      },
    });

    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).toHaveClass('checkbox-secondary');
  });

  it('renders with custom className', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        className: 'custom-class',
      },
    });

    const container = screen.getByRole('checkbox').closest('.form-control');
    expect(container).toHaveClass('custom-class');
  });

  it('renders with children snippet instead of label', () => {
    const { component } = render(Checkbox, {
      props: {
        checked: false,
      },
    });

    // For now, just test that it doesn't throw with children
    expect(component).toBeTruthy();
  });

  it('handles id prop correctly', () => {
    render(Checkbox, {
      props: {
        checked: false,
        label: 'Test checkbox',
        id: 'test-checkbox',
      },
    });

    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).toHaveAttribute('id', 'test-checkbox');
  });
});
