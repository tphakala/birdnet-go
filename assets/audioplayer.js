htmx.on('htmx:afterSettle', function (event) {
    // Initialize Web Audio Context
    let audioContext;
    try {
        audioContext = new (window.AudioContext || window.webkitAudioContext)();
    } catch (e) {
        console.warn('Web Audio API is not supported in this browser');
    }

    // Constants
    const GAIN_SLIDER_INACTIVITY_TIMEOUT_MS = 5000;
    const GAIN_SLIDER_MARGIN = 4;
    const GAIN_MAX_DB = 24;
    const MIN_PLAYER_WIDTH_FOR_CONTROLS = 175;

    const FILTER_TYPES = {
        highpass: 'highpass',
        lowpass: 'lowpass',
        bandpass: 'bandpass'
    };

    const FILTER_HP_DEFAULT_FREQ = 20; // Default highpass filter frequency
    const FILTER_HP_MIN_FREQ = 20; // Minimum highpass filter frequency
    const FILTER_HP_MAX_FREQ = 10000; // Maximum highpass filter frequency

    // SVG Icons
    const ICONS = {
        play: `<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
        </svg>`,
        pause: `<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z"></path>
        </svg>`,
        pauseCompact: `<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 9v6m4-6v6"></path>
        </svg>`,
        playCompact: `<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
        </svg>`,
        volume: `<svg width="16" height="16" viewBox="0 0 24 24" style="color: white;" aria-hidden="true">
            <path d="M12 5v14l-7-7h-3v-4h3l7-7z" fill="currentColor"/>
            <path d="M16 8a4 4 0 0 1 0 8" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
            <path d="M19 5a8 8 0 0 1 0 14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
        </svg>`
    };

    // Utility Functions
    const updateButtonIcon = (button, icon) => {
        button.innerHTML = ICONS[icon];
    };

    const setupEventListener = (element, event, handler, options = {}) => {
        element.addEventListener(event, handler, options);
        return () => element.removeEventListener(event, handler, options);
    };

    const createAudioNodes = (audioContext, audio) => {
        const audioSource = audioContext.createMediaElementSource(audio);
        const gainNode = audioContext.createGain();
        gainNode.gain.value = 1;

        const highPassFilter = audioContext.createBiquadFilter();
        highPassFilter.type = 'highpass';
        highPassFilter.frequency.value = FILTER_HP_DEFAULT_FREQ;
        highPassFilter.Q.value = 1;

        const compressor = audioContext.createDynamicsCompressor();
        compressor.threshold.value = -24;
        compressor.knee.value = 30;
        compressor.ratio.value = 12;
        compressor.attack.value = 0.003;
        compressor.release.value = 0.25;

        audioSource
            .connect(highPassFilter)
            .connect(gainNode)
            .connect(compressor)
            .connect(audioContext.destination);

        return {
            source: audioSource,
            gain: gainNode,
            compressor,
            filters: {
                highPass: highPassFilter
            }
        };
    };

    const setupClickPrevention = (elements, event) => {
        return !elements.some(el => el.contains(event.target));
    };

    // Utility functions for creating UI elements
    const createSlider = (className, height = 'h-32') => {
        const slider = document.createElement('div');
        slider.className = className;
        slider.setAttribute('role', 'slider');
        slider.setAttribute('aria-orientation', 'vertical');
        slider.setAttribute('tabindex', '0');
        slider.style.cssText = 'display: none; position: absolute; z-index: 1000; background: rgba(0, 0, 0, 0.2); backdrop-filter: blur(4px); padding: 8px; border-radius: 4px;';
        slider.innerHTML = `
            <div class="flex flex-col items-center h-full">
                <div class="relative h-full w-2 bg-white/50 dark:bg-white/10 rounded-full overflow-hidden">
                    <div class="absolute bottom-0 w-full rounded-full transition-all duration-100" style="height: 0%"></div>
                </div>
            </div>
        `;
        return slider;
    };

    const createControlButton = (position, content, ariaLabel) => {
        const control = document.createElement('div');
        control.style.cssText = `position: absolute; top: 8px; ${position}: 8px; z-index: 10; opacity: 0; transition: opacity 0.2s;`;
        control.innerHTML = `
            <div class="flex items-center gap-1" style="position: relative;">
                <button class="flex items-center justify-center gap-1" 
                    style="height: 28px; padding: 0 8px; border-radius: 14px; background: rgba(0, 0, 0, 0.5); transition: background 0.2s;"
                    aria-label="${ariaLabel}"
                    aria-expanded="false"
                    role="button"
                    tabindex="0">
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
                const top = 0;  // Align to top
                sliderElement.style.top = `${top}px`;
                sliderElement.style.height = `${containerRect.height}px`; // Match container height
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
    const setupSliderInteraction = (slider, manager, updateFn) => {
        const calculatePosition = (e) => {
            const sliderRect = slider.querySelector('.relative').getBoundingClientRect();
            let y = e.type.includes('touch') ? e.touches[0].clientY : e.clientY;
            let pos = (sliderRect.bottom - y) / sliderRect.height;
            return Math.max(0, Math.min(1, pos));
        };

        const handleUpdate = (e) => {
            e.preventDefault();
            manager.resetTimer();
            updateFn(calculatePosition(e));
        };

        const handleStart = (e) => {
            if (e.type === 'mousedown' && e.button !== 0) return;
            
            handleUpdate(e);
            const moveHandler = (e) => {
                e.preventDefault();
                handleUpdate(e);
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

    // Audio Player State Management
    const createPlayerState = (audio, updateProgress) => {
        let updateInterval;

        const startInterval = () => {
            updateInterval = setInterval(updateProgress, 100);
        };

        const stopInterval = () => {
            clearInterval(updateInterval);
        };

        const togglePlay = (e, audioContext) => {
            e.stopPropagation();
            if (audioContext && audioContext.state === 'suspended') {
                audioContext.resume();
            }
            if (audio.paused) {
                audio.play();
            } else {
                audio.pause();
            }
        };

        return {
            startInterval,
            stopInterval,
            togglePlay
        };
    };

    // UI Update Functions
    const createUIUpdaters = (gainNode, volumeControl, sliderElement, sliderBar) => {
        const updateGainValue = (dbValue) => {
            const gainValue = dbToGain(dbValue);
            gainNode.gain.value = gainValue;
            const gainDisplay = volumeControl.querySelector('.gain-display');
            const displayText = `${dbValue > 0 ? '+' : ''}${dbValue} dB`;
            gainDisplay.textContent = displayText;
            sliderElement.setAttribute('aria-valuenow', dbValue.toString());
            sliderElement.setAttribute('aria-valuetext', `${dbValue} decibels`);
            
            const heightPercent = (dbValue / GAIN_MAX_DB) * 100;
            sliderBar.style.height = `${heightPercent}%`;
        };

        return { updateGainValue };
    };

    const createFilterUpdaters = (filterControl, filterSlider, filterBar) => {
        const updateFilterDisplay = (freq) => {
            const filterLabel = filterControl.querySelector('span');
            const displayText = `HP: ${Math.round(freq)} Hz`;
            filterLabel.textContent = displayText;
            filterSlider.setAttribute('aria-valuenow', Math.round(freq).toString());
            filterSlider.setAttribute('aria-valuetext', `${Math.round(freq)} Hertz`);
            
            const pos = Math.log(freq/FILTER_HP_MIN_FREQ) / Math.log(FILTER_HP_MAX_FREQ/FILTER_HP_MIN_FREQ);
            filterBar.style.height = `${pos * 100}%`;
        };

        return { updateFilterDisplay };
    };

    // Keyboard Control Setup
    const setupKeyboardControls = (element, manager, updateFn, params) => {
        element.addEventListener('keydown', (e) => {
            if (manager.isActive()) {
                let newValue;
                
                switch(e.key) {
                    case 'ArrowUp':
                        newValue = params.up();
                        break;
                    case 'ArrowDown':
                        newValue = params.down();
                        break;
                    case 'Home':
                        newValue = params.min;
                        break;
                    case 'End':
                        newValue = params.max;
                        break;
                    default:
                        return;
                }
                
                if (newValue !== params.current) {
                    e.preventDefault();
                    updateFn(newValue);
                    manager.resetTimer();
                }
            }
        });
    };

    // Event Handling Setup
    const setupPlayerEvents = (playPause, playPauseCompact, audio, playerState) => {
        const handlers = {
            play: () => {
                updateButtonIcon(playPause, 'pause');
                if (playPauseCompact) {
                    updateButtonIcon(playPauseCompact, 'pauseCompact');
                }
                playerState.startInterval();
            },
            pause: () => {
                updateButtonIcon(playPause, 'play');
                if (playPauseCompact) {
                    updateButtonIcon(playPauseCompact, 'playCompact');
                }
                playerState.stopInterval();
            },
            ended: playerState.stopInterval
        };

        audio.addEventListener('play', handlers.play);
        audio.addEventListener('pause', handlers.pause);
        audio.addEventListener('ended', handlers.ended);

        return handlers;
    };

    // Slider Interaction Setup
    const createSliderInteraction = (slider, manager, updateFn) => {
        const handleUpdate = (e) => {
            e.preventDefault();
            manager.resetTimer();
            const sliderRect = slider.querySelector('.relative').getBoundingClientRect();
            let y = e.type.includes('touch') ? e.touches[0].clientY : e.clientY;
            
            let pos = (sliderRect.bottom - y) / sliderRect.height;
            pos = Math.max(0, Math.min(1, pos));
            
            return pos;
        };

        return handleUpdate;
    };

    // Seeking Setup
    const setupSeeking = (container, playerOverlay, elements, audio, updateProgress) => {
        if (isDesktop()) {
            container.addEventListener('click', (e) => {
                if (setupClickPrevention([playerOverlay, ...elements], e)) {
                    const rect = container.getBoundingClientRect();
                    const pos = (e.clientX - rect.left) / rect.width;
                    audio.currentTime = pos * audio.duration;
                    updateProgress();
                }
            });
        } else {
            let isDragging = false;
            
            container.addEventListener('touchstart', (e) => {
                if (!audio.paused && setupClickPrevention([playerOverlay, ...elements], e)) {
                    isDragging = true;
                }
            }, {passive: true});
            
            container.addEventListener('touchmove', (e) => {
                if (isDragging && !playerOverlay.contains(e.target)) {
                    e.preventDefault();
                    const touch = e.touches[0];
                    const rect = container.getBoundingClientRect();
                    const pos = (touch.clientX - rect.left) / rect.width;
                    audio.currentTime = pos * audio.duration;
                    updateProgress();
                }
            }, {passive: true});
            
            container.addEventListener('touchend', () => { isDragging = false; }, {passive: true});
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
            <svg width="16" height="16" viewBox="0 0 24 24" style="color: white;" aria-hidden="true">
                <path d="M12 5v14l-7-7h-3v-4h3l7-7z" fill="currentColor"/>
                <path d="M16 8a4 4 0 0 1 0 8" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
                <path d="M19 5a8 8 0 0 1 0 14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round"/>
            </svg>
            <span class="gain-display text-xs text-white" aria-live="polite">0 dB</span>
        `, 'Adjust volume gain');

        // Create volume slider
        const sliderElement = createSlider('volume-slider');
        sliderElement.setAttribute('aria-label', 'Volume gain control');
        sliderElement.setAttribute('aria-valuemin', '0');
        sliderElement.setAttribute('aria-valuemax', GAIN_MAX_DB.toString());
        sliderElement.setAttribute('aria-valuenow', '0');
        sliderElement.setAttribute('aria-valuetext', '0 decibels');
        const sliderBar = sliderElement.querySelector('.w-full');
        sliderBar.classList.add('bg-primary');

        // Create filter control UI
        const filterControl = createControlButton('left', `
            <span class="text-xs text-white" aria-live="polite">HP: ${FILTER_HP_DEFAULT_FREQ} Hz</span>
        `, 'Adjust high-pass filter');
        filterControl.classList.add('filter-control');

        // Create filter slider
        const filterSlider = createSlider('filter-slider');
        filterSlider.setAttribute('aria-label', 'High-pass filter frequency control');
        filterSlider.setAttribute('aria-valuemin', FILTER_HP_MIN_FREQ.toString());
        filterSlider.setAttribute('aria-valuemax', FILTER_HP_MAX_FREQ.toString());
        filterSlider.setAttribute('aria-valuenow', FILTER_HP_DEFAULT_FREQ.toString());
        filterSlider.setAttribute('aria-valuetext', `${FILTER_HP_DEFAULT_FREQ} Hertz`);
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

        // Function to check and update control visibility based on container width
        const updateControlsVisibility = () => {
            const containerWidth = spectrogramContainer.getBoundingClientRect().width;
            const shouldShowControls = containerWidth >= MIN_PLAYER_WIDTH_FOR_CONTROLS;
            
            volumeControl.style.display = shouldShowControls ? 'block' : 'none';
            filterControl.style.display = shouldShowControls ? 'block' : 'none';
            
            // If controls are hidden, also hide their sliders
            if (!shouldShowControls) {
                if (gainManager.isActive()) {
                    gainManager.hide();
                }
                if (filterManager.isActive()) {
                    filterManager.hide();
                }
            }
        };

        // Initial visibility check
        updateControlsVisibility();

        // Add resize observer to handle container width changes
        const resizeObserver = new ResizeObserver(() => {
            updateControlsVisibility();
        });
        resizeObserver.observe(spectrogramContainer);

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
                highPassFilter.frequency.value = FILTER_HP_DEFAULT_FREQ;
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
                    const displayText = `${dbValue > 0 ? '+' : ''}${dbValue} dB`;
                    gainDisplay.textContent = displayText;
                    sliderElement.setAttribute('aria-valuenow', dbValue.toString());
                    sliderElement.setAttribute('aria-valuetext', `${dbValue} decibels`);
                    
                    // Update slider bar height (0-GAIN_MAX_DB maps to 0-100%)
                    const heightPercent = (dbValue / GAIN_MAX_DB) * 100;
                    sliderBar.style.height = `${heightPercent}%`;
                };

                // Function to update filter frequency display
                const updateFilterDisplay = (freq) => {
                    const filterLabel = filterControl.querySelector('span');
                    const displayText = `HP: ${Math.round(freq)} Hz`;
                    filterLabel.textContent = displayText;
                    filterSlider.setAttribute('aria-valuenow', Math.round(freq).toString());
                    filterSlider.setAttribute('aria-valuetext', `${Math.round(freq)} Hertz`);
                    
                    // Calculate slider position based on frequency
                    const pos = Math.log(freq/FILTER_HP_MIN_FREQ) / Math.log(FILTER_HP_MAX_FREQ/FILTER_HP_MIN_FREQ);
                    filterBar.style.height = `${pos * 100}%`;
                };

                // Setup gain slider interaction
                setupSliderInteraction(sliderElement, gainManager, (pos) => {
                    const dbValue = Math.round(pos * GAIN_MAX_DB);
                    updateGainValue(dbValue);
                });

                // Setup filter slider interaction
                setupSliderInteraction(filterSlider, filterManager, (pos) => {
                    const freq = Math.round(FILTER_HP_MIN_FREQ * Math.pow(FILTER_HP_MAX_FREQ/FILTER_HP_MIN_FREQ, pos));
                    highPassFilter.frequency.value = freq;
                    updateFilterDisplay(freq);
                });

                // Volume button click handler
                volumeControl.querySelector('button').addEventListener('click', (e) => {
                    e.stopPropagation();
                    const button = volumeControl.querySelector('button');
                    if (!gainManager.isActive()) {
                        if (filterManager.isActive()) {
                            filterManager.hide();
                            filterControl.querySelector('button').setAttribute('aria-expanded', 'false');
                        }
                        hideActiveGainControl();
                        gainManager.show();
                        button.setAttribute('aria-expanded', 'true');
                        const { containerRect, sliderRect } = gainManager.updatePosition(spectrogramContainer, GAIN_SLIDER_MARGIN);
                        sliderElement.style.left = `${containerRect.width + GAIN_SLIDER_MARGIN}px`;
                        activeGainControl = {
                            id: id,
                            hideSlider: gainManager.hide
                        };
                    } else {
                        gainManager.hide();
                        button.setAttribute('aria-expanded', 'false');
                    }
                });

                // Filter button click handler
                filterControl.querySelector('button').addEventListener('click', (e) => {
                    e.stopPropagation();
                    const button = filterControl.querySelector('button');
                    if (!filterManager.isActive()) {
                        if (gainManager.isActive()) {
                            gainManager.hide();
                            volumeControl.querySelector('button').setAttribute('aria-expanded', 'false');
                        }
                        filterManager.show();
                        button.setAttribute('aria-expanded', 'true');
                        const { containerRect, sliderRect } = filterManager.updatePosition(spectrogramContainer, GAIN_SLIDER_MARGIN);
                        filterSlider.style.left = `${-(sliderRect.width + GAIN_SLIDER_MARGIN)}px`;
                    } else {
                        filterManager.hide();
                        button.setAttribute('aria-expanded', 'false');
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
                    playPause.innerHTML = ICONS.play;
                    if (playPauseCompact)
                        playPauseCompact.innerHTML = ICONS.playCompact;
                    startInterval();
                });

                // Update play/pause button icons and stop interval when audio is paused
                audio.addEventListener('pause', () => {
                    playPause.innerHTML = ICONS.pause;
                    if (playPauseCompact)
                        playPauseCompact.innerHTML = ICONS.pauseCompact;
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
                setupSeeking(spectrogramContainer, playerOverlay, [sliderElement, filterSlider, volumeControl, filterControl], audio, updateProgress);

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

                // Add keyboard controls for sliders
                sliderElement.addEventListener('keydown', (e) => {
                    if (gainManager.isActive()) {
                        const currentDb = Math.round(gainToDb(gainNode.gain.value));
                        let newValue = currentDb;
                        
                        switch(e.key) {
                            case 'ArrowUp':
                                newValue = Math.min(GAIN_MAX_DB, currentDb + 1);
                                break;
                            case 'ArrowDown':
                                newValue = Math.max(0, currentDb - 1);
                                break;
                            case 'Home':
                                newValue = 0;
                                break;
                            case 'End':
                                newValue = GAIN_MAX_DB;
                                break;
                            default:
                                return;
                        }
                        
                        if (newValue !== currentDb) {
                            e.preventDefault();
                            updateGainValue(newValue);
                            gainManager.resetTimer();
                        }
                    }
                });

                filterSlider.addEventListener('keydown', (e) => {
                    if (filterManager.isActive()) {
                        const currentFreq = highPassFilter.frequency.value;
                        let newFreq = currentFreq;
                        
                        switch(e.key) {
                            case 'ArrowUp':
                                newFreq = Math.min(FILTER_HP_MAX_FREQ, currentFreq * 1.1);
                                break;
                            case 'ArrowDown':
                                newFreq = Math.max(FILTER_HP_MIN_FREQ, currentFreq * 0.9);
                                break;
                            case 'Home':
                                newFreq = FILTER_HP_MIN_FREQ;
                                break;
                            case 'End':
                                newFreq = FILTER_HP_MAX_FREQ;
                                break;
                            default:
                                return;
                        }
                        
                        if (newFreq !== currentFreq) {
                            e.preventDefault();
                            highPassFilter.frequency.value = newFreq;
                            updateFilterDisplay(newFreq);
                            filterManager.resetTimer();
                        }
                    }
                });

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
