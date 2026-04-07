defmodule OmeglePhoenixWeb.HealthController do
  use OmeglePhoenixWeb, :controller
  require Logger

  def index(conn, _params) do
    response = %{
      service: "omegle-phoenix",
      status: "ok",
      timestamp: System.system_time(:millisecond)
    }

    response =
      if OmeglePhoenix.Config.health_details_enabled?() do
        details = %{
          node: Atom.to_string(Node.self()),
          connected_nodes: Enum.map(Node.list(), &Atom.to_string/1),
          queue_depths: OmeglePhoenix.Matchmaker.queue_depths(),
          metrics: OmeglePhoenix.Metrics.snapshot()
        }

        details =
          case OmeglePhoenix.SessionManager.count_active_sessions() do
            {:ok, active_sessions} ->
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
