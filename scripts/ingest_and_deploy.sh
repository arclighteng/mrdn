#!/usr/bin/env bash
# mrdn ingest + deploy — local port of .github/workflows/ingest-deploy.yml
# Owner: coder (Dade). Issue #24 / T-011.
#
# Runs the full pipeline:
#   build -> migrate -> ingest-once -> ingest-house-trades -> enrich-companies
#   -> generate-aliases -> backfill-sectors -> score-backfill -> export
#   -> copy frontend -> wrangler pages deploy -> D1 schema migrate
#   -> D1 data upload -> worker tests -> worker deploy -> KV signals upload
#   -> prune
#
# Logic is a step-for-step port from ingest-deploy.yml (PR #_ for T-011).
# Pipeline correctness changes must happen in the Go subcommands, not here.
#
# Failure handling: any step failure exits non-zero AND posts to Discord
# #the-hatch via `openclaw message send`. Success is silent (cron logs only).
#
# Secret resolution: indirected through resolve_secret() so the backend
# (1Password CLI vs ~/.openclaw/credentials/mrdn.env) can be swapped without
# touching pipeline code. See SECRET_BACKEND below.

set -euo pipefail

# -------------------------------------------------------------------
# Config
# -------------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

LOG_DIR="${HOME}/.openclaw/logs"
LOG_FILE="${LOG_DIR}/mrdn-ingest-$(date -u +%Y%m%dT%H%M%SZ).log"
mkdir -p "$LOG_DIR"

# Mirror all stdout/stderr to the log file as well as the terminal.
exec > >(tee -a "$LOG_FILE") 2>&1

DISCORD_CHANNEL="channel:1502383408129769623"  # #the-hatch

# Secret backend: "op" (1Password CLI) or "envfile" (~/.openclaw/credentials/mrdn.env).
# Default: "op" per T-011 spec resolution by arc 2026-06-03.
SECRET_BACKEND="${MRDN_SECRET_BACKEND:-op}"

# 1Password vault — TBD by arc; will be wired once vault is selected.
OP_VAULT="${MRDN_OP_VAULT:-PLACEHOLDER_VAULT}"

# Non-secret config (was GH `vars.*`, not `secrets.*`).
# Source the env file for non-secret values like CF_ACCOUNT_ID and KV namespace.
NONSECRET_ENV="${HOME}/.openclaw/credentials/mrdn.env"
if [[ -f "$NONSECRET_ENV" ]]; then
    # shellcheck disable=SC1090
    . "$NONSECRET_ENV"
fi

: "${CF_ACCOUNT_ID:?CF_ACCOUNT_ID not set — populate ${NONSECRET_ENV}}"
: "${MQL_KV_NAMESPACE_ID:?MQL_KV_NAMESPACE_ID not set — populate ${NONSECRET_ENV}}"

# -------------------------------------------------------------------
# Helpers
# -------------------------------------------------------------------

log() { printf '[%s] %s\n' "$(date -Iseconds)" "$*"; }

