import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../../test/render-helpers';
import ModelBuildNotes from './ModelBuildNotes.svelte';

describe('ModelBuildNotes', () => {
  it('shows no empty lead paragraph or bullet for blank text', () => {
    const { container } = renderTyped(ModelBuildNotes, {
      props: {
        record: {
          version: '2',
          build: 2,
          date: '2026-11-21',
          reason: '  ',
          notes: ['', 'Dogs.', ' '],
        },
      },
    });

    expect(container.querySelector('p')).toBeNull();
    expect(screen.getAllByRole('listitem').map(li => li.textContent)).toEqual(['Dogs.']);
  });
});
