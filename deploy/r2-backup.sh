#!/bin/bash
# Daily pg_dump → Cloudflare R2. Deploy to /opt/app/r2-backup.sh on all nodes.
# Cron runs this on every node; the script self-exits on non-primary nodes so
# the backup always comes from the current leader without manual coordination.
#
# Requires in /etc/app.env:
#   DATABASE_URL, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_ACCOUNT_ID
# R2 bucket name: app-backups (create in Cloudflare dashboard)
# Retention: keeps last 30 daily backups, deletes older ones.

set -euo pipefail

# Load env
set -a
# shellcheck disable=SC1091
source /etc/app.env
set +a

# Only the Patroni primary runs the backup. Replicas exit cleanly.
if ! curl -sf http://localhost:8008/primary > /dev/null 2>&1; then
    echo "$(date -u +%FT%TZ) not primary, skipping backup"
    exit 0
fi

DATE=$(date +%Y-%m-%d)
BACKUP_FILE="/tmp/app-${DATE}.sql.gz"

echo "$(date -u +%FT%TZ) starting backup for ${DATE}"

pg_dump "${DATABASE_URL}" | gzip > "${BACKUP_FILE}"

AWS_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID}" \
AWS_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY}" \
aws s3 cp "${BACKUP_FILE}" "s3://app-backups/${DATE}.sql.gz" \
    --endpoint-url "https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com" \
    --no-progress

rm -f "${BACKUP_FILE}"

echo "$(date -u +%FT%TZ) backup uploaded: ${DATE}.sql.gz"

# Prune backups beyond the 30 most recent
AWS_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID}" \
AWS_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY}" \
aws s3 ls "s3://app-backups/" \
    --endpoint-url "https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com" \
    | awk '{print $4}' | sort | head -n -30 \
    | xargs -r -I{} \
        env AWS_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID}" \
            AWS_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY}" \
        aws s3 rm "s3://app-backups/{}" \
            --endpoint-url "https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com" \
    || true

echo "$(date -u +%FT%TZ) backup complete"
