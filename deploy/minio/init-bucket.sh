#!/bin/sh

set -eu

: "${MINIO_ENDPOINT:=http://minio:9000}"
: "${MINIO_ACCESS_KEY:=minio}"
: "${MINIO_SECRET_KEY:=minio123}"
: "${MINIO_BUCKET:=kyc-documents}"

until mc alias set local "$MINIO_ENDPOINT" "$MINIO_ACCESS_KEY" "$MINIO_SECRET_KEY" >/dev/null 2>&1; do
  echo "waiting for MinIO at $MINIO_ENDPOINT ..."
  sleep 2
done

mc mb --ignore-existing "local/$MINIO_BUCKET"
echo "bucket '$MINIO_BUCKET' is ready"
