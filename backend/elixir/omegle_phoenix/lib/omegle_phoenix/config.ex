defmodule OmeglePhoenix.Config do
  @moduledoc """
  Configuration module for environment variables
  """

  def get(key, default \\ nil) do
    System.get_env(key) || default
  end

  def get_redis_host do
    get("REDIS_HOST", "localhost")
  end

  def get_redis_port do
    get("REDIS_PORT", "6379") |> String.to_integer()
  end

  def get_redis_password do
    password = get("REDIS_PASSWORD", "")
    if password == "", do: nil, else: password
  end

  def get_shared_secret do
    case System.get_env("SHARED_SECRET") do
      nil -> raise "SHARED_SECRET environment variable is required"
      "" -> raise "SHARED_SECRET environment variable must not be empty"
      secret -> secret
    end
  end

  def get_port do
    get("PORT", "8080") |> String.to_integer()
  end

  def get_match_timeout do
    get("MATCH_TIMEOUT", "30000") |> String.to_integer()
  end

  def get_session_ttl do
    get("SESSION_TTL", "3600") |> String.to_integer()
  end

  def get_admin_channel do
    get("ADMIN_CHANNEL", "admin:action")
  end

  def get_cors_origins do
    get("CORS_ORIGINS", "")
  end
end
