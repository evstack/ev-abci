#!/bin/bash
set -euo pipefail

DA_URL="${DA_URL:-http://local-da:7980}"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-30}"
SLEEP_INTERVAL="${SLEEP_INTERVAL:-2}"

echo "⏳ Waiting for local-da to be available at $DA_URL..."

for i in $(seq 1 $MAX_ATTEMPTS); do
    echo "   Attempt $i/$MAX_ATTEMPTS..."
    
    if nc -z local-da 7980 >/dev/null 2>&1; then
        echo "✅ local-da is ready!"
        exit 0
    fi
    
    if [[ $i -lt $MAX_ATTEMPTS ]]; then
        echo "   Not ready, waiting ${SLEEP_INTERVAL}s..."
        sleep $SLEEP_INTERVAL
    fi
done

echo "❌ local-da is not available after $MAX_ATTEMPTS attempts"
echo "   URL: $DA_URL"
echo "   Consider increasing MAX_ATTEMPTS or checking local-da service"
exit 1