#!/bin/bash
set -e

echo "Setting up BirdNET-Go..."

# Create directories on main disk (application only)
sudo mkdir -p /opt/birdnet-go/{config,scripts}

# Create mount point for data disk (persistent data)
sudo mkdir -p /data/birdnet-go/{clips,database,logs,backups}

# Create systemd mount unit for data disk
sudo tee /etc/systemd/system/data.mount > /dev/null << 'MOUNT_EOF'
[Unit]
Description=BirdNET-Go Data Disk
Before=birdnet-go.service

[Mount]
What=/dev/disk/by-label/birdnet-data
Where=/data
Type=ext4
Options=defaults,noatime

[Install]
WantedBy=multi-user.target
MOUNT_EOF

# Create fstab entry as backup
echo 'LABEL=birdnet-data /data ext4 defaults,noatime 0 2' | sudo tee -a /etc/fstab

# Set ownership
sudo chown -R birdnet:birdnet /opt/birdnet-go
sudo chown -R birdnet:birdnet /data/birdnet-go

# Download base configuration
curl -s https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml \
  -o /opt/birdnet-go/config/config.yaml

# Configure paths for separate data disk
sed -i 's|path: clips/|path: /data/birdnet-go/clips/|' /opt/birdnet-go/config/config.yaml
sed -i 's|database: birdnet.db|database: /data/birdnet-go/database/birdnet.db|' /opt/birdnet-go/config/config.yaml
sed -i 's|path: logs/|path: /data/birdnet-go/logs/|' /opt/birdnet-go/config/config.yaml

# Enable XNNPACK for performance
sed -i 's/usexnnpack: false/usexnnpack: true/' /opt/birdnet-go/config/config.yaml

# Create systemd service
sudo tee /etc/systemd/system/birdnet-go.service > /dev/null << 'SERVICE_EOF'
[Unit]
Description=BirdNET-Go
After=docker.service data.mount
Requires=docker.service data.mount

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

# Ensure data directories exist
ExecStartPre=/bin/mkdir -p /data/birdnet-go/clips /data/birdnet-go/database /data/birdnet-go/logs
ExecStartPre=/bin/chown -R 1000:1000 /data/birdnet-go

# Run BirdNET-Go with separate data volume
ExecStart=/usr/bin/docker run --rm \
  --name birdnet-go \
  -p 8080:8080 \
  --device /dev/snd \
  -v /opt/birdnet-go/config:/config:ro \
  -v /data/birdnet-go:/data \
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

# Enable services
sudo systemctl daemon-reload
sudo systemctl enable data.mount
sudo systemctl enable birdnet-go.service

# Pull Docker image
docker pull tphakala/birdnet-go:nightly

# Create data disk initialization script
sudo tee /usr/local/bin/init-data-disk > /dev/null << 'INIT_EOF'
#!/bin/bash
# BirdNET-Go Data Disk Initialization Script

DEVICE="$1"
if [ -z "$DEVICE" ]; then
    echo "Usage: $0 <device>"
    echo "Example: $0 /dev/vdb"
    exit 1
fi

echo "Initializing data disk: $DEVICE"

# Create filesystem with label
sudo mkfs.ext4 -L birdnet-data "$DEVICE"

# Mount temporarily to set up directory structure
sudo mkdir -p /mnt/temp-data
sudo mount "$DEVICE" /mnt/temp-data

# Create directory structure
sudo mkdir -p /mnt/temp-data/birdnet-go/{clips,database,logs,backups}
sudo chown -R 1000:1000 /mnt/temp-data/birdnet-go

# Unmount
sudo umount /mnt/temp-data
sudo rmdir /mnt/temp-data

echo "Data disk initialized successfully!"
echo "You can now start the data.mount service: sudo systemctl start data.mount"
INIT_EOF

sudo chmod +x /usr/local/bin/init-data-disk

echo "BirdNET-Go setup completed"
echo ""
echo "IMPORTANT: Before starting BirdNET-Go, you need to:"
echo "1. Attach a separate disk/volume for persistent data"
echo "2. Initialize it with: sudo /usr/local/bin/init-data-disk /dev/vdb"
echo "3. Start the data mount: sudo systemctl start data.mount"
echo "4. Start BirdNET-Go: sudo systemctl start birdnet-go" 