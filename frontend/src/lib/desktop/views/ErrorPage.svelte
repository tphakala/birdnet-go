<script lang="ts">
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn.js';

  interface Props {
    className?: string;
  }

  let { className = '' }: Props = $props();

  let zeroElement: HTMLElement;
  let gameContainer: HTMLElement;
  let scoreDisplay: HTMLElement;
  let currentScoreDisplay: HTMLElement;
  let timerDisplay: HTMLElement;
  let highScoreDisplay: HTMLElement;

  // Game Configuration
  const birds = ['🐦', '🦜', '🦢', '🦆', '🦅', '🦉'];
  const minSpawnDelay = 600;
  const maxSpawnDelay = 1500;
  const minStayTime = 2000;
  const maxStayTime = 4000;
  const isTouchDevice =
    typeof window !== 'undefined' && ('ontouchstart' in window || navigator.maxTouchPoints > 0);

  // Game State
  let isGameActive = $state(false);
  let score = $state(0);
  let startTime = $state(0);
  let timerInterval: ReturnType<typeof setTimeout> | undefined;
  let featherInterval: ReturnType<typeof setTimeout> | undefined;
  let highScore = $state({
    score: 0,
    time: 0,
  });

  function formatTime(ms: number): string {
    const seconds = Math.floor(ms / 1000);
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    return `${minutes}:${remainingSeconds.toString().padStart(2, '0')}`;
  }

  function updateTimer(): void {
    if (!isGameActive) return;
    const elapsed = Date.now() - startTime;
    if (timerDisplay) {
      timerDisplay.textContent = `Time: ${formatTime(elapsed)}`;
    }
  }

  function updateScore(): void {
    if (currentScoreDisplay) {
      currentScoreDisplay.textContent = `Birds Spotted: ${score}`;
    }
  }

  function updateHighScore(): void {
    const currentTime = Date.now() - startTime;
    if (score > highScore.score || (score === highScore.score && currentTime < highScore.time)) {
      highScore = { score, time: currentTime };
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem('birdSpotterHighScore', score.toString());
        localStorage.setItem('birdSpotterHighTime', currentTime.toString());
      }
    }
    if (highScoreDisplay) {
      highScoreDisplay.textContent = `Best: ${highScore.score} birds in ${formatTime(highScore.time)}`;
    }
  }

  function createSpottingEffect(x: number, y: number): void {
    const ring = document.createElement('div');
    ring.className = 'spotting-ring text-base-content';
    ring.style.left = x + 'px';
    ring.style.top = y + 'px';
    gameContainer.appendChild(ring);
    setTimeout(() => ring.remove(), 500);
  }

  function createFeather(): void {
    if (isGameActive) return;
    const feather = document.createElement('div');
    feather.className = 'feather';
    feather.textContent = ['🪶', '❃', '❋'][Math.floor(Math.random() * 3)];
    zeroElement.appendChild(feather);
    setTimeout(() => feather.remove(), 3000);
  }

  function startGame(): void {
    isGameActive = true;
    score = 0;
    startTime = Date.now();
    updateScore();
    scoreDisplay.classList.add('visible');
    zeroElement.classList.add('flying');
    if (featherInterval) clearInterval(featherInterval);
    zeroElement.querySelectorAll('.feather').forEach(f => f.remove());
    if (!isTouchDevice) {
      document.body.classList.add('cursor-binoculars');
    }
    timerInterval = setInterval(updateTimer, 1000);
    spawnBird();
  }

  function endGame(): void {
    isGameActive = false;
    zeroElement.classList.remove('flying');
    scoreDisplay.classList.remove('visible');
    document.body.classList.remove('cursor-binoculars');
    if (timerInterval) clearInterval(timerInterval);
    // Restart feather animation
    featherInterval = setInterval(() => {
      if (Math.random() < 0.3) {
        createFeather();
      }
    }, 4000);
    Array.from(gameContainer.children).forEach(bird => {
      if (!bird.classList.contains('spotting-ring')) {
        bird.remove();
      }
    });
  }

  function spawnBird(): void {
    if (!isGameActive) return;

    const bird = document.createElement('div');
    bird.className = 'game-bird' + (isTouchDevice ? '' : ' cursor-binoculars');
    bird.textContent = birds[Math.floor(Math.random() * birds.length)];
    bird.style.left = Math.random() * (window.innerWidth - 50) + 'px';
    bird.style.top = Math.random() * (window.innerHeight - 50) + 'px';

    const handleSpot = (event: Event): void => {
      if (bird.classList.contains('spotted')) return;

      score += 1;
      updateScore();
      bird.classList.add('spotted');

      const touchEvent = event as { touches?: Array<{ clientX: number; clientY: number }> };
      const mouseEvent = event as { clientX?: number; clientY?: number };
      const x = touchEvent.touches?.[0]?.clientX || mouseEvent.clientX || 0;
      const y = touchEvent.touches?.[0]?.clientY || mouseEvent.clientY || 0;
      createSpottingEffect(x, y);

      setTimeout(() => {
        bird.style.opacity = '0';
        setTimeout(() => bird.remove(), 300);
      }, 500);

      updateHighScore();
    };

    bird.addEventListener('click', handleSpot);
    bird.addEventListener('touchstart', handleSpot);

    gameContainer.appendChild(bird);

    // Make bird fly away after a random time
    setTimeout(
      () => {
        if (bird.parentNode && !bird.classList.contains('spotted')) {
          const direction = Math.random() > 0.5 ? 1 : -1;
          bird.style.transform = `translateX(${direction * window.innerWidth}px)`;
          bird.style.opacity = '0';
          setTimeout(() => bird.parentNode?.removeChild(bird), 1000);
        }
      },
      minStayTime + Math.random() * (maxStayTime - minStayTime)
    );

    // Schedule next bird
    setTimeout(spawnBird, minSpawnDelay + Math.random() * (maxSpawnDelay - minSpawnDelay));
  }

  function handleZeroClick(): void {
    if (!isGameActive) {
      startGame();
    }
  }

  function handleZeroKeydown(event: KeyboardEvent): void {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      handleZeroClick();
    }
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && isGameActive) {
      endGame();
    }
  }

  onMount(() => {
    // Load high score from localStorage
    if (typeof localStorage !== 'undefined') {
      highScore = {
        score: parseInt(localStorage.getItem('birdSpotterHighScore') || '0'),
        time: parseInt(localStorage.getItem('birdSpotterHighTime') || '0'),
      };
    }

    // Initialize feather animation
    featherInterval = setInterval(() => {
      if (Math.random() < 0.3) {
        createFeather();
      }
    }, 4000);

    // Create initial feather after a short delay
    setTimeout(createFeather, 1000);

    // Add keyboard listener
    document.addEventListener('keydown', handleKeydown);

    return () => {
      if (timerInterval) clearInterval(timerInterval);
      if (featherInterval) clearInterval(featherInterval);
      document.removeEventListener('keydown', handleKeydown);
    };
  });
