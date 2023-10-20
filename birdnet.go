package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"

	"github.com/tphakala/go-tflite"
)

//var Labels []string

// Required sample rate for input audio data
const SampleRate = 48000

type Result struct {
	Species    string
	Confidence float32
}

type DetectionsMap map[string][]Result

// initializeModel loads the model from the given path and creates a new interpreter
func initializeModel(modelPath string) (*tflite.Interpreter, error) {
	// Load the TensorFlow Lite model from the provided path
	model := tflite.NewModelFromFile(modelPath)
	if model == nil {
		return nil, fmt.Errorf("cannot load model from path: %s", modelPath)
	}

	// Get cpu core count for interpreter options
	cpuCount := runtime.NumCPU()

	// Configure the interpreter options
	options := tflite.NewInterpreterOptions()
	// XNNPACK delegate is commented out for now as interpreter creation fails
	// if it is used
	//options.AddDelegate(xnnpack.New(xnnpack.DelegateOptions{NumThreads: 1}))
	options.SetNumThread(cpuCount)
	options.SetErrorReporter(func(msg string, user_data interface{}) {
		fmt.Println(msg)
	}, nil)

	// Create a new TensorFlow Lite interpreter using the model and options
	interpreter := tflite.NewInterpreter(model, options)
	if interpreter == nil {
		return nil, fmt.Errorf("cannot create interpreter")
	}

	// Allocate tensors required for the interpreter
	status := interpreter.AllocateTensors()
	if status != tflite.OK {
		return nil, fmt.Errorf("tensor allocation failed")
	}

	return interpreter, nil
}

// loadLabels loads the labels from the provided file path into a slice and returns them.
// An error is returned if there's an issue reading the file or its content.
func loadLabels(labelspath string) ([]string, error) {
	var labels []string

	// Open the labels file.
	file, err := os.Open(labelspath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read each line in the file and append to the labels slice.
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		labels = append(labels, strings.TrimSpace(scanner.Text()))
	}

	// Check for errors from the scanner.
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return labels, nil
}

// pairLabelsAndConfidence pairs labels with their corresponding confidence values.
// It returns a slice of Result where each Result contains a species and its confidence.
// An error is returned if the length of labels and predictions do not match.
func pairLabelsAndConfidence(labels []string, preds []float32) ([]Result, error) {
	if len(labels) != len(preds) {
		return nil, fmt.Errorf("length of labels (%d) and predictions (%d) do not match", len(labels), len(preds))
	}

	results := make([]Result, len(labels))
	for i := range labels {
		results[i] = Result{
			Species:    labels[i],
			Confidence: preds[i],
		}
	}

	return results, nil
}

// sortResults sorts a slice of Result based on the confidence value in descending order.
func sortResults(results []Result) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence // ">" for descending order
	})
}

// customSigmoid calculates the sigmoid of x adjusted by a sensitivity factor.
// Sensitivity modifies the steepness of the curve. A higher value for sensitivity
// makes the curve steeper. It returns a value between 0 and 1.
func customSigmoid(x, sensitivity float64) float64 {
	return 1.0 / (1.0 + math.Exp(-sensitivity*x))
}

