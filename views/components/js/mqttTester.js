// MQTT Tester Component
// A reusable Alpine.js component for testing MQTT connections

document.addEventListener('alpine:init', () => {
    Alpine.data('mqttTester', (config = {}) => {
        return {
            // Configurable properties with defaults
            apiEndpoint: config.apiEndpoint || '/api/v1/mqtt/test',
            csrfToken: config.csrfToken || '',
            timeoutDuration: config.timeoutDuration || 15000,
            
            // Component state
            isTesting: false,
            testResults: [],
            currentTestStage: null,
            
            // Test stage definitions
            testStageOrder: [
                'Starting Test',
                'Service Check',
                'Service Start',
                'DNS Resolution',
                'TCP Connection', 
                'MQTT Connection',
                'Message Publishing'
            ],
            
            // Helper methods
            isProgressMessage(message) {
                const lowerMsg = message.toLowerCase();
                return lowerMsg.includes('running') || 
                       lowerMsg.includes('testing') || 
                       lowerMsg.includes('establishing') || 
                       lowerMsg.includes('initializing') ||
                       lowerMsg.includes('attempting to start');
            },
            
            // Test method
            runTest(mqttConfig) {
                this.isTesting = true;
                this.currentTestStage = 'Starting Test';
                this.testResults = [{
                    success: true,
                    stage: 'Starting Test',
                    message: 'Initializing MQTT connection test...',
                    state: 'running'
                }];
                
                // Create a timeout promise
                const timeoutPromise = new Promise((_, reject) => {
                    setTimeout(() => reject(new Error(`Test timeout after ${this.timeoutDuration/1000} seconds`)), this.timeoutDuration);
                });

                // Create the fetch promise
                const fetchPromise = fetch(this.apiEndpoint, {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-CSRF-Token': this.csrfToken
                    },
                    body: JSON.stringify(mqttConfig)
                });

                // Race between fetch and timeout
                Promise.race([fetchPromise, timeoutPromise])
                    .then(response => {
                        if (!response.ok) {
                            throw new Error(`HTTP error! status: ${response.status}`);
                        }
                        
                        const reader = response.body.getReader();
                        const decoder = new TextDecoder();
                        let buffer = '';

                        return new ReadableStream({
                            start: (controller) => {
                                const push = () => {
                                    reader.read().then(({done, value}) => {
                                        if (done) {
                                            controller.close();
                                            return;
                                        }

                                        buffer += decoder.decode(value, {stream: true});
                                        const lines = buffer.split('\n');
                                        buffer = lines.pop(); // Keep the incomplete line

                                        lines.forEach(line => {
                                            if (line.trim()) {
                                                try {
                                                    const result = JSON.parse(line);
                                                    this.currentTestStage = result.stage;
                                                    
                                                    // Find existing result for this stage
                                                    const existingIndex = this.testResults.findIndex(r => r.stage === result.stage);
                                                    
                                                    // Determine if this is a progress message
                                                    const isProgress = this.isProgressMessage(result.message);
                                                    
                                                    // Set the state based on the result
                                                    const state = result.state ? result.state :  // Use existing state if provided
                                                        result.error ? 'failed' :
                                                        isProgress ? 'running' :
                                                        result.success ? 'completed' :
                                                        'failed';

                                                    const updatedResult = {
                                                        ...result,
                                                        isProgress: isProgress && !result.error,  // Progress state is false if there's an error
                                                        state,
                                                        success: result.error ? false : result.success
                                                    };
                                                    
                                                    if (existingIndex >= 0) {
                                                        // Update existing result
                                                        this.testResults[existingIndex] = updatedResult;
                                                    } else {
                                                        // Add new result
                                                        this.testResults.push(updatedResult);
                                                    }

                                                    // Also update previous stages to completed if this is a new stage
                                                    if (!isProgress && result.success && !result.error) {
                                                        const currentStageIndex = this.testStageOrder.indexOf(result.stage);
                                                        this.testResults.forEach((r, idx) => {
                                                            const stageIndex = this.testStageOrder.indexOf(r.stage);
                                                            if (stageIndex < currentStageIndex && r.state === 'running') {
                                                                this.testResults[idx] = {
                                                                    ...r,
                                                                    state: 'completed',
                                                                    isProgress: false
                                                                };
                                                            }
                                                        });
                                                    }
                                                    
                                                    // Sort results according to stage order
                                                    this.testResults.sort((a, b) => 
                                                        this.testStageOrder.indexOf(a.stage) - this.testStageOrder.indexOf(b.stage)
                                                    );
                                                } catch (e) {
                                                    console.error('Failed to parse test result:', e);
                                                }
                                            }
                                        });

                                        controller.enqueue(value);
                                        push();
                                    }).catch(error => {
                                        controller.error(error);
                                    });
                                };

                                push();
                            }
                        });
                    })
                    .catch(error => {
                        const errorMessage = error.message.includes('timeout')
                            ? `The test took too long to complete. Please check your broker connection and try again.`
                            : 'Failed to perform MQTT test';
                        
                        this.testResults = [{
                            success: false,
                            stage: 'Error',
                            message: errorMessage,
                            error: error.message,
                            state: 'failed'
                        }];
                        this.currentTestStage = null;
                    })
                    .finally(() => {
                        this.isTesting = false;
                        this.currentTestStage = null;
                    });
            },
            
            // Check if test was successful
            testWasSuccessful() {
                return !this.isTesting && 
                       this.testResults.length > 0 &&
                       this.testResults.every(result => result.success) && 
                       this.testResults.some(result => result.stage === 'Message Publishing') &&
                       this.testResults[this.testResults.length - 1].stage === 'Message Publishing';
            }
        };
    });
}); 