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

defmodule OmeglePhoenixWeb.HealthController do
  use OmeglePhoenixWeb, :controller
  require Logger
  require OpenTelemetry.Tracer, as: Tracer
  alias OmeglePhoenix.Tracing

  def index(conn, _params) do
    Tracer.with_span "phoenix.health.index", %{kind: :server} do
      Tracing.annotate_server("phoenix.health.index")
      details_allowed = health_details_allowed?(conn)

      Tracer.set_attributes(%{
        "http.route" => "/api/health",
        "service.name" => "omegle-phoenix",
        "health.details_enabled" => details_allowed
      })

      response = %{
        service: "omegle-phoenix",
        status: "ok",
        timestamp: System.system_time(:millisecond)
      }

      response =
        if details_allowed do
          details = %{
            node: Atom.to_string(Node.self()),
            connected_nodes: Enum.map(Node.list(), &Atom.to_string/1),
            queue_depths: OmeglePhoenix.Matchmaker.queue_depths(),
            metrics: OmeglePhoenix.Metrics.snapshot()
          }

          details =
            case OmeglePhoenix.SessionManager.count_active_sessions() do
              {:ok, active_sessions} ->
                Tracer.set_attribute("session.active_count", active_sessions)
                Map.put(details, :active_sessions, active_sessions)

              {:error, reason} ->
                Logger.warning(
                  "Health controller could not load active session count: #{inspect(reason)}"
                )

                details
            end

          Map.merge(response, details)
        else
          response
        end

      json(conn, response)
    end
  end

  defp health_details_allowed?(conn) do
    OmeglePhoenix.Config.health_details_enabled?() or valid_internal_secret?(conn)
  end

  defp valid_internal_secret?(conn) do
    expected = System.get_env("SHARED_SECRET") || ""

    provided =
      conn
      |> get_req_header("x-shared-secret")
      |> List.first()
      |> to_string()

    expected != "" and provided == expected
  end
end