// predict uses a TensorFlow Lite interpreter to infer results from the provided sample.
// It then applies a custom sigmoid function to the raw predictions, pairs the results
// with their respective labels, sorts them by confidence, and returns the top results.
// The function returns an error if there's any issue during the prediction process.
func predict(sample [][]float32, sensitivity float64, interpreter *tflite.Interpreter, labels []string) ([]Result, error) {
	// Get the input tensor from the interpreter
	input := interpreter.GetInputTensor(0)
	if input == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	// Copy the sample data into the input tensor
	copy(input.Float32s(), sample[0])

	// Execute the inference using the interpreter
	status := interpreter.Invoke()
	if status != tflite.OK {
		return nil, fmt.Errorf("tensor invoke failed")
	}

	// Retrieve the output tensor from the interpreter
	output := interpreter.GetOutputTensor(0)
	outputSize := output.Dim(output.NumDims() - 1)

	// Create a slice to store the prediction results
	prediction := make([]float32, outputSize)

	// Copy the data from the output tensor into the prediction slice
	copy(prediction, output.Float32s())

	// Apply the custom sigmoid function to the prediction values to
	// get the confidence values
	confidence := make([]float32, len(prediction))
	for i := range prediction {
		confidence[i] = float32(customSigmoid(float64(prediction[i]), sensitivity))
	}

	results, err := pairLabelsAndConfidence(labels, confidence)
	if err != nil {
		return nil, fmt.Errorf("error pairing labels and confidence: %v", err)
	}

	// Do a inplace sorting of the results
	sortResults(results)

	// Only return n number of results per signal
	const maxResults = 1
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

// analyzeAudioData processes chunks of audio data using a given interpreter to produce
// predictions. Each chunk is processed individually, and the results are aggregated
// into a map with timestamps as keys. The sensitivity and overlap values affect the
// prediction process and the timestamp calculation, respectively.
func analyzeAudioData(chunks [][]float32, sensitivity, overlap float64, interpreter *tflite.Interpreter, labels []string) (map[string][]Result, error) {
	// Initialize an empty map to hold the detection results
	detections := make(map[string][]Result)

	fmt.Println("- Analyzing audio data")
	start := time.Now()

	// Start timestamp for the prediction. It will be adjusted for each chunk
	predStart := 0.0

	// Total number of chunks for progress indicator
	totalChunks := len(chunks)

	// Process each chunk of audio data
	for idx, c := range chunks {
		// Print progress indicator
		fmt.Printf("\r- Processing chunk [%d/%d]", idx+1, totalChunks)

		// Add the current chunk to the accumulated audio samples
		sig := [][]float32{c}

		// Predict labels for the current audio data
		p, err := predict(sig, sensitivity, interpreter, labels)
		if err != nil {
			return nil, fmt.Errorf("prediction failed: %v", err)
		}

		// Calculate the end timestamp for this prediction
		predEnd := predStart + 3.0

		// Store the prediction results in the detections map with the timestamp range as the key
		detections[fmt.Sprintf("%5.1f;%5.1f", predStart, predEnd)] = p

		// Adjust the start timestamp for the next prediction by considering the overlap
		predStart = predEnd - overlap
	}

	// Move to a new line after the loop ends to avoid printing on the same line.
	fmt.Println("")

	elapsed := time.Since(start)
	fmt.Printf("Time %f seconds\n", elapsed.Seconds())

	return detections, nil
}

// Read 48khz 16bit WAV file into 3 second chunks
func readAudioData(path string, overlap float64) ([][]float32, error) {
	fmt.Print("- Reading audio data")

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := wav.NewDecoder(file)
	decoder.ReadInfo()
	if !decoder.IsValidFile() {
		return nil, errors.New("input is not a valid WAV audio file")
	}

	/*
		fmt.Println("File is valid wav: ", decoder.IsValidFile())
		fmt.Println("Sample rate:", decoder.SampleRate)
		fmt.Println("Bits per sample:", decoder.BitDepth)
		fmt.Println("Channels:", decoder.NumChans)
	*/

	if decoder.SampleRate != SampleRate {
		return nil, errors.New("input file sample rate is not valid for BirdNet model")
	}

	// Divisor for converting audio sample chunk from int to float32
	var divisor float32

	switch decoder.BitDepth {
	case 16:
		divisor = 32768.0
	case 24:
		divisor = 8388608.0
	case 32:
		divisor = 2147483648.0
	default:
		return nil, errors.New("unsupported audio file bit depth")
	}

	step := int((3.0 - overlap) * float64(SampleRate))
	minLenSamples := int(1.5 * float32(SampleRate))
	secondsSamples := int(3.0 * float32(SampleRate))

	var chunks [][]float32
	var currentChunk []float32

	buf := &audio.IntBuffer{Data: make([]int, step), Format: &audio.Format{SampleRate: int(SampleRate), NumChannels: 1}}

	for {
		n, err := decoder.PCMBuffer(buf)
		if err != nil {
			return nil, err
		}

		// If no data is read, we've reached the end of the file
		if n == 0 {
			break
		}

		for _, sample := range buf.Data[:n] {
			// Convert sample from int to float32 type
			currentChunk = append(currentChunk, float32(sample)/divisor)

			if len(currentChunk) == secondsSamples {
				chunks = append(chunks, currentChunk)
				currentChunk = currentChunk[step:]
			}
		}
	}

	// Handle the last chunk
	if len(currentChunk) >= minLenSamples {
		if len(currentChunk) < secondsSamples {
			padding := make([]float32, secondsSamples-len(currentChunk))
			currentChunk = append(currentChunk, padding...)
		}
		chunks = append(chunks, currentChunk)
	}

	// Done reading audio data
	fmt.Printf(", done, read %d chunks\n", len(chunks))

	return chunks, nil
}

// If the input is "Cyanocitta_cristata_Blue Jay", the function will return "Blue Jay".
// If there's no underscore in the string or if the format is unexpected, it returns the input string itself.
func extractCommonName(species string) string {
	parts := strings.Split(species, "_")
	if len(parts) > 1 {
		return parts[1]
	}
	return species
}

// PrintDetectionsWithThreshold displays a list of detected species with their corresponding
// time intervals and confidence percentages. Only detections with confidence above the given
// threshold (e.g., 0.1 or 10%) are displayed.
func printDetectionsWithThreshold(detections DetectionsMap, threshold float32) {
	// Extract the keys (time intervals) from the map and sort them
	var intervals []string
	for interval := range detections {
		intervals = append(intervals, interval)
	}
	sort.Strings(intervals)

	for _, interval := range intervals {
		detectedPairs := detections[interval]
		var validDetections []string
		for _, pair := range detectedPairs {
			if pair.Confidence >= threshold {
				commonName := extractCommonName(pair.Species)
				validDetections = append(validDetections, fmt.Sprintf("%-30s %.1f%%", commonName, pair.Confidence*100))
			}
		}
		// Only print the interval if there are valid detections for it.
		if len(validDetections) > 0 {
			fmt.Printf("Time Interval: %s", interval)
			for _, detection := range validDetections {
				fmt.Printf("\t%s\n", detection)
			}
		}
	}
}

// Validate input flags for proper values
func validateFlags(inputAudioFile *string, modelPath *string, sensitivity, overlap *float64) error {

	if *inputAudioFile == "" {
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

func main() {
	// Define the flag for the input WAV file
	inputAudioFile := flag.String("input", "", "Path to the input audio file (WAV)")
	modelPath := flag.String("model", "BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite", "Path to the model file")
	sensitivity := flag.Float64("sensitivity", 1, "Sigmoid sensitivity value between 0.0 and 1.5")
	overlap := flag.Float64("overlap", 0, "Overlap value between 0.0 and 2.9")
	flag.Parse()

	if err := validateFlags(inputAudioFile, modelPath, sensitivity, overlap); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Starting BirdNet Analyzer")
	interpreter, err := initializeModel(*modelPath)
	if err != nil {
		log.Fatalf("Failed to initialize model: %v", err)
	}
	// Ensure the interpreter is deleted at the end
	defer interpreter.Delete()

	labels, err := loadLabels("BirdNET_GLOBAL_6K_V2.4_Labels.txt")
	if err != nil {
		log.Fatalf("Failed to load labels: %v", err)
	}
	//Labels = labels

	audioData, err := readAudioData(*inputAudioFile, *overlap)
	if err != nil {
		log.Fatalf("Error while reading input audio: %v", err)
	}

	detections, err := analyzeAudioData(audioData, *sensitivity, *overlap, interpreter, labels)
	if err != nil {
		log.Fatalf("Failed to analyze audio data: %v", err)
	}

	fmt.Println()
	printDetectionsWithThreshold(detections, 0.1)
}
