package diagnostics

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// captureSystemInfo captures system information, writes it to a debug file, and returns it as a string
func CaptureSystemInfo(errorMessage string) string {
	var info strings.Builder

	// Add a clear separator at the beginning
	separator := "======== DEBUG INFO START ========"
	info.WriteString(fmt.Sprintf("%s\n", separator))
	info.WriteString(fmt.Sprintf("Error Occurred: %s\n", errorMessage))

	// CPU Utilization
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err == nil {
		info.WriteString(fmt.Sprintf("CPU Utilization: %.2f%%\n", cpuPercent[0]))
	}

	// RAM Usage
	vmStat, err := mem.VirtualMemory()
	if err == nil {
		info.WriteString(fmt.Sprintf("RAM Usage: %.2f%%\n", vmStat.UsedPercent))
	}

	// Page File Usage (Swap)
	swapStat, err := mem.SwapMemory()
	if err == nil {
		info.WriteString(fmt.Sprintf("Page File Usage: %.2f%%\n", swapStat.UsedPercent))
	}

	// Run 'ps axuw' command
	cmd := exec.Command("ps", "axuww")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error running 'ps axuw': %v", err)
	} else {
		info.WriteString("\nProcess List (ps axuw):\n")
		info.Write(output)
	}

	// Go runtime statistics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	info.WriteString(fmt.Sprintf("Go Runtime: Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB, NumGC = %v\n",
		bToMb(m.Alloc), bToMb(m.TotalAlloc), bToMb(m.Sys), m.NumGC))

	// Add a clear separator at the end
	info.WriteString(fmt.Sprintf("%s\n", strings.ReplaceAll(separator, "START", "END")))

	// Get the path to the config file
	configPath, err := conf.FindConfigFile()
	if err != nil {
		log.Printf("Error finding config file: %v", err)
	} else {
		// Create the debug file name with date and time
		now := time.Now()
		debugFileName := fmt.Sprintf("debug_%s.txt", now.Format("2006-01-02_15-04-05"))
		debugFilePath := filepath.Join(filepath.Dir(configPath), debugFileName)

		// Write the debug info to the file
		err = os.WriteFile(debugFilePath, []byte(info.String()), 0644)
		if err != nil {
			log.Printf("Error writing debug file: %v", err)
		} else {
			log.Printf("Abnormal event detected. Debug information written to: %s", debugFilePath)
		}
	}

	return info.String()
}

// bToMb converts bytes to megabytes
func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}
