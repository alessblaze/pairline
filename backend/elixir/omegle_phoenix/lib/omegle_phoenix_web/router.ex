defmodule OmeglePhoenixWeb.Router do
  use Phoenix.Router

  pipeline :api do
    plug(:accepts, ["json"])
  end

  scope "/api", OmeglePhoenixWeb do
    pipe_through(:api)

    get("/health", HealthController, :index)
  end
end
