#!/bin/bash

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

    if [ "$NODE_DISTRIBUTION" = "long" ]; then
        exec elixir --name "$NODE_NAME" --cookie "$NODE_COOKIE" -S mix phx.server
    fi

    exec elixir --sname "$NODE_NAME" --cookie "$NODE_COOKIE" -S mix phx.server
fi

exec mix phx.server
