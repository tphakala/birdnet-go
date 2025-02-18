htmx.on('htmx:afterSettle', function (event) {
    // Initialize Web Audio Context
    let audioContext;
    try {
        audioContext = new (window.AudioContext || window.webkitAudioContext)();
    } catch (e) {
        console.warn('Web Audio API is not supported in this browser');
    }

    // Constants
    const SLIDER_INACTIVITY_TIMEOUT_MS = 5000; // 5 seconds
    const SLIDER_MARGIN = 8; // Margin between spectrogram and slider

    // Store audio nodes for each player
    const audioNodes = new Map();

    // Track currently active gain control
    let activeGainControl = null;

    // Function to convert decibels to gain value
    const dbToGain = (db) => Math.pow(10, db / 20);

    // Function to convert gain to decibels
    const gainToDb = (gain) => 20 * Math.log10(gain);

    // Function to hide active gain control
    const hideActiveGainControl = () => {
        if (activeGainControl) {
            activeGainControl.hideSlider();
            activeGainControl = null;
        }
    };

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

        // Create volume control elements
        const volumeControl = document.createElement('div');
        volumeControl.style.cssText = 'position: absolute; top: 8px; right: 8px; z-index: 10; opacity: 0; transition: opacity 0.2s;';
        volumeControl.innerHTML = `
            <div class="volume-control-container" style="position: relative;">
                <button class="volume-button" style="padding: 4px; border-radius: 50%; background: rgba(0, 0, 0, 0.5); transition: background 0.2s;">
                    <svg width="20" height="20" viewBox="0 0 24 24" style="color: white;">
                        <path d="M12 5v14l-7-7h-3v-4h3l7-7z" fill="currentColor"/>
                        <path d="M16 8a4 4 0 0 1 0 8" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
                        <path d="M19 5a8 8 0 0 1 0 14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
                    </svg>
                </button>
            </div>
        `;

        // Create slider element separately
        const sliderElement = document.createElement('div');
        sliderElement.className = 'volume-slider';
        sliderElement.style.cssText = 'display: none; position: absolute; background: rgba(0, 0, 0, 0.5); padding: 12px 8px; border-radius: 4px; z-index: 1000;';
        sliderElement.innerHTML = `
            <div style="display: flex; flex-direction: column; align-items: center; gap: 8px;">
                <input type="range" min="0" max="12" step="3" value="0" 
                       style="writing-mode: bt-lr; -webkit-appearance: slider-vertical; height: 100px; width: 24px; background: transparent;">
                <span style="color: white; font-size: 12px;">0 dB</span>
            </div>
        `;

        // Add volume control and slider to player
        spectrogramContainer.appendChild(volumeControl);
        spectrogramContainer.appendChild(sliderElement);

        // Style the volume slider to match player theme
        const sliderStyle = document.createElement('style');
        sliderStyle.textContent = `
            .volume-button:hover {
                background: rgba(0, 0, 0, 0.7) !important;
            }
            input[type=range]::-webkit-slider-thumb {
                -webkit-appearance: none;
                height: 12px;
                width: 12px;
                border-radius: 50%;
                background: white;
                cursor: pointer;
                margin-top: -4px;
            }
            input[type=range]::-webkit-slider-runnable-track {
                width: 100%;
                height: 4px;
                background: rgba(255, 255, 255, 0.3);
                border-radius: 2px;
            }
            input[type=range]:focus {
                outline: none;
            }
        `;
        document.head.appendChild(sliderStyle);

        let sliderTimeout;
        let isSliderActive = false;

        // Add hover behavior to match other controls
        spectrogramContainer.addEventListener('mouseenter', () => {
            volumeControl.style.opacity = '1'; // Keep fully visible
        });

        // Add hover behavior to match other controls
        spectrogramContainer.addEventListener('mouseleave', () => {
            // Only reduce opacity if the slider is NOT visible
            if (!isSliderActive) {
                volumeControl.style.opacity = '0';
            }
        });

        let updateInterval;
        let audioSource;
        let gainNode;

        // Initialize Web Audio nodes if supported
        if (audioContext) {
            try {
                audioSource = audioContext.createMediaElementSource(audio);
                gainNode = audioContext.createGain();
                gainNode.gain.value = 1; // 0dB = gain of 1
                
                // Create and configure compressor for normalization
                const compressor = audioContext.createDynamicsCompressor();
                compressor.threshold.value = -24;
                compressor.knee.value = 30;
                compressor.ratio.value = 12;
                compressor.attack.value = 0.003;
                compressor.release.value = 0.25;

                // Connect the audio graph
                audioSource
                    .connect(gainNode)
                    .connect(compressor)
                    .connect(audioContext.destination);

                // Store nodes for this player
                audioNodes.set(id, { source: audioSource, gain: gainNode, compressor });

                // Add volume control interactions
                const volumeButton = volumeControl.querySelector('.volume-button');
                const sliderInput = sliderElement.querySelector('input');
                const dbDisplay = sliderElement.querySelector('span');

                // Function to calculate optimal slider position
                const calculateSliderPosition = () => {
                    const spectrogramRect = spectrogramContainer.getBoundingClientRect();
                    const sliderRect = sliderElement.getBoundingClientRect();
                    const viewportWidth = window.innerWidth;
                    
                    // Calculate position relative to the spectrogram container
                    let left = spectrogramRect.width + SLIDER_MARGIN;
                    let top = 0;

                    // Check if slider would go off the right edge of the viewport
                    if (spectrogramRect.right + SLIDER_MARGIN + sliderRect.width > viewportWidth) {
                        // Position on the left side instead
                        left = -sliderRect.width - SLIDER_MARGIN;
                    }

                    // Vertically center the slider relative to the spectrogram
                    top = (spectrogramRect.height - sliderRect.height) / 2;

                    return { top, left };
                };

                // Function to update slider position
                const updateSliderPosition = () => {
                    if (sliderElement.style.display !== 'none') {
                        const { top, left } = calculateSliderPosition();
                        sliderElement.style.top = `${top}px`;
                        sliderElement.style.left = `${left}px`;
                    }
                };

                // Function to hide slider
                const hideSlider = () => {
                    sliderElement.style.display = 'none';
                    isSliderActive = false;

                    // Only lower opacity if the user is NOT hovering over the spectrogram
                    if (!spectrogramContainer.matches(':hover')) {
                        volumeControl.style.opacity = '0';
                    }

                    if (sliderTimeout) {
                        clearTimeout(sliderTimeout);
                        sliderTimeout = null;
                    }

                    // Clear active gain control if this one is being hidden
                    if (activeGainControl && activeGainControl.id === id) {
                        activeGainControl = null;
                    }
                };

                // Function to show slider
                const showSlider = () => {
                    // Hide any other active gain control first
                    hideActiveGainControl();

                    sliderElement.style.display = 'block';
                    isSliderActive = true;
                    volumeControl.style.opacity = '1';
                    updateSliderPosition();

                    // Set this as the active gain control
                    activeGainControl = {
                        id: id,
                        hideSlider: hideSlider
                    };

                    // Reset and start inactivity timer
                    if (sliderTimeout) {
                        clearTimeout(sliderTimeout);
                    }
                    sliderTimeout = setTimeout(hideSlider, SLIDER_INACTIVITY_TIMEOUT_MS);
                };

                // Function to reset inactivity timer
                const resetTimer = () => {
                    if (sliderTimeout) {
                        clearTimeout(sliderTimeout);
                    }
                    sliderTimeout = setTimeout(hideSlider, SLIDER_INACTIVITY_TIMEOUT_MS);
                };

                // Add scroll and resize listeners for slider positioning
                window.addEventListener('scroll', updateSliderPosition, { passive: true });
                window.addEventListener('resize', () => {
                    // Hide slider on window resize
                    hideSlider();
                }, { passive: true });

                // Toggle volume slider visibility
                volumeButton.addEventListener('click', (e) => {
                    e.stopPropagation();
                    if (sliderElement.style.display === 'none') {
                        showSlider();
                    } else {
                        hideSlider();
                    }
                });

                // Add interaction listeners to reset timer
                sliderElement.addEventListener('mouseover', resetTimer);
                sliderElement.addEventListener('mousemove', resetTimer);
                sliderInput.addEventListener('input', (e) => {
                    resetTimer();
                    const dbValue = parseInt(e.target.value);
                    const gainValue = dbToGain(dbValue);
                    gainNode.gain.value = gainValue;
                    dbDisplay.textContent = `${dbValue} dB`;
                });

                // Function to handle gain adjustment via mouse wheel
                const handleWheel = (e) => {
                    if (sliderElement.style.display !== 'none') {
                        e.preventDefault(); // Prevent page scrolling
                        resetTimer();
                        const currentValue = parseInt(sliderInput.value);
                        // Determine scroll direction and adjust by step value (3dB)
                        const step = e.deltaY < 0 ? 3 : -3;
                        const newValue = Math.max(0, Math.min(12, currentValue + step));
                        
                        if (newValue !== currentValue) {
                            sliderInput.value = newValue;
                            const gainValue = dbToGain(newValue);
                            gainNode.gain.value = gainValue;
                            dbDisplay.textContent = `${newValue} dB`;
                        }
                    }
                };

                // Add mouse wheel control for gain when slider is visible
                sliderElement.addEventListener('wheel', handleWheel, { passive: false });
                spectrogramContainer.addEventListener('wheel', handleWheel, { passive: false });

                // Hide volume slider when clicking outside
                document.addEventListener('click', (e) => {
                    if (!volumeControl.contains(e.target) && !sliderElement.contains(e.target)) {
                        hideSlider();
                    }
                });

            } catch (e) {
                console.warn('Error setting up Web Audio API:', e);
            }
        }

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
            if (audioContext && audioContext.state === 'suspended') {
                audioContext.resume();
            }
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
            }, {passive: true});
            spectrogramContainer.addEventListener('touchmove', (e) => {
                if (isDragging && !playerOverlay.contains(e.target)) {
                    e.preventDefault(); // Prevent scrolling while dragging
                    const touch = e.touches[0];
                    const rect = spectrogramContainer.getBoundingClientRect();
                    const pos = (touch.clientX - rect.left) / rect.width;
                    audio.currentTime = pos * audio.duration;
                    updateProgress(); // Immediately update visuals
                }
            }, {passive: true});
            spectrogramContainer.addEventListener('touchend', () => { isDragging = false; }, {passive: true});
        }

        if (isDesktop()) {
            // On desktop show full version player when hovering over the spectrogram
            spectrogramContainer.addEventListener('mouseenter', () => { playerOverlay.style.opacity = '1'; });
            spectrogramContainer.addEventListener('mouseleave', () => { playerOverlay.style.opacity = '0'; });
            playerOverlay.style.opacity = '0';
        } else {
            // On mobile show always full version player controls
            playerOverlay.style.opacity = '1';
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

function isDesktop() {
	return !isTouchDevice();
};
