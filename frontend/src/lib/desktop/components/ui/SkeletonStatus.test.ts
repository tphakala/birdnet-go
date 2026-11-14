import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import SkeletonStatus from './SkeletonStatus.svelte';

describe('SkeletonStatus', () => {
  it('shows its label as visible text inside a status region', () => {
    renderTyped(SkeletonStatus, { props: { label: 'Loading...', className: 'text-xs' } });

    const status = screen.getByRole('status');
    expect(status).toHaveTextContent('Loading...');
    expect(status).toBeVisible();
    expect(status).not.toHaveClass('sr-only');
    expect(status).not.toHaveAttribute('aria-hidden');
    expect(status).toHaveClass('text-xs');
  });
});
