package spinner

import (
	"fmt"
)

// Spinner struct holds the spinner state
type Spinner struct {
	frames []string
	index  int
}

// NewSpinner creates a new spinner with a set of frames
func NewSpinner() *Spinner {
	// Define a new spinner sequence to create an effect of a braille arrow
	return &Spinner{
		frames: []string{
			"⣀⣀ ",
			"⣄⣀ ",
			"⣤⣀ ",
			"⣦⣄ ",
			"⣶⣤ ",
			"⣿⣦ ",
			"⣿⣷ ",
			"⣿⣿ ",
			"⣿⣿ ",
			"⣷⣿ ",
			"⣦⣿ ",
			"⣤⣷ ",
			"⣄⣦ ",
			"⣀⣤ ",
			"⣀⣄ ",
			"⣀⣀ ",
		},
		index: 0,
	}
}

// Update advances the spinner to the next frame and prints it.
func (s *Spinner) Update() {
	// Hide cursor
	fmt.Print("\033[?25l")

	// Print the current frame
	fmt.Printf("\r%s", s.frames[s.index])

	// Advance to the next frame
	s.index++
	if s.index >= len(s.frames) {
		s.index = 0
	}
}

// Cleanup hides the spinner and shows the cursor
func (s *Spinner) Cleanup() {
	fmt.Printf("\r \r")    // Clear the spinner
	fmt.Print("\033[?25h") // Show cursor
}
