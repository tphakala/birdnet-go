import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import LanguageSelector from './LanguageSelector.svelte';

describe('LanguageSelector Accessibility', () => {
  it('describes the trigger with the aria-describedby prop', () => {
    renderTyped(LanguageSelector, {
      props: { id: 'language-field', 'aria-describedby': 'language-help' },
    });

    expect(document.getElementById('language-field')).toHaveAttribute(
      'aria-describedby',
      'language-help'
    );
    expect(screen.getAllByRole('button')).toHaveLength(1);
  });

  it('adds no aria-describedby without the prop', () => {
    renderTyped(LanguageSelector, { props: { id: 'language-field' } });

    expect(document.getElementById('language-field')).not.toHaveAttribute('aria-describedby');
  });
});
