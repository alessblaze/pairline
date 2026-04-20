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

defmodule OmeglePhoenix.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    :opentelemetry_cowboy.setup()
    OpentelemetryPhoenix.setup(adapter: :cowboy2, endpoint_prefix: [:omegle_phoenix, :endpoint])

    children = [
      {Finch,
       name: OmeglePhoenixFinch,
       pools: %{default: [size: OmeglePhoenix.Config.get_finch_pool_size()]}},
      {Phoenix.PubSub, name: OmeglePhoenix.PubSub},
      {Task.Supervisor, name: OmeglePhoenix.TaskSupervisor},
      OmeglePhoenix.Bots.Supervisor,
      OmeglePhoenix.Redis,
      OmeglePhoenix.ClusterConnector,
      OmeglePhoenix.Metrics,
      OmeglePhoenix.OTLPMetrics,
      OmeglePhoenix.MessageModeration,
      OmeglePhoenix.SessionManager,
      OmeglePhoenix.Router,
      OmeglePhoenix.Matchmaker,
      OmeglePhoenix.Reaper,
      OmeglePhoenixWeb.Endpoint
    ]

    opts = [strategy: :one_for_one, name: OmeglePhoenix.Supervisor]
    Supervisor.start_link(children, opts)
  end
end
