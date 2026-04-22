#!/usr/bin/env sh
set -eu

CLUSTER_FILE="elixir-cluster-compose.yml"
CF_CLUSTER_FILE="elixir-cluster-compose-cf.yml"
BUILD_SCRIPT="./build-container.sh"

use_default=false
use_cf=false
down_only=false

for arg in "$@"; do
  case "$arg" in
    --default)
      use_default=true
      ;;
    --cf)
      use_cf=true
      ;;
    --down)
      down_only=true
      ;;
    *)
      echo "Unknown option: $arg" >&2
      echo "Usage: $0 [--default] [--cf] [--down]" >&2
      exit 1
      ;;
  esac
done

selected_compose=""
if [ "$use_default" = true ]; then
  selected_compose=""
elif [ "$use_cf" = true ] && [ -f "$CF_CLUSTER_FILE" ]; then
  selected_compose="$CF_CLUSTER_FILE"
elif [ -f "$CLUSTER_FILE" ]; then
  selected_compose="$CLUSTER_FILE"
fi

if [ -z "$selected_compose" ]; then
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
    docker compose -f "$selected_compose" down
    exit 0
  fi

  if [ ! -f "$BUILD_SCRIPT" ]; then
    echo "Missing $BUILD_SCRIPT" >&2
    exit 1
  fi

  bash "$BUILD_SCRIPT"
  docker compose -f "$selected_compose" down
  docker compose -f "$selected_compose" up -d
fi
