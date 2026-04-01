defmodule OmeglePhoenix.Matchmaker do
  use GenServer
  require Logger

  @mode_queue_prefix "matchmaking_queue"
  @lock_key "matchmaking:leader"
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

    case OmeglePhoenix.Redis.command([
           "ZADD",
           queue_key(preferences),
           to_string(timestamp),
           session_id
         ]) do
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
    commands =
      Enum.map(queue_keys(), fn queue_key ->
        ["ZREM", queue_key, session_id]
      end)

    case OmeglePhoenix.Redis.pipeline(commands) do
      {:ok, _results} ->
        :ok

      {:error, reason} = error ->
        Logger.warning(
          "Failed to remove #{session_id} from matchmaking queues: #{inspect(reason)}"
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
    Enum.map(["lobby", "text", "video"], &queue_key/1)
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
    if match_leader?() do
      renewer = start_leader_renewer()

      try do
        Enum.each(queue_keys(), &do_matching/1)
      rescue
        e -> Logger.error("Matching error: #{inspect(e)}")
      after
        stop_renewer(renewer)
      end
    end

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
          OmeglePhoenix.Redis.command(["ZREM", queue_key, session_id])

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
        :ok

      {:ok, [_single]} ->
        :ok

      {:ok, session_ids_with_scores} when is_list(session_ids_with_scores) ->
        now_ms = System.system_time(:millisecond)
        entries = Enum.chunk_every(session_ids_with_scores, 2)
        session_ids = Enum.map(entries, fn [sid, _score_str] -> sid end)
        {:ok, sessions_by_id} = OmeglePhoenix.SessionManager.get_sessions(session_ids)

        sessions_with_prefs =
          Enum.reduce(entries, [], fn
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

        match_from_pool(queue_key, sessions_with_prefs, MapSet.new())

      _ ->
        :ok
    end
  end

  defp match_from_pool(_queue_key, [], _matched), do: :ok

  defp match_from_pool(queue_key, [{sid1, session1, wait1} | rest], matched) do
    if MapSet.member?(matched, sid1) do
      match_from_pool(queue_key, rest, matched)
    else
      case find_compatible_partner(sid1, session1, wait1, rest, matched) do
        {sid2, _session2, remaining} ->
          pair_users(queue_key, sid1, sid2)
          match_from_pool(queue_key, remaining, MapSet.put(MapSet.put(matched, sid1), sid2))

        nil ->
          match_from_pool(queue_key, rest, matched)
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

  defp pair_users(queue_key, session_id1, session_id2) do
    OmeglePhoenix.SessionLock.with_locks([session_id1, session_id2], fn ->
      OmeglePhoenix.Redis.command(["ZREM", queue_key, session_id1])
      OmeglePhoenix.Redis.command(["ZREM", queue_key, session_id2])

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
                  common_interests: length(common_interests)
                }
              )

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

  defp match_leader? do
    node_name = Atom.to_string(Node.self())

    case OmeglePhoenix.Redis.command([
           "SET",
           @lock_key,
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
               @lock_key,
               node_name,
               Integer.to_string(OmeglePhoenix.Config.get_match_leader_ttl_ms())
             ]) do
          {:ok, 1} -> true
          _ -> false
        end
    end
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

  defp queue_key(%{"mode" => mode}), do: queue_key(mode)
  defp queue_key(%{mode: mode}), do: queue_key(mode)

  defp queue_key(mode) when mode in ["lobby", "text", "video"] do
    "#{@mode_queue_prefix}:#{mode}"
  end

  defp queue_key(_mode), do: "#{@mode_queue_prefix}:text"

  defp start_leader_renewer do
    parent = self()
    node_name = Atom.to_string(Node.self())
    ttl_ms = OmeglePhoenix.Config.get_match_leader_ttl_ms()

    spawn(fn ->
      parent_ref = Process.monitor(parent)
      leader_renew_loop(node_name, ttl_ms, parent_ref)
    end)
  end

  defp leader_renew_loop(node_name, ttl_ms, parent_ref) do
    receive do
      :stop ->
        :ok

      {:DOWN, ^parent_ref, :process, _pid, _reason} ->
        :ok
    after
      max(div(ttl_ms, 2), 250) ->
        _ = renew_leader(node_name, ttl_ms)
        leader_renew_loop(node_name, ttl_ms, parent_ref)
    end
  end

  defp renew_leader(node_name, ttl_ms) do
    OmeglePhoenix.Redis.command([
      "EVAL",
      @renew_lock_script,
      "1",
      @lock_key,
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
