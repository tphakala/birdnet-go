#!/bin/bash
set -e

echo "Configuring system services..."

# Configure firewall
sudo ufw --force enable
sudo ufw allow ssh
sudo ufw allow 8080/tcp

# Configure automatic updates
sudo systemctl enable unattended-upgrades

# Configure log rotation
sudo tee /etc/logrotate.d/birdnet-go > /dev/null << 'LOGROTATE_EOF'
/opt/birdnet-go/data/logs/*.log {
  daily
  rotate 7
  compress
  delaycompress
  missingok
  notifempty
  create 0644 birdnet birdnet
  postrotate
    systemctl reload birdnet-go || true
  endscript
}
LOGROTATE_EOF

# Create update script
sudo tee /usr/local/bin/update-birdnet-go > /dev/null << 'UPDATE_EOF'
#!/bin/bash
echo "Updating BirdNET-Go..."
systemctl stop birdnet-go
docker pull tphakala/birdnet-go:nightly
systemctl start birdnet-go
echo "Update completed"
UPDATE_EOF

sudo chmod +x /usr/local/bin/update-birdnet-go

# Create weekly update cron job
echo "0 2 * * 0 /usr/local/bin/update-birdnet-go" | sudo tee /etc/cron.d/birdnet-go-update

echo "Service configuration completed" 