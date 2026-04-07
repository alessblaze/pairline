import Config
host = System.get_env("PHX_HOST") || "example.com"
port = String.to_integer(System.get_env("PORT") || "8080")

config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
  http: [
    ip:
      if(System.get_env("ENABLE_IPV6") == "true",
        do: {0, 0, 0, 0, 0, 0, 0, 0},
        else: {0, 0, 0, 0}
      ),
    port: port
  ],
  url: [host: host, port: 443, scheme: "https"],
  check_origin:
    String.split(
      System.get_env("CORS_ORIGINS") || "http://localhost:5173,http://127.0.0.1:5173",
      ","
    ),
  secret_key_base: System.get_env("SECRET_KEY_BASE") || raise("SECRET_KEY_BASE is not set")

config :omegle_phoenix, OmeglePhoenix.PubSub, adapter: Phoenix.PubSub.PG
config :phoenix, :json_library, Jason
config :logger, level: :info
# config :logger, :console, format: "[$level] $message\n"
