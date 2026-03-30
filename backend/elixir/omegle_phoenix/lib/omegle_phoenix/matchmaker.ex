defmodule OmeglePhoenix.Matchmaker do
  use GenServer
  require Logger

  defstruct match_timer: nil

  @queue_key "matchmaking_queue"

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
    try do
      do_matching()
    rescue
      e -> Logger.error("Matching error: #{inspect(e)}")
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

              case OmeglePhoenix.Router.find_process(session_id) do
                {:ok, pid} ->
                  send(pid, :timeout)

                _ ->
                  :ok
              end

            _ ->
              :ok
          end
        end)

      _ ->
        :ok
    end

    # Fetch all waiting sessions sorted by join time (oldest first).
    # We walk the list to find compatible pairs instead of blindly popping 2,
    # which avoids the busy-loop where incompatible users keep getting re-added.
    case OmeglePhoenix.Redis.command(["ZRANGEBYSCORE", @queue_key, "0", "+inf", "LIMIT", "0", "100"]) do
      {:ok, []} ->
        :ok

      {:ok, [_single]} ->
        :ok

      {:ok, session_ids} when is_list(session_ids) ->
        # Load preferences for all queued sessions
        sessions_with_prefs =
          session_ids
          |> Enum.map(fn sid ->
            case OmeglePhoenix.SessionManager.get_session(sid) do
              {:ok, session} -> {sid, session}
              _ -> nil
            end
          end)
          |> Enum.reject(&is_nil/1)

        match_from_pool(sessions_with_prefs, MapSet.new())

      _ ->
        :ok
    end
  end

  defp match_from_pool([], _matched), do: :ok

  defp match_from_pool([{sid1, session1} | rest], matched) do
    if MapSet.member?(matched, sid1) do
      match_from_pool(rest, matched)
    else
      # Find the first compatible partner in the remaining pool
      case find_compatible_partner(sid1, session1, rest, matched) do
        {sid2, _session2, remaining} ->
          pair_users(sid1, sid2)
          match_from_pool(remaining, MapSet.put(MapSet.put(matched, sid1), sid2))

        nil ->
          match_from_pool(rest, matched)
      end
    end
  end

  defp find_compatible_partner(_sid1, _session1, [], _matched), do: nil

  defp find_compatible_partner(sid1, session1, [{sid2, session2} | rest], matched) do
    if MapSet.member?(matched, sid2) do
      find_compatible_partner(sid1, session1, rest, matched)
    else
      # Check if they just recently skipped each other
      if session1.last_partner_id == sid2 or session2.last_partner_id == sid1 do
        find_compatible_partner(sid1, session1, rest, matched)
      else
        if compatible?(session1.preferences, session2.preferences) do
          {sid2, session2, rest}
        else
          find_compatible_partner(sid1, session1, rest, matched)
        end
      end
    end
  end

  defp pair_users(session_id1, session_id2) do
    # Remove both users from the queue FIRST to prevent double-matching
    OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id1])
    OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id2])

    with {:ok, session1} <- OmeglePhoenix.SessionManager.get_session(session_id1),
         {:ok, session2} <- OmeglePhoenix.SessionManager.get_session(session_id2) do
      if session1.ban_status or session2.ban_status do
        {:error, :user_banned}
      else
        common_interests = get_common_interests(session1.preferences, session2.preferences)

        OmeglePhoenix.SessionManager.update_session(session_id1, %{
          status: :matched,
          partner_id: session_id2,
          last_partner_id: session_id2
        })

        OmeglePhoenix.SessionManager.update_session(session_id2, %{
          status: :matched,
          partner_id: session_id1,
          last_partner_id: session_id1
        })

        OmeglePhoenix.Redis.command(["SETEX", "match:#{session_id1}", "3600", session_id2])
        OmeglePhoenix.Redis.command(["SETEX", "match:#{session_id2}", "3600", session_id1])
        OmeglePhoenix.Redis.command(["SETEX", "recent_match:#{session_id1}", "900", session_id2])
        OmeglePhoenix.Redis.command(["SETEX", "recent_match:#{session_id2}", "900", session_id1])

        notify_match(session_id1, session_id2, common_interests)
        notify_match(session_id2, session_id1, common_interests)

        :ok
      end
    else
      {:error, :not_found} ->
        Logger.warning("Matchmaker: session disappeared during pairing (#{session_id1} or #{session_id2})")
        :ok
    end
  end

  defp compatible?(preferences1, preferences2) do
    mode1 = Map.get(preferences1, "mode")
    mode2 = Map.get(preferences2, "mode")

    if mode1 != mode2 do
      false
    else
      interests1 = Map.get(preferences1, "interests", "") |> String.trim()
      interests2 = Map.get(preferences2, "interests", "") |> String.trim()

      if interests1 == "" and interests2 == "" do
        true
      else
        if interests1 != "" and interests2 != "" do
          tags1 = parse_interests(interests1)
          tags2 = parse_interests(interests2)
          not MapSet.disjoint?(tags1, tags2)
        else
          false # Asymmetric interests (one has, one doesn't) -> incompatible
        end
      end
    end
  end

  defp parse_interests(str) do
    # Defense-in-depth: enforce limits on malicious large payloads bypassing the frontend
    truncated_str = String.slice(str, 0, 500)

    truncated_str
    |> String.downcase()
    |> String.split([",", ";"], trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.take(10) # Maximum 10 interests allowed
    |> MapSet.new()
  end

  defp get_common_interests(p1, p2) do
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

  defp notify_match(session_id, partner_session_id, common_interests) do
    case OmeglePhoenix.Router.find_process(session_id) do
      {:ok, pid} ->
        send(pid, {:match, partner_session_id, common_interests})

      {:error, :not_found} ->
        :ok
    end
  end
end
