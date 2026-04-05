defmodule OmeglePhoenixWeb.HealthController do
  use OmeglePhoenixWeb, :controller

  def index(conn, _params) do
    response = %{
      service: "omegle-phoenix",
      status: "ok",
      timestamp: System.system_time(:millisecond)
    }

    response =
      if OmeglePhoenix.Config.health_details_enabled?() do
        Map.merge(response, %{
          node: Atom.to_string(Node.self()),
          connected_nodes: Enum.map(Node.list(), &Atom.to_string/1),
          active_sessions: OmeglePhoenix.SessionManager.count_active_sessions(),
          queue_depths: OmeglePhoenix.Matchmaker.queue_depths(),
          metrics: OmeglePhoenix.Metrics.snapshot()
        })
      else
        response
      end

    json(conn, response)
  end
end
