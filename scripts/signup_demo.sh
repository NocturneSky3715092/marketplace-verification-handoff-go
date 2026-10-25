#!/bin/sh
set -eu

curl --fail-with-body -X POST http://localhost:8080/signups \
  -H 'Content-Type: application/json' \
  -d '{"signup_id":"signup-demo-01","seller_name":"Ada","seller_email":"seller@example.com","buyer_email":"buyer@example.com","asset_id":"asset-ledger-7","asset_name":"Ledger export","order_id":"order-9001"}'
