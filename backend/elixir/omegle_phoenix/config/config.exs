import Config

config :omegle_phoenix,
  ecto_repos: [],
  generators: [timestamp_type: :utc_datetime]

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  url: [host: "localhost"],
  adapter: Phoenix.Endpoint.Cowboy2Adapter,
  render_errors: [
    formats: [html: OmeglePhoenixWeb.ErrorHTML, json: OmeglePhoenixWeb.ErrorJSON],
    layout: false
  ],
  pubsub_server: OmeglePhoenix.PubSub

config :logger, :console,
  format: "$time $metadata[$level] $message\n",
  metadata: [:request_id]

import_config "#{config_env()}.exs"
