package processor

import "log"

// Control signal types
const (
	ReloadRangeFilter = "reload_range_filter"
	ReloadBirdNET     = "reload_birdnet"
)

// controlSignalMonitor handles various control signals for the processor
func (p *Processor) controlSignalMonitor() {
	go func() {
		for signal := range p.controlChan {
			switch signal {
			case ReloadRangeFilter:
				if err := p.ReloadRangeFilter(); err != nil {
					log.Printf("\033[31m❌ Error handling range filter reload: %v\033[0m", err)
				} else {
					log.Printf("\033[32m🔄 Range filter reloaded successfully\033[0m")
				}
			default:
				log.Printf("Received unknown control signal: %v", signal)
			}
		}
	}()
}
