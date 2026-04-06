FROM hexpm/elixir:1.17.3-erlang-27.1.2-debian-bookworm-20260316-slim

RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential \
 && rm -rf /var/lib/apt/lists/*
