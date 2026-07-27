#!/bin/bash
# Quick diagnostic check for BirdNET-Go Docker deployment

set -euo pipefail

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}🐦 BirdNET-Go Docker Quick Check${NC}"
echo "================================"
echo ""

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
    echo -e "${RED}❌ Docker is not installed${NC}"
    exit 1
fi

# Check if systemd service exists
if systemctl list-unit-files | grep -q "birdnet-go.service"; then
    echo -e "${GREEN}✅ BirdNET-Go service found${NC}"
    
    # Check service status
    STATUS=$(systemctl is-active birdnet-go || echo "inactive")
    if [ "$STATUS" = "active" ]; then
        echo -e "${GREEN}✅ Service is active${NC}"
    else
        echo -e "${RED}❌ Service is not active (status: $STATUS)${NC}"
        echo "   Run: sudo systemctl start birdnet-go"
    fi
else
    echo -e "${RED}❌ BirdNET-Go service not found${NC}"
    echo "   Install with: curl -s https://raw.githubusercontent.com/tphakala/birdnet-go/main/install.sh | bash"
fi

# Check for running container
echo ""
CONTAINER=$(docker ps --format "{{.Names}}" | grep -E "^birdnet-go$" || true)
if [ -n "$CONTAINER" ]; then
    echo -e "${GREEN}✅ Container 'birdnet-go' is running${NC}"
    
    # Get container info
    echo ""
    echo "Container Information:"
    docker ps --filter "name=birdnet-go" --format "table {{.Status}}\t{{.Ports}}"
    
    # Check web interface
    echo ""
    PORT=$(docker port birdnet-go 8080 2>/dev/null | cut -d: -f2 || echo "8080")
    if curl -s -f "http://localhost:${PORT}" > /dev/null 2>&1; then
        echo -e "${GREEN}✅ Web interface is accessible at http://localhost:${PORT}${NC}"
    else
        echo -e "${YELLOW}⚠️  Web interface not accessible at http://localhost:${PORT}${NC}"
    fi
    
    # Check profiling
    echo ""
    if docker exec birdnet-go grep -A3 '^diagnostics:' /config/config.yaml 2>/dev/null | grep -q "enabled: true"; then
        echo -e "${GREEN}✅ Profiling is enabled${NC}"
        echo "   Profiling endpoints available at: http://localhost:${PORT}/debug/pprof/"
        echo "   If no authentication is configured, append ?token=<diagnostics.profiling.token>"
    else
        echo -e "${YELLOW}ℹ️  Profiling is disabled${NC}"
        echo "   To enable: Set 'diagnostics.profiling.enabled: true' in config.yaml and restart"
    fi
    
    # Check audio device
    echo ""
    if docker exec birdnet-go test -d /dev/snd 2>/dev/null; then
        echo -e "${GREEN}✅ Audio device is mounted${NC}"
    else
        echo -e "${YELLOW}⚠️  No audio device mounted${NC}"
        echo "   Container may be using RTSP stream instead"
    fi
    
else
    echo -e "${RED}❌ No BirdNET-Go container is running${NC}"
    
    # Check for stopped containers
    STOPPED=$(docker ps -a --format "{{.Names}}" | grep -E "^birdnet-go$" || true)
    if [ -n "$STOPPED" ]; then
        echo -e "${YELLOW}ℹ️  Found stopped container 'birdnet-go'${NC}"
        echo "   Recent logs:"
        docker logs birdnet-go --tail 10 2>&1 | sed 's/^/   /'
    fi
fi

# Check config and data directories
echo ""
echo "Checking directories:"
CONFIG_DIR="$HOME/birdnet-go"
DATA_DIR="/var/birdnet-go"

if [ -d "$CONFIG_DIR" ]; then
    echo -e "${GREEN}✅ Config directory exists: $CONFIG_DIR${NC}"
    if [ -f "$CONFIG_DIR/config.yaml" ]; then
        echo -e "   ✅ config.yaml found${NC}"
    else
        echo -e "   ${RED}❌ config.yaml not found${NC}"
    fi
else
    echo -e "${RED}❌ Config directory not found: $CONFIG_DIR${NC}"
fi

if [ -d "$DATA_DIR" ]; then
    echo -e "${GREEN}✅ Data directory exists: $DATA_DIR${NC}"
    # Check disk space
    USAGE=$(df -h "$DATA_DIR" | awk 'NR==2 {print $5}' | sed 's/%//')
    if [ "$USAGE" -gt 90 ]; then
        echo -e "   ${RED}⚠️  Disk usage is high: ${USAGE}%${NC}"
    else
        echo -e "   ✅ Disk usage: ${USAGE}%${NC}"
    fi
else
    echo -e "${RED}❌ Data directory not found: $DATA_DIR${NC}"
fi

echo ""
echo "Quick Actions:"
echo "• View logs:      sudo journalctl -u birdnet-go -f"
echo "• Restart:        sudo systemctl restart birdnet-go"
echo "• Stop:           sudo systemctl stop birdnet-go"
echo "• Update:         Run the install script again"
echo "• Debug data:     ./scripts/collect-debug-data-docker.sh"
echo ""