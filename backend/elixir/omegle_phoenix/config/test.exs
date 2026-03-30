import Config

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  http: [ip: {0, 0, 0, 0}, port: 4002],
  secret_key_base: "secret_key_base_test",
  server: false

config :logger, level: :warn
