defmodule OmeglePhoenix.Reaper do
  @moduledoc """
  Periodically cleans orphaned Redis-backed coordination state.
  """

  use GenServer

  @leader_key "reaper:leader"
  @leader_ttl_ms 5_000
  @active_sessions_key "sessions:active"
  @queue_key "matchmaking_queue"

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    state = %{interval_ms: OmeglePhoenix.Config.get_reaper_interval_ms()}
    send(self(), :reap)
    {:ok, state}
  end

  @impl true
  def handle_info(:reap, state) do
    if leader?() do
      reap_orphaned_sessions()
      reap_stale_queue_entries()
    end

    Process.send_after(self(), :reap, state.interval_ms)
    {:noreply, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  defp reap_orphaned_sessions do
    case OmeglePhoenix.Redis.command(["SMEMBERS", @active_sessions_key]) do
      {:ok, session_ids} when is_list(session_ids) ->
        Enum.each(session_ids, fn session_id ->
          case OmeglePhoenix.SessionManager.get_session(session_id) do
            {:ok, _session} ->
              :ok

            _ ->
              _ = OmeglePhoenix.SessionManager.cleanup_orphaned_session(session_id)

              :telemetry.execute(
                [:omegle_phoenix, :reaper, :orphaned_session],
                %{count: 1},
                %{session_id: session_id}
              )
          end
        end)

      _ ->
        :ok
    end
  end

  defp reap_stale_queue_entries do
    case OmeglePhoenix.Redis.command(["ZRANGE", @queue_key, "0", "-1"]) do
      {:ok, session_ids} when is_list(session_ids) ->
        Enum.each(session_ids, fn session_id ->
          case OmeglePhoenix.SessionManager.get_session(session_id) do
            {:ok, %{status: :waiting}} ->
              :ok

            _ ->
              OmeglePhoenix.Redis.command(["ZREM", @queue_key, session_id])

              :telemetry.execute(
                [:omegle_phoenix, :reaper, :queue_entry_removed],
                %{count: 1},
                %{session_id: session_id}
              )
          end
        end)

      _ ->
        :ok
    end
  end

  defp leader? do
    node_name = Atom.to_string(Node.self())

    case OmeglePhoenix.Redis.command([
           "SET",
           @leader_key,
           node_name,
           "PX",
           Integer.to_string(@leader_ttl_ms),
           "NX"
         ]) do
      {:ok, "OK"} ->
        true

      _ ->
        case OmeglePhoenix.Redis.command(["GET", @leader_key]) do
          {:ok, ^node_name} ->
            OmeglePhoenix.Redis.command(["PEXPIRE", @leader_key, Integer.to_string(@leader_ttl_ms)])
            true

          _ ->
            false
        end
    end
  end
end