notify_failure() {
    local step="$1"
    local exit_code="$2"
    local tail_log
    tail_log="$(tail -n 30 "$LOG_FILE" 2>/dev/null | sed 's/`/'\''/g' || true)"
    local msg
    msg=$(cat <<EOF
mrdn-ingest FAILED at step: \`${step}\` (exit ${exit_code})

Host: $(hostname)
Run log: \`${LOG_FILE}\`

Tail (last 30 lines):
\`\`\`
${tail_log}
\`\`\`
EOF
)
    if command -v openclaw >/dev/null; then
        openclaw message send \
            --channel discord \
            --account default \
            --target "$DISCORD_CHANNEL" \
            --message "$msg" || log "WARN: openclaw message send failed"
    else
        log "WARN: openclaw CLI not on PATH — failure not posted to Discord"
    fi
}

# Trap any failure and notify. The trap fires on `set -e` exit too.
on_err() {
    local exit_code=$?
    notify_failure "${CURRENT_STEP:-unknown}" "$exit_code"
    exit "$exit_code"
}
trap on_err ERR

# Resolve a single secret by its conventional name. The caller passes the
# logical key (e.g. MRDN_POLYGON_API_KEY); we map to the backend-specific
# lookup. Output: the secret value on stdout.
resolve_secret() {
    local key="$1"
    case "$SECRET_BACKEND" in
        op)
            # Convention: op item per secret, field named "credential".
            # e.g. `op read "op://${OP_VAULT}/${key}/credential"`
            op read "op://${OP_VAULT}/${key}/credential" 2>/dev/null \
                || { log "ERROR: op read failed for ${key}"; return 1; }
            ;;
        envfile)
            # Read from a chmod-600 dotenv at ~/.openclaw/credentials/mrdn.secrets.env
            local secrets_file="${HOME}/.openclaw/credentials/mrdn.secrets.env"
            if [[ ! -f "$secrets_file" ]]; then
                log "ERROR: ${secrets_file} not found"
                return 1
            fi
            # shellcheck disable=SC1090
            ( . "$secrets_file" && printf '%s' "${!key:-}" )
            ;;
        *)
            log "ERROR: unknown SECRET_BACKEND=${SECRET_BACKEND}"
            return 1
            ;;
    esac
}

run_step() {
    local name="$1"
    shift
    CURRENT_STEP="$name"
    log "==== STEP: ${name} ===="
    "$@"
    log "==== STEP DONE: ${name} ===="
}

# -------------------------------------------------------------------
# Load secrets (fail fast before doing any work)
# -------------------------------------------------------------------

log "Loading secrets via backend: ${SECRET_BACKEND}"
CURRENT_STEP="load-secrets"
MRDN_POLYGON_API_KEY=$(resolve_secret MRDN_POLYGON_API_KEY)
MRDN_FEC_API_KEY=$(resolve_secret MRDN_FEC_API_KEY)
MRDN_FINNHUB_API_KEY=$(resolve_secret MRDN_FINNHUB_API_KEY)
MRDN_COURTLISTENER_TOKEN=$(resolve_secret MRDN_COURTLISTENER_TOKEN)
MRDN_FMP_API_KEY=$(resolve_secret MRDN_FMP_API_KEY)
CF_API_TOKEN=$(resolve_secret CF_API_TOKEN)
export MRDN_POLYGON_API_KEY MRDN_FEC_API_KEY MRDN_FINNHUB_API_KEY \
       MRDN_COURTLISTENER_TOKEN MRDN_FMP_API_KEY \
       CF_API_TOKEN \
       CLOUDFLARE_API_TOKEN="$CF_API_TOKEN" \
       CLOUDFLARE_ACCOUNT_ID="$CF_ACCOUNT_ID"

export DATABASE_URL="file:mrdn.db"

# -------------------------------------------------------------------
# Steps — straight port of ingest-deploy.yml
# -------------------------------------------------------------------

run_step build go build -o mrdn ./cmd/mrdn

run_step migrate ./mrdn migrate

run_step ingest-once ./mrdn ingest-once

run_step ingest-house-trades \
    ./mrdn ingest-house-trades --file data/seed/house_trades.json

run_step enrich-companies ./mrdn enrich-companies

run_step generate-aliases ./mrdn generate-aliases

run_step backfill-sectors \
    ./mrdn backfill-sectors --file data/seed/house_trades.json

run_step score-backfill ./mrdn score-backfill --workers 4

run_step export ./mrdn export --out dist/data

CURRENT_STEP=copy-frontend
log "==== STEP: copy-frontend ===="
cp web/static/* dist/
log "==== STEP DONE: copy-frontend ===="

run_step pages-deploy \
    npx wrangler pages deploy dist --project-name=mrdn

# ----- D1 schema migrate -----
CURRENT_STEP=d1-schema-migrate
log "==== STEP: d1-schema-migrate ===="
(
    cd workers/query
    npx wrangler d1 execute mrdn-db \
        --file=../../internal/db/migrations/001_sqlite_initial.sql \
        --remote
)
log "==== STEP DONE: d1-schema-migrate ===="

# ----- D1 data upload (clean slate, then chunked insert) -----
CURRENT_STEP=d1-data-upload
log "==== STEP: d1-data-upload ===="

TABLES="companies persons events congressional_trades contracts sanctions tariffs tariff_hs_codes tariff_countries warn_filings donations lobbying court_filings court_filing_parties market_data insider_trades person_committees company_hs_codes score_weights bills entity_aliases entity_links source_meta scores api_keys party_history"

# Clean slate: delete all rows in dep-correct (reverse) order.
: > d1-delete.sql
echo "PRAGMA foreign_keys = OFF;" >> d1-delete.sql
REVERSED=$(echo "$TABLES" | tr ' ' '\n' | tac | tr '\n' ' ')
for TABLE in $REVERSED; do
    echo "DELETE FROM $TABLE;" >> d1-delete.sql
done
echo "PRAGMA foreign_keys = ON;" >> d1-delete.sql

(
    cd workers/query
    npx wrangler d1 execute mrdn-db --file=../../d1-delete.sql --remote || true
)

# Dump local rows as INSERT statements.
: > d1-data.sql
for TABLE in $TABLES; do
    echo "-- $TABLE" >> d1-data.sql
    sqlite3 mrdn.db ".mode insert $TABLE" "SELECT * FROM $TABLE;" >> d1-data.sql 2>/dev/null || true
done

# INSERT OR IGNORE as safety net against duplicates mid-batch.
sed -i 's/^INSERT INTO/INSERT OR IGNORE INTO/g' d1-data.sql

# Chunk + execute (D1 has a per-request size limit).
split -l 500 -d d1-data.sql d1-chunk-
(
    cd workers/query
    for CHUNK in ../../d1-chunk-*; do
        npx wrangler d1 execute mrdn-db --file="$CHUNK" --remote || true
    done
)
rm -f d1-chunk-* d1-delete.sql d1-data.sql
log "==== STEP DONE: d1-data-upload ===="

# ----- Worker tests + deploy -----
CURRENT_STEP=worker-deps
log "==== STEP: worker-deps ===="
( cd workers/query && npm ci )
log "==== STEP DONE: worker-deps ===="

run_step worker-tests bash -c 'cd workers/query && npx vitest run'

run_step worker-deploy bash -c 'cd workers/query && npx wrangler deploy'

# ----- KV: signals + metadata -----
CURRENT_STEP=kv-upload
log "==== STEP: kv-upload ===="

EXPORTED_AT=$(python3 -c "import json; print(json.load(open('dist/data/meta.json'))['exported_at'])")
npx wrangler kv key put --namespace-id="$MQL_KV_NAMESPACE_ID" "meta:data_as_of" "$EXPORTED_AT"

for SIGNAL_FILE in dist/data/signals/swarms.json dist/data/signals/first-movers.json dist/data/signals/round-trips.json dist/data/signals/partisan-consensus.json dist/data/signals/partisan-contrarian.json; do
    SIGNAL_NAME=$(basename "$SIGNAL_FILE" .json)
    TICKERS=$(python3 -c "
import json
data = json.load(open('$SIGNAL_FILE'))
items = data.get('data', [])
tickers = list(set(item.get('ticker', '') for item in items if item.get('ticker')))
print(json.dumps(tickers))
")
    npx wrangler kv key put --namespace-id="$MQL_KV_NAMESPACE_ID" "signal:$SIGNAL_NAME" "$TICKERS"
done
log "==== STEP DONE: kv-upload ===="

# ----- Prune old local data -----
run_step prune ./mrdn prune --keep-days 90

# -------------------------------------------------------------------
# Audit trail: commit the export JSON back to the repo on `audit` branch.
# Separate commit, message includes [skip ci] to avoid GHA loops.
# -------------------------------------------------------------------

CURRENT_STEP=audit-commit
log "==== STEP: audit-commit ===="
if [[ -d dist/data ]]; then
    git add -f dist/data
    if ! git diff --cached --quiet; then
        git commit -m "audit: export $(date -u +%Y-%m-%dT%H:%M:%SZ) [skip ci]"
        # Push only if remote/origin and we have credentials.
        # NOTE: requires git credential helper to be set up for the cron user.
        git push origin "$(git branch --show-current)" || \
            log "WARN: audit commit push failed (left local)"
    else
        log "No changes in dist/data — skipping audit commit."
    fi
else
    log "No dist/data dir — skipping audit commit."
fi
log "==== STEP DONE: audit-commit ===="

log "ALL STEPS COMPLETE."
