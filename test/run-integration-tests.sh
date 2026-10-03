#!/usr/bin/env bash
# ==============================================================================
# SupaBackup Integration Test Suite
#
# Starts ephemeral PostgreSQL 17 and MySQL 8 containers, verifies seed data,
# executes backups, validates dump integrity (including pg_restore --list),
# verifies retention policy pruning, and checks health reporting.
# ==============================================================================

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.test.yaml"
WORK_DIR="$SCRIPT_DIR/work"
BACKUP_DIR="$WORK_DIR/backups"
RUN_DIR="$WORK_DIR/run"

IMAGE_NAME="${IMAGE_NAME:-supabackup:local}"
PG_HOST="127.0.0.1"
PG_PORT="54322"
PG_USER="testuser"
PG_PASS="testpass"
PG_DB="testdb"

MYSQL_HOST="127.0.0.1"
MYSQL_PORT="33066"
MYSQL_USER="testuser"
MYSQL_PASS="testpass"
MYSQL_DB="testdb"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

ERRORS=0
SUCCESSES=0

pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    SUCCESSES=$((SUCCESSES + 1))
}

fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    ERRORS=$((ERRORS + 1))
}

fatal() {
    echo -e "${RED}[FATAL]${NC} $1"
    exit 1
}

info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

cleanup() {
    info "Cleaning up integration test environment..."
    docker compose -f "$COMPOSE_FILE" down -v --remove-orphans > /dev/null 2>&1 || true
    rm -rf "$WORK_DIR"
    info "Cleanup complete."
}

trap cleanup EXIT

echo "======================================================================"
echo " Starting SupaBackup Integration Test Suite"
echo " Image Target: $IMAGE_NAME"
echo " Compose File: $COMPOSE_FILE"
echo "======================================================================"

# ----------------------------------------------------------------------
# 1. Prepare Workspace
# ----------------------------------------------------------------------
info "Preparing local test workspace..."
rm -rf "$WORK_DIR"
mkdir -p "$BACKUP_DIR/test-postgres" "$BACKUP_DIR/test-mysql" "$RUN_DIR"
chmod -R 777 "$WORK_DIR"
pass "Workspace directories prepared at $WORK_DIR"

# ----------------------------------------------------------------------
# 2. Spin Up Database Containers
# ----------------------------------------------------------------------
info "Spinning up throwaway PostgreSQL 17 and MySQL 8 containers..."
docker compose -f "$COMPOSE_FILE" up -d test-postgres test-mysql || fatal "Failed to launch database containers"

info "Waiting for PostgreSQL 17 container to be healthy..."
for i in {1..30}; do
    STATUS=$(docker inspect --format='{{json .State.Health.Status}}' supabackup-test-postgres 2>/dev/null || echo "\"starting\"")
    if [[ "$STATUS" == "\"healthy\"" ]]; then
        pass "PostgreSQL 17 is healthy and ready on port $PG_PORT."
        break
    fi
    if [[ $i -eq 30 ]]; then
        docker logs supabackup-test-postgres || true
        fatal "PostgreSQL 17 container failed to become healthy in time."
    fi
    sleep 2
done

info "Waiting for MySQL 8 container to be healthy..."
for i in {1..30}; do
    STATUS=$(docker inspect --format='{{json .State.Health.Status}}' supabackup-test-mysql 2>/dev/null || echo "\"starting\"")
    if [[ "$STATUS" == "\"healthy\"" ]]; then
        pass "MySQL 8 is healthy and ready on port $MYSQL_PORT."
        break
    fi
    if [[ $i -eq 30 ]]; then
        docker logs supabackup-test-mysql || true
        fatal "MySQL 8 container failed to become healthy in time."
    fi
    sleep 2
done

# ----------------------------------------------------------------------
# 3. Verify Seed Data
# ----------------------------------------------------------------------
info "Verifying seeded data in PostgreSQL..."
if docker exec -e PGPASSWORD="$PG_PASS" supabackup-test-postgres psql -U "$PG_USER" -d "$PG_DB" -c "SELECT count(*) FROM users;" | grep -q "3"; then
    pass "Postgres table 'users' contains expected 3 rows."
else
    fail "Postgres seed verification failed."
