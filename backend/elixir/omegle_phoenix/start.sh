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
