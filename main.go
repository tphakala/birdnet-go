package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/config"
	"github.com/tphakala/go-birdnet/pkg/myaudio"
	"github.com/tphakala/go-birdnet/pkg/output"
)

const (
	DefaultModelPath = "model/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite"
	//LabelFilePath    = "model/labels_fi.txt"
)

func main() {
	var cfg config.Settings

	rootCmd := &cobra.Command{
		Use:   "birdnet",
		Short: "Go-BirdNET CLI",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if _, exists := config.Locales[cfg.Locale]; !exists {
				if fullLocale, exists := config.LocaleCodes[strings.ToLower(cfg.Locale)]; exists {
					cfg.Locale = fullLocale
				} else {
					return fmt.Errorf("unsupported locale: %s", cfg.Locale)
				}
			}
			LabelFilePath := fmt.Sprintf("model/%s", config.Locales[cfg.Locale])

			// Check if label file exists on disk.
			if _, err := os.Stat(LabelFilePath); os.IsNotExist(err) {
				// Attempt to extract from ZIP.
				if err := extractLabelFileFromZip("model/labels_nm.zip", LabelFilePath); err != nil {
					return fmt.Errorf("error extracting label file: %v", err)
				}
			}
			setupBirdNET(cfg.ModelPath, LabelFilePath)
			return nil
		},
	}

	// Set up the persistent debug flag for the root command.
	rootCmd.PersistentFlags().BoolVar(&cfg.Debug, "debug", false, "Enable debug output")
	rootCmd.PersistentFlags().StringVar(&cfg.ModelPath, "model", DefaultModelPath, "Path to the model file")
	rootCmd.PersistentFlags().Float64Var(&cfg.Sensitivity, "sensitivity", 1, "Sigmoid sensitivity value between 0.0 and 1.5")
	rootCmd.PersistentFlags().StringVar(&cfg.Locale, "locale", "English", "Set the locale for labels. Accepts full name or 2-letter code.")

	fileCmd := setupFileCommand(&cfg)
	realtimeCmd := setupRealtimeCommand(&cfg)

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

	cmd.PersistentFlags().StringVar(&cfg.CapturePath, "savepath", "./clips", "Path to save audio data to")
	cmd.PersistentFlags().StringVar(&cfg.LogPath, "logpath", "./log/detections.log", "Path to save log file to")
	cmd.PersistentFlags().Float64Var(&cfg.Threshold, "threshold", 0.8, "Threshold for detections")
	cmd.PersistentFlags().BoolVar(&cfg.ProcessingTime, "processingtime", false, "Report processing time for each detection")

	return cmd
}

// setupBirdNET initializes and loads the BirdNET model.
func setupBirdNET(modelPath string, LabelFilePath string) error {
	fmt.Println("Loading BirdNET model")
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

	notes, err := birdnet.AnalyzeAudio(audioData, cfg)
	if err != nil {
		log.Fatalf("Failed to analyze audio data: %v", err)
	}

	fmt.Println() // Empty line for better readability.
	// Print the detections (notes) with a threshold of, for example, 10%
	output.PrintNotesWithThreshold(notes, 0.1)
}

func executeRealtimeAnalysis(cfg *config.Settings) {
	fmt.Println("Starting BirdNET Analyzer in realtime mode")

	myaudio.StartGoRoutines(cfg)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	close(myaudio.QuitChannel)
}

func extractLabelFileFromZip(zipPath, labelFilePath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == filepath.Base(labelFilePath) {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			f, err := os.Create(labelFilePath)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(f, rc)
			return err
		}
	}
	return fmt.Errorf("label file not found in the zip archive")
}
