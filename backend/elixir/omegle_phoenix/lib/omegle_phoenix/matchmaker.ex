defmodule OmeglePhoenix.Matchmaker do
  use GenServer
  require Logger

  @mode_queue_prefix "matchmaking_queue"
  @queue_registry_key "matchmaking:queues"
  @session_queue_prefix "matchmaking:session_queues"
  @lock_key_prefix "matchmaking:leader"
  @stream_reconnect_message :connect_match_stream
  @stream_consume_message :consume_match_stream
  @sweep_message :sweep_match_queues
  @delayed_match_event_message :delayed_match_event
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
      end) ++
        [["EXPIRE", membership_key, Integer.to_string(OmeglePhoenix.Config.get_session_ttl())]]

    case OmeglePhoenix.Redis.pipeline(commands) do
      {:ok, _result} ->
        emit_match_event(queue_keys, "join", session_id)
        schedule_fallback_checks(queue_keys, normalized_preferences, session_id)

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
        Logger.warning("Failed to load queue membership for #{session_id}: #{inspect(reason)}")

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
    state = %{
      stream_conn: nil,
      stream: OmeglePhoenix.Config.get_match_event_stream(),
      group: OmeglePhoenix.Config.get_match_event_stream_group(),
      consumer: match_stream_consumer_name(),
      sweep_interval_ms: OmeglePhoenix.Config.get_match_sweep_interval_ms(),
      sweep_stale_after_ms: OmeglePhoenix.Config.get_match_sweep_stale_after_ms(),
      recent_queue_events: %{}
    }

    send(self(), @stream_reconnect_message)
    maybe_schedule_sweep(state.sweep_interval_ms)
    {:ok, state}
  end

  @impl true
  def handle_info(@stream_reconnect_message, state) do
    stop_stream_connection(state.stream_conn)

    case start_stream_connection() do
      {:ok, connection} ->
        case ensure_stream_group(connection, state.stream, state.group) do
          :ok ->
            claim_stale_pending(connection, state.stream, state.group, state.consumer)
            cleanup_stale_consumers(connection, state.stream, state.group, state.consumer)
            send(self(), @stream_consume_message)
            {:noreply, %{state | stream_conn: connection}}

          {:error, reason} ->
            Logger.error(
              "Failed to initialize matchmaking stream #{state.stream} / #{state.group}: #{inspect(reason)}"
            )

            stop_stream_connection(connection)
            Process.send_after(self(), @stream_reconnect_message, 1_000)
            {:noreply, %{state | stream_conn: nil}}
        end

      {:error, reason} ->
        Logger.error("Failed to connect matchmaking stream consumer: #{inspect(reason)}")
        Process.send_after(self(), @stream_reconnect_message, 1_000)
        {:noreply, %{state | stream_conn: nil}}
    end
  end

  def handle_info(@stream_consume_message, %{stream_conn: nil} = state) do
    {:noreply, state}
  end

  def handle_info(@stream_consume_message, state) do
    state =
      case consume_stream_entries(state) do
        {:ok, updated_state} ->
          updated_state

        {:error, reason} ->
          Logger.warning("Matchmaking stream consumer disconnected: #{inspect(reason)}")
          stop_stream_connection(state.stream_conn)
          Process.send_after(self(), @stream_reconnect_message, 1_000)
          %{state | stream_conn: nil}
      end

    if state.stream_conn != nil do
      send(self(), @stream_consume_message)
    end

    {:noreply, state}
  end

  def handle_info(@sweep_message, state) do
    now_ms = System.system_time(:millisecond)

    stale_queue_keys =
      queue_keys()
      |> Enum.filter(fn queue_key ->
        sweep_queue?(queue_key, state.recent_queue_events, now_ms, state.sweep_stale_after_ms)
      end)

    stale_queue_keys
    |> Task.async_stream(&do_matching/1,
      max_concurrency: System.schedulers_online(),
      timeout: 15_000,
      on_timeout: :kill_task,
      ordered: false
    )
    |> Stream.run()

    maybe_schedule_sweep(state.sweep_interval_ms)

    {:noreply,
     %{
       state
       | recent_queue_events:
           prune_recent_queue_events(
             state.recent_queue_events,
             now_ms,
             state.sweep_stale_after_ms
           )
     }}
  end

  def handle_info({@delayed_match_event_message, queue_keys, session_id, phase}, state) do
    emit_match_event(queue_keys, phase, session_id)
    {:noreply, state}
  end

  def handle_info(_info, state) do
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    stop_stream_connection(Map.get(state, :stream_conn))
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

      match_from_pool(queue_key, sessions_with_prefs, MapSet.new())
      attempt_overflow_matching(queue_key, sessions_with_prefs, now, batch_size)

      prune_queue_if_empty(queue_key)
    end)
  end

  defp match_from_pool(_queue_key, [], _matched), do: :ok

  defp match_from_pool(queue_key, [{sid1, session1, wait1} | rest], matched) do
    if MapSet.member?(matched, sid1) do
      match_from_pool(queue_key, rest, matched)
    else
      {frontier, remaining_tail} = take_frontier(rest, matched)

      case find_compatible_partner(queue_key, sid1, session1, wait1, frontier, matched) do
        {sid2, _session2, remaining_frontier} ->
          remaining = remaining_frontier ++ remaining_tail

          case pair_users(sid1, sid2, :local) do
            :ok ->
              match_from_pool(
                queue_key,
                remaining,
                MapSet.put(MapSet.put(matched, sid1), sid2)
              )

            _ ->
              # Pairing failed (locked/unavailable); skip sid2, retry sid1 with remaining
              match_from_pool(queue_key, remaining, MapSet.put(matched, sid2))
          end

        nil ->
          match_from_pool(queue_key, rest, matched)
      end
    end
  end

  defp find_compatible_partner(_queue_key, _sid1, _session1, _wait1, [], _matched), do: nil

  defp find_compatible_partner(
         queue_key,
         sid1,
         session1,
         wait1,
         [{sid2, session2, wait2} | rest],
         matched
       ) do
    if MapSet.member?(matched, sid2) do
      find_compatible_partner(queue_key, sid1, session1, wait1, rest, matched)
    else
      if session1.last_partner_id == sid2 or session2.last_partner_id == sid1 do
        find_compatible_partner(queue_key, sid1, session1, wait1, rest, matched)
      else
        if compatible?(queue_key, session1, wait1, session2, wait2) do
          {sid2, session2, rest}
        else
          find_compatible_partner(queue_key, sid1, session1, wait1, rest, matched)
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

  defp queue_ready_session?(session) do
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

  defp compatible?(queue_key, session1, wait1, session2, wait2) do
    if session1.mode != session2.mode do
      false
    else
      shared_interest? =
        not MapSet.disjoint?(
          MapSet.new(session1.interest_buckets),
          MapSet.new(session2.interest_buckets)
        )

      cond do
        strict_bucket_queue?(queue_key) ->
          shared_interest?

        relaxed_bucket_queue?(queue_key) ->
          shared_interest? or
            (can_relax_interest_match?(session1.interest_buckets, wait1) and
               can_relax_interest_match?(session2.interest_buckets, wait2))

        shared_random_queue?(queue_key) ->
          can_match_in_shared_random_queue?(session1.interest_buckets, wait1) and
            can_match_in_shared_random_queue?(session2.interest_buckets, wait2)

        random_queue?(queue_key) ->
          can_match_in_random_queue?(session1.interest_buckets, wait1) and
            can_match_in_random_queue?(session2.interest_buckets, wait2)

        true ->
          shared_interest?
      end
    end
  end

  defp strict_bucket_queue?(queue_key), do: String.contains?(queue_key, ":bucket:strict:")
  defp relaxed_bucket_queue?(queue_key), do: String.contains?(queue_key, ":bucket:relaxed:")
  defp shared_random_queue?(queue_key), do: String.ends_with?(queue_key, ":random:shared")

  defp random_queue?(queue_key) do
    String.contains?(queue_key, ":random:")
  end

  defp can_relax_interest_match?(interest_buckets, wait_time_ms) do
    cond do
      interest_buckets == [] -> true
      OmeglePhoenix.Config.get_match_relaxed_wait_ms() <= 0 -> true
      true -> wait_time_ms >= OmeglePhoenix.Config.get_match_relaxed_wait_ms()
    end
  end

  defp can_match_in_random_queue?(interest_buckets, wait_time_ms) do
    cond do
      interest_buckets == [] -> true
      OmeglePhoenix.Config.get_match_overflow_wait_ms() <= 0 -> true
      true -> wait_time_ms >= OmeglePhoenix.Config.get_match_overflow_wait_ms()
    end
  end

  defp can_match_in_shared_random_queue?(interest_buckets, wait_time_ms) do
    cond do
      interest_buckets == [] -> true
      OmeglePhoenix.Config.get_match_relaxed_wait_ms() <= 0 -> true
      true -> wait_time_ms >= OmeglePhoenix.Config.get_match_relaxed_wait_ms()
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
    interest_buckets = interest_buckets(preferences)

    strict_bucket_keys =
      Enum.flat_map(interest_buckets, fn bucket ->
        strict_bucket_queue_keys(mode, bucket, session_id)
      end)

    relaxed_bucket_keys =
      interest_buckets
      |> Enum.map(&relaxed_bucket_family/1)
      |> Enum.uniq()
      |> Enum.flat_map(fn family ->
        relaxed_bucket_queue_keys(mode, family, session_id)
      end)

    random_keys =
      if strict_bucket_keys == [] do
        [shared_random_queue_key(mode)]
      else
        [shared_random_queue_key(mode) | random_queue_keys(mode, session_id)]
      end

    (strict_bucket_keys ++ relaxed_bucket_keys ++ random_keys)
    |> Enum.uniq()
  end

  defp schedule_fallback_checks(queue_keys, preferences, session_id) do
    if interest_buckets(preferences) != [] do
      schedule_delayed_match_event(
        queue_keys,
        session_id,
        "relaxed_wait_elapsed",
        OmeglePhoenix.Config.get_match_relaxed_wait_ms()
      )

      schedule_delayed_match_event(
        queue_keys,
        session_id,
        "overflow_wait_elapsed",
        OmeglePhoenix.Config.get_match_overflow_wait_ms()
      )
    end

    :ok
  end

  defp schedule_delayed_match_event(_queue_keys, _session_id, _phase, delay_ms)
       when not is_integer(delay_ms) or delay_ms <= 0 do
    :ok
  end

  defp schedule_delayed_match_event(queue_keys, session_id, phase, delay_ms) do
    Process.send_after(
      __MODULE__,
      {@delayed_match_event_message, queue_keys, session_id, phase},
      delay_ms
    )

    :ok
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

  defp strict_bucket_queue_keys(mode, bucket, _session_id) do
    [strict_bucket_queue_key(mode, bucket)]
  end

  defp relaxed_bucket_queue_keys(mode, family, session_id) do
    _ = session_id
    [relaxed_bucket_queue_key(mode, family)]
  end

  defp random_queue_keys(mode, session_id) do
    queue_shards(mode, {:random, session_id})
    |> Enum.map(fn shard -> random_queue_key(mode, shard) end)
  end

  defp queue_shards(mode, identity) do
    shard_count = OmeglePhoenix.Config.get_match_shard_count()
    primary = :erlang.phash2({mode, identity}, shard_count)

    [primary, rem(primary + 1, shard_count)]
    |> Enum.uniq()
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
          wait_ms >= overflow_wait_ms and queue_ready_session?(session)
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
            wait_ms >= overflow_wait_ms and queue_ready_session?(session) and
              not Enum.any?(local_candidates, fn {local_sid, _, _} -> local_sid == sid end)
          end)

        match_across_pools(overflow_queue_key, local_candidates, remote_candidates, MapSet.new())
      end
    else
      _ -> :ok
    end
  end

  defp match_across_pools(_queue_key, [], _remote_candidates, _matched_remote), do: :ok

  defp match_across_pools(
         queue_key,
         [{sid1, session1, wait1} | rest],
         remote_candidates,
         matched_remote
       ) do
    {frontier, remaining_tail} = take_frontier(remote_candidates, matched_remote)

    case find_compatible_partner(
           queue_key,
           sid1,
           session1,
           wait1,
           frontier,
           matched_remote
         ) do
      {sid2, _session2, remaining_frontier} ->
        remaining = remaining_frontier ++ remaining_tail

        case pair_users(sid1, sid2, :overflow) do
          :ok ->
            match_across_pools(queue_key, rest, remaining, MapSet.put(matched_remote, sid2))

          _ ->
            # Pairing failed (locked/unavailable); mark sid2 and retry sid1
            match_across_pools(
              queue_key,
              [{sid1, session1, wait1} | rest],
              remaining,
              MapSet.put(matched_remote, sid2)
            )
        end

      nil ->
        match_across_pools(queue_key, rest, remote_candidates, matched_remote)
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

  defp build_session_pool(session_ids_with_scores, now_ms)
       when is_list(session_ids_with_scores) do
    entries = Enum.chunk_every(session_ids_with_scores, 2)
    session_ids = Enum.map(entries, fn [sid, _score_str] -> sid end)

    sessions_by_id =
      case OmeglePhoenix.SessionManager.get_queue_ready_sessions(session_ids) do
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
    |> Enum.filter(fn {_sid, session, _wait} -> queue_ready_session?(session) end)
  end

  defp strict_bucket_queue_key(mode, bucket) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}:bucket:strict:#{bucket}"
  end

  defp strict_bucket_queue_key(_mode, bucket) do
    "#{@mode_queue_prefix}:text:bucket:strict:#{bucket}"
  end

  defp relaxed_bucket_queue_key(mode, family) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}:bucket:relaxed:#{family}"
  end

  defp relaxed_bucket_queue_key(_mode, family) do
    "#{@mode_queue_prefix}:text:bucket:relaxed:#{family}"
  end

  defp relaxed_bucket_family(bucket) do
    case String.slice(bucket, 0, 2) do
      nil -> "misc"
      "" -> "misc"
      family -> family
    end
  end

  defp random_queue_key(mode, shard) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}:random:shard:#{shard}"
  end

  defp random_queue_key(_mode, shard), do: "#{@mode_queue_prefix}:text:random:shard:#{shard}"

  defp shared_random_queue_key(mode) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}:random:shared"
  end

  defp shared_random_queue_key(_mode), do: "#{@mode_queue_prefix}:text:random:shared"

  defp parse_random_queue_key(queue_key) do
    prefix = "#{@mode_queue_prefix}:"

    with true <- String.starts_with?(queue_key, prefix),
         [mode, "random", "shard", shard_str] <-
           String.split(String.replace_prefix(queue_key, prefix, ""), ":"),
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

  defp emit_match_event([], _event, _session_id), do: :ok

  defp emit_match_event(queue_keys, event, session_id) do
    payload = Jason.encode!(Enum.uniq(queue_keys))

    _ =
      OmeglePhoenix.Redis.command([
        "XADD",
        OmeglePhoenix.Config.get_match_event_stream(),
        "MAXLEN",
        "~",
        Integer.to_string(OmeglePhoenix.Config.get_match_event_stream_maxlen()),
        "*",
        "event",
        event,
        "session_id",
        session_id,
        "queue_keys",
        payload
      ])

    :ok
  end

  defp start_stream_connection do
    opts = [
      host: OmeglePhoenix.Config.get_redis_host(),
      port: OmeglePhoenix.Config.get_redis_port()
    ]

    opts =
      case OmeglePhoenix.Config.get_redis_password() do
        nil -> opts
        password -> Keyword.put(opts, :password, password)
      end

    Redix.start_link(opts)
  end

  defp stop_stream_connection(nil), do: :ok

  defp stop_stream_connection(connection) do
    try do
      Redix.stop(connection)
    rescue
      _ -> :ok
    end
  end

  defp ensure_stream_group(connection, stream, group) do
    case Redix.command(connection, ["XGROUP", "CREATE", stream, group, "$", "MKSTREAM"]) do
      {:ok, "OK"} ->
        :ok

      {:error, %Redix.Error{message: <<"BUSYGROUP", _::binary>>}} ->
        :ok

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp claim_stale_pending(connection, stream, group, consumer) do
    case Redix.command(connection, [
           "XAUTOCLAIM",
           stream,
           group,
           consumer,
           "30000",
           "0-0",
           "COUNT",
           "100"
         ]) do
      {:ok, _} -> :ok
      {:error, _} -> :ok
    end
  end

  defp cleanup_stale_consumers(connection, stream, group, current_consumer) do
    active_consumers = active_consumer_names(current_consumer)
    idle_cutoff_ms = OmeglePhoenix.Config.get_stream_stale_consumer_idle_ms()

    case Redix.command(connection, ["XINFO", "CONSUMERS", stream, group]) do
      {:ok, consumers} when is_list(consumers) ->
        Enum.each(consumers, fn consumer_info ->
          info = xinfo_to_map(consumer_info)
          name = Map.get(info, "name")
          idle_ms = xinfo_integer(info, "idle")

          if is_binary(name) and name != current_consumer and idle_ms >= idle_cutoff_ms and
               not MapSet.member?(active_consumers, name) do
            _ = Redix.command(connection, ["XGROUP", "DELCONSUMER", stream, group, name])
          end
        end)

      _ ->
        :ok
    end
  end

  defp consume_stream_entries(state) do
    with {:ok, pending_state} <- consume_pending_entries(state),
         {:ok, entries} <- read_stream(pending_state, ">") do
      process_stream_entries(pending_state, entries)
    end
  end

  defp consume_pending_entries(state) do
    case read_stream(state, "0") do
      {:ok, []} ->
        {:ok, state}

      {:ok, entries} ->
        process_stream_entries(state, entries)

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp process_stream_entries(state, []), do: {:ok, state}

  defp process_stream_entries(state, entries) do
    {processed_queue_keys, state} =
      Enum.reduce(entries, {[], state}, fn entry, {keys_acc, acc_state} ->
        queue_keys = handle_stream_entry(entry)
        {queue_keys ++ keys_acc, acc_state}
      end)

    with :ok <- ack_stream_entries(state, entries) do
      now_ms = System.system_time(:millisecond)

      {:ok,
       %{
         state
         | recent_queue_events:
             record_recent_queue_events(state.recent_queue_events, processed_queue_keys, now_ms)
       }}
    end
  end

  defp read_stream(state, stream_id) do
    command = [
      "XREADGROUP",
      "GROUP",
      state.group,
      state.consumer,
      "COUNT",
      Integer.to_string(OmeglePhoenix.Config.get_match_event_stream_batch_size()),
      "BLOCK",
      Integer.to_string(OmeglePhoenix.Config.get_match_event_stream_block_ms()),
      "STREAMS",
      state.stream,
      stream_id
    ]

    case Redix.command(
           state.stream_conn,
           command,
           timeout: OmeglePhoenix.Config.get_match_event_stream_block_ms() + 2_000
         ) do
      {:ok, nil} ->
        {:ok, []}

      {:ok, [[_stream, entries]]} when is_list(entries) ->
        {:ok, entries}

      {:ok, _other} ->
        {:ok, []}

      {:error, reason} ->
        {:error, reason}
    end
  end

  defp ack_stream_entries(_state, []), do: :ok

  defp ack_stream_entries(state, entries) do
    ids = Enum.map(entries, fn [entry_id, _fields] -> entry_id end)

    case Redix.command(state.stream_conn, ["XACK", state.stream, state.group | ids]) do
      {:ok, _} -> :ok
      {:error, reason} -> {:error, reason}
    end
  end

  defp handle_stream_entry([_entry_id, fields]) when is_list(fields) do
    data =
      fields
      |> Enum.chunk_every(2)
      |> Enum.reduce(%{}, fn
        [key, value], acc -> Map.put(acc, key, value)
        _pair, acc -> acc
      end)

    queue_keys =
      case Map.get(data, "queue_keys") do
        nil ->
          []

        raw ->
          case Jason.decode(raw) do
            {:ok, keys} when is_list(keys) -> Enum.filter(keys, &is_binary/1)
            _ -> []
          end
      end

    queue_keys =
      queue_keys
      |> Enum.uniq()

    Enum.each(queue_keys, fn queue_key ->
      try do
        do_matching(queue_key)
      rescue
        e -> Logger.error("Stream-triggered matching error for #{queue_key}: #{inspect(e)}")
      end
    end)

    queue_keys
  end

  defp take_frontier(candidates, matched) do
    frontier_size = OmeglePhoenix.Config.get_match_frontier_size()

    Enum.reduce(candidates, {[], [], 0}, fn candidate, {frontier, deferred, count} ->
      {sid, _session, _wait_ms} = candidate

      cond do
        MapSet.member?(matched, sid) ->
          {frontier, deferred, count}

        count < frontier_size ->
          {[candidate | frontier], deferred, count + 1}

        true ->
          {frontier, [candidate | deferred], count}
      end
    end)
    |> then(fn {frontier, deferred, _count} ->
      {Enum.reverse(frontier), Enum.reverse(deferred)}
    end)
  end

  defp maybe_schedule_sweep(interval_ms) when is_integer(interval_ms) and interval_ms > 0 do
    Process.send_after(self(), @sweep_message, interval_ms)
  end

  defp maybe_schedule_sweep(_interval_ms), do: :ok

  defp sweep_queue?(queue_key, recent_queue_events, now_ms, stale_after_ms) do
    case Map.get(recent_queue_events, queue_key) do
      nil -> true
      last_seen_ms -> now_ms - last_seen_ms >= stale_after_ms
    end
  end

  defp record_recent_queue_events(recent_queue_events, queue_keys, now_ms) do
    Enum.reduce(queue_keys, recent_queue_events, fn queue_key, acc ->
      Map.put(acc, queue_key, now_ms)
    end)
  end

  defp prune_recent_queue_events(recent_queue_events, now_ms, stale_after_ms) do
    cutoff_ms = now_ms - stale_after_ms * 2

    Enum.reduce(recent_queue_events, %{}, fn {queue_key, seen_at}, acc ->
      if seen_at >= cutoff_ms do
        Map.put(acc, queue_key, seen_at)
      else
        acc
      end
    end)
  end

  defp match_stream_consumer_name do
    Node.self() |> Atom.to_string() |> String.replace(~r/[^a-zA-Z0-9:_-]/u, "_")
  end

  defp active_consumer_names(current_consumer) do
    [Node.self() | Node.list()]
    |> Enum.map(&match_stream_consumer_name/1)
    |> Enum.concat([current_consumer])
    |> MapSet.new()
  end

  defp match_stream_consumer_name(node) when is_atom(node) do
    node |> Atom.to_string() |> String.replace(~r/[^a-zA-Z0-9:_-]/u, "_")
  end

  defp xinfo_to_map(list) when is_list(list) do
    cond do
      Enum.all?(list, &match?([_, _], &1)) ->
        Map.new(list)

      rem(length(list), 2) == 0 ->
        list
        |> Enum.chunk_every(2)
        |> Enum.reduce(%{}, fn
          [key, value], acc when is_binary(key) -> Map.put(acc, key, value)
          _, acc -> acc
        end)

      true ->
        %{}
    end
  end

  defp xinfo_to_map(_), do: %{}

  defp xinfo_integer(info, key) do
    case Map.get(info, key) do
      value when is_integer(value) ->
        value

      value when is_binary(value) ->
        case Integer.parse(value) do
          {parsed, _} -> parsed
          :error -> 0
        end

      _ ->
        0
    end
  end
end
