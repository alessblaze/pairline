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

  def get_redis_mode do
    case get("REDIS_MODE", "standalone") |> String.downcase() do
      "cluster" -> :cluster
      _ -> :standalone
    end
  end

  def redis_cluster? do
    get_redis_mode() == :cluster
  end

  def get_redis_cluster_nodes do
    get("REDIS_CLUSTER_NODES", "")
    |> String.split(",", trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.map(fn node ->
      case String.split(node, ":", parts: 2) do
        [host, port] ->
          {host, String.to_integer(port)}

        [host] ->
          {host, get_redis_port()}
      end
    end)
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

  def get_match_leader_ttl_ms do
    get("MATCH_LEADER_TTL_MS", "5000") |> String.to_integer()
  end

  def get_session_ttl do
    get("SESSION_TTL", "3600") |> String.to_integer()
  end

  def get_report_grace_seconds do
    get("REPORT_GRACE_SECONDS", "900")
    |> String.to_integer()
    |> max(60)
  end

  def get_admin_stream do
    get("ADMIN_STREAM", "admin:action:stream")
  end

  def get_admin_stream_group do
    get("ADMIN_STREAM_GROUP", "admin:workers")
  end

  def get_admin_stream_block_ms do
    get("ADMIN_STREAM_BLOCK_MS", "1000") |> String.to_integer()
  end

  def get_admin_stream_batch_size do
    get("ADMIN_STREAM_BATCH_SIZE", "50") |> String.to_integer()
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

  def get_cluster_initial_connect_delay_ms do
    get("CLUSTER_INITIAL_CONNECT_DELAY_MS", "3000") |> String.to_integer()
  end

  def get_cluster_connect_retry_attempts do
    get("CLUSTER_CONNECT_RETRY_ATTEMPTS", "3")
    |> String.to_integer()
    |> max(1)
  end

  def get_cluster_connect_retry_delay_ms do
    get("CLUSTER_CONNECT_RETRY_DELAY_MS", "1000") |> String.to_integer()
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

  def get_match_frontier_size do
    get("MATCH_FRONTIER_SIZE", "16")
    |> String.to_integer()
    |> max(1)
  end

  def get_match_sweep_interval_ms do
    get("MATCH_SWEEP_INTERVAL_MS", "15000")
    |> String.to_integer()
    |> max(0)
  end

  def get_match_sweep_stale_after_ms do
    get("MATCH_SWEEP_STALE_AFTER_MS", "30000")
    |> String.to_integer()
    |> max(1_000)
  end

  def get_match_event_stream do
    get("MATCH_EVENT_STREAM", "matchmaking:events")
  end

  def get_match_event_stream_group do
    get("MATCH_EVENT_STREAM_GROUP", "matchmaking:workers")
  end

  def get_match_event_stream_block_ms do
    get("MATCH_EVENT_STREAM_BLOCK_MS", "1000") |> String.to_integer()
  end

  def get_match_event_stream_batch_size do
    get("MATCH_EVENT_STREAM_BATCH_SIZE", "100") |> String.to_integer()
  end

  def get_match_event_stream_maxlen do
    get("MATCH_EVENT_STREAM_MAXLEN", "20000") |> String.to_integer()
  end

  def get_match_shard_count do
    get("MATCH_SHARD_COUNT", "8")
    |> String.to_integer()
    |> max(1)
  end

  def get_match_overflow_wait_ms do
    get("MATCH_OVERFLOW_WAIT_MS", "15000")
    |> String.to_integer()
    |> max(0)
  end

  def get_match_relaxed_wait_ms do
    get("MATCH_RELAXED_WAIT_MS", "5000")
    |> String.to_integer()
    |> max(0)
  end

  def get_router_owner_ttl_seconds do
    get("ROUTER_OWNER_TTL_SECONDS", "30")
    |> String.to_integer()
    |> max(5)
  end

  def get_stream_stale_consumer_idle_ms do
    get("STREAM_STALE_CONSUMER_IDLE_MS", "300000")
    |> String.to_integer()
    |> max(60_000)
  end

  def health_details_enabled? do
    bool_env("HEALTH_DETAILS_ENABLED", System.get_env("MIX_ENV") != "prod")
  end

  def turnstile_insecure_bypass_allowed? do
    bool_env("TURNSTILE_ALLOW_INSECURE_BYPASS", System.get_env("MIX_ENV") != "prod")
  end

  defp bool_env(key, default) do
    case get(key) do
      nil -> default
      value -> parse_bool(value, default)
    end
  end

  defp parse_bool(value, default) when is_binary(value) do
    case value |> String.trim() |> String.downcase() do
      "1" -> true
      "true" -> true
      "yes" -> true
      "on" -> true
      "0" -> false
      "false" -> false
      "no" -> false
      "off" -> false
      _ -> default
    end
  end
end
