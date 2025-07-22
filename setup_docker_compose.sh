#!/usr/bin/env bash
set -euo pipefail

VOLUME_NAME="indexer_storage_data"

# UID/GID for Chainguard's non-root user
CONTAINER_USER="65532"
CONTAINER_GROUP="65532"

echo "==> Bringing down any existing containers..."
docker compose down || true

echo "==> WARNING removing old volume '$VOLUME_NAME' (if it exists)..."
echo "Proceeding in:"
for i in {5..1}; do
    echo "$i..."
    sleep 1
done
docker volume rm "$VOLUME_NAME" || true

echo "==> Creating volume '$VOLUME_NAME' and adjusting ownership..."
docker run --rm \
  -v "$VOLUME_NAME":/data \
  alpine sh -c "mkdir -p /data && chown -R ${CONTAINER_USER}:${CONTAINER_GROUP} /data"

echo "==> Setting ownership for the logs directory..."
mkdir -p assets/logs

docker run --rm \
  -v "$(pwd)/assets/logs:/logs" \
  alpine sh -c "chown -R ${CONTAINER_USER}:${CONTAINER_GROUP} /logs && chmod -R 755 /logs"

echo "==> Adjusting ownership for config.yaml"
touch config.yaml

docker run --rm \
  -v "$(pwd)/config.yaml:/config.yaml" \
  alpine sh -c "chown ${CONTAINER_USER}:${CONTAINER_GROUP} /config.yaml && chmod 644 /config.yaml"

echo "Permissions Sucessfully Configured"
