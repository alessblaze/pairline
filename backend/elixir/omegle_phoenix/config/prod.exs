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

import Config
host = System.get_env("PHX_HOST") || "example.com"
port = String.to_integer(System.get_env("PORT") || "8080")

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  http: [
    ip:
      if(System.get_env("ENABLE_IPV6") == "true",
        do: {0, 0, 0, 0, 0, 0, 0, 0},
        else: {0, 0, 0, 0}
      ),
    port: port
  ],
  url: [host: host, port: 443, scheme: "https"],
  check_origin:
    String.split(
      System.get_env("CORS_ORIGINS") || "http://localhost:5173,http://127.0.0.1:5173",
      ","
    ),
  secret_key_base: System.get_env("SECRET_KEY_BASE") || raise("SECRET_KEY_BASE is not set")

config :omegle_phoenix, OmeglePhoenix.PubSub, adapter: Phoenix.PubSub.PG
config :phoenix, :json_library, Jason
config :logger, level: :info
# config :logger, :console, format: "[$level] $message\n"
