#!/usr/bin/env bash
# ==============================================================================
# SupaBackup Security Verification Suite
#
# Asserts:
# 1. While a dump is running, ps inside the container reveals no database secrets.
# 2. A forced connection failure leaves no secret in container logs or status.json.
# ==============================================================================

set -uo pipefail

IMAGE_NAME="${IMAGE_NAME:-supabackup:local}"
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    exit 1
}

info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

echo "======================================================================"
echo " Starting SupaBackup Security Verification"
echo " Image Target: $IMAGE_NAME"
echo "======================================================================"

# ----------------------------------------------------------------------
# Test 1: Process Table Security (ps aux during dump execution)
# ----------------------------------------------------------------------
info "Testing process table security: Verifying no passwords exposed in ps aux during dump..."

# A: Postgres ps check
docker run --rm --entrypoint sh \
  -e SECRET_PASS="super_secret_pg_pass_12345" \
  "$IMAGE_NAME" -c '
    echo "jobs:
  - name: sec-pg
    engine: postgres
    schedule: \"* * * * *\"
    url_env: TEST_PG" > /etc/supabackup/config.yaml

    export TEST_PG="postgresql://testuser:${SECRET_PASS}@127.0.0.1:54399/testdb"

    mv /usr/bin/pg_dump /usr/bin/pg_dump.real
    cat << "EOF" > /usr/bin/pg_dump
#!/bin/sh
ps aux > /tmp/ps_pg.txt
exec /usr/bin/pg_dump.real "$@"
EOF
    chmod +x /usr/bin/pg_dump

    /bin/supabackup run-now >/dev/null 2>&1 || true

    if grep -q "$SECRET_PASS" /tmp/ps_pg.txt; then
      echo "FAIL: Postgres password found in ps aux"
      exit 1
    fi
' || fail "Postgres secret was exposed in process table (ps aux)!"
pass "Postgres dump process table (ps aux) contains no password credentials."

# B: MySQL ps check
docker run --rm --entrypoint sh \
  -e SECRET_PASS="super_secret_mysql_pass_67890" \
  "$IMAGE_NAME" -c '
    echo "jobs:
  - name: sec-mysql
    engine: mysql
    schedule: \"* * * * *\"
    url_env: TEST_MYSQL" > /etc/supabackup/config.yaml

    export TEST_MYSQL="mysql://testuser:${SECRET_PASS}@127.0.0.1:33099/testdb"

    mv /usr/bin/mysqldump /usr/bin/mysqldump.real
    cat << "EOF" > /usr/bin/mysqldump
#!/bin/sh
ps aux > /tmp/ps_mysql.txt
exec /usr/bin/mysqldump.real "$@"
EOF
    chmod +x /usr/bin/mysqldump

    /bin/supabackup run-now >/dev/null 2>&1 || true

    if grep -q "$SECRET_PASS" /tmp/ps_mysql.txt; then
      echo "FAIL: MySQL password found in ps aux"
      exit 1
    fi
' || fail "MySQL secret was exposed in process table (ps aux)!"
pass "MySQL dump process table (ps aux) contains no password credentials."

# ----------------------------------------------------------------------
# Test 2: Error & Status Redaction on Forced Connection Failure
# ----------------------------------------------------------------------
info "Testing secret redaction: Verifying forced connection failure does not leak secrets..."

docker run --rm --entrypoint sh \
  -e SECRET_PG_PASS="pg_secret_canary_98765" \
  -e SECRET_MYSQL_PASS="mysql_secret_canary_43210" \
  "$IMAGE_NAME" -c '
    echo "jobs:
  - name: fail-pg
    engine: postgres
    schedule: \"* * * * *\"
    url_env: TEST_FAIL_PG
  - name: fail-mysql
    engine: mysql
    schedule: \"* * * * *\"
    url_env: TEST_FAIL_MYSQL" > /etc/supabackup/config.yaml

    export TEST_FAIL_PG="postgresql://failuser:${SECRET_PG_PASS}@127.0.0.1:54399/faildb"
    export TEST_FAIL_MYSQL="mysql://failuser:${SECRET_MYSQL_PASS}@127.0.0.1:33099/faildb"

    OUTPUT=$(/bin/supabackup run-now 2>&1 || true)
    STATUS=$(cat /var/run/supabackup/status.json 2>/dev/null || true)

    if echo "$OUTPUT" | grep -q "$SECRET_PG_PASS" || echo "$STATUS" | grep -q "$SECRET_PG_PASS"; then
      echo "FAIL: Postgres password canary leaked in logs or status.json!"
      exit 1
    fi

    if echo "$OUTPUT" | grep -q "$SECRET_MYSQL_PASS" || echo "$STATUS" | grep -q "$SECRET_MYSQL_PASS"; then
      echo "FAIL: MySQL password canary leaked in logs or status.json!"
      exit 1
    fi
' || fail "Database secret canary leaked during forced connection failure!"
pass "Forced connection failures properly sanitized; zero secrets leaked in logs or status.json."

echo "======================================================================"
echo -e "${GREEN}All Security Verification Checks Passed!${NC}"
echo "======================================================================"
