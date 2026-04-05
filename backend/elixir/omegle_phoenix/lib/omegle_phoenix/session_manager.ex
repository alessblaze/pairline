defmodule OmeglePhoenix.SessionManager do
  use GenServer

  @session_fields [
    :id,
    :token,
    :ip,
    :redis_shard,
    :status,
    :partner_id,
    :last_partner_id,
    :signaling_ready,
    :webrtc_started,
    :preferences,
    :created_at,
    :last_activity,
    :ban_status,
    :ban_reason
  ]
  @allowed_statuses [:waiting, :matched, :disconnecting]

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def get_session_route(session_id) when is_binary(session_id) do
    OmeglePhoenix.RedisKeys.resolve_session_route(session_id)
  end

  def get_session(session_id) when is_binary(session_id) do
    with {:ok, route} <- get_session_route(session_id) do
      case OmeglePhoenix.Redis.command(["GET", session_key(session_id, route)]) do
        {:ok, nil} -> {:error, :not_found}
        {:ok, payload} -> decode_session(payload)
        _ -> {:error, :not_found}
      end
    end
  end

  def get_session(_session_id), do: {:error, :not_found}

  def get_sessions(session_ids) when is_list(session_ids) do
    ordered_ids =
      session_ids
      |> Enum.filter(&is_binary/1)
      |> Enum.uniq()

    case ordered_ids do
      [] ->
        {:ok, %{}}

      ids ->
        with {:ok, routes} <- load_session_routes(ids) do
          present_ids = Enum.filter(ids, &Map.has_key?(routes, &1))

          if present_ids == [] do
            {:ok, %{}}
          else
            keys = Enum.map(present_ids, &session_key(&1, Map.fetch!(routes, &1)))

            case OmeglePhoenix.Redis.mget(keys) do
              {:ok, payloads} when is_list(payloads) ->
                sessions =
                  present_ids
                  |> Enum.zip(payloads)
                  |> Enum.reduce(%{}, fn
                    {_id, nil}, acc ->
                      acc

                    {id, payload}, acc ->
                      case decode_session(payload) do
                        {:ok, session} -> Map.put(acc, id, session)
                        _ -> acc
                      end
                  end)

                {:ok, sessions}

              _ ->
                {:ok, %{}}
            end
          end
        else
          _ -> {:ok, %{}}
        end
    end
  end

  def get_queue_ready_sessions(session_ids) when is_list(session_ids) do
    ordered_ids =
      session_ids
      |> Enum.filter(&is_binary/1)
      |> Enum.uniq()

    case ordered_ids do
      [] ->
        {:ok, %{}}

      ids ->
        with {:ok, routes} <- load_session_routes(ids) do
          present_ids = Enum.filter(ids, &Map.has_key?(routes, &1))

          if present_ids == [] do
            {:ok, %{}}
          else
            keys = Enum.map(present_ids, &queue_meta_key(&1, Map.fetch!(routes, &1)))

            case OmeglePhoenix.Redis.mget(keys) do
              {:ok, payloads} when is_list(payloads) ->
                sessions =
                  present_ids
                  |> Enum.zip(payloads)
                  |> Enum.reduce(%{}, fn
                    {_id, nil}, acc ->
                      acc

                    {id, payload}, acc ->
                      case decode_queue_meta(payload) do
                        {:ok, meta} -> Map.put(acc, id, meta)
                        _ -> acc
                      end
                  end)

                {:ok, sessions}

              _ ->
                {:ok, %{}}
            end
          end
        else
          _ -> {:ok, %{}}
        end
    end
  end

  def get_all_sessions do
    sessions =
      case OmeglePhoenix.Redis.command(["SMEMBERS", OmeglePhoenix.RedisKeys.active_sessions_key()]) do
        {:ok, session_ids} when is_list(session_ids) ->
          {:ok, batched_sessions} = get_sessions(session_ids)
          _ = prune_stale_session_ids(OmeglePhoenix.RedisKeys.active_sessions_key(), session_ids, batched_sessions)
          batched_sessions

        _ ->
          %{}
      end

    {:ok, sessions}
  end

  def get_sessions_by_ip(ip) when is_binary(ip) do
    sessions =
      case OmeglePhoenix.Redis.command(["SMEMBERS", ip_sessions_key(ip)]) do
        {:ok, session_ids} when is_list(session_ids) ->
          {:ok, batched_sessions} = get_sessions(session_ids)
          _ = prune_stale_session_ids(ip_sessions_key(ip), session_ids, batched_sessions)
          Map.values(batched_sessions)

        _ ->
          []
      end

    {:ok, sessions}
  end

  def get_sessions_by_ip(_ip), do: {:ok, []}

  def count_active_sessions do
    case OmeglePhoenix.Redis.command(["SCARD", OmeglePhoenix.RedisKeys.active_sessions_key()]) do
      {:ok, count} when is_integer(count) -> count
      _ -> 0
    end
  end

  def create_session(session_id, ip, preferences) do
    session_token = generate_session_token()
    now = System.system_time(:second)
    normalized_preferences = normalize_preferences(preferences)
    redis_shard =
      OmeglePhoenix.RedisKeys.initial_shard(
        Map.get(normalized_preferences, "mode", "text"),
        normalized_preferences,
        session_id
      )

    session = %{
      id: session_id,
      token: session_token,
      ip: ip,
      redis_shard: redis_shard,
      status: :waiting,
      partner_id: nil,
      last_partner_id: nil,
      signaling_ready: false,
      webrtc_started: false,
      preferences: normalized_preferences,
      created_at: now,
      last_activity: now,
      ban_status: false,
      ban_reason: nil
    }

    case persist_session(session) do
      :ok ->
        {:ok, session}

      {:error, reason} ->
        {:error, reason}
    end
  end

  def update_session(session_id, updates) do
    with {:ok, session} <- get_session(session_id) do
      normalized_updates = normalize_updates(updates)
      updated_session = Map.merge(session, normalized_updates) |> touch_last_activity()

      case persist_session(updated_session) do
        :ok -> {:ok, updated_session}
        {:error, reason} -> {:error, reason}
      end
    end
  end

  def refresh_session(session_id) do
    case OmeglePhoenix.RedisState.touch_session(session_id, ttl_seconds()) do
      {:ok, "ok"} -> {:ok, session_id}
      {:ok, "not_found"} -> {:error, :not_found}
      {:error, reason} -> {:error, reason}
      _ -> {:error, :not_found}
    end
  end

  def delete_session(session_id) do
    case get_session(session_id) do
      {:ok, session} ->
        case OmeglePhoenix.RedisState.delete_session(
               session_id,
               session.ip,
               report_grace_seconds()
             ) do
          {:ok, _} -> :ok
          {:error, reason} -> {:error, reason}
        end

      {:error, :not_found} = error ->
        error
    end
  end

  def emergency_ban(session_id, reason) do
    case OmeglePhoenix.RedisState.atomic_emergency_ban(session_id, reason, ttl_seconds()) do
      {:ok, "not_found"} ->
        {:error, :not_found}

      {:ok, "already_banned"} ->
        {:ok, %{id: session_id, ban_status: true, ban_reason: reason}}

      {:ok, old_partner_id} ->
        OmeglePhoenix.Matchmaker.leave_queue(session_id)
        OmeglePhoenix.Router.notify_banned(session_id, reason)

        if old_partner_id != "nil" do
          disconnect_known_partner(old_partner_id, session_id)
        end

        {:ok, %{id: session_id, ban_status: true, ban_reason: reason}}

      {:error, err} ->
        {:error, err}
    end
  end

  def emergency_disconnect(session_id) do
    case OmeglePhoenix.RedisState.atomic_emergency_disconnect(session_id, ttl_seconds()) do
      {:ok, "not_found"} ->
        {:error, :not_found}

      {:ok, old_partner_id} ->
        OmeglePhoenix.Matchmaker.leave_queue(session_id)
        OmeglePhoenix.Router.notify_disconnect(session_id, "disconnected by administrator")

        if old_partner_id != "nil" do
          disconnect_known_partner(old_partner_id, session_id)
        end

        {:ok, %{id: session_id}}

      {:error, err} ->
        {:error, err}
    end
  end

  def emergency_ban_ip(ip, reason) do
    {:ok, sessions} = get_sessions_by_ip(ip)

    banned_sessions =
      sessions
      |> Enum.flat_map(fn session ->
        case emergency_ban(session.id, reason) do
          {:ok, _updated} -> [session.id]
          _ -> []
        end
      end)

    {:ok, banned_sessions}
  end

  def emergency_unban(session_id) do
    update_session(session_id, %{ban_status: false, ban_reason: nil})
  end

  def emergency_unban_ip(ip) do
    {:ok, sessions} = get_sessions_by_ip(ip)

    Enum.each(sessions, fn session ->
      _ = emergency_unban(session.id)
    end)

    :ok
  end

  def ip_ban_reason(ip) do
    case OmeglePhoenix.Redis.command(["GET", "ban:ip:#{ip}"]) do
      {:ok, nil} -> nil
      {:ok, reason} -> reason
      _ -> nil
    end
  end

  def pair_sessions(session1, session2) do
    if session1.redis_shard != session2.redis_shard do
      {:error, :cross_shard_pairing_unsupported}
    else
      common_interests = get_common_interests(session1.preferences, session2.preferences)

      updated_session1 =
        touch_last_activity(%{
          session1
          | status: :matched,
            partner_id: session2.id,
            last_partner_id: session2.id,
            signaling_ready: false,
            webrtc_started: false
        })

      updated_session2 =
        touch_last_activity(%{
          session2
          | status: :matched,
            partner_id: session1.id,
            last_partner_id: session1.id,
            signaling_ready: false,
            webrtc_started: false
        })

      case OmeglePhoenix.RedisState.pair_sessions(
             updated_session1,
             updated_session2,
             ttl_seconds(),
             report_grace_seconds()
           ) do
        {:ok, _} -> {:ok, updated_session1, updated_session2, common_interests}
        {:error, reason} -> {:error, reason}
      end
    end
  end

  def reset_pair(session1, session2) do
    if session1.redis_shard != session2.redis_shard do
      {:error, :cross_shard_pairing_unsupported}
    else
      updated_session1 =
        touch_last_activity(%{
          session1
          | partner_id: nil,
            status: :waiting,
            signaling_ready: false,
            webrtc_started: false
        })

      updated_session2 =
        touch_last_activity(%{
          session2
          | partner_id: nil,
            status: :waiting,
            signaling_ready: false,
            webrtc_started: false
        })

      case OmeglePhoenix.RedisState.reset_pair(updated_session1, updated_session2, ttl_seconds()) do
        {:ok, _} -> {:ok, updated_session1, updated_session2}
        {:error, reason} -> {:error, reason}
      end
    end
  end

  def move_session_shard(session_id, new_shard) when is_binary(session_id) and is_integer(new_shard) do
    with {:ok, session} <- get_session(session_id) do
      current_mode = OmeglePhoenix.RedisKeys.mode(session.preferences)
      normalized_shard = rem(max(new_shard, 0), OmeglePhoenix.Config.get_match_shard_count())

      if session.redis_shard == normalized_shard do
        {:ok, session}
      else
        old_route = OmeglePhoenix.RedisKeys.route_for_session(session)

        updated_session =
          session
          |> Map.put(:redis_shard, normalized_shard)
          |> touch_last_activity()

        case persist_session(updated_session) do
          :ok ->
            _ = migrate_owner_record(session_id, old_route, current_mode, normalized_shard)
            {:ok, updated_session}

          {:error, reason} ->
            {:error, reason}
        end
      end
    end
  end

  def cleanup_orphaned_session(session_id) do
    case OmeglePhoenix.RedisState.cleanup_orphaned_session(session_id) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  @impl true
  def init(_opts) do
    {:ok, %{}}
  end

  @impl true
  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state) do
    :ok
  end

  defp normalize_updates(updates) when is_map(updates) do
    updates
    |> Enum.reduce(%{}, fn {key, value}, acc ->
      atom_key = normalize_update_key(key)

      if atom_key do
        Map.put(acc, atom_key, normalize_field(atom_key, value))
      else
        acc
      end
    end)
  end

  defp normalize_updates(_updates), do: %{}

  defp normalize_field(:status, value), do: normalize_status(value)
  defp normalize_field(:preferences, value), do: normalize_preferences(value)
  defp normalize_field(:redis_shard, value) when is_integer(value), do: value
  defp normalize_field(:signaling_ready, value), do: truthy?(value)
  defp normalize_field(:webrtc_started, value), do: truthy?(value)
  defp normalize_field(:ban_status, value), do: truthy?(value)
  defp normalize_field(_field, value), do: value

  defp normalize_update_key(key) when is_atom(key) and key in @session_fields, do: key

  defp normalize_update_key(key) when is_binary(key) do
    try do
      atom_key = String.to_existing_atom(key)
      if atom_key in @session_fields, do: atom_key, else: nil
    rescue
      ArgumentError -> nil
    end
  end

  defp normalize_update_key(_key), do: nil

  defp truthy?(true), do: true
  defp truthy?(1), do: true
  defp truthy?("true"), do: true
  defp truthy?("1"), do: true
  defp truthy?(_value), do: false

  defp touch_last_activity(session) do
    Map.put(session, :last_activity, System.system_time(:second))
  end

  defp disconnect_known_partner(nil, _origin_session_id), do: :ok

  defp disconnect_known_partner(partner_id, origin_session_id) do
    # Atomic: only resets the partner if they still point at origin_session_id.
    # Prevents disrupting a new match the partner may have formed during the gap.
    case OmeglePhoenix.RedisState.atomic_disconnect_partner(
           partner_id,
           origin_session_id,
           ttl_seconds()
         ) do
      {:ok, "ok"} ->
        OmeglePhoenix.Router.notify_disconnect(partner_id, "partner disconnected")
        :ok

      {:ok, _} ->
        # "not_found" or "partner_changed" — nothing to do
        :ok

      {:error, _} ->
        :ok
    end
  end

  defp persist_session(session) do
    case OmeglePhoenix.RedisState.persist_session(session, ttl_seconds()) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  defp migrate_owner_record(session_id, old_route, new_mode, new_shard) do
    old_owner_key = OmeglePhoenix.RedisKeys.session_owner_key(session_id, old_route)
    new_owner_key = OmeglePhoenix.RedisKeys.session_owner_key(session_id, %{mode: new_mode, shard: new_shard})

    case OmeglePhoenix.Redis.command(["GET", old_owner_key]) do
      {:ok, owner_value} when is_binary(owner_value) ->
        _ =
          OmeglePhoenix.Redis.command([
            "SETEX",
            new_owner_key,
            Integer.to_string(OmeglePhoenix.Config.get_router_owner_ttl_seconds()),
            owner_value
          ])

        :ok

      _ ->
        :ok
    end
  end

  defp report_grace_seconds do
    OmeglePhoenix.Config.get_report_grace_seconds()
  end

  defp decode_session(payload) do
    with {:ok, raw} <- Jason.decode(payload),
         {:ok, session} <- deserialize_session(raw) do
      {:ok, session}
    else
      _ -> {:error, :not_found}
    end
  end

  defp decode_queue_meta(payload) do
    with {:ok, raw} <- Jason.decode(payload),
         {:ok, meta} <- deserialize_queue_meta(raw) do
      {:ok, meta}
    else
      _ -> {:error, :not_found}
    end
  end

  defp deserialize_session(raw) when is_map(raw) do
    session =
      Enum.reduce(@session_fields, %{}, fn field, acc ->
        key = Atom.to_string(field)
        Map.put(acc, field, deserialize_field(field, Map.get(raw, key)))
      end)

    {:ok, session}
  end

  defp deserialize_session(_raw), do: {:error, :invalid}

  defp deserialize_queue_meta(raw) when is_map(raw) do
    meta = %{
      id: Map.get(raw, "id"),
      redis_shard: parse_redis_shard(Map.get(raw, "redis_shard")),
      status: normalize_status(Map.get(raw, "status")),
      partner_id: Map.get(raw, "partner_id"),
      last_partner_id: Map.get(raw, "last_partner_id"),
      mode: normalize_mode(Map.get(raw, "mode"), "text"),
      interest_buckets:
        raw
        |> Map.get("interest_buckets", [])
        |> normalize_interest_buckets()
    }

    {:ok, meta}
  end

  defp deserialize_queue_meta(_raw), do: {:error, :invalid}

  defp deserialize_field(:status, nil), do: :waiting
  defp deserialize_field(:redis_shard, value), do: parse_redis_shard(value)
  defp deserialize_field(:status, value), do: normalize_status(value)
  defp deserialize_field(:signaling_ready, value), do: truthy?(value)
  defp deserialize_field(:webrtc_started, value), do: truthy?(value)
  defp deserialize_field(:ban_status, value), do: truthy?(value)
  defp deserialize_field(:preferences, value), do: normalize_preferences(value)
  defp deserialize_field(_field, value), do: value

  defp normalize_interest_buckets(values) when is_list(values) do
    values
    |> Enum.filter(&is_binary/1)
    |> Enum.map(&String.slice(&1, 0, 32))
    |> Enum.reject(&(&1 == ""))
    |> Enum.uniq()
    |> Enum.take(3)
  end

  defp normalize_interest_buckets(_values), do: []

  defp normalize_preferences(preferences) when is_map(preferences) do
    %{
      "mode" =>
        Map.get(preferences, "mode", Map.get(preferences, :mode, "text"))
        |> safe_string("text")
        |> normalize_mode("text"),
      "interests" =>
        Map.get(preferences, "interests", Map.get(preferences, :interests, ""))
        |> safe_string("")
        |> String.slice(0, 255)
    }
  end

  defp normalize_preferences(_), do: %{"mode" => "text", "interests" => ""}

  defp parse_redis_shard(value) when is_integer(value) and value >= 0, do: value

  defp parse_redis_shard(value) when is_binary(value) do
    case Integer.parse(value) do
      {shard, ""} when shard >= 0 -> shard
      _ -> 0
    end
  end

  defp parse_redis_shard(_value), do: 0

  defp normalize_status(value) when is_atom(value) and value in @allowed_statuses, do: value

  defp normalize_status(value) when is_binary(value) do
    case value do
      "waiting" -> :waiting
      "matched" -> :matched
      "disconnecting" -> :disconnecting
      _ -> :waiting
    end
  end

  defp normalize_status(_value), do: :waiting

  defp normalize_mode(mode, _default) when mode in ["lobby", "text", "video"], do: mode
  defp normalize_mode(_mode, default), do: default

  defp safe_string(nil, default), do: default
  defp safe_string(value, _default) when is_binary(value), do: value
  defp safe_string(value, _default) when is_atom(value), do: Atom.to_string(value)
  defp safe_string(value, _default) when is_integer(value), do: Integer.to_string(value)

  defp safe_string(value, _default) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp safe_string(_value, default), do: default

  defp get_common_interests(p1, p2) do
    p1 = normalize_preferences(p1)
    p2 = normalize_preferences(p2)

    i1 = Map.get(p1, "interests", "") |> String.trim()
    i2 = Map.get(p2, "interests", "") |> String.trim()

    if i1 != "" and i2 != "" do
      set1 = parse_interests(i1)
      set2 = parse_interests(i2)
      MapSet.intersection(set1, set2) |> MapSet.to_list()
    else
      []
    end
  end

  defp parse_interests(str) do
    str
    |> safe_string("")
    |> String.slice(0, 500)
    |> String.downcase()
    |> String.split([",", ";"], trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.take(10)
    |> MapSet.new()
  end

  defp generate_session_token do
    :crypto.strong_rand_bytes(32)
    |> Base.url_encode64(padding: false)
  end

  defp ttl_seconds do
    OmeglePhoenix.Config.get_session_ttl()
  end

  defp load_session_routes(session_ids) do
    locator_keys = Enum.map(session_ids, &OmeglePhoenix.RedisKeys.session_locator_key/1)

    case OmeglePhoenix.Redis.mget(locator_keys) do
      {:ok, locators} when is_list(locators) ->
        routes =
          session_ids
          |> Enum.zip(locators)
          |> Enum.reduce(%{}, fn
            {_id, nil}, acc ->
              acc

            {id, locator}, acc ->
              case OmeglePhoenix.RedisKeys.decode_locator(id, locator) do
                {:ok, route} -> Map.put(acc, id, route)
                _ -> acc
              end
          end)

        {:ok, routes}

      other ->
        other
    end
  end

  defp prune_stale_session_ids(index_key, session_ids, sessions_by_id)
       when is_binary(index_key) and is_list(session_ids) and is_map(sessions_by_id) do
    stale_ids = Enum.reject(session_ids, &Map.has_key?(sessions_by_id, &1))

    case stale_ids do
      [] ->
        :ok

      _ ->
        case OmeglePhoenix.Redis.command(["SREM", index_key | stale_ids]) do
          {:ok, _} -> :ok
          {:error, reason} -> {:error, reason}
          _ -> :ok
        end
    end
  end

  defp session_key(session_id, route), do: OmeglePhoenix.RedisKeys.session_key(session_id, route)
  defp queue_meta_key(session_id, route), do: OmeglePhoenix.RedisKeys.queue_meta_key(session_id, route)
  defp ip_sessions_key(ip), do: "ip:#{ip}"
end