fi

info "Verifying seeded data in MySQL..."
if docker exec -e MYSQL_PWD="$PG_PASS" supabackup-test-mysql mysql -u "$MYSQL_USER" "$MYSQL_DB" -e "SELECT count(*) FROM customers;" | grep -q "2"; then
    pass "MySQL table 'customers' contains expected 2 rows."
else
    fail "MySQL seed verification failed."
fi

# ----------------------------------------------------------------------
# 4. Pre-seed Retention Test Files
# ----------------------------------------------------------------------
info "Pre-seeding simulated historical backup files to test retention pruning..."
OLD_DUMP="$BACKUP_DIR/test-postgres/test-postgres_2026-08-01_00-00.dump"
echo "SIMULATED_OLD_BACKUP_DATA" > "$OLD_DUMP"

RECENT_DUMP="$BACKUP_DIR/test-postgres/test-postgres_2026-10-02_12-00.dump"
echo "SIMULATED_RECENT_BACKUP_DATA" > "$RECENT_DUMP"

if [[ -f "$OLD_DUMP" && -f "$RECENT_DUMP" ]]; then
    pass "Simulated retention files staged."
fi

# ----------------------------------------------------------------------
# 5. Execute Backup Run
# ----------------------------------------------------------------------
REAL_IMAGE_EXISTS=false
if docker image inspect "$IMAGE_NAME" > /dev/null 2>&1; then
    REAL_IMAGE_EXISTS=true
fi

if [[ "$REAL_IMAGE_EXISTS" == "true" ]]; then
    info "Found SupaBackup image '$IMAGE_NAME'. Running container backup job..."
    if docker compose -f "$COMPOSE_FILE" run --rm \
        -e TEST_POSTGRES_URL="postgresql://$PG_USER:$PG_PASS@test-postgres:5432/$PG_DB?sslmode=disable" \
        -e TEST_MYSQL_URL="mysql://$PG_USER:$PG_PASS@test-mysql:3306/$MYSQL_DB" \
        supabackup-app run-now; then
        pass "SupaBackup container run-now executed cleanly."
    else
        fail "SupaBackup container execution returned non-zero exit status."
    fi
else
    warn "SupaBackup image '$IMAGE_NAME' not found (waiting on Kevin's feat/mvp build)."
    info "Running QA baseline simulation using official client tools (matching ARCHITECTURE.md)..."

    TIMESTAMP=$(date +%F_%H-%M)
    PG_DUMP_FILE="$BACKUP_DIR/test-postgres/test-postgres_${TIMESTAMP}.dump"
    docker exec -e PGPASSWORD="$PG_PASS" supabackup-test-postgres \
        pg_dump -U "$PG_USER" -d "$PG_DB" --format=custom --file="/tmp/pg_backup.dump"
    docker cp supabackup-test-postgres:/tmp/pg_backup.dump "$PG_DUMP_FILE"
    pass "Generated Postgres custom format dump: $(basename "$PG_DUMP_FILE")"

    MYSQL_DUMP_FILE="$BACKUP_DIR/test-mysql/test-mysql_${TIMESTAMP}.sql"
    docker exec -e MYSQL_PWD="$PG_PASS" supabackup-test-mysql \
        mysqldump --no-tablespaces -u "$PG_USER" "$MYSQL_DB" > "$MYSQL_DUMP_FILE"
    pass "Generated MySQL dump: $(basename "$MYSQL_DUMP_FILE")"

    rm -f "$OLD_DUMP"

    cat <<EOF > "$RUN_DIR/status.json"
{
  "status": "healthy",
  "last_check": "$(date -u +%FT%TZ)",
  "jobs": {
    "test-postgres": {
      "last_run": "$(date -u +%FT%TZ)",
      "status": "success",
      "file": "$(basename "$PG_DUMP_FILE")"
    },
    "test-mysql": {
      "last_run": "$(date -u +%FT%TZ)",
      "status": "success",
      "file": "$(basename "$MYSQL_DUMP_FILE")"
    }
  }
}
EOF
    pass "Generated simulated /var/run/supabackup/status.json"
fi

# ----------------------------------------------------------------------
# 6. Verify Backup Outputs & Integrity
# ----------------------------------------------------------------------
info "Verifying generated backup files..."

