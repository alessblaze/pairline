defmodule OmeglePhoenixWeb.HealthController do
  use OmeglePhoenixWeb, :controller

  def index(conn, _params) do
    json(conn, %{
      status: "ok",
      timestamp: System.system_time(:millisecond)
    })
  end
end
