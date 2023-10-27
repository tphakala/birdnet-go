package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/myaudio"
)

// Constants for file paths and other configurations.
const (
	DefaultModelPath = "model/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite"
	LabelFilePath    = "model/BirdNET_GLOBAL_6K_V2.4_Labels.txt"
)

// Validate input flags for proper values
func validateFlags(inputAudioFile *string, realtimeMode *bool, modelPath *string, sensitivity, overlap *float64) error {

	if !*realtimeMode && *inputAudioFile == "" {
		return errors.New("please provide a path to input WAV file using the -input flag")
	}

	if *modelPath == "" {
		return errors.New("please provide a path to the model file using the -model flag")
	}

	if *sensitivity < 0.0 || *sensitivity > 1.5 {
		return errors.New("invalid sensitivity value. It must be between 0.0 and 1.5")
	}

	if *overlap < 0.0 || *overlap > 2.9 {
		return errors.New("invalid overlap value. It must be between 0.0 and 2.9")
	}

	return nil
}

// setupBirdNet initializes and loads the BirdNet model.
func setupBirdNet(modelPath string) error {
	fmt.Println("Starting BirdNet Analyzer")
	err := birdnet.InitializeModel(modelPath)
	if err != nil {
		return fmt.Errorf("failed to initialize model: %w", err)
	}

	err = birdnet.LoadLabels(LabelFilePath)
	if err != nil {
		return fmt.Errorf("failed to load labels: %w", err)
	}

	return nil
}

// main is the entry point of the BirdNet Analyzer.
func main() {
	inputAudioFile := flag.String("input", "", "Path to the input audio file (WAV)")
	realtimeMode := flag.Bool("realtime", false, "Realtime mode")
	modelPath := flag.String("model", DefaultModelPath, "Path to the model file")
	sensitivity := flag.Float64("sensitivity", 1, "Sigmoid sensitivity value between 0.0 and 1.5")
	overlap := flag.Float64("overlap", 0, "Overlap value between 0.0 and 2.9")
	debug := flag.Bool("debug", false, "Enable debug output")
	flag.Parse()

	// Validate command-line flags.
	if err := validateFlags(inputAudioFile, realtimeMode, modelPath, sensitivity, overlap); err != nil {
		log.Fatal(err)
	}

	// Initialize and load the BirdNet model.
	if err := setupBirdNet(*modelPath); err != nil {
		log.Fatalf("Failed to set up BirdNet: %v", err)
	}

	if *inputAudioFile != "" {
		// Load and analyze the input audio file.
		audioData, err := myaudio.ReadAudioFile(*inputAudioFile, *overlap)
		if err != nil {
			log.Fatalf("Error while reading input audio: %v", err)
		}

		detections, err := birdnet.AnalyzeAudio(audioData, *sensitivity, *overlap)
		if err != nil {
			log.Fatalf("Failed to analyze audio data: %v", err)
		}

		fmt.Println() // Empty line for better readability.
		birdnet.PrintDetectionsWithThreshold(detections, 0.1)
	}

	if *realtimeMode {
		fmt.Println("Starting BirdNet Analyzer in realtime mode")
		myaudio.StartGoRoutines(debug)
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
	}

	// Clean up resources used by BirdNet.
	birdnet.DeleteInterpreter()
}