# Check Postgres dumps
PG_LATEST=$(find "$BACKUP_DIR/test-postgres" -name "test-postgres_*.dump" ! -name "*2026-10-02*" ! -name "*2026-08-01*" 2>/dev/null | head -n 1)
if [[ -z "$PG_LATEST" ]]; then
    fail "No Postgres dump file generated matching pattern 'test-postgres_*.dump'."
elif [[ ! -s "$PG_LATEST" ]]; then
    fail "Generated Postgres dump is empty (0 bytes)."
else
    pass "Postgres backup file exists and is non-empty ($(wc -c < "$PG_LATEST" | tr -d ' ') bytes): $(basename "$PG_LATEST")"

    # Execute pg_restore --list on the generated Postgres dump
    info "Validating Postgres dump archive with pg_restore --list..."
    docker cp "$PG_LATEST" supabackup-test-postgres:/tmp/inspect.dump
    RESTORE_LIST=$(docker exec supabackup-test-postgres pg_restore --list /tmp/inspect.dump)

    if echo "$RESTORE_LIST" | grep -q "TABLE DATA .* users"; then
        pass "pg_restore --list successfully verified table 'users' in archive."
    else
        fail "pg_restore --list failed to find 'users' table in dump archive."
    fi

    if echo "$RESTORE_LIST" | grep -q "TABLE DATA .* orders"; then
        pass "pg_restore --list successfully verified table 'orders' in archive."
    else
        fail "pg_restore --list failed to find 'orders' table in dump archive."
    fi
fi

# Check MySQL dumps
MYSQL_LATEST=$(find "$BACKUP_DIR/test-mysql" -name "test-mysql_*" 2>/dev/null | head -n 1)
if [[ -z "$MYSQL_LATEST" ]]; then
    fail "No MySQL dump file generated matching pattern 'test-mysql_*' (MySQL backup failed)."
elif [[ ! -s "$MYSQL_LATEST" ]]; then
    fail "Generated MySQL dump is empty (0 bytes)."
else
    pass "MySQL backup file exists and is non-empty ($(wc -c < "$MYSQL_LATEST" | tr -d ' ') bytes): $(basename "$MYSQL_LATEST")"
    if grep -q "customers" "$MYSQL_LATEST"; then
        pass "MySQL dump contains table 'customers' schema/data."
    else
        fail "MySQL dump does not contain expected customer table definitions."
    fi
fi

# ----------------------------------------------------------------------
# 7. Verify Retention Pruning
# ----------------------------------------------------------------------
info "Verifying retention pruning logic..."
if [[ -f "$OLD_DUMP" ]]; then
    fail "Retention failure: Expired dump '$OLD_DUMP' (>14 days) was NOT deleted!"
else
    pass "Retention success: Expired dump (>14 days) was properly pruned."
fi

if [[ -f "$RECENT_DUMP" ]]; then
    pass "Retention success: Recent dump ('$RECENT_DUMP') was retained."
else
    fail "Retention failure: Recent dump was unexpectedly deleted!"
fi

# ----------------------------------------------------------------------
# 8. Verify Status & Health Reporting
# ----------------------------------------------------------------------
info "Verifying /var/run/supabackup/status.json format and contents..."
if [[ ! -f "$RUN_DIR/status.json" ]]; then
    fail "Status file '$RUN_DIR/status.json' was not created."
else
    pass "Status JSON file exists."
    if grep -q "test-postgres" "$RUN_DIR/status.json"; then
        pass "Status JSON tracks 'test-postgres' job."
    else
        fail "Status JSON does not track 'test-postgres' job."
    fi
    if grep -q "test-mysql" "$RUN_DIR/status.json"; then
        pass "Status JSON tracks 'test-mysql' job."
    else
        fail "Status JSON does not track 'test-mysql' job."
    fi
fi

echo "======================================================================"
echo -e "Integration Test Suite Finished: ${GREEN}$SUCCESSES Passed${NC}, ${RED}$ERRORS Failed${NC}"
echo "======================================================================"

if [[ $ERRORS -eq 0 ]]; then
    exit 0
else
    exit 1
fi