</script>

<svelte:head>
  <title>404 - Page Not Found</title>
  <style>
    /* Custom binoculars cursor - black outline for light theme */
    .cursor-binoculars {
      cursor:
        url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='24' height='24' viewBox='0 0 24 24' fill='none' stroke='black' stroke-width='1.5'%3E%3Ccircle cx='8' cy='14' r='3.5'/%3E%3Ccircle cx='16' cy='14' r='3.5'/%3E%3Cpath d='M8 11V8h8v3'/%3E%3C/svg%3E")
          12 12,
        crosshair;
    }

    /* Custom binoculars cursor - white outline for dark theme */
    [data-theme='dark'] .cursor-binoculars {
      cursor:
        url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='24' height='24' viewBox='0 0 24 24' fill='none' stroke='white' stroke-width='1.5'%3E%3Ccircle cx='8' cy='14' r='3.5'/%3E%3Ccircle cx='16' cy='14' r='3.5'/%3E%3Cpath d='M8 11V8h8v3'/%3E%3C/svg%3E")
          12 12,
        crosshair;
    }

    /* Touch device support */
    @media (hover: none) {
      .cursor-binoculars {
        cursor: default;
      }

      .game-bird::before {
        content: '👁';
        position: absolute;
        top: -1.5rem;
        left: 50%;
        transform: translateX(-50%);
        font-size: 1.5rem;
        opacity: 0.7;
      }
    }

    @keyframes fly {
      0% {
        transform: translate(0, 0) rotate(0deg);
      }

      25% {
        transform: translate(10px, -10px) rotate(5deg);
      }

      50% {
        transform: translate(0, -15px) rotate(0deg);
      }

      75% {
        transform: translate(-10px, -10px) rotate(-5deg);
      }

      100% {
        transform: translate(0, 0) rotate(0deg);
      }
    }

    @keyframes gentle-pulse {
      0%,
      100% {
        opacity: 1;
      }

      50% {
        opacity: 0.7;
      }
    }

    @keyframes feather-fall {
      0% {
        transform: translate(-50%, 0) rotate(0deg);
        opacity: 1;
      }

      100% {
        transform: translate(calc(-50% + 30px), 100px) rotate(45deg);
        opacity: 0;
      }
    }

    .bird {
      cursor: pointer;
      user-select: none;
      transition: all 0.3s ease;
      position: relative;
      display: inline-block;
    }

    .bird:hover {
      transform: scale(1.1);
      filter: brightness(1.2);
      animation: gentle-bounce 1s infinite;
    }

    .bird::after {
      content: '👀';
      position: absolute;
      top: -1.5em;
      left: 50%;
      transform: translateX(-50%) translateY(10px);
      font-size: 0.4em;
      opacity: 0;
      transition: all 0.3s ease;
      pointer-events: none;
      white-space: nowrap;
    }

    .bird:hover::after {
      opacity: 1;
      transform: translateX(-50%) translateY(0);
    }

    @keyframes gentle-bounce {
      0%,
      100% {
        transform: translateY(0) scale(1.1);
      }

      50% {
        transform: translateY(-3px) scale(1.1);
      }
    }

    .bird.flying {
      animation: gentle-pulse 2s infinite;
      transform: scale(1.1);
    }

    .bird.caught {
      transform: scale(1.2);
      filter: brightness(1.2);
    }

    #game-container {
      position: fixed;
      inset: 0;
      pointer-events: none;
      z-index: 50;
    }

    .game-bird {
      position: absolute;
      font-size: 2rem;
      cursor: pointer;
      pointer-events: auto;
      transition: all 0.2s ease;
      animation: fly 1s infinite;
      touch-action: manipulation;
    }

    .game-bird.spotted {
      animation: none;
      filter: brightness(1.2) sepia(0.3);
    }

    .score-display {
      position: fixed;
      top: 1rem;
      right: 1rem;
      padding: 0.5rem 1rem;
      border-radius: 0.5rem;
      font-weight: bold;
      opacity: 0;
      transition: opacity 0.3s ease;
      display: flex;
      flex-direction: column;
      gap: 0.5rem;
      min-width: 200px;
      text-align: right;
    }

    .score-display.visible {
      opacity: 1;
    }

    .high-score {
      font-size: 0.875rem;
      opacity: 0.8;
    }

    .timer {
      font-size: 0.875rem;
      color: var(--fallback-p, oklch(var(--p) / 1));
    }

    /* Spotting effect */
    .spotting-ring {
      position: absolute;
      border: 2px solid currentcolor;
      border-radius: 50%;
      pointer-events: none;
      opacity: 0;
      transform: translate(-50%, -50%) scale(0.5);
      animation: spotting-ring 0.5s ease-out forwards;
    }

    @keyframes spotting-ring {
      0% {
        opacity: 1;
        width: 30px;
        height: 30px;
      }

      100% {
        opacity: 0;
        width: 60px;
        height: 60px;
      }
    }

    @media (hover: none) {
      .bird:hover {
        animation: none;
        transform: none;
      }

      .bird::after {
        display: none;
      }
    }

    .feather {
      position: absolute;
      left: 50%;
      top: 100%;
      font-size: 0.4em;
      pointer-events: none;
      animation: feather-fall 3s ease-in-out forwards;
    }
  </style>
</svelte:head>

<div class={cn('min-h-screen bg-base-200 flex items-center justify-center', className)}>
  <div class="text-center p-8 rounded-lg bg-base-100 shadow-lg">
    <h1 class="text-6xl font-bold text-base-content mb-4">
      4<span
        class="bird"
        bind:this={zeroElement}
        onclick={handleZeroClick}
        onkeydown={handleZeroKeydown}
        role="button"
        tabindex="0">0</span
      >4
    </h1>
    <h2 class="text-3xl font-semibold text-base-content/70 mb-8">Page Not Found</h2>

    <!-- Link Button -->
    <a
      href="/"
      class="btn btn-primary normal-case text-base font-semibold hover:bg-primary-focus transition duration-300"
    >
      Go to Dashboard
    </a>
  </div>

  <!-- Game Container -->
  <div bind:this={gameContainer} id="game-container"></div>
  <div bind:this={scoreDisplay} class="score-display bg-base-100 shadow-lg text-base-content">
    <div bind:this={currentScoreDisplay} class="current-score">Birds Spotted: 0</div>
    <div bind:this={timerDisplay} class="timer">Time: 0:00</div>
    <div bind:this={highScoreDisplay} class="high-score">Best: 0 birds in 0:00</div>
  </div>
</div>
