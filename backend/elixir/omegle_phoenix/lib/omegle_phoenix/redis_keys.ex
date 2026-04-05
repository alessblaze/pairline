defmodule OmeglePhoenix.RedisKeys do
  @moduledoc false

  @allowed_modes ["lobby", "text", "video"]
  @owner_value_separator "|"

  def active_sessions_key, do: "sessions:active"
  def session_locator_key(session_id), do: "session:locator:" <> session_id
  def session_ip_locator_key(session_id), do: "session:ip_locator:" <> session_id
  def ip_sessions_key(ip), do: "ip:" <> ip

  def route_for_session(%{id: session_id, redis_shard: shard, preferences: preferences}) do
    %{mode: mode(preferences), shard: normalize_shard(shard, mode(preferences)), session_id: session_id}
  end

  def route_for_session(session_id, preferences) when is_binary(session_id) and is_map(preferences) do
    mode = mode(preferences)
    %{mode: mode, shard: initial_shard(mode, preferences, session_id), session_id: session_id}
  end

  def resolve_session_route(session_id, opts \\ []) when is_binary(session_id) do
    verify_exists? = Keyword.get(opts, :verify_exists, true)

    case OmeglePhoenix.Redis.command(["GET", session_locator_key(session_id)]) do
      {:ok, locator} when is_binary(locator) ->
        with {:ok, route} <- decode_locator(session_id, locator),
             :ok <- maybe_verify_session_exists(session_id, route, verify_exists?) do
          {:ok, route}
        else
          {:error, :not_found} = error ->
            _ = OmeglePhoenix.Redis.command(["DEL", session_locator_key(session_id)])
            error

          {:error, _reason} = error ->
            error
        end

      {:ok, nil} ->
        {:error, :not_found}

      {:error, reason} ->
        {:error, reason}

      _ ->
        {:error, :invalid_locator}
    end
  end

  def encode_locator(%{mode: mode, shard: shard}) do
    mode <> @owner_value_separator <> Integer.to_string(shard)
  end

  def decode_locator(session_id, locator) when is_binary(locator) do
    case String.split(locator, @owner_value_separator, parts: 2) do
      [mode, shard_str] when mode in @allowed_modes ->
        case Integer.parse(shard_str) do
          {shard, ""} ->
            {:ok, %{mode: mode, shard: shard, session_id: session_id}}

          _ ->
            {:error, :invalid_locator}
        end

      _ ->
        {:error, :invalid_locator}
    end
  end

  def session_key(route_or_session_id, route \\ nil)

  def session_key(%{} = route, nil), do: "session:" <> tag(route) <> ":data:" <> route.session_id
  def session_key(session_id, %{} = route), do: "session:" <> tag(route) <> ":data:" <> session_id

  def session_ip_key(route_or_session_id, route \\ nil)

  def session_ip_key(%{} = route, nil), do: "session:" <> tag(route) <> ":ip:" <> route.session_id
  def session_ip_key(session_id, %{} = route), do: "session:" <> tag(route) <> ":ip:" <> session_id

  def session_token_key(route_or_session_id, route \\ nil)

  def session_token_key(%{} = route, nil), do: "session:" <> tag(route) <> ":token:" <> route.session_id
  def session_token_key(session_id, %{} = route), do: "session:" <> tag(route) <> ":token:" <> session_id

  def queue_meta_key(route_or_session_id, route \\ nil)

  def queue_meta_key(%{} = route, nil),
    do: "session:" <> tag(route) <> ":queue_meta:" <> route.session_id

  def queue_meta_key(session_id, %{} = route),
    do: "session:" <> tag(route) <> ":queue_meta:" <> session_id

  def session_owner_key(route_or_session_id, route \\ nil)

  def session_owner_key(%{} = route, nil),
    do: "session:" <> tag(route) <> ":owner:" <> route.session_id

  def session_owner_key(session_id, %{} = route),
    do: "session:" <> tag(route) <> ":owner:" <> session_id

  def match_key(route_or_session_id, route \\ nil)

  def match_key(%{} = route, nil), do: "match:" <> tag(route) <> ":" <> route.session_id
  def match_key(session_id, %{} = route), do: "match:" <> tag(route) <> ":" <> session_id

  def recent_match_key(route_or_session_id, route \\ nil)

  def recent_match_key(%{} = route, nil), do: "recent_match:" <> tag(route) <> ":" <> route.session_id
  def recent_match_key(session_id, %{} = route), do: "recent_match:" <> tag(route) <> ":" <> session_id

  def session_queue_key(route_or_session_id, route \\ nil)

  def session_queue_key(%{} = route, nil),
    do: "matchmaking:session_queues:" <> tag(route) <> ":" <> route.session_id

  def session_queue_key(session_id, %{} = route),
    do: "matchmaking:session_queues:" <> tag(route) <> ":" <> session_id

  def session_lock_key(route_or_session_id, route \\ nil)

  def session_lock_key(%{} = route, nil),
    do: "session:lock:" <> tag(route) <> ":" <> route.session_id

  def session_lock_key(session_id, %{} = route),
    do: "session:lock:" <> tag(route) <> ":" <> session_id

  def queue_registry_key(mode, shard) do
    "matchmaking:" <> tag(mode, shard) <> ":queues"
  end

  def strict_bucket_queue_key(mode, shard, bucket) do
    "matchmaking_queue:" <> tag(mode, shard) <> ":bucket:strict:" <> bucket
  end

  def relaxed_bucket_queue_key(mode, shard, family) do
    "matchmaking_queue:" <> tag(mode, shard) <> ":bucket:relaxed:" <> family
  end

  def random_queue_key(mode, shard) do
    "matchmaking_queue:" <> tag(mode, shard) <> ":random:local"
  end

  def shared_random_queue_key(mode, shard) do
    "matchmaking_queue:" <> tag(mode, shard) <> ":random:shared"
  end

  def queue_registry_keys do
    for mode <- @allowed_modes,
        shard <- 0..(OmeglePhoenix.Config.get_match_shard_count() - 1) do
      queue_registry_key(mode, shard)
    end
  end

  def primary_shard(mode, session_id) when is_binary(session_id) do
    :erlang.phash2({normalize_mode(mode), {:primary, session_id}}, OmeglePhoenix.Config.get_match_shard_count())
  end

  def initial_shard(mode, preferences, session_id)
      when is_binary(session_id) and is_map(preferences) do
    _ = preferences
    _ = session_id
    shared_matchmaking_shard(mode)
  end

  def overflow_shard(mode, shard) do
    _ = shard
    shared_matchmaking_shard(mode)
  end

  def normalize_mode(mode) when mode in @allowed_modes, do: mode
  def normalize_mode(mode) when is_atom(mode), do: mode |> Atom.to_string() |> normalize_mode()
  def normalize_mode(_mode), do: "text"

  def mode(preferences) when is_map(preferences) do
    preferences
    |> Map.get("mode", Map.get(preferences, :mode, "text"))
    |> normalize_mode()
  end

  def tag(%{mode: mode, shard: shard}), do: tag(mode, shard)
  def tag(mode, shard), do: "{#{normalize_mode(mode)}:#{normalize_shard(shard, normalize_mode(mode))}}"

  defp maybe_verify_session_exists(_session_id, _route, false), do: :ok
  defp maybe_verify_session_exists(session_id, route, true), do: ensure_session_exists(session_id, route)

  defp ensure_session_exists(session_id, route) do
    case OmeglePhoenix.Redis.command(["EXISTS", session_key(session_id, route)]) do
      {:ok, 1} -> :ok
      {:ok, 0} -> {:error, :not_found}
      {:error, reason} -> {:error, reason}
      _ -> {:error, :not_found}
    end
  end

  defp shared_matchmaking_shard(mode) do
    :erlang.phash2({normalize_mode(mode), :shared_matchmaking}, OmeglePhoenix.Config.get_match_shard_count())
  end

  defp normalize_shard(shard, _mode) when is_integer(shard) and shard >= 0 do
    rem(shard, OmeglePhoenix.Config.get_match_shard_count())
    |> max(0)
    |> case do
      normalized -> normalized
    end
  end

  defp normalize_shard(_shard, mode), do: primary_shard(mode, "fallback")
end
