#!/bin/bash
set -e

echo "Setting up BirdNET-Go..."

# Create directories
sudo mkdir -p /opt/birdnet-go/{config,data,scripts}
sudo chown -R birdnet:birdnet /opt/birdnet-go

# Download base configuration
curl -s https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml \
  -o /opt/birdnet-go/config/config.yaml

# Set absolute path for audio clips
sed -i 's|path: clips/|path: /opt/birdnet-go/data/clips/|' /opt/birdnet-go/config/config.yaml

# Enable XNNPACK for performance
sed -i 's/usexnnpack: false/usexnnpack: true/' /opt/birdnet-go/config/config.yaml

# Create clips directory
mkdir -p /opt/birdnet-go/data/clips

# Create systemd service
sudo tee /etc/systemd/system/birdnet-go.service > /dev/null << 'SERVICE_EOF'
[Unit]
Description=BirdNET-Go
After=docker.service
Requires=docker.service

[Service]
Type=simple
User=birdnet
Group=birdnet
Restart=always
RestartSec=10

# Pull latest image
ExecStartPre=/usr/bin/docker pull tphakala/birdnet-go:nightly

# Remove any existing container
ExecStartPre=-/usr/bin/docker rm -f birdnet-go

# Run BirdNET-Go
ExecStart=/usr/bin/docker run --rm \
  --name birdnet-go \
  -p 8080:8080 \
  --device /dev/snd \
  -v /opt/birdnet-go/config:/config \
  -v /opt/birdnet-go/data:/data \
  -e TZ=UTC \
  -e BIRDNET_UID=1000 \
  -e BIRDNET_GID=1000 \
  tphakala/birdnet-go:nightly

# Cleanup
ExecStop=-/usr/bin/docker stop birdnet-go
ExecStopPost=-/usr/bin/docker rm -f birdnet-go

[Install]
WantedBy=multi-user.target
SERVICE_EOF

# Enable service
sudo systemctl daemon-reload
sudo systemctl enable birdnet-go.service

# Pull Docker image
docker pull tphakala/birdnet-go:nightly

echo "BirdNET-Go setup completed" 