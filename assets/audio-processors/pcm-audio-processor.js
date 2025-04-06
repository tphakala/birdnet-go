/**
 * PCM Audio Processor for BirdNET-Go
 * 
 * This AudioWorkletProcessor handles PCM audio data for live streaming.
 * It processes 16-bit PCM audio data from WebSocket connections.
 */

class PCMAudioProcessor extends AudioWorkletProcessor {
    constructor() {
        super();
        this.bufferQueue = [];
        this.processedSamples = 0;
        
        // Listen for messages from the main thread
        this.port.onmessage = (event) => {
            if (event.data.type === 'buffer') {
                this.bufferQueue.push(event.data.buffer);
            }
        };
    }
    
    process(inputs, outputs, parameters) {
        const output = outputs[0];
        const channel = output[0];
        
        // If we don't have data, output silence
        if (this.bufferQueue.length === 0) {
            for (let i = 0; i < channel.length; i++) {
                channel[i] = 0;
            }
            return true;
        }
        
        // Get the next buffer to process
        const currentBuffer = this.bufferQueue[0];
        
        // Process each sample
        let samplesProcessed = 0;
        let currentPos = 0;
        
        while (samplesProcessed < channel.length) {
            // If we've used all the current buffer, move to the next one
            if (currentPos >= currentBuffer.length) {
                this.bufferQueue.shift();
                
                // If no more buffers, fill the rest with silence
                if (this.bufferQueue.length === 0) {
                    for (let i = samplesProcessed; i < channel.length; i++) {
                        channel[i] = 0;
                    }
                    break;
                }
                
                // Otherwise, get the next buffer
                currentPos = 0;
                continue;
            }
            
            // Check if we have at least 2 bytes left for a complete sample
            if (currentPos + 1 < currentBuffer.length) {
                // Read 16-bit sample (little-endian)
                const lo = currentBuffer[currentPos++];
                const hi = currentBuffer[currentPos++];
                let sample = (hi << 8) | lo;
                
                // Convert from unsigned to signed (two's complement)
                if (sample > 32767) {
                    sample -= 65536;
                }
                
                // Convert to float in [-1.0, 1.0] with proper scaling
                channel[samplesProcessed++] = sample / 32768.0;
            } else {
                // Not enough bytes for a sample, skip to next buffer
                this.bufferQueue.shift();
                if (this.bufferQueue.length === 0) {
                    // If no more buffers, fill the rest with silence
                    for (let i = samplesProcessed; i < channel.length; i++) {
                        channel[i] = 0;
                    }
                    break;
                }
                currentPos = 0;
            }
        }
        
        this.processedSamples += samplesProcessed;
        
        // Send message back to main thread occasionally with stats
        if (this.processedSamples >= 48000) { // Roughly once per second
            this.port.postMessage({ 
                type: 'stats',
                queueLength: this.bufferQueue.length 
            });
            this.processedSamples = 0;
        }
        
        return true; // Keep the node alive
    }
}

// Register the processor
registerProcessor('pcm-audio-processor', PCMAudioProcessor); 