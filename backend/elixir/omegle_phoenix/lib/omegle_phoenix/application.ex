defmodule OmeglePhoenix.Application do
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    children = [
      {Finch, name: OmeglePhoenixFinch, pools: %{default: [size: 10]}},
      {Phoenix.PubSub, name: OmeglePhoenix.PubSub},
      OmeglePhoenix.Redis,
      OmeglePhoenix.SessionManager,
      OmeglePhoenix.Router,
      OmeglePhoenix.Matchmaker,
      OmeglePhoenixWeb.Endpoint
    ]

    opts = [strategy: :one_for_one, name: OmeglePhoenix.Supervisor]
    Supervisor.start_link(children, opts)
  end
end
