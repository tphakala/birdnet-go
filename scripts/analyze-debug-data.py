#!/usr/bin/env python3
"""
BirdNET-Go Debug Data Analyzer
Analyzes collected debug data and generates a comprehensive report
"""

import os
import sys
import json
import subprocess
import re
from datetime import datetime
from pathlib import Path
import tempfile
import shutil

class BirdNETDebugAnalyzer:
    def __init__(self, debug_dir):
        self.debug_dir = Path(debug_dir)
        self.report = []
        self.issues = []
        self.metrics = {}
        
    def run_pprof_command(self, profile_path, *args):
        """Run go tool pprof command and return output"""
        try:
            cmd = ["go", "tool", "pprof", "-text"] + list(args) + [str(profile_path)]
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
            return result.stdout
        except subprocess.CalledProcessError as e:
            return f"Error running pprof: {e.stderr}"
    
    def analyze_heap_profile(self):
        """Analyze heap memory profile"""
        heap_file = self.debug_dir / "heap.pprof"
        if not heap_file.exists():
            return
        
        self.add_section("Heap Memory Analysis")
        
        # Get top memory consumers
        output = self.run_pprof_command(heap_file, "-top", "-unit=mb")
        self.add_text("Top Memory Consumers (MB):")
        self.add_code_block(output.split('\n')[:20])
        
        # Extract total memory
        match = re.search(r'(\d+\.?\d*)(MB|GB) total', output)
        if match:
            value = float(match.group(1))
            unit = match.group(2)
            if unit == 'GB':
                value *= 1024
            self.metrics['heap_total_mb'] = value
            
            # Flag high memory usage
            if value > 500:
                self.add_issue(f"High memory usage: {value:.1f} MB", "warning")
            if value > 1024:
                self.add_issue(f"Very high memory usage: {value/1024:.1f} GB", "critical")
        
        # Get inuse objects
        output = self.run_pprof_command(heap_file, "-inuse_objects", "-top")
        self.add_text("\nTop Object Allocations:")
        self.add_code_block(output.split('\n')[:10])
        
    def analyze_goroutines(self):
        """Analyze goroutine profile"""
        goroutine_file = self.debug_dir / "goroutine.pprof"
        if not goroutine_file.exists():
            return
            
        self.add_section("Goroutine Analysis")
        
        output = self.run_pprof_command(goroutine_file)
        lines = output.split('\n')
        
        # Count total goroutines
        total_goroutines = 0
        goroutine_types = {}
        
        for line in lines:
            match = re.match(r'\s*(\d+)\s+@?\s*0x[0-9a-f]+\s+(.*)', line)
            if match:
                count = int(match.group(1))
                location = match.group(2).strip()
                total_goroutines += count
                
                # Categorize goroutines
                if 'runtime.gopark' in location:
                    goroutine_types['parked'] = goroutine_types.get('parked', 0) + count
                elif 'chan receive' in location:
                    goroutine_types['chan_receive'] = goroutine_types.get('chan_receive', 0) + count
                elif 'chan send' in location:
                    goroutine_types['chan_send'] = goroutine_types.get('chan_send', 0) + count
                elif 'select' in location:
                    goroutine_types['select'] = goroutine_types.get('select', 0) + count
        
        self.metrics['goroutines_total'] = total_goroutines
        self.add_text(f"Total Goroutines: {total_goroutines}")
        
        # Flag high goroutine count
        if total_goroutines > 1000:
            self.add_issue(f"High goroutine count: {total_goroutines}", "warning")
        if total_goroutines > 5000:
            self.add_issue(f"Very high goroutine count: {total_goroutines} (possible leak)", "critical")
        
        self.add_text("\nGoroutine Distribution:")
        for gtype, count in sorted(goroutine_types.items(), key=lambda x: x[1], reverse=True):
            self.add_text(f"  {gtype}: {count}")
        
        self.add_text("\nTop Goroutine Locations:")
        self.add_code_block(lines[:20])
        
    def analyze_cpu_profile(self):
        """Analyze CPU profile"""
        cpu_file = self.debug_dir / "cpu.pprof"
        if not cpu_file.exists():
            return
            
        self.add_section("CPU Profile Analysis")
        
        output = self.run_pprof_command(cpu_file, "-top", "-cum")
        self.add_text("Top CPU Consumers (cumulative):")
        self.add_code_block(output.split('\n')[:20])
        
        # Check for high CPU functions
        for line in output.split('\n')[5:15]:  # Skip header
            if 'runtime.gcBgMarkWorker' in line:
                match = re.search(r'(\d+\.?\d*)%', line)
                if match and float(match.group(1)) > 20:
                    self.add_issue(f"High GC CPU usage: {match.group(1)}%", "warning")
            elif 'syscall' in line:
                match = re.search(r'(\d+\.?\d*)%', line)
                if match and float(match.group(1)) > 30:
                    self.add_issue(f"High syscall CPU usage: {match.group(1)}%", "info")
    
    def analyze_mutex_profile(self):
        """Analyze mutex contention"""
        mutex_file = self.debug_dir / "mutex.pprof"
        if not mutex_file.exists():
            return
            
        self.add_section("Mutex Contention Analysis")
        
        output = self.run_pprof_command(mutex_file, "-top")
        lines = output.split('\n')
        
        if len(lines) > 10:
            self.add_text("Top Mutex Contention Points:")
            self.add_code_block(lines[:15])
            
            # Check for high contention
            for line in lines[5:10]:
                if re.search(r'[1-9]\d{6,}', line):  # More than 100k contentions
                    self.add_issue("High mutex contention detected", "warning")
                    break
        else:
            self.add_text("Low or no mutex contention detected (good!)")
    
    def analyze_block_profile(self):
        """Analyze blocking operations"""
        block_file = self.debug_dir / "block.pprof"
        if not block_file.exists():
            return
            
        self.add_section("Blocking Operations Analysis")
        
        output = self.run_pprof_command(block_file, "-top")
        lines = output.split('\n')
        
        if len(lines) > 10:
            self.add_text("Top Blocking Operations:")
            self.add_code_block(lines[:15])
        else:
            self.add_text("Low or no blocking operations detected (good!)")
    
    def analyze_time_series(self):
        """Analyze memory growth over time"""
        time_series_dir = self.debug_dir / "time-series"
        if not time_series_dir.exists():
            return
            
        heap_files = sorted(time_series_dir.glob("heap-*.pprof"))
        if len(heap_files) < 2:
            return
            
        self.add_section("Memory Growth Analysis")
        
        # Compare first and last heap
        first_heap = heap_files[0]
        last_heap = heap_files[-1]
        
        output = self.run_pprof_command(last_heap, "-base", str(first_heap), "-top", "-unit=mb")
        self.add_text(f"Memory growth between {first_heap.name} and {last_heap.name}:")
        self.add_code_block(output.split('\n')[:15])
        
        # Check for significant growth
        growth_match = re.search(r'(\d+\.?\d*)(MB|GB) total', output)
        if growth_match:
            value = float(growth_match.group(1))
            unit = growth_match.group(2)
            if unit == 'GB' or value > 50:
                self.add_issue(f"Significant memory growth detected: {value}{unit}", "warning")
    
    def analyze_system_info(self):
        """Analyze system information"""
        sys_file = self.debug_dir / "system-info.txt"
        if not sys_file.exists():
            return
            
        self.add_section("System Information")
        
        with open(sys_file, 'r') as f:
            content = f.read()
            
        # Extract key metrics
        mem_match = re.search(r'Mem:\s+total\s+used\s+free.*\n\s*(\S+)\s+(\S+)\s+(\S+)', content, re.MULTILINE)
        if mem_match:
            total_mem = mem_match.group(1)
            used_mem = mem_match.group(2)
            self.add_text(f"System Memory: {used_mem} used / {total_mem} total")
        
        # Extract CPU info
        cpu_match = re.search(r'CPU\(s\):\s*(\d+)', content)
        if cpu_match:
            self.add_text(f"CPU Cores: {cpu_match.group(1)}")
        
        # Check for BirdNET-Go process
        process_match = re.search(r'birdnet-go.*?(\d+\.?\d*)%.*?(\d+\.?\d*)%.*?(\S+)\s+RSS', content, re.IGNORECASE)
        if process_match:
            cpu_percent = float(process_match.group(1))
            mem_percent = float(process_match.group(2))
            self.add_text(f"\nBirdNET-Go Process:")
            self.add_text(f"  CPU Usage: {cpu_percent}%")
            self.add_text(f"  Memory Usage: {mem_percent}%")
            
            if cpu_percent > 80:
                self.add_issue(f"High CPU usage by BirdNET-Go: {cpu_percent}%", "warning")
            if mem_percent > 50:
                self.add_issue(f"High memory usage by BirdNET-Go: {mem_percent}%", "warning")
    
    def generate_summary(self):
        """Generate executive summary"""
        self.add_section("Executive Summary", level=1)
        
        # Overall health assessment
        critical_issues = len([i for i in self.issues if i['severity'] == 'critical'])
        warning_issues = len([i for i in self.issues if i['severity'] == 'warning'])
        
        if critical_issues > 0:
            health = "CRITICAL"
            self.add_text(f"⚠️  **System Health: {health}**")
        elif warning_issues > 2:
            health = "WARNING"
            self.add_text(f"⚠️  **System Health: {health}**")
        else:
            health = "GOOD"
            self.add_text(f"✅ **System Health: {health}**")
        
        # Key metrics
        self.add_text("\n**Key Metrics:**")
        if 'heap_total_mb' in self.metrics:
            self.add_text(f"- Memory Usage: {self.metrics['heap_total_mb']:.1f} MB")
        if 'goroutines_total' in self.metrics:
            self.add_text(f"- Goroutines: {self.metrics['goroutines_total']}")
        
        # Issues summary
        if self.issues:
            self.add_text("\n**Issues Found:**")
            for issue in self.issues:
                icon = "🔴" if issue['severity'] == 'critical' else "🟡" if issue['severity'] == 'warning' else "🔵"
                self.add_text(f"{icon} {issue['message']}")
        else:
            self.add_text("\n✅ No significant issues detected")
        
        # Recommendations
        self.add_text("\n**Recommendations:**")
        if critical_issues > 0:
            self.add_text("1. Address critical issues immediately")
            self.add_text("2. Consider restarting BirdNET-Go if memory usage is excessive")
        elif warning_issues > 0:
            self.add_text("1. Monitor the warnings and plan for optimization")
            self.add_text("2. Review the detailed analysis sections below")
        else:
            self.add_text("1. Continue regular monitoring")
            self.add_text("2. Consider setting up automated alerts for resource usage")
    
    def add_section(self, title, level=2):
        """Add a section to the report"""
        self.report.append(f"\n{'#' * level} {title}\n")
    
    def add_text(self, text):
        """Add text to the report"""
        self.report.append(text)
    
    def add_code_block(self, lines):
        """Add a code block to the report"""
        self.report.append("```")
        if isinstance(lines, list):
            self.report.extend(lines)
        else:
            self.report.append(lines)
        self.report.append("```")
    
    def add_issue(self, message, severity="info"):
        """Add an issue to the issues list"""
        self.issues.append({
            'message': message,
            'severity': severity
        })
    
    def analyze(self):
        """Run all analyses"""
        self.analyze_system_info()
        self.analyze_heap_profile()
        self.analyze_goroutines()
        self.analyze_cpu_profile()
        self.analyze_mutex_profile()
        self.analyze_block_profile()
        self.analyze_time_series()
        
        # Generate summary at the beginning
        summary_report = []
        self.report, summary_report = summary_report, self.report
        self.generate_summary()
        self.report.extend(summary_report)
    
    def save_report(self, output_file=None):
        """Save the analysis report"""
        if output_file is None:
            output_file = self.debug_dir / "analysis-report.md"
        
        with open(output_file, 'w') as f:
            f.write('\n'.join(self.report))
        
        print(f"Analysis report saved to: {output_file}")
        return output_file

def main():
    if len(sys.argv) < 2:
        print("Usage: analyze-debug-data.py <debug-data-directory>")
        print("Example: analyze-debug-data.py debug-data-20240629-143022")
        sys.exit(1)
    
    debug_dir = sys.argv[1]
    if not os.path.exists(debug_dir):
        print(f"Error: Directory '{debug_dir}' not found")
        sys.exit(1)
    
    # Check for go tool
    try:
        subprocess.run(["go", "version"], capture_output=True, check=True)
    except (subprocess.CalledProcessError, FileNotFoundError):
        print("Error: 'go' command not found. Please install Go to analyze profiles.")
        sys.exit(1)
    
    print(f"Analyzing debug data in: {debug_dir}")
    analyzer = BirdNETDebugAnalyzer(debug_dir)
    analyzer.analyze()
    report_file = analyzer.save_report()
    
    print("\n" + "="*50)
    print("Analysis complete!")
    print("="*50)
    print(f"\nView the report: {report_file}")
    print(f"\nFor interactive analysis:")
    print(f"  go tool pprof -http=:8081 {debug_dir}/heap.pprof")
    print(f"  go tool pprof -http=:8081 {debug_dir}/cpu.pprof")

if __name__ == "__main__":
    main()