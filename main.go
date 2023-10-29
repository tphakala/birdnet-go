package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/config"
	"github.com/tphakala/go-birdnet/pkg/myaudio"
)

const (
	DefaultModelPath = "model/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite"
	LabelFilePath    = "model/labels_fi.txt"
)

func main() {
	var cfg config.Settings

	rootCmd := &cobra.Command{
		Use:   "birdnet",
		Short: "Go-BirdNET CLI",
	}

	// Set up the persistent debug flag for the root command.
	rootCmd.PersistentFlags().BoolVar(&cfg.Debug, "debug", false, "Enable debug output")
	rootCmd.PersistentFlags().StringVar(&cfg.ModelPath, "model", DefaultModelPath, "Path to the model file")
	rootCmd.PersistentFlags().Float64Var(&cfg.Sensitivity, "sensitivity", 1, "Sigmoid sensitivity value between 0.0 and 1.5")
	rootCmd.PersistentFlags().StringVar(&cfg.Locale, "locale", "", "Language to use for labels")

	fileCmd := setupFileCommand(&cfg)
	realtimeCmd := setupRealtimeCommand(&cfg)

	setupBirdNET(cfg.ModelPath)

	rootCmd.AddCommand(fileCmd, realtimeCmd)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func setupFileCommand(cfg *config.Settings) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "file [input.wav]",
		Short: "Analyze an audio file",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg.InputAudioFile = args[0]
			executeFileAnalysis(cfg)
		},
	}

	cmd.PersistentFlags().Float64Var(&cfg.Overlap, "overlap", 0, "Overlap value between 0.0 and 2.9")

	return cmd
}

func setupRealtimeCommand(cfg *config.Settings) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "realtime",
		Short: "Analyze audio in realtime mode",
		Run:   func(cmd *cobra.Command, args []string) { executeRealtimeAnalysis(cfg) },
	}

	cmd.PersistentFlags().StringVar(&cfg.CapturePath, "savepath", "./", "Path to save audio data to")
	cmd.PersistentFlags().StringVar(&cfg.LogPath, "logpath", "./", "Path to save log file to")
	cmd.PersistentFlags().Float64Var(&cfg.Threshold, "threshold", 0.8, "Threshold for detections")
	cmd.PersistentFlags().BoolVar(&cfg.ProcessingTime, "processingtime", false, "Report processing time for each detection")

	return cmd
}

// setupBirdNET initializes and loads the BirdNET model.
func setupBirdNET(modelPath string) error {
	//fmt.Println("Starting BirdNET Analyzer")
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

func executeFileAnalysis(cfg *config.Settings) {
	// Load and analyze the input audio file.
	audioData, err := myaudio.ReadAudioFile(cfg)
	if err != nil {
		log.Fatalf("Error while reading input audio: %v", err)
	}

	detections, err := birdnet.AnalyzeAudio(audioData, cfg)
	if err != nil {
		log.Fatalf("Failed to analyze audio data: %v", err)
	}

	fmt.Println() // Empty line for better readability.
	birdnet.PrintDetectionsWithThreshold(detections, 0.1)
}

func executeRealtimeAnalysis(cfg *config.Settings) {
	fmt.Println("Starting BirdNET Analyzer in realtime mode")
	myaudio.StartGoRoutines(cfg)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	close(myaudio.QuitChannel)
}
