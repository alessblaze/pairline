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
  def handle_call({:join_queue, session_id, preferences}, _from, state) do
    timestamp = System.system_time(:millisecond)
    preferences_json = Jason.encode!(preferences)

    OmeglePhoenix.Redis.command([
      "ZADD",
      @queue_key,
      to_string(timestamp),
      session_id
    ])

    OmeglePhoenix.Redis.command([
      "SETEX",
      "#{session_id}:preferences",
      "600",
      preferences_json
    ])

    {:reply, :ok, state}
  end

  def handle_call({:leave_queue, session_id}, _from, state) do
    OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id])
    OmeglePhoenix.Redis.command(["DEL", "#{session_id}:preferences"])

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

    case OmeglePhoenix.Redis.command(["ZPOPMIN", @queue_key, "2"]) do
      {:ok, []} ->
        :ok

      {:ok, [session_id1, score1]} ->
        OmeglePhoenix.Redis.command(["ZADD", @queue_key, score1, session_id1])
        :ok

      {:ok, [session_id1, _score1, session_id2, _score2]} ->
        pair_users(session_id1, session_id2)

      _ ->
        :ok
    end
  end

  defp pair_users(session_id1, session_id2) do
    with {:ok, session1} <- OmeglePhoenix.SessionManager.get_session(session_id1),
         {:ok, session2} <- OmeglePhoenix.SessionManager.get_session(session_id2) do
      if session1.ban_status or session2.ban_status do
        {:error, :user_banned}
      else
        preferences1 = session1.preferences
        preferences2 = session2.preferences

        if compatible?(preferences1, preferences2) do
          OmeglePhoenix.SessionManager.update_session(session_id1, %{
            status: :matched,
            partner_id: session_id2
          })

          OmeglePhoenix.SessionManager.update_session(session_id2, %{
            status: :matched,
            partner_id: session_id1
          })

          OmeglePhoenix.Redis.command(["SETEX", "match:#{session_id1}", "3600", session_id2])
          OmeglePhoenix.Redis.command(["SETEX", "match:#{session_id2}", "3600", session_id1])
          OmeglePhoenix.Redis.command(["SETEX", "recent_match:#{session_id1}", "900", session_id2])
          OmeglePhoenix.Redis.command(["SETEX", "recent_match:#{session_id2}", "900", session_id1])

          notify_match(session_id1, session_id2)
          notify_match(session_id2, session_id1)

          :ok
        else
          now_bin = to_string(System.system_time(:millisecond))
          OmeglePhoenix.Redis.command(["ZADD", @queue_key, now_bin, session_id1])
          OmeglePhoenix.Redis.command(["ZADD", @queue_key, now_bin, session_id2])
          :ok
        end
      end
    else
      {:error, :not_found} ->
        Logger.warning("Matchmaker: session disappeared during pairing (#{session_id1} or #{session_id2})")
        :ok
    end
  end

  defp compatible?(preferences1, preferences2) do
    Enum.all?(preferences1, fn {key, value} ->
      case Map.get(preferences2, key) do
        nil -> false
        ^value -> true
        _ -> false
      end
    end)
  end

  defp notify_match(session_id, partner_session_id) do
    case OmeglePhoenix.Router.find_process(session_id) do
      {:ok, pid} ->
        send(pid, {:match, partner_session_id})

      {:error, :not_found} ->
        :ok
    end
  end
end
