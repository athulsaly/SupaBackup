# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and compile statically
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/supabackup ./cmd/supabackup

# Final minimal production image
FROM alpine:3.21

# Install database clients (PostgreSQL 17+ client, MariaDB client) and runtime tools
RUN apk add --no-cache \
    postgresql17-client \
    mariadb-client \
    mariadb-connector-c \
    su-exec \
    tzdata \
    ca-certificates

# Copy compiled binary and entrypoint script
COPY --from=builder /bin/supabackup /bin/supabackup
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh /bin/supabackup

# Setup directories with default TrueNAS apps UID/GID 568
ENV PUID=568 \
    PGID=568 \
    CONFIG_PATH=/etc/supabackup/config.yaml \
    BACKUP_DIR=/backups \
    STATUS_FILE=/var/run/supabackup/status.json

RUN mkdir -p /backups /var/run/supabackup /etc/supabackup

VOLUME ["/backups", "/etc/supabackup"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/bin/supabackup", "check"]

ENTRYPOINT ["/entrypoint.sh"]
CMD ["run"]
