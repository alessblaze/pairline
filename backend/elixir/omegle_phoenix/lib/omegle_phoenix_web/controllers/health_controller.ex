defmodule OmeglePhoenixWeb.HealthController do
  use OmeglePhoenixWeb, :controller

  def index(conn, _params) do
    json(conn, %{
      status: "ok",
      timestamp: System.system_time(:millisecond),
      node: Atom.to_string(Node.self()),
      connected_nodes: Enum.map(Node.list(), &Atom.to_string/1),
      active_sessions: OmeglePhoenix.SessionManager.count_active_sessions(),
      queue_depths: OmeglePhoenix.Matchmaker.queue_depths(),
      metrics: OmeglePhoenix.Metrics.snapshot()
    })
  end
end
