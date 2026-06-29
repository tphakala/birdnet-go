# External Media (USB, SD Card, File Shares)

BirdNET-Go can read from and write to removable or network-attached storage
mounted on the host. The `/external` directory inside the container maps to
`/mnt/birdnet-go/external` on the host. Planned uses include importing
recordings from BirdNET-Pi and other bird detectors, and writing backups.

## How it works

The container bind-mounts `/mnt/birdnet-go/external` with `rslave` propagation.
Any media the host mounts under that directory after the container is already
running will appear inside the container at the corresponding path without
requiring a container restart. For example, a USB drive the host mounts at
`/mnt/birdnet-go/external/usb1` becomes visible inside the container at
`/external/usb1` automatically. The host directory is made a shared mount
(`--make-rshared`) and the container bind uses `rslave`, so sub-mounts
propagate one way: host into container, not the other way around.

## Host setup

### install.sh (systemd) installations

The installer-generated systemd service handles this automatically on every
container start:

1. Creates `/mnt/birdnet-go/external` if it does not exist.
2. Bind-mounts it to itself if it is not already a mount point.
3. Marks it as a shared mount (`--make-rshared`) so sub-mounts propagate into
   the container.

No manual steps are needed for install.sh users. Re-running the installer after
upgrading regenerates the service unit and picks up these steps.

### Docker Compose installations

The external media volume is commented out in `docker-compose.yml` by default.
`docker compose up` works without any host setup. To enable it, perform the
one-time host setup below, then uncomment the bind block in `docker-compose.yml`.

#### One-time manual setup

```bash
sudo mkdir -p /mnt/birdnet-go/external
sudo mount --bind /mnt/birdnet-go/external /mnt/birdnet-go/external
sudo mount --make-rshared /mnt/birdnet-go/external
# Use the same UID:GID as BIRDNET_UID:BIRDNET_GID in docker-compose.yml (default 1000).
sudo chown -h "${BIRDNET_UID:-1000}:${BIRDNET_GID:-1000}" /mnt/birdnet-go/external
```

An fstab bind entry alone does NOT establish `rshared` propagation and is
therefore not sufficient for hot-plug to work. Both the self-bind and the
`make-rshared` step are required.

#### Making the setup persistent across reboots

Create a one-shot systemd service that runs both steps in the correct order and
is guaranteed to complete before the container service starts:

```ini
# /etc/systemd/system/birdnet-external-media.service
[Unit]
Description=Prepare BirdNET-Go external media mount point
DefaultDependencies=no
After=local-fs.target
Before=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=-/bin/mkdir -p /mnt/birdnet-go/external
ExecStart=-/bin/sh -c 'mountpoint -q /mnt/birdnet-go/external || mount --bind /mnt/birdnet-go/external /mnt/birdnet-go/external'
ExecStart=-/bin/sh -c 'mount --make-rshared /mnt/birdnet-go/external'
# Adjust 1000:1000 to match BIRDNET_UID:BIRDNET_GID if you override them.
ExecStart=-/bin/chown -h 1000:1000 /mnt/birdnet-go/external

[Install]
WantedBy=multi-user.target
```

Note: this unit intentionally does NOT use `RequiredBy=docker.service`. It is
best-effort: if the mount setup fails, docker.service still starts normally.
Only hot-plug propagation is lost; a hard dependency would take down all
containers if the mount step failed.

Enable it with:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now birdnet-external-media.service
```

Note: if the host root filesystem is already `rshared` (common on modern systemd
distributions), the bind step may not be strictly required, but running it
idempotently does no harm.

## Mounting media on the host

Once the host setup is complete, mount any USB drive, SD card, or file share
under `/mnt/birdnet-go/external`:

```bash
# USB drive example (replace /dev/sdb1 and label as appropriate)
sudo mkdir -p /mnt/birdnet-go/external/usb1
sudo mount /dev/sdb1 /mnt/birdnet-go/external/usb1

# NFS share example
sudo mkdir -p /mnt/birdnet-go/external/nfs-backup
sudo mount -t nfs 192.168.1.10:/exports/birdnet /mnt/birdnet-go/external/nfs-backup

# SMB/CIFS share example (store credentials in a file, not on the command line)
# A cleartext password= option in the mount command is visible in /proc/mounts,
# ps output, and the journal. Use a credentials file instead:
#   sudo install -m 600 /dev/null /etc/birdnet-go-smb.cred
#   sudoedit /etc/birdnet-go-smb.cred
#   # then add these two lines to the file:
#   #   username=YOUR_USERNAME
#   #   password=YOUR_PASSWORD
sudo mkdir -p /mnt/birdnet-go/external/smb-share
sudo mount -t cifs //192.168.1.10/share /mnt/birdnet-go/external/smb-share \
    -o credentials=/etc/birdnet-go-smb.cred,uid=1000,gid=1000
```

The mounted media will appear inside the container at `/external/usb1`,
`/external/nfs-backup`, etc.

## Notes

- Hot-plug propagation requires the host shared-mount setup described above.
  The wiring has been validated end to end on Docker 29.5 (arm64), but behavior
  may vary on older Docker versions, so validate on your specific platform before
  relying on it.
- The `/external` path is read-write inside the container.
- Writes from the container go to the mounted media on the host. Ensure the
  media filesystem and mount permissions allow writes by the container user
  (UID/GID set via `BIRDNET_UID` / `BIRDNET_GID`).
