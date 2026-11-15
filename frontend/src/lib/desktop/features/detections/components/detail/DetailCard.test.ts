import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import DetailCard from './DetailCard.svelte';

describe('DetailCard header', () => {
  afterEach(() => cleanup());

  // Regression: a subtitle given without a title was dropped with the whole header.
  it('shows a subtitle given without a title', () => {
    render(DetailCard, { props: { subtitle: 'Last 30 days' } });

    expect(screen.getByText('Last 30 days')).toBeInTheDocument();
  });

  it('leaves the header out when there is nothing to put in it', () => {
    const { container } = render(DetailCard, { props: {} });

    expect(container.querySelector('header')).toBeNull();
  });
});
