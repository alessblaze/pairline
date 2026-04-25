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

FROM registry.access.redhat.com/ubi9/ubi-minimal

ARG TARGETARCH
ARG ERLANG_VERSION=28.4.3
ARG ELIXIR_VERSION=1.19.5
ARG ELIXIR_OTP=28

ENV LANG=en_US.UTF-8 \
    LANGUAGE=en_US:en \
    LC_ALL=en_US.UTF-8 \
    ELIXIR_HOME=/usr/local/elixir

ENV PATH="${ELIXIR_HOME}/bin:${PATH}"

RUN set -eux; \
    microdnf install -y \
      ca-certificates \
      unzip \
      glibc-langpack-en; \
    case "${TARGETARCH}" in \
      amd64) RPM_ARCH="x86_64" ;; \
      arm64) RPM_ARCH="aarch64" ;; \
      *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/erlang.rpm \
      "https://github.com/rabbitmq/erlang-rpm/releases/download/v${ERLANG_VERSION}/erlang-${ERLANG_VERSION}-1.el9.${RPM_ARCH}.rpm"; \
    rpm -ivh /tmp/erlang.rpm; \
    rm -f /tmp/erlang.rpm; \
    curl -fsSL -o /tmp/elixir.zip \
      "https://github.com/elixir-lang/elixir/releases/download/v${ELIXIR_VERSION}/elixir-otp-${ELIXIR_OTP}.zip"; \
    mkdir -p "${ELIXIR_HOME}"; \
    unzip /tmp/elixir.zip -d "${ELIXIR_HOME}"; \
    rm -f /tmp/elixir.zip; \
    microdnf clean all; \
    rm -rf /var/cache/dnf /var/cache/yum

CMD ["iex"]