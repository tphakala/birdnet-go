htmx.on('htmx:afterSettle', function (event) {
    // Initialize Web Audio Context
    let audioContext;
    try {
        audioContext = new (window.AudioContext || window.webkitAudioContext)();
    } catch (e) {
        console.warn('Web Audio API is not supported in this browser');
    }

    // Constants
    const GAIN_SLIDER_INACTIVITY_TIMEOUT_MS = 5000; // Hide gain slider after 5 seconds of inactivity
    const GAIN_SLIDER_MARGIN = 4; // Gain slider margin from spectrogram
    const GAIN_MAX_DB = 24; // Maximum gain value in decibels

    const FILTER_TYPES = {
        highpass: 'highpass',
        lowpass: 'lowpass',
        bandpass: 'bandpass'
    };
    

    // Utility functions for creating UI elements
    const createSlider = (className, height = 'h-32') => {
        const slider = document.createElement('div');
        slider.className = className;
        slider.style.cssText = 'display: none; position: absolute; z-index: 1000; background: rgba(0, 0, 0, 0.2); backdrop-filter: blur(4px); padding: 8px; border-radius: 4px;';
        slider.innerHTML = `
            <div class="flex flex-col items-center ${height}">
                <div class="relative h-full w-2 bg-white/50 dark:bg-white/10 rounded-full overflow-hidden">
                    <div class="absolute bottom-0 w-full rounded-full transition-all duration-100" style="height: 0%"></div>
                </div>
            </div>
        `;
        return slider;
    };

    const createControlButton = (position, content) => {
        const control = document.createElement('div');
        control.style.cssText = `position: absolute; top: 8px; ${position}: 8px; z-index: 10; opacity: 0; transition: opacity 0.2s;`;
        control.innerHTML = `
            <div class="flex items-center gap-1" style="position: relative;">
                <button class="flex items-center justify-center gap-1" style="height: 28px; padding: 0 8px; border-radius: 14px; background: rgba(0, 0, 0, 0.5); transition: background 0.2s;">
                    ${content}
                </button>
            </div>
        `;
        return control;
    };

    // Utility functions for slider management
    const createSliderManager = (sliderElement, controlElement, timeoutMs) => {
        let isActive = false;
        let timeout = null;

        const hide = () => {
            sliderElement.style.display = 'none';
            isActive = false;

            if (!sliderElement.closest('.relative').matches(':hover')) {
                controlElement.style.opacity = '0';
            }

            if (timeout) {
                clearTimeout(timeout);
                timeout = null;
            }
        };

        const show = () => {
            sliderElement.style.display = 'block';
            isActive = true;
            controlElement.style.opacity = '1';
            resetTimer();
        };

        const resetTimer = () => {
            if (timeout) {
                clearTimeout(timeout);
            }
            timeout = setTimeout(hide, timeoutMs);
        };

        const updatePosition = (container, margin) => {
            if (sliderElement.style.display !== 'none') {
                const containerRect = container.getBoundingClientRect();
                const sliderRect = sliderElement.getBoundingClientRect();
                const top = (containerRect.height - sliderRect.height) / 2;
                sliderElement.style.top = `${top}px`;
                return { containerRect, sliderRect, top };
            }
            return null;
        };

        return {
            isActive: () => isActive,
            hide,
            show,
            resetTimer,
            updatePosition
        };
    };

    // Utility function for mouse/touch event handling
    const setupSliderInteraction = (slider, updateFn) => {
        const handleStart = (e) => {
            if (e.type === 'mousedown' && e.button !== 0) return;
            
            updateFn(e);
            const moveHandler = (e) => {
                e.preventDefault();
                updateFn(e);
            };

            if (e.type === 'mousedown') {
                document.addEventListener('mousemove', moveHandler);
                document.addEventListener('mouseup', () => {
                    document.removeEventListener('mousemove', moveHandler);
                }, { once: true });
            } else {
                document.addEventListener('touchmove', moveHandler, { passive: false });
                document.addEventListener('touchend', () => {
                    document.removeEventListener('touchmove', moveHandler);
                }, { once: true });
            }
        };

        slider.addEventListener('mousedown', handleStart);
        slider.addEventListener('touchstart', handleStart);
    };

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
        const volumeControl = createControlButton('right', `
            <svg width="16" height="16" viewBox="0 0 24 24" style="color: white;">
                <path d="M12 5v14l-7-7h-3v-4h3l7-7z" fill="currentColor"/>
                <path d="M16 8a4 4 0 0 1 0 8" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
                <path d="M19 5a8 8 0 0 1 0 14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
            </svg>
            <span class="gain-display text-xs text-white">0 dB</span>
        `);

        // Create volume slider
        const sliderElement = createSlider('volume-slider');
        const sliderBar = sliderElement.querySelector('.w-full');
        sliderBar.classList.add('bg-primary');

        // Create filter control UI
        const filterControl = createControlButton('left', `
            <span class="text-xs text-white">HP: 500 Hz</span>
        `);
        filterControl.classList.add('filter-control');

        // Create filter slider
        const filterSlider = createSlider('filter-slider');
        const filterBar = filterSlider.querySelector('.w-full');
        filterBar.classList.add('bg-blue-500');

        // Add controls to player
        spectrogramContainer.appendChild(volumeControl);
        spectrogramContainer.appendChild(sliderElement);
        spectrogramContainer.appendChild(filterControl);
        spectrogramContainer.appendChild(filterSlider);

        // Initialize slider managers
        const gainManager = createSliderManager(sliderElement, volumeControl, GAIN_SLIDER_INACTIVITY_TIMEOUT_MS);
        const filterManager = createSliderManager(filterSlider, filterControl, GAIN_SLIDER_INACTIVITY_TIMEOUT_MS);

        // Show controls on hover
        spectrogramContainer.addEventListener('mouseenter', () => {
            volumeControl.style.opacity = '1';
            filterControl.style.opacity = '1';
        });

        spectrogramContainer.addEventListener('mouseleave', () => {
            if (!gainManager.isActive()) {
                volumeControl.style.opacity = '0';
            }
            if (!filterManager.isActive()) {
                filterControl.style.opacity = '0';
                filterSlider.style.display = 'none';
            }
        });

        // Initialize Web Audio nodes if supported
        if (audioContext) {
            try {
                const audioSource = audioContext.createMediaElementSource(audio);
                const gainNode = audioContext.createGain();
                gainNode.gain.value = 1; // 0dB = gain of 1
                
                // Create filters
                const highPassFilter = audioContext.createBiquadFilter();
                highPassFilter.type = 'highpass';
                highPassFilter.frequency.value = 500;
                highPassFilter.Q.value = 1;

                // Create and configure compressor for normalization
                const compressor = audioContext.createDynamicsCompressor();
                compressor.threshold.value = -24;
                compressor.knee.value = 30;
                compressor.ratio.value = 12;
                compressor.attack.value = 0.003;
                compressor.release.value = 0.25;

                // Connect the audio graph with filters
                audioSource
                    .connect(highPassFilter)
                    .connect(gainNode)
                    .connect(compressor)
                    .connect(audioContext.destination);

                // Store nodes for this player
                audioNodes.set(id, { 
                    source: audioSource, 
                    gain: gainNode, 
                    compressor,
                    filters: {
                        highPass: highPassFilter
                    }
                });

                // Function to update gain value and display
                const updateGainValue = (dbValue) => {
                    const gainValue = dbToGain(dbValue);
                    gainNode.gain.value = gainValue;
                    const gainDisplay = volumeControl.querySelector('.gain-display');
                    gainDisplay.textContent = `${dbValue > 0 ? '+' : ''}${dbValue} dB`;
                    
                    // Update slider bar height (0-GAIN_MAX_DB maps to 0-100%)
                    const heightPercent = (dbValue / GAIN_MAX_DB) * 100;
                    sliderBar.style.height = `${heightPercent}%`;
                };

                // Function to update filter frequency display
                const updateFilterDisplay = (freq) => {
                    const filterLabel = filterControl.querySelector('span');
                    filterLabel.textContent = `HP: ${Math.round(freq)} Hz`;
                    // Calculate slider position based on frequency
                    const minFreq = 20;
                    const maxFreq = 10000;
                    const pos = Math.log(freq/minFreq) / Math.log(maxFreq/minFreq);
                    filterBar.style.height = `${pos * 100}%`;
                };

                // Setup gain slider interaction
                const updateGainFromPosition = (e) => {
                    e.preventDefault();
                    gainManager.resetTimer();
                    const sliderRect = sliderElement.querySelector('.relative').getBoundingClientRect();
                    let y = e.type.includes('touch') ? e.touches[0].clientY : e.clientY;
                    
                    let pos = (sliderRect.bottom - y) / sliderRect.height;
                    pos = Math.max(0, Math.min(1, pos));
                    
                    const dbValue = Math.round(pos * GAIN_MAX_DB);
                    updateGainValue(dbValue);
                };

                // Setup filter slider interaction
                const updateFilterFrequency = (e) => {
                    e.preventDefault();
                    filterManager.resetTimer();
                    const sliderRect = filterSlider.querySelector('.relative').getBoundingClientRect();
                    let y = e.type.includes('touch') ? e.touches[0].clientY : e.clientY;
                    
                    let pos = (sliderRect.bottom - y) / sliderRect.height;
                    pos = Math.max(0, Math.min(1, pos));
                    
                    const minFreq = 20;
                    const maxFreq = 10000;
                    const freq = Math.round(minFreq * Math.pow(maxFreq/minFreq, pos));
                    
                    highPassFilter.frequency.value = freq;
                    updateFilterDisplay(freq);
                };

                // Setup slider interactions
                setupSliderInteraction(sliderElement, updateGainFromPosition);
                setupSliderInteraction(filterSlider, updateFilterFrequency);

                // Volume button click handler
                volumeControl.querySelector('button').addEventListener('click', (e) => {
                    e.stopPropagation();
                    if (!gainManager.isActive()) {
                        if (filterManager.isActive()) {
                            filterManager.hide();
                        }
                        hideActiveGainControl();
                        gainManager.show();
                        const { containerRect, sliderRect } = gainManager.updatePosition(spectrogramContainer, GAIN_SLIDER_MARGIN);
                        sliderElement.style.left = `${containerRect.width + GAIN_SLIDER_MARGIN}px`;
                        activeGainControl = {
                            id: id,
                            hideSlider: gainManager.hide
                        };
                    } else {
                        gainManager.hide();
                    }
                });

                // Filter button click handler
                filterControl.querySelector('button').addEventListener('click', (e) => {
                    e.stopPropagation();
                    if (!filterManager.isActive()) {
                        if (gainManager.isActive()) {
                            gainManager.hide();
                        }
                        filterManager.show();
                        const { containerRect, sliderRect } = filterManager.updatePosition(spectrogramContainer, GAIN_SLIDER_MARGIN);
                        filterSlider.style.left = `${-(sliderRect.width + GAIN_SLIDER_MARGIN)}px`;
                    } else {
                        filterManager.hide();
                    }
                });

                // Add a unified wheel event handler for the spectrogram container
                spectrogramContainer.addEventListener('wheel', (e) => {
                    if (gainManager.isActive() || filterManager.isActive()) {
                        e.preventDefault();
                        
                        if (gainManager.isActive()) {
                            gainManager.resetTimer();
                            const currentDb = Math.round(gainToDb(gainNode.gain.value));
                            const step = e.deltaY < 0 ? 1 : -1;
                            const newValue = Math.max(0, Math.min(GAIN_MAX_DB, currentDb + step));
                            
                            if (newValue !== currentDb) {
                                updateGainValue(newValue);
                            }
                        } else if (filterManager.isActive()) {
                            filterManager.resetTimer();
                            const currentFreq = highPassFilter.frequency.value;
                            const direction = e.deltaY > 0 ? 0.97 : 1.03;
                            const newFreq = Math.min(10000, Math.max(20, currentFreq * direction));
                            highPassFilter.frequency.value = newFreq;
                            updateFilterDisplay(newFreq);
                        }
                    }
                }, { passive: false });

                // Handle window resize
                window.addEventListener('resize', () => {
                    if (gainManager.isActive()) {
                        gainManager.hide();
                    }
                    if (filterManager.isActive()) {
                        filterManager.hide();
                    }
                });

                // Set initial displays
                updateGainValue(0);
                updateFilterDisplay(highPassFilter.frequency.value);

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

                let updateInterval;
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
                        // Ignore clicks if they are on the player overlay, sliders, or their controls
                        if (!playerOverlay.contains(e.target) && 
                            !sliderElement.contains(e.target) && 
                            !filterSlider.contains(e.target) &&
                            !volumeControl.contains(e.target) &&
                            !filterControl.contains(e.target)) {
                            const rect = spectrogramContainer.getBoundingClientRect();
                            const pos = (e.clientX - rect.left) / rect.width;
                            audio.currentTime = pos * audio.duration;
                            updateProgress(); // Immediately update visuals
                        }
                    });
                } else {
                    let isDragging = false;
                    spectrogramContainer.addEventListener('touchstart', (e) => { 
                        // Ignore touch if it starts on the player overlay, sliders, or their controls
                        if (!audio.paused && 
                            !playerOverlay.contains(e.target) &&
                            !sliderElement.contains(e.target) && 
                            !filterSlider.contains(e.target) &&
                            !volumeControl.contains(e.target) &&
                            !filterControl.contains(e.target)) {
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

            } catch (e) {
                console.warn('Error setting up Web Audio API:', e);
            }
        }
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
