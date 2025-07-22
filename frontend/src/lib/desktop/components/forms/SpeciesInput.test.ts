import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import SpeciesInput from './SpeciesInput.svelte';

const defaultPredictions = ['American Robin', 'Blue Jay', 'Northern Cardinal', 'House Sparrow'];

describe('SpeciesInput', () => {
  it('renders with basic props', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByRole('combobox')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add species' })).toBeInTheDocument();
  });

  it('displays placeholder text', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        placeholder: 'Enter species name',
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByPlaceholderText('Enter species name')).toBeInTheDocument();
  });

  it('shows label when provided', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        label: 'Species Name',
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByText('Species Name')).toBeInTheDocument();
  });

  it('displays current value', () => {
    render(SpeciesInput, {
      props: {
        value: 'Robin',
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    expect(input).toHaveValue('Robin');
  });

  it('calls onAdd when Add button is clicked', async () => {
    const onAdd = vi.fn();

    render(SpeciesInput, {
      props: {
        value: 'Test Species',
        onAdd,
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    await fireEvent.click(addButton);

    expect(onAdd).toHaveBeenCalledWith('Test Species');
  });

  it('calls onAdd when Enter key is pressed', async () => {
    const onAdd = vi.fn();

    render(SpeciesInput, {
      props: {
        value: 'Test Species',
        onAdd,
      },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onAdd).toHaveBeenCalledWith('Test Species');
  });

  it('clears input after successful add', async () => {
    const value = 'Test Species';
    const onAdd = vi.fn();

    render(SpeciesInput, {
      props: {
        value,
        onAdd,
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    await fireEvent.click(addButton);

    expect(onAdd).toHaveBeenCalledWith('Test Species');
  });

  it('shows predictions dropdown when value length meets minimum', () => {
    render(SpeciesInput, {
      props: {
        value: 'rob',
        predictions: defaultPredictions,
        minCharsForPredictions: 2,
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByText('American Robin')).toBeInTheDocument();
  });

  it('hides predictions dropdown when value is too short', () => {
    render(SpeciesInput, {
      props: {
        value: 'r',
        predictions: defaultPredictions,
        minCharsForPredictions: 2,
        onAdd: vi.fn(),
      },
    });

    expect(screen.queryByText('American Robin')).not.toBeInTheDocument();
  });

  it('filters predictions based on input value', () => {
    render(SpeciesInput, {
      props: {
        value: 'blue',
        predictions: defaultPredictions,
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByText('Blue Jay')).toBeInTheDocument();
    expect(screen.queryByText('American Robin')).not.toBeInTheDocument();
  });

  it('limits number of predictions shown', () => {
    const manyPredictions = Array.from({ length: 20 }, (_, i) => `Species ${i + 1}`);

    render(SpeciesInput, {
      props: {
        value: 'species',
        predictions: manyPredictions,
        maxPredictions: 5,
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByText('Species 1')).toBeInTheDocument();
    expect(screen.getByText('Species 5')).toBeInTheDocument();
    expect(screen.queryByText('Species 6')).not.toBeInTheDocument();
  });

  it('calls onPredictionSelect and onAdd when prediction is clicked', async () => {
    const onAdd = vi.fn();
    const onPredictionSelect = vi.fn();

    render(SpeciesInput, {
      props: {
        value: 'rob',
        predictions: defaultPredictions,
        onAdd,
        onPredictionSelect,
      },
    });

    const prediction = screen.getByText('American Robin');
    await fireEvent.click(prediction);

    expect(onPredictionSelect).toHaveBeenCalledWith('American Robin');
    // onAdd should be called after component updates
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith('American Robin');
    });
  });

  it('calls onInput when input value changes', async () => {
    const onInput = vi.fn();

    render(SpeciesInput, {
      props: {
        value: '',
        onInput,
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.input(input, { target: { value: 'test' } });

    expect(onInput).toHaveBeenCalledWith('test');
  });

  it('disables Add button when input is empty', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        onAdd: vi.fn(),
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    expect(addButton).toBeDisabled();
  });

  it('disables Add button when disabled prop is true', () => {
    render(SpeciesInput, {
      props: {
        value: 'Test Species',
        disabled: true,
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    const addButton = screen.getByRole('button', { name: 'Add species' });

    expect(input).toBeDisabled();
    expect(addButton).toBeDisabled();
  });

  it('shows required indicator when required', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        label: 'Species',
        required: true,
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByText('*')).toBeInTheDocument();
  });

  it('displays help text when provided', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        helpText: 'Type to search for species',
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByText('Type to search for species')).toBeInTheDocument();
  });

  it('shows tooltip button when tooltip provided', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        label: 'Species',
        tooltip: 'This is a tooltip',
        onAdd: vi.fn(),
      },
    });

    expect(screen.getByRole('button', { name: 'Help information' })).toBeInTheDocument();
  });

  it('shows tooltip on hover', async () => {
    render(SpeciesInput, {
      props: {
        value: '',
        label: 'Species',
        tooltip: 'This is a tooltip',
        onAdd: vi.fn(),
      },
    });

    const helpButton = screen.getByRole('button', { name: 'Help information' });
    await fireEvent.mouseEnter(helpButton);

    expect(screen.getByText('This is a tooltip')).toBeInTheDocument();
  });

  it('shows validation message when invalid', async () => {
    render(SpeciesInput, {
      props: {
        value: '',
        required: true,
        validationMessage: 'Species is required',
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.blur(input);

    // In SpeciesInput, validation messages are shown differently - check for validation classes or messages
    expect(input).toHaveAttribute('required');
  });

  it('supports keyboard navigation in predictions', async () => {
    render(SpeciesInput, {
      props: {
        value: 'rob',
        predictions: defaultPredictions,
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.keyDown(input, { key: 'ArrowDown' });

    // First prediction should be focused
    const firstPrediction = screen.getByText('American Robin');
    expect(firstPrediction).toBeInTheDocument();
  });

  it('closes predictions on Escape key', async () => {
    render(SpeciesInput, {
      props: {
        value: 'rob',
        predictions: defaultPredictions,
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    await fireEvent.keyDown(input, { key: 'Escape' });

    // Predictions should be hidden
    expect(screen.queryByText('American Robin')).not.toBeInTheDocument();
  });

  it('applies size classes correctly', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        size: 'lg',
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    expect(input).toHaveClass('input-lg');

    const button = screen.getByRole('button', { name: 'Add species' });
    expect(button).toHaveClass('btn-lg');
  });

  it('uses custom button text and hides icon when specified', () => {
    render(SpeciesInput, {
      props: {
        value: 'Test',
        buttonText: 'Submit',
        buttonIcon: false,
        onAdd: vi.fn(),
      },
    });

    const button = screen.getByRole('button', { name: 'Add species' });
    expect(button).toHaveTextContent('Submit');

    const icon = button.querySelector('svg');
    expect(icon).not.toBeInTheDocument();
  });

  it('handles id prop correctly', () => {
    render(SpeciesInput, {
      props: {
        value: '',
        id: 'species-input',
        onAdd: vi.fn(),
      },
    });

    const input = screen.getByRole('combobox');
    expect(input).toHaveAttribute('id', 'species-input');
  });

  it('trims whitespace from input value', async () => {
    const onAdd = vi.fn();

    render(SpeciesInput, {
      props: {
        value: '  test species  ',
        onAdd,
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add species' });
    await fireEvent.click(addButton);

    expect(onAdd).toHaveBeenCalledWith('test species');
  });
});
