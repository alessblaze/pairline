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

  def get_redis_pool_size do
    get("REDIS_POOL_SIZE", "16")
    |> String.to_integer()
    |> max(1)
  end

  def get_cors_origins do
    get("CORS_ORIGINS", "")
  end

  def get_cluster_nodes do
    get("CLUSTER_NODES", "")
    |> String.split(",", trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.map(&String.to_atom/1)
  end

  def get_cluster_connect_interval_ms do
    get("CLUSTER_CONNECT_INTERVAL_MS", "5000") |> String.to_integer()
  end

  def get_reaper_interval_ms do
    get("REAPER_INTERVAL_MS", "10000") |> String.to_integer()
  end

  def get_reaper_batch_size do
    get("REAPER_BATCH_SIZE", "200") |> String.to_integer()
  end

  def get_match_batch_size do
    get("MATCH_BATCH_SIZE", "200") |> String.to_integer()
  end
end
