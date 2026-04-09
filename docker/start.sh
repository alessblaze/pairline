#!/usr/bin/env sh
set -eu

CLUSTER_FILE="elixir-cluster-compose.yml"
BUILD_SCRIPT="./build-container.sh"

use_default=false
down_only=false

for arg in "$@"; do
  case "$arg" in
    --default)
      use_default=true
      ;;
    --down)
      down_only=true
      ;;
    *)
      echo "Unknown option: $arg" >&2
      echo "Usage: $0 [--default] [--down]" >&2
      exit 1
      ;;
  esac
done

if [ "$use_default" = true ] || [ ! -f "$CLUSTER_FILE" ]; then
  if [ "$down_only" = true ]; then
    docker compose down
    exit 0
  fi

  if [ ! -f "$BUILD_SCRIPT" ]; then
    echo "Missing $BUILD_SCRIPT" >&2
    exit 1
  fi

  bash "$BUILD_SCRIPT"
  docker compose down
  docker compose up -d
else
  if [ "$down_only" = true ]; then
    docker compose -f "$CLUSTER_FILE" down
    exit 0
  fi

  if [ ! -f "$BUILD_SCRIPT" ]; then
    echo "Missing $BUILD_SCRIPT" >&2
    exit 1
  fi

  bash "$BUILD_SCRIPT"
  docker compose -f "$CLUSTER_FILE" down
  docker compose -f "$CLUSTER_FILE" up -d
fi