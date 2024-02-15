package queue

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
)

type Results struct {
	StartTime   time.Time
	PCMdata     []byte
	Results     []birdnet.Result
	ElapsedTime time.Duration
	ClipName    string
}

var (
	ResultsQueue chan *Results
	RetryQueue   chan Results
)

func Init(mainSize, retrySize int) {
	ResultsQueue = make(chan *Results, mainSize)
	RetryQueue = make(chan Results, retrySize)
}
