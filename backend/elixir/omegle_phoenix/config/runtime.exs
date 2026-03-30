import Config

if config_env() == :prod do
  port = System.get_env("PORT") || "8080"
  cors_origins =
    System.get_env("CORS_ORIGINS", "")
    |> String.split(",", trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))

  endpoint_config = [
    http: [ip: {0, 0, 0, 0}, port: String.to_integer(port)],
    secret_key_base: System.get_env("SECRET_KEY_BASE")
  ]

  endpoint_config =
    if cors_origins == [] do
      endpoint_config
    else
      Keyword.put(endpoint_config, :check_origin, cors_origins)
    end

  config :omegle_phoenix, OmeglePhoenixWeb.Endpoint,
    endpoint_config
end
