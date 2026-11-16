#!/bin/bash
kubectl exec -n "$PREVIEW_NAMESPACE" deploy/preview-postgres -- psql -c "insert into users ..."
