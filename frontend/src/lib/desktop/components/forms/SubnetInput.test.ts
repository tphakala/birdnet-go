import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import SubnetInput from './SubnetInput.svelte';

const defaultSubnets = ['192.168.1.0/24', '10.0.0.0/16'];

describe('SubnetInput', () => {
  it('renders with basic props', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('Allowed Subnets')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('192.168.1.0/24')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add subnet' })).toBeInTheDocument();
  });

  it('shows empty state when no subnets configured', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByText('No subnets configured')).toBeInTheDocument();
  });

  it('displays existing subnets', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: defaultSubnets,
        onUpdate: vi.fn(),
      },
    });

    expect(screen.getByDisplayValue('192.168.1.0/24')).toBeInTheDocument();
    expect(screen.getByDisplayValue('10.0.0.0/16')).toBeInTheDocument();
    expect(screen.getByText('Allowed Subnets (2/10):')).toBeInTheDocument();
  });

  it('adds new subnet when Add button is clicked', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: '172.16.0.0/12' } });
    await fireEvent.click(addButton);

    expect(onUpdate).toHaveBeenCalledWith(['172.16.0.0/12']);
  });

  it('adds new subnet when Enter key is pressed', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');

    await fireEvent.input(input, { target: { value: '172.16.0.0/12' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(onUpdate).toHaveBeenCalledWith(['172.16.0.0/12']);
  });

  it('clears input after adding subnet', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24') as HTMLInputElement;
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: '172.16.0.0/12' } });
    await fireEvent.click(addButton);

    expect(input.value).toBe('');
  });

  it('validates CIDR format and shows error for invalid input', async () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    await fireEvent.input(input, { target: { value: 'invalid-cidr' } });

    expect(
      screen.getByText('Invalid CIDR format. Use format like 192.168.1.0/24')
    ).toBeInTheDocument();
  });

  it('validates prefix length range', async () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    await fireEvent.input(input, { target: { value: '192.168.1.0/33' } });

    expect(screen.getByText('Prefix length must be between 0 and 32')).toBeInTheDocument();
  });

  it('validates IP address octets', async () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    await fireEvent.input(input, { target: { value: '256.168.1.0/24' } });

    expect(screen.getByText('Invalid IP address. Each octet must be 0-255')).toBeInTheDocument();
  });

  it('prevents adding duplicate subnets', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: defaultSubnets,
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: '192.168.1.0/24' } });
    await fireEvent.click(addButton);

    // Should not call onUpdate for duplicate subnet
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('removes subnet when remove button is clicked', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: defaultSubnets,
        onUpdate,
      },
    });

    const removeButtons = screen.getAllByLabelText('Remove subnet');
    await fireEvent.click(removeButtons[0]);

    expect(onUpdate).toHaveBeenCalledWith(['10.0.0.0/16']);
  });

  it('updates existing subnet when modified', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: defaultSubnets,
        onUpdate,
      },
    });

    const subnetInput = screen.getByDisplayValue('192.168.1.0/24');
    await fireEvent.input(subnetInput, { target: { value: '192.168.2.0/24' } });

    expect(onUpdate).toHaveBeenCalledWith(['192.168.2.0/24', '10.0.0.0/16']);
  });

  it('shows validation error for edited subnet', async () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: defaultSubnets,
        onUpdate: vi.fn(),
      },
    });

    const subnetInput = screen.getByDisplayValue('192.168.1.0/24');
    await fireEvent.input(subnetInput, { target: { value: 'invalid' } });

    expect(
      screen.getByText('Invalid CIDR format. Use format like 192.168.1.0/24')
    ).toBeInTheDocument();
  });

  it('disables Add button when input is empty', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    const addButton = screen.getByRole('button', { name: 'Add subnet' });
    expect(addButton).toBeDisabled();
  });

  it('disables Add button when input is invalid', async () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: 'invalid' } });

    expect(addButton).toBeDisabled();
  });

  it('enables Add button when input is valid', async () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: '172.16.0.0/12' } });

    expect(addButton).not.toBeDisabled();
  });

  it('shows maximum items warning when limit reached', () => {
    const maxSubnets = Array.from({ length: 10 }, (_, i) => `192.168.${i}.0/24`);

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: maxSubnets,
        onUpdate: vi.fn(),
        maxItems: 10,
      },
    });

    expect(screen.getByText('Maximum number of subnets (10) reached.')).toBeInTheDocument();
  });

  it('disables all inputs when disabled prop is true', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: defaultSubnets,
        onUpdate: vi.fn(),
        disabled: true,
      },
    });

    const inputs = screen.getAllByRole('textbox');
    inputs.forEach(input => {
      expect(input).toBeDisabled();
    });

    const buttons = screen.getAllByRole('button');
    buttons.forEach(button => {
      expect(button).toBeDisabled();
    });
  });

  it('shows help text when provided', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
        helpText: 'Configure subnets that bypass authentication',
      },
    });

    expect(screen.getByText('Configure subnets that bypass authentication')).toBeInTheDocument();
  });

  it('shows error message when provided', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
        error: 'Subnet configuration error',
      },
    });

    expect(screen.getByText('Subnet configuration error')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
        className: 'custom-class',
      },
    });

    const container = screen.getByText('Allowed Subnets').closest('.form-control');
    expect(container).toHaveClass('custom-class');
  });

  it('uses custom placeholder when provided', () => {
    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate: vi.fn(),
        placeholder: '10.0.0.0/8',
      },
    });

    expect(screen.getByPlaceholderText('10.0.0.0/8')).toBeInTheDocument();
  });

  it('trims whitespace from new subnet input', async () => {
    const onUpdate = vi.fn();

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: [],
        onUpdate,
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: '  172.16.0.0/12  ' } });
    await fireEvent.click(addButton);

    expect(onUpdate).toHaveBeenCalledWith(['172.16.0.0/12']);
  });

  it('handles custom maxItems limit', async () => {
    const onUpdate = vi.fn();
    const threeSubnets = ['192.168.1.0/24', '192.168.2.0/24', '192.168.3.0/24'];

    render(SubnetInput, {
      props: {
        label: 'Allowed Subnets',
        subnets: threeSubnets,
        onUpdate,
        maxItems: 3,
      },
    });

    const input = screen.getByPlaceholderText('192.168.1.0/24');
    const addButton = screen.getByRole('button', { name: 'Add subnet' });

    await fireEvent.input(input, { target: { value: '172.16.0.0/12' } });
    await fireEvent.click(addButton);

    // Should not add when max limit reached
    expect(onUpdate).not.toHaveBeenCalled();
  });
});
