import Config

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  http: [
    ip:
      if(System.get_env("ENABLE_IPV6") == "true",
        do: {0, 0, 0, 0, 0, 0, 0, 0},
        else: {0, 0, 0, 0}
      ),
    port: 8080
  ],
  check_origin:
    String.split(
      System.get_env("CORS_ORIGINS") || "http://localhost:5173,http://127.0.0.1:5173",
      ","
    ),
  code_reloader: true,
  debug_errors: true,
  secret_key_base: "secret_key_base_dev"

config :omegle_phoenix, OmeglePhoenix.PubSub, adapter: Phoenix.PubSub.PG2

config :logger, :console, format: "[$level] $message\n"

config :phoenix, :json_library, Jason
