import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createComponentTestFactory, screen } from '../../../test/render-helpers';
import NavSectionHeader from './NavSectionHeader.svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

describe('NavSectionHeader', () => {
  const headerTest = createComponentTestFactory(NavSectionHeader);

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the label text', () => {
    headerTest.render({ id: 'nav-section-explore', label: 'Explore', isCollapsed: false });
    expect(screen.getByText('Explore')).toBeInTheDocument();
  });

  it('renders with the given id attribute', () => {
    headerTest.render({ id: 'nav-section-explore', label: 'Explore', isCollapsed: false });
    const el = document.getElementById('nav-section-explore');
    expect(el).toBeInTheDocument();
  });

  it('is visible (not sr-only) when expanded', () => {
    headerTest.render({ id: 'nav-section-patterns', label: 'Patterns', isCollapsed: false });
    const el = document.getElementById('nav-section-patterns');
    expect(el).not.toHaveClass('sr-only');
  });

  it('applies sr-only when collapsed so aria-labelledby stays valid', () => {
    headerTest.render({ id: 'nav-section-patterns', label: 'Patterns', isCollapsed: true });
    const el = document.getElementById('nav-section-patterns');
    expect(el).toHaveClass('sr-only');
  });

  it('still renders label text in DOM when collapsed (keeps aria-labelledby valid)', () => {
    headerTest.render({ id: 'nav-section-env', label: 'Environment', isCollapsed: true });
    // The element should be in the DOM even if sr-only
    expect(screen.getByText('Environment')).toBeInTheDocument();
  });
});
