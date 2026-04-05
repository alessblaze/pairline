import Config

port = String.to_integer(System.get_env("PORT") || "8080")
endpoint_host = System.get_env("PHX_HOST") || "localhost"

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  url: [host: endpoint_host, port: port],
  http: [
    ip:
      if(System.get_env("ENABLE_IPV6") == "true",
        do: {0, 0, 0, 0, 0, 0, 0, 0},
        else: {0, 0, 0, 0}
      ),
    port: port
  ],
  check_origin:
    String.split(
      System.get_env("CORS_ORIGINS") || "http://localhost:5173,http://127.0.0.1:5173",
      ","
    ),
  code_reloader: true,
  debug_errors: true,
  secret_key_base: System.get_env("SECRET_KEY_BASE") || raise("SECRET_KEY_BASE is not set")

config :omegle_phoenix, OmeglePhoenix.PubSub, adapter: Phoenix.PubSub.PG2

config :logger, :console, format: "[$level] $message\n"

config :phoenix, :json_library, Jason
