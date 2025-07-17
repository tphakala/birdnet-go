import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import SpeciesManager from './SpeciesManager.svelte';

describe('SpeciesManager', () => {
  it('renders with default props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any);

    const input = screen.getByPlaceholderText('Enter species name...');
    expect(input).toBeInTheDocument();
  });

  it('renders with label', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: { label: 'Select Species' },
    });

    expect(screen.getByText('Select Species')).toBeInTheDocument();
  });

  it('renders with help text', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: { helpText: 'Choose from available species' },
    });

    expect(screen.getByText('Choose from available species')).toBeInTheDocument();
  });

  it('adds species on Enter key', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: { onChange },
    });

    const input = screen.getByPlaceholderText('Enter species name...');

    await user.type(input, 'Robin');
    await user.keyboard('{Enter}');

    expect(onChange).toHaveBeenCalledWith(['Robin']);
    expect(input).toHaveValue('');
  });

  it('displays existing species', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin', 'Blue Jay', 'Cardinal'],
      },
    });

    expect(screen.getByText('Robin')).toBeInTheDocument();
    expect(screen.getByText('Blue Jay')).toBeInTheDocument();
    expect(screen.getByText('Cardinal')).toBeInTheDocument();
  });

  it('prevents duplicate species (case-insensitive)', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin'],
        onChange,
      },
    });

    const input = screen.getByPlaceholderText('Enter species name...');

    await user.type(input, 'ROBIN');
    await user.keyboard('{Enter}');

    expect(onChange).not.toHaveBeenCalled();
    expect(input).toHaveValue('');
  });

  it('removes species', async () => {
    const onChange = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin', 'Blue Jay'],
        onChange,
      },
    });

    const removeButtons = screen.getAllByLabelText('Remove species');
    await fireEvent.click(removeButtons[0]);

    expect(onChange).toHaveBeenCalledWith(['Blue Jay']);
  });

  it('allows editing species', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin'],
        onChange,
      },
    });

    const editButton = screen.getByLabelText('Edit species');
    await fireEvent.click(editButton);

    const editInput = screen.getByDisplayValue('Robin');
    await user.clear(editInput);
    await user.type(editInput, 'American Robin');
    await user.keyboard('{Enter}');

    expect(onChange).toHaveBeenCalledWith(['American Robin']);
  });

  it('cancels edit on Escape', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin'],
        onChange,
      },
    });

    const editButton = screen.getByLabelText('Edit species');
    await fireEvent.click(editButton);

    const editInput = screen.getByDisplayValue('Robin');
    await user.clear(editInput);
    await user.type(editInput, 'American Robin');
    await user.keyboard('{Escape}');

    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText('Robin')).toBeInTheDocument();
  });

  it('respects maxItems limit', async () => {
    const onChange = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin', 'Blue Jay'],
        maxItems: 2,
        onChange,
      },
    });

    expect(screen.queryByPlaceholderText('Enter species name...')).not.toBeInTheDocument();
    expect(screen.getByText('Maximum of 2 species reached')).toBeInTheDocument();
  });

  it('validates against allowed species', async () => {
    const onChange = vi.fn();
    const onValidate = vi.fn().mockReturnValue(false);
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        allowedSpecies: ['Robin', 'Blue Jay'],
        onValidate,
        onChange,
      },
    });

    const input = screen.getByPlaceholderText('Enter species name...');

    await user.type(input, 'Cardinal');
    await user.keyboard('{Enter}');

    expect(onValidate).toHaveBeenCalledWith('Cardinal');
    expect(onChange).not.toHaveBeenCalled();
  });

  it('shows predictions based on allowed species', async () => {
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        allowedSpecies: ['Robin', 'Blue Jay', 'Cardinal', 'Crow'],
      },
    });

    const input = screen.getByPlaceholderText('Enter species name...');
    await user.type(input, 'ro');

    await waitFor(() => {
      expect(screen.getByText('Robin')).toBeInTheDocument();
      expect(screen.getByText('Crow')).toBeInTheDocument();
      expect(screen.queryByText('Blue Jay')).not.toBeInTheDocument();
    });
  });

  it('selects prediction on click', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        allowedSpecies: ['Robin', 'Blue Jay'],
        onChange,
      },
    });

    const input = screen.getByPlaceholderText('Enter species name...');
    await user.type(input, 'ro');

    await waitFor(() => {
      expect(screen.getByText('Robin')).toBeInTheDocument();
    });

    await fireEvent.click(screen.getByText('Robin'));

    expect(onChange).toHaveBeenCalledWith(['Robin']);
  });

  it('sorts species when sortable is true', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Cardinal', 'Blue Jay', 'Robin'],
        sortable: true,
      },
    });

    const speciesElements = screen.getAllByText(/Blue Jay|Cardinal|Robin/);
    const speciesTexts = speciesElements.map(el => el.textContent);

    expect(speciesTexts).toEqual(['Blue Jay', 'Cardinal', 'Robin']);
  });

  it('disables editing when editable is false', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: ['Robin'],
        editable: false,
      },
    });

    expect(screen.queryByPlaceholderText('Enter species name...')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Edit species')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Remove species')).not.toBeInTheDocument();
  });

  it('shows empty state when not editable and no species', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(SpeciesManager as any, {
      props: {
        species: [],
        editable: false,
      },
    });

    expect(screen.getByText('No species added')).toBeInTheDocument();
  });
});
