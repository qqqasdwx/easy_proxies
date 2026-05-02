#!/bin/bash
set -euo pipefail

mkdir -p data logs

docker compose pull
docker compose down
docker compose up -d
