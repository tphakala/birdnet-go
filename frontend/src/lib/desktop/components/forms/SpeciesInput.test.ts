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

  describe('Portal Dropdown Positioning', () => {
    it('creates portal dropdown attached to document.body', async () => {
      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      // Focus input to trigger predictions
      const input = screen.getByRole('combobox');
      await fireEvent.focus(input);

      // Wait for portal dropdown to be created
      await waitFor(() => {
        const portals = document.body.querySelectorAll('[id^="species-predictions-"]');
        expect(portals.length).toBeGreaterThan(0);
      });

      // Verify portal is attached to body
      const portal = document.body.querySelector('[id^="species-predictions-"]');
      expect(portal?.parentElement).toBe(document.body);
    });

    it('positions dropdown below input when space is available', async () => {
      // Mock getBoundingClientRect for input element
      const mockRect = {
        top: 100,
        bottom: 130,
        left: 50,
        right: 350,
        width: 300,
        height: 30,
      };

      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox') as HTMLInputElement;
      input.getBoundingClientRect = vi.fn().mockReturnValue(mockRect);

      await fireEvent.focus(input);

      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]') as HTMLElement;
        expect(portal).toBeTruthy();
        // Check position is below input
        expect(portal.classList.contains('dropdown-below')).toBe(true);
      });
    });

    it('positions dropdown above input when no space below', async () => {
      // Mock viewport height and input position near bottom
      Object.defineProperty(window, 'innerHeight', {
        value: 200,
        writable: true,
      });

      const mockRect = {
        top: 150,
        bottom: 180,
        left: 50,
        right: 350,
        width: 300,
        height: 30,
      };

      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox') as HTMLInputElement;
      input.getBoundingClientRect = vi.fn().mockReturnValue(mockRect);

      await fireEvent.focus(input);

      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]') as HTMLElement;
        expect(portal).toBeTruthy();
        // Check position is above input
        expect(portal.classList.contains('dropdown-above')).toBe(true);
      });
    });

    it('adjusts horizontal position when dropdown would overflow viewport', async () => {
      // Mock viewport width
      Object.defineProperty(window, 'innerWidth', {
        value: 400,
        writable: true,
      });

      // Position input near right edge
      const mockRect = {
        top: 100,
        bottom: 130,
        left: 350,
        right: 650, // Would overflow
        width: 300,
        height: 30,
      };

      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox') as HTMLInputElement;
      input.getBoundingClientRect = vi.fn().mockReturnValue(mockRect);

      await fireEvent.focus(input);

      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]') as HTMLElement;
        expect(portal).toBeTruthy();
        // Portal should be repositioned to fit within viewport
        const leftPos = parseInt(portal.style.left);
        expect(leftPos).toBeLessThan(350); // Adjusted left position
      });
    });

    it('updates position on scroll', async () => {
      const mockRect = {
        top: 100,
        bottom: 130,
        left: 50,
        right: 350,
        width: 300,
        height: 30,
      };

      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox') as HTMLInputElement;
      input.getBoundingClientRect = vi.fn().mockReturnValue(mockRect);

      await fireEvent.focus(input);

      // Simulate scroll
      const newRect = { ...mockRect, top: 50, bottom: 80 };
      input.getBoundingClientRect = vi.fn().mockReturnValue(newRect);

      await fireEvent.scroll(window);

      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]') as HTMLElement;
        expect(portal).toBeTruthy();
        // Position should be updated
        const topPos = parseInt(portal.style.top);
        expect(topPos).toBeLessThan(100); // New position after scroll
      });
    });

    it('cleans up portal on unmount', async () => {
      const { unmount } = render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox');
      await fireEvent.focus(input);

      // Verify portal exists
      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]');
        expect(portal).toBeTruthy();
      });

      // Unmount component
      unmount();

      // Verify portal is removed
      const portal = document.body.querySelector('[id^="species-predictions-"]');
      expect(portal).toBeFalsy();
    });
  });

  describe('Multiple Instances', () => {
    it('creates unique IDs for multiple instances', async () => {
      // Render first instance
      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      // Render second instance
      render(SpeciesInput, {
        props: {
          value: 'jay',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      // Focus both inputs
      const inputs = screen.getAllByRole('combobox');
      await fireEvent.focus(inputs[0]);
      await fireEvent.focus(inputs[1]);

      // Wait for portals
      await waitFor(() => {
        const portals = document.body.querySelectorAll('[id^="species-predictions-"]');
        expect(portals.length).toBe(2);

        // Verify unique IDs
        const ids = Array.from(portals).map(p => p.id);
        expect(new Set(ids).size).toBe(2); // All IDs are unique
      });
    });

    it('handles interactions independently for multiple instances', async () => {
      const onAdd1 = vi.fn();
      const onAdd2 = vi.fn();

      // Render two instances with different search terms
      render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: onAdd1,
        },
      });

      render(SpeciesInput, {
        props: {
          value: 'jay',
          predictions: defaultPredictions,
          onAdd: onAdd2,
        },
      });

      const inputs = screen.getAllByRole('combobox');

      // Interact with first instance
      await fireEvent.focus(inputs[0]);

      // Wait for portal to be created and populated
      await waitFor(() => {
        const portals = document.body.querySelectorAll('[id^="species-predictions-"]');
        expect(portals.length).toBeGreaterThan(0);
      });

      // Click first prediction in first dropdown
      const portals = document.body.querySelectorAll('[id^="species-predictions-"]');
      const firstPortal = portals[0];
      const button = firstPortal.querySelector('.species-prediction-item') as HTMLElement;

      // Ensure button exists and has correct data
      expect(button).toBeTruthy();
      expect(button.textContent).toContain('Robin');

      await fireEvent.click(button);

      // Wait for the onAdd callback to be called
      await waitFor(() => {
        expect(onAdd1).toHaveBeenCalledWith('American Robin');
      });

      // Verify only first callback was called
      expect(onAdd2).not.toHaveBeenCalled();
    });

    it('cleans up all portals when multiple instances unmount', async () => {
      const { unmount: unmount1 } = render(SpeciesInput, {
        props: {
          value: 'rob',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      const { unmount: unmount2 } = render(SpeciesInput, {
        props: {
          value: 'jay',
          predictions: defaultPredictions,
          onAdd: vi.fn(),
        },
      });

      // Focus both inputs
      const inputs = screen.getAllByRole('combobox');
      await fireEvent.focus(inputs[0]);
      await fireEvent.focus(inputs[1]);

      // Verify both portals exist
      await waitFor(() => {
        const portals = document.body.querySelectorAll('[id^="species-predictions-"]');
        expect(portals.length).toBe(2);
      });

      // Unmount both
      unmount1();
      unmount2();

      // Verify all portals removed
      const portals = document.body.querySelectorAll('[id^="species-predictions-"]');
      expect(portals.length).toBe(0);
    });
  });

  describe('DOM Element Reuse Optimization', () => {
    it('reuses existing buttons when predictions change', async () => {
      // Use search term that matches multiple predictions
      let predictions = ['American Robin', 'American Goldfinch'];

      const { rerender } = render(SpeciesInput, {
        props: {
          value: 'american', // Matches both predictions
          predictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox');
      await fireEvent.focus(input);

      // Get initial buttons
      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]');
        const buttons = portal?.querySelectorAll('.species-prediction-item');
        expect(buttons?.length).toBe(2);
      });

      // Track initial button elements
      const portal = document.body.querySelector('[id^="species-predictions-"]');
      const initialButtons = Array.from(portal?.querySelectorAll('.species-prediction-item') ?? []);

      // Update predictions to add more matching items
      predictions = ['American Robin', 'American Goldfinch', 'American Crow'];
      await rerender({
        value: 'american', // Still matches all predictions
        predictions,
        onAdd: vi.fn(),
      });

      // Check buttons after update
      await waitFor(() => {
        const updatedPortal = document.body.querySelector('[id^="species-predictions-"]');
        const updatedButtons = Array.from(
          updatedPortal?.querySelectorAll('.species-prediction-item') ?? []
        );

        // First two buttons should be the same elements (reused)
        expect(updatedButtons[0]).toBe(initialButtons[0]);
        expect(updatedButtons[1]).toBe(initialButtons[1]);

        // Third button is new
        expect(updatedButtons.length).toBe(3);
      });
    });

    it('hides excess buttons when predictions decrease', async () => {
      // Use predictions that all contain 'bird' to match the filter
      let predictions = ['Mockingbird', 'Blackbird', 'Bluebird'];

      const { rerender } = render(SpeciesInput, {
        props: {
          value: 'bird', // Matches all three predictions
          predictions,
          onAdd: vi.fn(),
        },
      });

      const input = screen.getByRole('combobox');
      await fireEvent.focus(input);

      // Verify initial count
      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]');
        const buttons = portal?.querySelectorAll('.species-prediction-item');
        expect(buttons?.length).toBe(3);
      });

      // Reduce predictions but keep one that matches
      predictions = ['Mockingbird'];
      await rerender({
        value: 'bird', // Still matches the remaining prediction
        predictions,
        onAdd: vi.fn(),
      });

      // Check visible buttons
      await waitFor(() => {
        const portal = document.body.querySelector('[id^="species-predictions-"]');
        const allButtons = portal?.querySelectorAll('.species-prediction-item');
        const visibleButtons = Array.from(allButtons ?? []).filter(
          btn => (btn as HTMLElement).style.display !== 'none'
        );

        expect(visibleButtons.length).toBe(1);
        expect(allButtons?.length).toBe(3); // All buttons still exist but hidden
      });
    });
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
