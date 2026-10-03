#!/bin/sh
set -e

# Default to TrueNAS SCALE 'apps' user/group ID 568
PUID=${PUID:-568}
PGID=${PGID:-568}

# Fast path for check / health subcommands (used by HEALTHCHECK)
if [ "$1" = "check" ] || [ "$1" = "health" ]; then
    exec /bin/supabackup "$@"
fi

# Ensure group exists for PGID
if ! getent group "$PGID" >/dev/null 2>&1; then
    addgroup -g "$PGID" -S appgroup 2>/dev/null || true
fi
GROUP_NAME=$(getent group "$PGID" 2>/dev/null | cut -d: -f1)
if [ -z "$GROUP_NAME" ]; then
    GROUP_NAME="appgroup"
fi

# Ensure user exists for PUID
if ! getent passwd "$PUID" >/dev/null 2>&1; then
    adduser -u "$PUID" -G "$GROUP_NAME" -D -H -s /bin/sh appuser 2>/dev/null || true
fi

# Ensure necessary runtime directories exist and have proper permissions
mkdir -p /backups /var/run/supabackup /etc/supabackup
chown -R "$PUID:$PGID" /backups /var/run/supabackup 2>/dev/null || true

# If running as root, drop privileges with su-exec to PUID:PGID
if [ "$(id -u)" = "0" ]; then
    exec su-exec "$PUID:$PGID" /bin/supabackup "$@"
fi

exec /bin/supabackup "$@"
