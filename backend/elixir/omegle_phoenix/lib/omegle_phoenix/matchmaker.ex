defmodule OmeglePhoenix.Matchmaker do
  use GenServer
  require Logger

  @mode_queue_prefix "matchmaking_queue"
  @queue_registry_key "matchmaking:queues"
  @session_queue_prefix "matchmaking:session_queues"
  @lock_key_prefix "matchmaking:leader"
  @prune_queue_script """
  if redis.call('ZCARD', KEYS[1]) == 0 then
    redis.call('SREM', KEYS[2], KEYS[1])
    return 1
  end
  return 0
  """
  @renew_lock_script """
  if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('PEXPIRE', KEYS[1], ARGV[2])
  end
  return 0
  """

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def join_queue(session_id, preferences) do
    timestamp = System.system_time(:millisecond)
    normalized_preferences = normalize_preferences(preferences)
    queue_keys = queue_keys_for_session(session_id, normalized_preferences)
    membership_key = session_queue_key(session_id)

    commands =
      Enum.flat_map(queue_keys, fn queue_key ->
        [
          ["ZADD", queue_key, to_string(timestamp), session_id],
          ["SADD", @queue_registry_key, queue_key],
          ["SADD", membership_key, queue_key]
        ]
      end) ++ [["EXPIRE", membership_key, Integer.to_string(OmeglePhoenix.Config.get_session_ttl())]]

    case OmeglePhoenix.Redis.pipeline(commands) do
      {:ok, _result} ->
        :telemetry.execute([:omegle_phoenix, :matchmaking, :queued], %{count: 1}, %{
          session_id: session_id
        })

        :ok

      {:error, reason} = error ->
        Logger.warning("Failed to queue #{session_id}: #{inspect(reason)}")
        error
    end
  end

  def leave_queue(session_id) do
    membership_key = session_queue_key(session_id)

    case OmeglePhoenix.Redis.command(["SMEMBERS", membership_key]) do
      {:ok, []} ->
        :ok

      {:ok, queue_keys} when is_list(queue_keys) ->
        commands =
          Enum.map(queue_keys, fn queue_key ->
            ["ZREM", queue_key, session_id]
          end) ++
            Enum.map(queue_keys, fn queue_key ->
              ["SREM", membership_key, queue_key]
            end)

        case OmeglePhoenix.Redis.pipeline(commands) do
          {:ok, _results} ->
            Enum.each(queue_keys, &prune_queue_if_empty/1)
            :ok

          {:error, reason} = error ->
            Logger.warning(
              "Failed to remove #{session_id} from matchmaking queues: #{inspect(reason)}"
            )

            error
        end

      {:error, reason} = error ->
        Logger.warning(
          "Failed to load queue membership for #{session_id}: #{inspect(reason)}"
        )

        error
    end
  end

  def check_match(session_id) do
    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} when session.status == :matched ->
        case OmeglePhoenix.SessionManager.get_session(session.partner_id) do
          {:ok, partner_session} ->
            {:matched, partner_session}

          {:error, :not_found} ->
            {:waiting, :none}
        end

      _ ->
        {:waiting, :none}
    end
  end

  def queue_keys do
    case OmeglePhoenix.Redis.command(["SMEMBERS", @queue_registry_key]) do
      {:ok, queue_keys} when is_list(queue_keys) ->
        queue_keys
        |> Enum.filter(&is_binary/1)
        |> Enum.sort()

      _ ->
        []
    end
  end

  def queue_depths do
    Map.new(queue_keys(), fn key ->
      count =
        case OmeglePhoenix.Redis.command(["ZCARD", key]) do
          {:ok, value} when is_integer(value) -> value
          _ -> 0
        end

      {key, count}
    end)
  end

  @impl true
  def init(_opts) do
    send(self(), :check_matches)
    {:ok, %{}}
  end

  @impl true
  def handle_info(:check_matches, state) do
    queue_keys()
    |> Task.async_stream(&do_matching/1,
      max_concurrency: System.schedulers_online(),
      timeout: 15_000,
      on_timeout: :kill_task,
      ordered: false
    )
    |> Stream.run()

    Process.send_after(self(), :check_matches, 100)
    {:noreply, state}
  end

  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, _state) do
    :ok
  end

  defp do_matching(queue_key) do
    with_queue_leader(queue_key, fn ->
      now = System.system_time(:millisecond)
      expiration_time = now - OmeglePhoenix.Config.get_match_timeout()
      batch_size = OmeglePhoenix.Config.get_match_batch_size()

      case OmeglePhoenix.Redis.command([
             "ZRANGEBYSCORE",
             queue_key,
             "0",
             to_string(expiration_time),
             "LIMIT",
             "0",
             Integer.to_string(batch_size)
           ]) do
        {:ok, expired_sessions} ->
          Enum.each(expired_sessions, fn session_id ->
            leave_queue(session_id)

            case OmeglePhoenix.SessionManager.get_session(session_id) do
              {:ok, session} when session.status == :waiting ->
                OmeglePhoenix.SessionManager.update_session(session_id, %{status: :disconnecting})
                OmeglePhoenix.Router.notify_timeout(session_id)

                :telemetry.execute([:omegle_phoenix, :matchmaking, :timeout], %{count: 1}, %{
                  session_id: session_id
                })

              _ ->
                :ok
            end
          end)

        _ ->
          :ok
      end

      sessions_with_prefs =
        case OmeglePhoenix.Redis.command([
               "ZRANGEBYSCORE",
               queue_key,
               "0",
               "+inf",
               "WITHSCORES",
               "LIMIT",
               "0",
               Integer.to_string(batch_size)
             ]) do
          {:ok, []} ->
            []

          {:ok, [_single]} ->
            []

          {:ok, session_ids_with_scores} when is_list(session_ids_with_scores) ->
            build_session_pool(session_ids_with_scores, now)

          _ ->
            []
        end

      match_from_pool(sessions_with_prefs, MapSet.new())
      attempt_overflow_matching(queue_key, sessions_with_prefs, now, batch_size)

      prune_queue_if_empty(queue_key)
    end)
  end

  defp match_from_pool([], _matched), do: :ok

  defp match_from_pool([{sid1, session1, wait1} | rest], matched) do
    if MapSet.member?(matched, sid1) do
      match_from_pool(rest, matched)
    else
      case find_compatible_partner(sid1, session1, wait1, rest, matched) do
        {sid2, _session2, remaining} ->
          case pair_users(sid1, sid2, :local) do
            :ok ->
              match_from_pool(remaining, MapSet.put(MapSet.put(matched, sid1), sid2))

            _ ->
              # Pairing failed (locked/unavailable); skip sid2, retry sid1 with remaining
              match_from_pool(remaining, MapSet.put(matched, sid2))
          end

        nil ->
          match_from_pool(rest, matched)
      end
    end
  end

  defp find_compatible_partner(_sid1, _session1, _wait1, [], _matched), do: nil

  defp find_compatible_partner(sid1, session1, wait1, [{sid2, session2, wait2} | rest], matched) do
    if MapSet.member?(matched, sid2) do
      find_compatible_partner(sid1, session1, wait1, rest, matched)
    else
      if session1.last_partner_id == sid2 or session2.last_partner_id == sid1 do
        find_compatible_partner(sid1, session1, wait1, rest, matched)
      else
        if compatible?(session1.preferences, wait1, session2.preferences, wait2) do
          {sid2, session2, rest}
        else
          find_compatible_partner(sid1, session1, wait1, rest, matched)
        end
      end
    end
  end

  defp pair_users(session_id1, session_id2, strategy) do
    OmeglePhoenix.SessionLock.with_locks([session_id1, session_id2], fn ->
      leave_queue(session_id1)
      leave_queue(session_id2)

      with {:ok, session1} <- OmeglePhoenix.SessionManager.get_session(session_id1),
           {:ok, session2} <- OmeglePhoenix.SessionManager.get_session(session_id2),
           true <- pairable_session?(session1),
           true <- pairable_session?(session2) do
        if session1.ban_status or session2.ban_status do
          {:error, :user_banned}
        else
          case OmeglePhoenix.SessionManager.pair_sessions(session1, session2) do
            {:ok, _updated_session1, _updated_session2, common_interests} ->
              OmeglePhoenix.Router.notify_match(session_id1, session_id2, common_interests)
              OmeglePhoenix.Router.notify_match(session_id2, session_id1, common_interests)

              :telemetry.execute(
                [:omegle_phoenix, :matchmaking, :matched],
                %{count: 1},
                %{
                  session_id: session_id1,
                  partner_id: session_id2,
                  common_interests: length(common_interests),
                  strategy: strategy
                }
              )

              event =
                case strategy do
                  :overflow -> [:omegle_phoenix, :matchmaking, :matched_overflow]
                  _ -> [:omegle_phoenix, :matchmaking, :matched_local]
                end

              :telemetry.execute(event, %{count: 1}, %{
                session_id: session_id1,
                partner_id: session_id2
              })

              :ok

            {:error, reason} ->
              {:error, reason}
          end
        end
      else
        {:error, :not_found} ->
          Logger.warning(
            "Matchmaker: session disappeared during pairing (#{session_id1} or #{session_id2})"
          )

          :ok

        false ->
          requeue_if_waiting(session_id1)
          requeue_if_waiting(session_id2)
          :ok

        _ ->
          :ok
      end
    end)
  end

  defp pairable_session?(session) do
    session.status == :waiting and is_nil(session.partner_id)
  end

  defp requeue_if_waiting(session_id) do
    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} ->
        if pairable_session?(session) do
          _ = join_queue(session_id, session.preferences)
        else
          :ok
        end

      _ ->
        :ok
    end
  end

  defp compatible?(preferences1, wait1, preferences2, wait2) do
    preferences1 = normalize_preferences(preferences1)
    preferences2 = normalize_preferences(preferences2)

    mode1 = Map.get(preferences1, "mode", "text")
    mode2 = Map.get(preferences2, "mode", "text")

    if mode1 != mode2 do
      false
    else
      interests1 = Map.get(preferences1, "interests", "") |> String.trim()
      interests2 = Map.get(preferences2, "interests", "") |> String.trim()

      cond do
        interests1 == "" and interests2 == "" ->
          true

        interests1 != "" and interests2 != "" ->
          tags1 = parse_interests(interests1)
          tags2 = parse_interests(interests2)

          if not MapSet.disjoint?(tags1, tags2) do
            true
          else
            can_fallback_to_random?(interests1, wait1) and
              can_fallback_to_random?(interests2, wait2)
          end

        true ->
          can_fallback_to_random?(interests1, wait1) and
            can_fallback_to_random?(interests2, wait2)
      end
    end
  end

  defp can_fallback_to_random?(interests, wait_time_ms) do
    if interests == "" do
      true
    else
      wait_time_ms >= 10_000
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

  defp normalize_preferences(preferences) when is_map(preferences) do
    %{
      "mode" =>
        Map.get(preferences, "mode", "text")
        |> safe_string("text")
        |> normalize_mode("text"),
      "interests" =>
        Map.get(preferences, "interests", "")
        |> safe_string("")
        |> String.slice(0, 255)
    }
  end

  defp normalize_preferences(_), do: %{"mode" => "text", "interests" => ""}

  defp safe_string(nil, default), do: default
  defp safe_string(value, _default) when is_binary(value), do: value
  defp safe_string(value, _default) when is_atom(value), do: Atom.to_string(value)
  defp safe_string(value, _default) when is_integer(value), do: Integer.to_string(value)

  defp safe_string(value, _default) when is_float(value),
    do: :erlang.float_to_binary(value, [:compact])

  defp safe_string(_value, default), do: default

  defp normalize_mode(mode, _default) when mode in ["lobby", "text", "video"], do: mode
  defp normalize_mode(_mode, default), do: default

  defp queue_keys_for_session(session_id, preferences) do
    mode = Map.get(preferences, "mode", "text")

    bucket_keys =
      preferences
      |> interest_buckets()
      |> Enum.map(fn bucket -> bucket_queue_key(mode, bucket) end)

    random_keys = random_queue_keys(mode, session_id)

    (bucket_keys ++ random_keys)
    |> Enum.uniq()
  end

  defp interest_buckets(preferences) do
    preferences
    |> Map.get("interests", "")
    |> parse_interests()
    |> Enum.take(3)
    |> case do
      [] -> []
      tags -> Enum.map(tags, &bucket_name/1)
    end
  end

  defp bucket_name(tag) do
    normalized =
      tag
      |> String.downcase()
      |> String.replace(~r/[^a-z0-9]+/u, "-")
      |> String.trim("-")
      |> String.slice(0, 32)

    if normalized == "", do: "misc", else: normalized
  end

  defp random_queue_keys(mode, session_id) do
    shard_count = OmeglePhoenix.Config.get_match_shard_count()
    primary = shard_for_session(session_id, shard_count)

    [primary, rem(primary + 1, shard_count)]
    |> Enum.uniq()
    |> Enum.map(fn shard -> random_queue_key(mode, shard) end)
  end

  defp shard_for_session(session_id, shard_count) do
    :erlang.phash2(session_id, shard_count)
  end

  defp attempt_overflow_matching(queue_key, sessions_with_prefs, now_ms, batch_size) do
    overflow_wait_ms =
      adaptive_overflow_wait_ms(
        OmeglePhoenix.Config.get_match_overflow_wait_ms(),
        length(sessions_with_prefs),
        batch_size
      )

    with true <- overflow_wait_ms > 0,
         {:ok, mode, shard} <- parse_random_queue_key(queue_key) do
      local_candidates =
        Enum.filter(sessions_with_prefs, fn {_sid, session, wait_ms} ->
          wait_ms >= overflow_wait_ms and pairable_session?(session)
        end)

      if local_candidates == [] do
        :ok
      else
        :telemetry.execute([:omegle_phoenix, :matchmaking, :overflow_attempt], %{count: 1}, %{
          queue_key: queue_key,
          candidates: length(local_candidates),
          overflow_wait_ms: overflow_wait_ms
        })

        overflow_queue_key = random_queue_key(mode, overflow_shard(mode, shard))

        remote_candidates =
          overflow_queue_key
          |> fetch_queue_entries(batch_size)
          |> build_session_pool(now_ms)
          |> Enum.filter(fn {sid, session, wait_ms} ->
            wait_ms >= overflow_wait_ms and pairable_session?(session) and
              not Enum.any?(local_candidates, fn {local_sid, _, _} -> local_sid == sid end)
          end)

        match_across_pools(local_candidates, remote_candidates, MapSet.new())
      end
    else
      _ -> :ok
    end
  end

  defp match_across_pools([], _remote_candidates, _matched_remote), do: :ok

  defp match_across_pools([{sid1, session1, wait1} | rest], remote_candidates, matched_remote) do
    case find_compatible_partner(sid1, session1, wait1, remote_candidates, matched_remote) do
      {sid2, _session2, _remaining} ->
        pair_users(sid1, sid2, :overflow)
        match_across_pools(rest, remote_candidates, MapSet.put(matched_remote, sid2))

      nil ->
        match_across_pools(rest, remote_candidates, matched_remote)
    end
  end

  defp fetch_queue_entries(queue_key, batch_size) do
    case OmeglePhoenix.Redis.command([
           "ZRANGEBYSCORE",
           queue_key,
           "0",
           "+inf",
           "WITHSCORES",
           "LIMIT",
           "0",
           Integer.to_string(batch_size)
         ]) do
      {:ok, entries} when is_list(entries) -> entries
      _ -> []
    end
  end

  defp build_session_pool(session_ids_with_scores, now_ms) when is_list(session_ids_with_scores) do
    entries = Enum.chunk_every(session_ids_with_scores, 2)
    session_ids = Enum.map(entries, fn [sid, _score_str] -> sid end)
    sessions_by_id =
      case OmeglePhoenix.SessionManager.get_sessions(session_ids) do
        {:ok, map} -> map
        _ -> %{}
      end

    entries
    |> Enum.reduce([], fn
      [sid, score_str], acc ->
        case Map.get(sessions_by_id, sid) do
          nil ->
            acc

          session ->
            join_time =
              case Float.parse(score_str) do
                {f, _} -> trunc(f)
                :error -> now_ms
              end

            [{sid, session, now_ms - join_time} | acc]
        end

      _entry, acc ->
        acc
    end)
    |> Enum.reverse()
    |> Enum.filter(fn {_sid, session, _wait} -> pairable_session?(session) end)
  end

  defp bucket_queue_key(mode, bucket) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}:bucket:#{bucket}"
  end

  defp bucket_queue_key(_mode, bucket), do: "#{@mode_queue_prefix}:text:bucket:#{bucket}"

  defp random_queue_key(mode, shard) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}:random:shard:#{shard}"
  end

  defp random_queue_key(_mode, shard), do: "#{@mode_queue_prefix}:text:random:shard:#{shard}"

  defp parse_random_queue_key(queue_key) do
    prefix = "#{@mode_queue_prefix}:"

    with true <- String.starts_with?(queue_key, prefix),
         [mode, "random", "shard", shard_str] <- String.split(String.replace_prefix(queue_key, prefix, ""), ":"),
         {shard, ""} <- Integer.parse(shard_str) do
      {:ok, mode, shard}
    else
      _ -> :error
    end
  end

  defp overflow_shard(mode, shard) do
    shard_count = OmeglePhoenix.Config.get_match_shard_count()
    step = overflow_step(mode, shard_count)
    rem(shard + step, shard_count)
  end

  defp overflow_step(mode, shard_count) do
    if shard_count <= 2 do
      1
    else
      base = :erlang.phash2({mode, "overflow"}, shard_count - 1) + 1
      if base == 1, do: 2, else: base
    end
  end

  defp adaptive_overflow_wait_ms(base_wait_ms, queue_depth, batch_size) do
    cond do
      base_wait_ms <= 0 ->
        0

      queue_depth <= 2 ->
        max(div(base_wait_ms, 2), 1_000)

      queue_depth >= max(div(batch_size, 2), 8) ->
        trunc(base_wait_ms * 1.5)

      true ->
        base_wait_ms
    end
  end

  defp session_queue_key(session_id), do: "#{@session_queue_prefix}:#{session_id}"

  defp prune_queue_if_empty(queue_key) do
    _ =
      OmeglePhoenix.Redis.command([
        "EVAL",
        @prune_queue_script,
        "2",
        queue_key,
        @queue_registry_key
      ])

    :ok
  end

  defp with_queue_leader(queue_key, fun) do
    lock_key = leader_lock_key(queue_key)

    if match_leader?(lock_key) do
      renewer = start_leader_renewer(lock_key)

      try do
        fun.()
      rescue
        e -> Logger.error("Matching error for #{queue_key}: #{inspect(e)}")
      after
        stop_renewer(renewer)
      end
    else
      :ok
    end
  end

  defp match_leader?(lock_key) do
    node_name = Atom.to_string(Node.self())

    case OmeglePhoenix.Redis.command([
           "SET",
           lock_key,
           node_name,
           "PX",
           Integer.to_string(OmeglePhoenix.Config.get_match_leader_ttl_ms()),
           "NX"
         ]) do
      {:ok, "OK"} ->
        true

      _ ->
        case OmeglePhoenix.Redis.command([
               "EVAL",
               @renew_lock_script,
               "1",
               lock_key,
               node_name,
               Integer.to_string(OmeglePhoenix.Config.get_match_leader_ttl_ms())
             ]) do
          {:ok, 1} -> true
          _ -> false
        end
    end
  end

  defp leader_lock_key(queue_key), do: "#{@lock_key_prefix}:#{queue_key}"

  defp start_leader_renewer(lock_key) do
    parent = self()
    node_name = Atom.to_string(Node.self())
    ttl_ms = OmeglePhoenix.Config.get_match_leader_ttl_ms()

    spawn(fn ->
      parent_ref = Process.monitor(parent)
      leader_renew_loop(lock_key, node_name, ttl_ms, parent_ref)
    end)
  end

  defp leader_renew_loop(lock_key, node_name, ttl_ms, parent_ref) do
    receive do
      :stop ->
        :ok

      {:DOWN, ^parent_ref, :process, _pid, _reason} ->
        :ok
    after
      max(div(ttl_ms, 2), 250) ->
        _ = renew_leader(lock_key, node_name, ttl_ms)
        leader_renew_loop(lock_key, node_name, ttl_ms, parent_ref)
    end
  end

  defp renew_leader(lock_key, node_name, ttl_ms) do
    OmeglePhoenix.Redis.command([
      "EVAL",
      @renew_lock_script,
      "1",
      lock_key,
      node_name,
      Integer.to_string(ttl_ms)
    ])
  end

  defp stop_renewer(nil), do: :ok

  defp stop_renewer(pid) when is_pid(pid) do
    send(pid, :stop)
    :ok
  end
end
