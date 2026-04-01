defmodule OmeglePhoenix.Matchmaker do
  use GenServer
  require Logger

  defstruct match_timer: nil

  @queue_key "matchmaking_queue"
  @lock_key "matchmaking:leader"
  @lock_ttl_ms 500
  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  def join_queue(session_id, preferences) do
    GenServer.call(__MODULE__, {:join_queue, session_id, preferences})
  end

  def leave_queue(session_id) do
    GenServer.call(__MODULE__, {:leave_queue, session_id})
  end

  def check_match(session_id) do
    GenServer.call(__MODULE__, {:check_match, session_id})
  end

  @impl true
  def init(_opts) do
    send(self(), :check_matches)
    {:ok, %__MODULE__{}}
  end

  @impl true
  def handle_call({:join_queue, session_id, _preferences}, _from, state) do
    timestamp = System.system_time(:millisecond)

    OmeglePhoenix.Redis.command([
      "ZADD",
      @queue_key,
      to_string(timestamp),
      session_id
    ])

    :telemetry.execute([:omegle_phoenix, :matchmaking, :queued], %{count: 1}, %{session_id: session_id})

    {:reply, :ok, state}
  end

  def handle_call({:leave_queue, session_id}, _from, state) do
    OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id])

    {:reply, :ok, state}
  end

  def handle_call({:check_match, session_id}, _from, state) do
    case OmeglePhoenix.SessionManager.get_session(session_id) do
      {:ok, session} when session.status == :matched ->
        case OmeglePhoenix.SessionManager.get_session(session.partner_id) do
          {:ok, partner_session} ->
            {:reply, {:matched, partner_session}, state}

          {:error, :not_found} ->
            {:reply, {:waiting, :none}, state}
        end

      _ ->
        {:reply, {:waiting, :none}, state}
    end
  end

  def handle_call(_request, _from, state) do
    {:reply, {:error, :unknown_request}, state}
  end

  @impl true
  def handle_cast(_msg, state) do
    {:noreply, state}
  end

  @impl true
  def handle_info(:check_matches, state) do
    if match_leader?() do
      try do
        do_matching()
      rescue
        e -> Logger.error("Matching error: #{inspect(e)}")
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

  defp do_matching do
    now = System.system_time(:millisecond)
    expiration_time = now - OmeglePhoenix.Config.get_match_timeout()

    case OmeglePhoenix.Redis.command([
           "ZRANGEBYSCORE",
           @queue_key,
           "0",
           to_string(expiration_time)
         ]) do
      {:ok, expired_sessions} ->
        Enum.each(expired_sessions, fn session_id ->
          OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id])

          case OmeglePhoenix.SessionManager.get_session(session_id) do
            {:ok, session} when session.status == :waiting ->
              OmeglePhoenix.SessionManager.update_session(session_id, %{status: :disconnecting})
              OmeglePhoenix.Router.notify_timeout(session_id)
              :telemetry.execute([:omegle_phoenix, :matchmaking, :timeout], %{count: 1}, %{session_id: session_id})

            _ ->
              :ok
          end
        end)

      _ ->
        :ok
    end

    case OmeglePhoenix.Redis.command([
           "ZRANGEBYSCORE",
           @queue_key,
           "0",
           "+inf",
           "WITHSCORES",
           "LIMIT",
           "0",
           "100"
         ]) do
      {:ok, []} ->
        :ok

      {:ok, [_single]} ->
        :ok

      {:ok, session_ids_with_scores} when is_list(session_ids_with_scores) ->
        now_ms = System.system_time(:millisecond)

        sessions_with_prefs =
          session_ids_with_scores
          |> Enum.chunk_every(2)
          |> Enum.map(fn
            [sid, score_str] ->
              case OmeglePhoenix.SessionManager.get_session(sid) do
                {:ok, session} ->
                  join_time =
                    case Float.parse(score_str) do
                      {f, _} -> trunc(f)
                      :error -> now_ms
                    end

                  wait_time_ms = now_ms - join_time
                  {sid, session, wait_time_ms}

                _ ->
                  nil
              end

            _ ->
              nil
          end)
          |> Enum.reject(&is_nil/1)

        match_from_pool(sessions_with_prefs, MapSet.new())

      _ ->
        :ok
    end
  end

  defp match_from_pool([], _matched), do: :ok

  defp match_from_pool([{sid1, session1, wait1} | rest], matched) do
    if MapSet.member?(matched, sid1) do
      match_from_pool(rest, matched)
    else
      case find_compatible_partner(sid1, session1, wait1, rest, matched) do
        {sid2, _session2, remaining} ->
          pair_users(sid1, sid2)
          match_from_pool(remaining, MapSet.put(MapSet.put(matched, sid1), sid2))

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

  defp pair_users(session_id1, session_id2) do
    OmeglePhoenix.SessionLock.with_locks([session_id1, session_id2], fn ->
      OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id1])
      OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id2])

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
                %{session_id: session_id1, partner_id: session_id2, common_interests: length(common_interests)}
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
           Integer.to_string(@lock_ttl_ms),
           "NX"
         ]) do
      {:ok, "OK"} ->
        true

      _ ->
        case OmeglePhoenix.Redis.command(["GET", @lock_key]) do
          {:ok, ^node_name} ->
            OmeglePhoenix.Redis.command(["PEXPIRE", @lock_key, Integer.to_string(@lock_ttl_ms)])
            true

          _ ->
            false
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
          join_queue(session_id, session.preferences)
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
      "mode" => safe_string(Map.get(preferences, "mode", "text"), "text"),
      "interests" => safe_string(Map.get(preferences, "interests", ""), "")
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
end
