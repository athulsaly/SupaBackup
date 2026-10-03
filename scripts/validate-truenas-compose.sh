#!/usr/bin/env bash
# ==============================================================================
# TrueNAS SCALE Docker Compose Compliance Validator
#
# Validates docker-compose.yaml for TrueNAS SCALE "Install via YAML" (Custom Apps):
# 1. Non-root execution (PUID/PGID 568 or user 568:568)
# 2. No privileged containers (privileged: false / no dangerous capabilities)
# 3. Explicit host bind mount for the backup dataset (/backups)
# 4. No plaintext credentials hardcoded in compose environment
# 5. Defined healthcheck for TrueNAS application health reporting
# ==============================================================================

set -euo pipefail

TARGET_COMPOSE="${1:-docker-compose.yaml}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

ERRORS=0
WARNINGS=0

pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    ERRORS=$((ERRORS + 1))
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
    WARNINGS=$((WARNINGS + 1))
}

info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

echo "======================================================================"
echo " TrueNAS SCALE Compose Specification Validator"
echo " Target File: $TARGET_COMPOSE"
echo "======================================================================"

if [[ ! -f "$TARGET_COMPOSE" ]]; then
    fail "Target compose file '$TARGET_COMPOSE' does not exist."
    echo "Summary: 1 failure(s), 0 warning(s)."
    exit 1
fi

COMPOSE_CONTENT=$(cat "$TARGET_COMPOSE")

# ----------------------------------------------------------------------
# 1. Non-Root Execution (PUID 568, PGID 568, or user: 568:568)
# ----------------------------------------------------------------------
info "Checking container user permissions (TrueNAS apps UID/GID 568)..."

if echo "$COMPOSE_CONTENT" | grep -qE "user:\s*[\"']?0(:0)?[\"']?|user:\s*[\"']?root[\"']?"; then
    fail "Explicit root execution detected ('user: root' or 'user: 0'). TrueNAS apps must run non-root."
elif echo "$COMPOSE_CONTENT" | grep -qE "user:\s*[\"']?568(:568)?[\"']?"; then
    pass "Container explicitly configured to run as user 568 ('user: 568:568')."
elif echo "$COMPOSE_CONTENT" | grep -qE "(PUID|puid):\s*[\"']?568[\"']?" && echo "$COMPOSE_CONTENT" | grep -qE "(PGID|pgid):\s*[\"']?568[\"']?"; then
    pass "Container specifies PUID=568 and PGID=568 environment variables."
else
    fail "No non-root user (568:568) or PUID/PGID 568 configuration found for TrueNAS compatibility."
fi

# ----------------------------------------------------------------------
# 2. Privileged Mode & Dangerous Capabilities
# ----------------------------------------------------------------------
info "Checking security posture (no privileged mode or dangerous capabilities)..."

if echo "$COMPOSE_CONTENT" | grep -qE "privileged:\s*true"; then
    fail "Insecure setting detected: 'privileged: true' is forbidden on TrueNAS SCALE."
else
    pass "Privileged mode is disabled."
fi

if echo "$COMPOSE_CONTENT" | grep -qE "(SYS_ADMIN|ALL)"; then
    fail "Dangerous Linux capability detected (SYS_ADMIN or ALL)."
else
    pass "No dangerous Linux capabilities requested."
fi

# ----------------------------------------------------------------------
# 3. Host Bind Mount for Backup Storage Dataset
# ----------------------------------------------------------------------
info "Checking storage configuration for TrueNAS dataset bind mount..."

if echo "$COMPOSE_CONTENT" | grep -qE "\s*-\s*[^:]+:/backups(:[a-z,]+)?"; then
    pass "Explicit bind-mount detected for target mount point '/backups'."
elif echo "$COMPOSE_CONTENT" | grep -qE "target:\s*[\"']?/backups[\"']?"; then
    pass "Long-syntax bind mount detected targeting '/backups'."
else
    fail "No volume or bind mount found mapping a host dataset to container path '/backups'."
fi

# ----------------------------------------------------------------------
# 4. Sensitive Credential Exposure Check
# ----------------------------------------------------------------------
info "Checking for hardcoded database credentials..."

if echo "$COMPOSE_CONTENT" | grep -qE "postgresql://[^:\$]+:[^@\$]+@|mysql://[^:\$]+:[^@\$]+@"; then
    fail "Hardcoded plaintext database connection string detected in compose file. Use environment variables or *_FILE secrets."
else
    pass "No hardcoded plaintext database connection strings found."
fi

# ----------------------------------------------------------------------
# 5. Docker Healthcheck Definition
# ----------------------------------------------------------------------
info "Checking Docker Healthcheck specification..."

if echo "$COMPOSE_CONTENT" | grep -qE "healthcheck:"; then
    pass "Container includes a 'healthcheck' definition for TrueNAS health reporting."
else
    warn "No 'healthcheck' defined in compose file. TrueNAS SCALE UI may not accurately reflect service health."
fi

# ----------------------------------------------------------------------
# 6. Restart Policy
# ----------------------------------------------------------------------
info "Checking container restart policy..."

if echo "$COMPOSE_CONTENT" | grep -qE "restart:\s*(unless-stopped|always)"; then
    pass "Valid restart policy specified."
else
    warn "Restart policy is not set to 'unless-stopped' or 'always'."
fi

# ----------------------------------------------------------------------
# Summary & Exit
# ----------------------------------------------------------------------
echo "======================================================================"
if [[ $ERRORS -eq 0 ]]; then
    echo -e "${GREEN}TrueNAS Compose Validation PASSED${NC} (Warnings: $WARNINGS)"
    exit 0
else
    echo -e "${RED}TrueNAS Compose Validation FAILED${NC} with $ERRORS error(s) and $WARNINGS warning(s)."
    exit 1
fi
