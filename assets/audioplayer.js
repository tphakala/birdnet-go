htmx.on('htmx:afterSettle', function (event) {
    // Attach the audio players
    const audioElements = document.querySelectorAll('[id^="audio-"]');
    audioElements && audioElements.forEach(audio => {
        // Check if this audio element has already been initialized
        if (audio.dataset.initialized) return;

        const id = audio.id.split('-')[1];
        const playPause = document.getElementById(`playPause-${id}`);
        const playPauseCompact = document.getElementById(`playPause-compact-${id}`);
        const progress = document.getElementById(`progress-${id}`);
        const currentTime = document.getElementById(`currentTime-${id}`);
        const playerOverlay = playPause.closest('.absolute');
        const spectrogramContainer = playerOverlay.closest('.relative');
        const positionIndicator = document.getElementById(`position-indicator-${id}`);

        let updateInterval;

        // Function to update progress
        const updateProgress = () => {
            const percent = (audio.currentTime / audio.duration) * 100;
            progress.firstElementChild.style.width = `${percent}%`;
            currentTime.textContent = formatTime(audio.currentTime);

            // Update position indicator
            positionIndicator.style.left = `${percent}%`;
            positionIndicator.style.opacity = 
                (audio.currentTime === 0 || audio.currentTime === audio.duration) ? '0' : '0.7';
        };

        // Function to start the interval
        const startInterval = () => {
            updateInterval = setInterval(updateProgress, 100);
        };

        // Function to stop the interval
        const stopInterval = () => {
            clearInterval(updateInterval);
        };

        // Function to toggle play/pause state of the audio
        const togglePlay = (e) => {
            e.stopPropagation(); // Prevent event from bubbling up
            if (audio.paused) {
                audio.play();
            } else {
                audio.pause();
            }
        };

        if (playPauseCompact) {
            // Editable translucency parameter (0 to 1, where 1 is fully opaque)
            const playerOpacity = 0.7;
            playPauseCompact.style.setProperty('--player-opacity', playerOpacity);
            // Add event listeners for play/pause buttons
            playPauseCompact.addEventListener('click', togglePlay);
        }

        // Add event listeners for play/pause buttons
        playPause.addEventListener('click', togglePlay);

        // Update play/pause button icons and start/stop interval when audio is played
        audio.addEventListener('play', () => {
            playPause.innerHTML = `
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z"></path>
                </svg>
            `;
            if (playPauseCompact)
                playPauseCompact.innerHTML = `
                    <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 9v6m4-6v6"></path>
                    </svg>
                `;
            startInterval();
        });

        // Update play/pause button icons and stop interval when audio is paused
        audio.addEventListener('pause', () => {
            playPause.innerHTML = `
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
                </svg>
            `;
            if (playPauseCompact)
                playPauseCompact.innerHTML = `
                    <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
                    </svg>
                `;
            stopInterval();
        });

        // Stop interval when audio ends
        audio.addEventListener('ended', stopInterval);

        // Initial update and interval start if audio is already playing
        if (!audio.paused) {
            updateProgress();
            startInterval();
        }

        // Allow seeking by clicking on the progress bar
        progress.addEventListener('click', (e) => {
            e.stopPropagation(); // Prevent event from bubbling up
            const rect = progress.getBoundingClientRect();
            const pos = (e.clientX - rect.left) / rect.width;
            audio.currentTime = pos * audio.duration;
            updateProgress(); // Immediately update visuals
        });

        // Seeking functionality
        if (isDesktop()) {
            spectrogramContainer.addEventListener('click', (e) => {
                if (!playerOverlay.contains(e.target)) {
                    const rect = spectrogramContainer.getBoundingClientRect();
                    const pos = (e.clientX - rect.left) / rect.width;
                    audio.currentTime = pos * audio.duration;
                    updateProgress(); // Immediately update visuals
                }
            });
        } else {
            let isDragging = false;
            spectrogramContainer.addEventListener('touchstart', (e) => { 
                if (!audio.paused && !playerOverlay.contains(e.target)) {
                    isDragging = true; 
                }
            });
            spectrogramContainer.addEventListener('touchmove', (e) => {
                if (isDragging && !playerOverlay.contains(e.target)) {
                    e.preventDefault(); // Prevent scrolling while dragging
                    const touch = e.touches[0];
                    const rect = spectrogramContainer.getBoundingClientRect();
                    const pos = (touch.clientX - rect.left) / rect.width;
                    audio.currentTime = pos * audio.duration;
                    updateProgress(); // Immediately update visuals
                }
            });
            spectrogramContainer.addEventListener('touchend', () => { isDragging = false; });
        }

        // Show full version player when hovering over the spectrogram (desktop only)
        if (isDesktop()) {
            spectrogramContainer.addEventListener('mouseenter', () => { playerOverlay.style.opacity = '1'; });
            spectrogramContainer.addEventListener('mouseleave', () => { playerOverlay.style.opacity = '0'; });
        }

        // Mark this audio element as initialized
        audio.dataset.initialized = 'true';
    });
});

// Function to format time in minutes and seconds
function formatTime(seconds) {
	const minutes = Math.floor(seconds / 60);
	const remainingSeconds = Math.floor(seconds % 60);
	return `${minutes}:${remainingSeconds.toString().padStart(2, '0')}`;
}

function isTouchDevice() {
	const hasWindowTouch = 'ontouchstart' in window;
	const hasDocumentTouch =
		window.DocumentTouch &&
		typeof document !== 'undefined' &&
		document instanceof window.DocumentTouch;

	if (hasWindowTouch || hasDocumentTouch) {
		return true;
	}

	if (typeof navigator !== 'undefined') {
		const navigatorTouch = navigator.maxTouchPoints || navigator.msMaxTouchPoints;
		return navigatorTouch > 0;
	}

	return false;
}

function isMobileUserAgent() {
	return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent);
};

function isDesktop() {
	return !isMobileUserAgent() && !isTouchDevice();
};
