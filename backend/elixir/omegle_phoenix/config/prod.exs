import Config

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  http: [ip: {0, 0, 0, 0}, port: 8080],
  url: [host: "localhost", port: 8080],
  cache_static_manifest: "priv/static/cache_manifest.json"

config :logger, level: :info
