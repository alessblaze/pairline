#!/usr/bin/env bash
# Pairline - Open Source Video Chat and Matchmaking
# Copyright (C) 2026 Albert Blasczykowski
# Aless Microsystems
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

set -u

COMPOSE_FILE="${COMPOSE_FILE:-docker/docker-compose.yml}"
SERVICE="${SERVICE:-phoenix1}"
PHOENIX_IMAGE="${PHOENIX_IMAGE:-pairline-phoenix:local}"
AUTO_BUILD_IMAGE="${AUTO_BUILD_IMAGE:-1}"

SECRET_KEY_BASE="${SECRET_KEY_BASE:-test-secret}"
SHARED_SECRET="${SHARED_SECRET:-test-shared}"

RUN_UNIT="${RUN_UNIT:-1}"
RUN_LIVE="${RUN_LIVE:-1}"
RUN_STRESS="${RUN_STRESS:-1}"
RUN_GO_TESTS="${RUN_GO_TESTS:-1}"
TEST_TRACE="${TEST_TRACE:-1}"

STRESS_SESSION_COUNT="${STRESS_SESSION_COUNT:-12000}"
STRESS_CONCURRENCY="${STRESS_CONCURRENCY:-6000}"
STRESS_PAIR_COUNT="${STRESS_PAIR_COUNT:-1600}"
STRESS_LEAVE_COUNT="${STRESS_LEAVE_COUNT:-2500}"
STRESS_DISCONNECT_COUNT="${STRESS_DISCONNECT_COUNT:-1000}"

declare -A RESULTS

print_header() {
  printf '\n== %s ==\n' "$1"
}

service_running() {
  docker compose -f "$COMPOSE_FILE" ps --status running "$SERVICE" 2>/dev/null | grep -q "$SERVICE"
}

wait_for_service() {
  local attempts="${1:-60}"

  while (( attempts > 0 )); do
    if service_running; then
      return 0
    fi

    sleep 2
    attempts=$((attempts - 1))
  done

  return 1
}

ensure_image() {
  if docker image inspect "$PHOENIX_IMAGE" >/dev/null 2>&1; then
    return 0
  fi

  if ! stage_enabled "$AUTO_BUILD_IMAGE"; then
    echo "Docker image $PHOENIX_IMAGE is missing and AUTO_BUILD_IMAGE is disabled."
    return 1
  fi

  print_header "Build Phoenix Image"
  ./docker/build-container.sh "$PHOENIX_IMAGE"
}

ensure_stack_running() {
  if service_running; then
    return 0
  fi

  ensure_image || return 1

  print_header "Start Docker Stack"
  docker compose -f "$COMPOSE_FILE" up -d

  if wait_for_service; then
    return 0
  fi

  echo "Service $SERVICE did not become ready in time."
  return 1
}

run_in_service() {
  local command="$1"

  docker compose -f "$COMPOSE_FILE" exec -T \
    -e MIX_ENV=test \
    -e SECRET_KEY_BASE="$SECRET_KEY_BASE" \
    -e SHARED_SECRET="$SHARED_SECRET" \
    -e LIVE_REDIS_CLUSTER_TESTS=1 \
    -e STRESS_SESSION_COUNT="$STRESS_SESSION_COUNT" \
    -e STRESS_CONCURRENCY="$STRESS_CONCURRENCY" \
    -e STRESS_PAIR_COUNT="$STRESS_PAIR_COUNT" \
    -e STRESS_LEAVE_COUNT="$STRESS_LEAVE_COUNT" \
    -e STRESS_DISCONNECT_COUNT="$STRESS_DISCONNECT_COUNT" \
    "$SERVICE" \
    bash -lc "cd /app && $command"
}

run_stage() {
  local key="$1"
  local label="$2"
  local command="$3"

  print_header "$label"

  if run_in_service "$command"; then
    RESULTS["$key"]="PASS"
  else
    RESULTS["$key"]="FAIL"
  fi
}

stage_enabled() {
  case "$1" in
    1|true|TRUE|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

mix_test_command() {
  local base_command="$1"

  if stage_enabled "$TEST_TRACE"; then
    printf '%s --trace' "$base_command"
  else
    printf '%s' "$base_command"
  fi
}

if ! service_running; then
  if ! ensure_stack_running; then
    exit 1
  fi
fi

if stage_enabled "$RUN_UNIT"; then
  run_stage "unit" "Elixir Unit Tests" "$(mix_test_command "mix test --no-start")"
fi

if stage_enabled "$RUN_LIVE"; then
  run_stage "live" "Live Redis Integration Tests" "$(mix_test_command "mix test test/redis_live_integration_test.exs")"
fi

if stage_enabled "$RUN_STRESS"; then
  run_stage "stress" "Redis Stress Harness" "mix run scripts/redis_live_stress.exs"
fi

if stage_enabled "$RUN_GO_TESTS"; then
  print_header "Golang Tests"
  if cd backend/golang && go test -v -cover ./...; then
    RESULTS["golang"]="PASS"
  else
    RESULTS["golang"]="FAIL"
  fi
fi

print_header "Summary"

overall=0

for key in unit live stress golang; do
  if [[ -n "${RESULTS[$key]:-}" ]]; then
    printf '%-12s %s\n' "$key" "${RESULTS[$key]}"
    if [[ "${RESULTS[$key]}" != "PASS" ]]; then
      overall=1
    fi
  fi
done

if [[ "$overall" -eq 0 ]]; then
  echo "All selected test stages passed."
else
  echo "One or more test stages failed."
fi
echo "Sometimes Some Live Tests fail due to unclean state of redis."
echo "or tests started before full cluster is stabilized."
echo "in that case do run tests multiple times as these are not static,"
echo "but dynamic test."
echo "stress tests are completely syntetic, proper load profile and"
echo "careful tuning of parameters and timing is needed."
echo "This is the beauty of it, the elixir doesn't hard crash."
echo "supervisor manages the restarts clean."
exit "$overall"