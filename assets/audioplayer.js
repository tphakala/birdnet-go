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
            <div class="volume-control-container flex items-center gap-1" style="position: relative;">
                <span class="gain-display text-xs text-white bg-black/50 mb-1 flex items-center" style="height: 28px; line-height: 28px;">0 dB</span>
                <button class="volume-button flex items-center justify-center" style="height: 28px; width: 28px; border-radius: 50%; background: rgba(0, 0, 0, 0.5); transition: background 0.2s;">
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
        sliderElement.style.cssText = 'display: none; position: absolute; z-index: 1000; background: rgba(0, 0, 0, 0.2); backdrop-filter: blur(4px); padding: 8px; border-radius: 4px;';
        sliderElement.innerHTML = `
            <div class="flex flex-col items-center h-full">
                <div class="relative h-full w-2 bg-white/50 dark:bg-white/10 rounded-full overflow-hidden">
                    <div class="absolute bottom-0 w-full bg-primary rounded-full transition-all duration-100" style="height: 0%"></div>
                </div>
            </div>
        `;

        // Add volume control and slider to player
        spectrogramContainer.appendChild(volumeControl);
        spectrogramContainer.appendChild(sliderElement);

        let sliderTimeout;
        let isSliderActive = false;

        // Function to update slider height to match container
        const updateSliderHeight = () => {
            const containerHeight = spectrogramContainer.clientHeight;
            sliderElement.style.height = `${containerHeight}px`;
        };

        // Initial height update
        updateSliderHeight();

        // Add hover behavior to match other controls
        spectrogramContainer.addEventListener('mouseenter', () => {
            volumeControl.style.opacity = '1';
        });

        spectrogramContainer.addEventListener('mouseleave', () => {
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

                // Add volume control interactions
                const volumeButton = volumeControl.querySelector('.volume-button');
                const gainDisplay = volumeControl.querySelector('.gain-display');
                const sliderBar = sliderElement.querySelector('.bg-primary');

                // Function to calculate optimal slider position
                const calculateSliderPosition = () => {
                    const spectrogramRect = spectrogramContainer.getBoundingClientRect();
                    const sliderRect = sliderElement.getBoundingClientRect();
                    const viewportWidth = window.innerWidth;
                    
                    // Position slider closer to the spectrogram
                    let left = spectrogramRect.width + GAIN_SLIDER_MARGIN;
                    let top = 0;

                    if (spectrogramRect.right + GAIN_SLIDER_MARGIN + sliderRect.width > viewportWidth) {
                        // If slider would go off screen, position it on the left side
                        left = -(sliderRect.width + GAIN_SLIDER_MARGIN);
                    }

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

                    if (!spectrogramContainer.matches(':hover')) {
                        volumeControl.style.opacity = '0';
                    }

                    if (sliderTimeout) {
                        clearTimeout(sliderTimeout);
                        sliderTimeout = null;
                    }

                    if (activeGainControl && activeGainControl.id === id) {
                        activeGainControl = null;
                    }
                };

                // Add window resize listener after hideSlider is defined
                const resizeHandler = () => {
                    if (isSliderActive) {
                        hideSlider();
                    }
                    updateSliderHeight();
                };
                window.addEventListener('resize', resizeHandler);

                // Function to show slider
                const showSlider = () => {
                    hideActiveGainControl();

                    sliderElement.style.display = 'block';
                    isSliderActive = true;
                    volumeControl.style.opacity = '1';
                    updateSliderPosition();
                    updateSliderHeight(); // Update height when showing

                    activeGainControl = {
                        id: id,
                        hideSlider: hideSlider
                    };

                    if (sliderTimeout) {
                        clearTimeout(sliderTimeout);
                    }
                    sliderTimeout = setTimeout(hideSlider, GAIN_SLIDER_INACTIVITY_TIMEOUT_MS);
                };

                // Function to reset inactivity timer
                const resetTimer = () => {
                    if (sliderTimeout) {
                        clearTimeout(sliderTimeout);
                    }
                    sliderTimeout = setTimeout(hideSlider, GAIN_SLIDER_INACTIVITY_TIMEOUT_MS);
                };

                // Function to update gain value and display
                const updateGainValue = (dbValue) => {
                    const gainValue = dbToGain(dbValue);
                    gainNode.gain.value = gainValue;
                    gainDisplay.textContent = `${dbValue > 0 ? '+' : ''}${dbValue} dB`;
                    
                    // Update slider bar height (0-GAIN_MAX_DB maps to 0-100%)
                    const heightPercent = (dbValue / GAIN_MAX_DB) * 100;
                    sliderBar.style.height = `${heightPercent}%`;
                };

                // Function to handle slider interaction
                const handleSliderInteraction = (e) => {
                    if (!isSliderActive) return;
                    
                    e.preventDefault();
                    resetTimer();

                    const sliderRect = sliderElement.querySelector('.relative').getBoundingClientRect();
                    let y = e.type.includes('touch') ? e.touches[0].clientY : e.clientY;
                    
                    // Calculate position (0 at top, 1 at bottom)
                    let pos = (sliderRect.bottom - y) / sliderRect.height;
                    pos = Math.max(0, Math.min(1, pos));
                    
                    // Map position to dB value (0-GAIN_MAX_DB)
                    const dbValue = Math.round(pos * GAIN_MAX_DB);
                    updateGainValue(dbValue);
                };

                // Add mouse/touch event listeners for slider interaction
                sliderElement.addEventListener('mousedown', (e) => {
                    if (e.button === 0) { // Left click only
                        handleSliderInteraction(e);
                        document.addEventListener('mousemove', handleSliderInteraction);
                        document.addEventListener('mouseup', () => {
                            document.removeEventListener('mousemove', handleSliderInteraction);
                        }, { once: true });
                    }
                });

                sliderElement.addEventListener('touchstart', (e) => {
                    handleSliderInteraction(e);
                    document.addEventListener('touchmove', handleSliderInteraction, { passive: false });
                    document.addEventListener('touchend', () => {
                        document.removeEventListener('touchmove', handleSliderInteraction);
                    }, { once: true });
                });

                // Toggle volume slider visibility
                volumeButton.addEventListener('click', (e) => {
                    e.stopPropagation();
                    if (sliderElement.style.display === 'none') {
                        showSlider();
                    } else {
                        hideSlider();
                    }
                });

                // Add mouse wheel control for gain
                const handleWheel = (e) => {
                    e.preventDefault(); // Prevent page scrolling
                    
                    // Show slider if it's not already visible
                    if (sliderElement.style.display === 'none') {
                        showSlider();
                    }
                    
                    resetTimer();

                    const currentDb = Math.round(gainToDb(gainNode.gain.value));
                    // Determine scroll direction and adjust by 1dB
                    const step = e.deltaY < 0 ? 1 : -1;
                    const newValue = Math.max(0, Math.min(GAIN_MAX_DB, currentDb + step));
                    
                    if (newValue !== currentDb) {
                        updateGainValue(newValue);
                    }
                };

                // Add wheel event listeners to both spectrogram and slider
                sliderElement.addEventListener('wheel', handleWheel, { passive: false });
                spectrogramContainer.addEventListener('wheel', handleWheel, { passive: false });

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

                // Create filter control UI
                const filterControl = document.createElement('div');
                filterControl.className = 'filter-control';
                filterControl.style.cssText = 'position: absolute; top: 8px; left: 8px; z-index: 10; opacity: 0; transition: opacity 0.2s;';
                filterControl.innerHTML = `
                    <div class="filter-control-container flex items-center gap-1" style="position: relative;">
                        <button class="filter-button flex items-center justify-center" style="height: 28px; padding: 0 8px; border-radius: 14px; background: rgba(0, 0, 0, 0.5); transition: background 0.2s;">
                            <span class="text-xs text-white">HP: 500 Hz</span>
                        </button>
                    </div>
                `;

                // Create filter slider
                const filterSlider = document.createElement('div');
                filterSlider.className = 'filter-slider';
                filterSlider.style.cssText = 'display: none; position: absolute; z-index: 1000; background: rgba(0, 0, 0, 0.2); backdrop-filter: blur(4px); padding: 8px; border-radius: 4px;';
                filterSlider.innerHTML = `
                    <div class="flex flex-col items-center h-32">
                        <div class="relative h-full w-2 bg-white/50 dark:bg-white/10 rounded-full overflow-hidden">
                            <div class="absolute bottom-0 w-full bg-blue-500 rounded-full transition-all duration-100" style="height: 50%"></div>
                        </div>
                    </div>
                `;

                // Add filter controls to player
                spectrogramContainer.appendChild(filterControl);
                spectrogramContainer.appendChild(filterSlider);

                // Show controls on hover
                spectrogramContainer.addEventListener('mouseenter', () => {
                    filterControl.style.opacity = '1';
                });

                spectrogramContainer.addEventListener('mouseleave', () => {
                    filterControl.style.opacity = '0';
                    if (!isFilterSliderActive) {
                        filterSlider.style.display = 'none';
                    }
                });

                // Add filter control logic
                let isFilterSliderActive = false;
                const filterButton = filterControl.querySelector('.filter-button');
                const filterLabel = filterButton.querySelector('span');
                const filterBar = filterSlider.querySelector('.bg-blue-500');
                
                // Initialize filter frequency display
                const updateFilterDisplay = (freq) => {
                    filterLabel.textContent = `HP: ${Math.round(freq)} Hz`;
                    // Calculate slider position based on frequency
                    const minFreq = 20;
                    const maxFreq = 10000;
                    const pos = Math.log(freq/minFreq) / Math.log(maxFreq/minFreq);
                    filterBar.style.height = `${pos * 100}%`;
                };

                // Set initial display
                updateFilterDisplay(highPassFilter.frequency.value);

                filterButton.addEventListener('click', (e) => {
                    e.stopPropagation();
                    isFilterSliderActive = !isFilterSliderActive;
                    filterSlider.style.display = isFilterSliderActive ? 'block' : 'none';
                    
                    if (isFilterSliderActive) {
                        // Calculate optimal slider position
                        const spectrogramRect = spectrogramContainer.getBoundingClientRect();
                        const sliderRect = filterSlider.getBoundingClientRect();
                        
                        // Position slider to the left of the spectrogram
                        let left = -(sliderRect.width + GAIN_SLIDER_MARGIN);
                        let top = (spectrogramRect.height - sliderRect.height) / 2;

                        filterSlider.style.top = `${top}px`;
                        filterSlider.style.left = `${left}px`;
                    }
                });

                // Add a resize handler to update the filter slider position
                const updateFilterSliderPosition = () => {
                    if (filterSlider.style.display !== 'none') {
                        const spectrogramRect = spectrogramContainer.getBoundingClientRect();
                        const sliderRect = filterSlider.getBoundingClientRect();
                        
                        let left = -(sliderRect.width + GAIN_SLIDER_MARGIN);
                        let top = (spectrogramRect.height - sliderRect.height) / 2;

                        filterSlider.style.top = `${top}px`;
                        filterSlider.style.left = `${left}px`;
                    }
                };

                // Add the resize handler to your existing window resize listener
                window.addEventListener('resize', () => {
                    if (isFilterSliderActive) {
                        updateFilterSliderPosition();
                    }
                });

                // Handle filter slider interaction
                const updateFilterFrequency = (e) => {
                    e.preventDefault();
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

                // Add mouse and touch event listeners for filter slider
                filterSlider.addEventListener('mousedown', (e) => {
                    if (e.button === 0) {
                        updateFilterFrequency(e);
                        document.addEventListener('mousemove', updateFilterFrequency);
                        document.addEventListener('mouseup', () => {
                            document.removeEventListener('mousemove', updateFilterFrequency);
                        }, { once: true });
                    }
                });

                filterSlider.addEventListener('touchstart', (e) => {
                    updateFilterFrequency(e);
                    document.addEventListener('touchmove', updateFilterFrequency, { passive: false });
                    document.addEventListener('touchend', () => {
                        document.removeEventListener('touchmove', updateFilterFrequency);
                    }, { once: true });
                });

                // Add wheel event listener for fine adjustment
                filterSlider.addEventListener('wheel', (e) => {
                    e.preventDefault();
                    const currentFreq = highPassFilter.frequency.value;
                    const direction = e.deltaY > 0 ? 0.97 : 1.03;
                    const newFreq = Math.min(10000, Math.max(20, currentFreq * direction));
                    highPassFilter.frequency.value = newFreq;
                    updateFilterDisplay(newFreq);
                }, { passive: false });

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
