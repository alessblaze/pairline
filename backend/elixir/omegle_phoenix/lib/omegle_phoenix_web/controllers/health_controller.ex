defmodule OmeglePhoenixWeb.HealthController do
  use OmeglePhoenixWeb, :controller

  def index(conn, _params) do
    {:ok, sessions} = OmeglePhoenix.SessionManager.get_all_sessions()

    json(conn, %{
      status: "ok",
      timestamp: System.system_time(:millisecond),
      node: Atom.to_string(Node.self()),
      connected_nodes: Enum.map(Node.list(), &Atom.to_string/1),
      active_sessions: map_size(sessions),
      metrics: OmeglePhoenix.Metrics.snapshot()
    })
  end
end
