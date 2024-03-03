package queue

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

type Results struct {
	StartTime   time.Time
	PCMdata     []byte
	Results     []datastore.Results
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
