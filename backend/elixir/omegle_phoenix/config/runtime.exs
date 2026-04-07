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

if config_env() == :prod do
  port = System.get_env("PORT") || "8080"

  cors_origins =
    System.get_env("CORS_ORIGINS", "")
    |> String.split(",", trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))

  endpoint_config = [
    http: [
      ip:
        if(System.get_env("ENABLE_IPV6") == "true",
          do: {0, 0, 0, 0, 0, 0, 0, 0},
          else: {0, 0, 0, 0}
        ),
      port: String.to_integer(port)
    ],
    secret_key_base: System.get_env("SECRET_KEY_BASE")
  ]

  endpoint_config =
    if cors_origins == [] do
      endpoint_config
    else
      Keyword.put(endpoint_config, :check_origin, cors_origins)
    end

  config :omegle_phoenix, OmeglePhoenixWeb.Endpoint, endpoint_config
end
