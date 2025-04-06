/**
 * Simple Test Processor for AudioWorklet diagnostics
 * 
 * This provides a minimal processor implementation for testing AudioWorklet support
 * without the complexity of the main audio processor.
 */

class TestProcessor extends AudioWorkletProcessor {
    constructor() { 
        super(); 
        console.log('TestProcessor constructed successfully');
    }
    
    process() { 
        // Just return true to keep the processor alive
        return true; 
    }
}

// Register the test processor
registerProcessor('test-processor', TestProcessor); 