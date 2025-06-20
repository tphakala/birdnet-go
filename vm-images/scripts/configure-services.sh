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
/data/birdnet-go/logs/*.log {
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

# Create database backup script
sudo tee /usr/local/bin/backup-birdnet-db > /dev/null << 'BACKUP_EOF'
#!/bin/bash
# BirdNET-Go Database Backup Script

BACKUP_DIR="/data/birdnet-go/backups"
DB_FILE="/data/birdnet-go/database/birdnet.db"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="$BACKUP_DIR/birdnet-$DATE.db"

# Create backup directory if it doesn't exist
mkdir -p "$BACKUP_DIR"

# Check if database exists
if [ ! -f "$DB_FILE" ]; then
    echo "Database file not found: $DB_FILE"
    exit 1
fi

# Create backup
echo "Creating database backup: $BACKUP_FILE"
sqlite3 "$DB_FILE" ".backup $BACKUP_FILE"

if [ $? -eq 0 ]; then
    echo "Backup completed successfully"
    
    # Compress backup
    gzip "$BACKUP_FILE"
    echo "Backup compressed: $BACKUP_FILE.gz"
    
    # Remove backups older than 30 days
    find "$BACKUP_DIR" -name "birdnet-*.db.gz" -mtime +30 -delete
    echo "Old backups cleaned up"
else
    echo "Backup failed"
    exit 1
fi
BACKUP_EOF

sudo chmod +x /usr/local/bin/backup-birdnet-db

# Create systemd timer for daily backups
sudo tee /etc/systemd/system/birdnet-go-backup.service > /dev/null << 'BACKUP_SERVICE_EOF'
[Unit]
Description=BirdNET-Go Database Backup
After=data.mount

[Service]
Type=oneshot
User=birdnet
Group=birdnet
ExecStart=/usr/local/bin/backup-birdnet-db
BACKUP_SERVICE_EOF

sudo tee /etc/systemd/system/birdnet-go-backup.timer > /dev/null << 'BACKUP_TIMER_EOF'
[Unit]
Description=Daily BirdNET-Go Database Backup
Requires=birdnet-go-backup.service

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
BACKUP_TIMER_EOF

# Enable backup timer
sudo systemctl daemon-reload
sudo systemctl enable birdnet-go-backup.timer

echo "Service configuration completed" 