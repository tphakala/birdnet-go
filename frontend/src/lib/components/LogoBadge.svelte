<!--
LogoBadge.svelte - Stylized badge logo for BirdNET-Go

A compact, stylized badge that can be used as an alternative to the bird image logo.
Designed for use in collapsed sidebars or compact UI areas.

Props:
- size?: 'sm' | 'md' | 'lg' - Badge size (default: 'md')
- variant?: 'sunset' | 'ocean' | 'forest' | 'aurora' | 'amber' | 'violet' | 'rose' | 'scheme' | 'solid'
  Each color scheme has a dedicated vibrant gradient. 'scheme' is a fallback for custom schemes.
  'solid' uses a flat --color-primary background without gradient.
- className?: string - Additional CSS classes
-->
<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import { Bird } from '@lucide/svelte';

  interface Props {
    size?: 'sm' | 'md' | 'lg';
    variant?:
      | 'sunset'
      | 'ocean'
      | 'forest'
      | 'aurora'
      | 'amber'
      | 'violet'
      | 'rose'
      | 'scheme'
      | 'solid';
    className?: string;
  }

  let { size = 'md', variant = 'sunset', className = '' }: Props = $props();

  // Size classes for badge dimensions and icon size
  function getSizeClass(s: typeof size): string {
    if (s === 'sm') return 'h-7 w-7';
    if (s === 'lg') return 'h-11 w-11';
    return 'h-9 w-9'; // md default
  }

  function getIconSize(s: typeof size): number {
    if (s === 'sm') return 16;
    if (s === 'lg') return 26;
    return 22; // md default
  }

  // Vibrant gradient variants — each scheme gets a handcrafted 3-stop gradient
  function getVariantClass(v: typeof variant): string {
    if (v === 'solid') return 'logo-badge-solid';
    if (v === 'scheme') return 'logo-badge-scheme';
    if (v === 'ocean') return 'logo-badge-ocean';
    if (v === 'forest') return 'logo-badge-forest';
    if (v === 'aurora') return 'logo-badge-aurora';
    if (v === 'amber') return 'logo-badge-amber';
    if (v === 'violet') return 'logo-badge-violet';
    if (v === 'rose') return 'logo-badge-rose';
    return 'logo-badge-sunset'; // sunset default
  }
</script>

<div
  class={cn(
    'logo-badge flex items-center justify-center rounded-xl select-none shrink-0 transition-all duration-200',
    getSizeClass(size),
    getVariantClass(variant),
    className
  )}
  aria-hidden="true"
>
  <Bird size={getIconSize(size)} strokeWidth={2.5} class="drop-shadow-sm" />
</div>

<style>
  /* Base badge styles */
  .logo-badge {
    position: relative;
    color: white;
    box-shadow:
      0 4px 14px -2px rgba(0, 0, 0, 0.25),
      0 0 20px -5px var(--glow-color, rgba(255, 100, 50, 0.4));
  }

  /* Sunset gradient - warm oranges to pinks */
  .logo-badge-sunset {
    background: linear-gradient(135deg, #ff6b35 0%, #f72585 50%, #7209b7 100%);

    --glow-color: rgba(247, 37, 133, 0.5);
  }

  /* Ocean gradient - teals to blues */
  .logo-badge-ocean {
    background: linear-gradient(135deg, #00d4aa 0%, #0096c7 50%, #023e8a 100%);

    --glow-color: rgba(0, 150, 199, 0.5);
  }

  /* Forest gradient - greens to teals */
  .logo-badge-forest {
    background: linear-gradient(135deg, #84cc16 0%, #22c55e 50%, #059669 100%);

    --glow-color: rgba(34, 197, 94, 0.5);
  }

  /* Aurora gradient - purples to cyans */
  .logo-badge-aurora {
    background: linear-gradient(135deg, #a855f7 0%, #6366f1 50%, #06b6d4 100%);

    --glow-color: rgba(99, 102, 241, 0.5);
  }

  /* Amber gradient - warm golds to deep oranges */
  .logo-badge-amber {
    background: linear-gradient(135deg, #fbbf24 0%, #d97706 50%, #c2410c 100%);

    --glow-color: rgba(217, 119, 6, 0.5);
  }

  /* Violet gradient - lavenders to deep purples */
  .logo-badge-violet {
    background: linear-gradient(135deg, #c084fc 0%, #7c3aed 50%, #4c1d95 100%);

    --glow-color: rgba(124, 58, 237, 0.5);
  }

  /* Rose gradient - pinks to deep reds */
  .logo-badge-rose {
    background: linear-gradient(135deg, #fda4af 0%, #e11d48 50%, #9f1239 100%);

    --glow-color: rgba(225, 29, 72, 0.5);
  }

  /* Scheme gradient - fallback for custom schemes */
  .logo-badge-scheme {
    background: linear-gradient(
      135deg,
      color-mix(in srgb, var(--color-primary) 70%, white) 0%,
      var(--color-primary) 45%,
      color-mix(in srgb, var(--color-accent) 70%, black) 100%
    );
    color: var(--color-primary-content);
    transition:
      background 0.3s ease,
      box-shadow 0.3s ease;

    --glow-color: color-mix(in srgb, var(--color-primary) 50%, transparent);
  }

  /* Solid - flat color from active scheme */
  .logo-badge-solid {
    background: var(--color-primary);
    color: var(--color-primary-content);
    transition:
      background 0.3s ease,
      box-shadow 0.3s ease;

    --glow-color: color-mix(in srgb, var(--color-primary) 50%, transparent);
  }
</style>
