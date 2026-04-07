#!/bin/bash
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


# Load environment variables from .env file if it exists and isn't explicitly disabled.
# Existing environment variables win, which keeps Docker/provisioned env stable.
if [ "${SKIP_DOTENV:-0}" != "1" ] && [ -f .env ]; then
    while IFS='=' read -r key value; do
        if [ -z "$key" ] || [[ "$key" =~ ^# ]]; then
            continue
        fi

        if [ -z "${!key+x}" ]; then
            export "$key=$value"
        fi
    done < .env
fi

if [ -n "$NODE_NAME" ]; then
    if [ -z "$NODE_COOKIE" ]; then
        echo "NODE_COOKIE must be set when NODE_NAME is provided" >&2
        exit 1
    fi

    NODE_DISTRIBUTION="${NODE_DISTRIBUTION:-short}"
    NORMALIZED_NODE_NAME="$NODE_NAME"

    case "$NODE_DISTRIBUTION" in
        long)
            if [[ "$NORMALIZED_NODE_NAME" != *@* ]]; then
                echo "NODE_NAME must include @host when NODE_DISTRIBUTION=long" >&2
                exit 1
            fi
            ;;
        short)
            if [[ "$NORMALIZED_NODE_NAME" == *@* ]]; then
                NORMALIZED_NODE_NAME="${NORMALIZED_NODE_NAME%@*}"
            fi
            ;;
        *)
            echo "NODE_DISTRIBUTION must be 'short' or 'long'" >&2
            exit 1
            ;;
    esac

    if [ "$NODE_DISTRIBUTION" = "long" ]; then
        exec elixir --name "$NORMALIZED_NODE_NAME" --cookie "$NODE_COOKIE" -S mix phx.server
    fi

    exec elixir --sname "$NORMALIZED_NODE_NAME" --cookie "$NODE_COOKIE" -S mix phx.server
fi

exec mix phx.server
