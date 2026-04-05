defmodule OmeglePhoenix.Reaper do
  @moduledoc """
  Periodically cleans orphaned Redis-backed coordination state.
  """

  use GenServer
  require Logger

  @leader_key "reaper:leader"
  @leader_ttl_ms 5_000
  @active_sessions_key "sessions:active"
  @renew_lock_script """
  if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('PEXPIRE', KEYS[1], ARGV[2])
  end
  return 0
  """
  @release_lock_script """
  if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
  end
  return 0
  """

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    state = %{
      interval_ms: OmeglePhoenix.Config.get_reaper_interval_ms(),
      batch_size: OmeglePhoenix.Config.get_reaper_batch_size(),
      session_cursor: "0",
      queue_cursors: Map.new(OmeglePhoenix.Matchmaker.queue_keys(), &{&1, "0"})
    }

    send(self(), :reap)
    {:ok, state}
  end

  @impl true
  def handle_info(:reap, state) do
    state =
      with_reaper_leader(state, fn state ->
        state
        |> reap_orphaned_sessions()
        |> reap_stale_queue_entries()
      end)

    Process.send_after(self(), :reap, state.interval_ms)
    {:noreply, state}
  end

  def handle_info(_message, state) do
    {:noreply, state}
  end

  defp with_reaper_leader(state, fun) do
    leader_token = leader_token()

    if leader?(leader_token) do
      renewer = start_leader_renewer(leader_token)

      try do
        fun.(state)
      rescue
        e ->
          Logger.error("Reaper error: #{inspect(e)}")
          state
      after
        stop_renewer(renewer)
        release_leader(leader_token)
      end
    else
      state
    end
  end

  defp start_leader_renewer(leader_token) do
    parent = self()

    spawn(fn ->
      parent_ref = Process.monitor(parent)
      leader_renew_loop(leader_token, parent_ref)
    end)
  end

  defp leader_renew_loop(leader_token, parent_ref) do
    receive do
      :stop -> :ok
      {:DOWN, ^parent_ref, :process, _pid, _reason} -> :ok
    after
      max(div(@leader_ttl_ms, 2), 250) ->
        _ =
          OmeglePhoenix.Redis.command([
            "EVAL",
            @renew_lock_script,
            "1",
            @leader_key,
            leader_token,
            Integer.to_string(@leader_ttl_ms)
          ])

        leader_renew_loop(leader_token, parent_ref)
    end
  end

  defp stop_renewer(nil), do: :ok
  defp stop_renewer(pid) when is_pid(pid), do: send(pid, :stop)

  defp reap_orphaned_sessions(state) do
    case OmeglePhoenix.Redis.command([
           "SSCAN",
           @active_sessions_key,
           state.session_cursor,
           "COUNT",
           Integer.to_string(state.batch_size)
         ]) do
      {:ok, [next_cursor, session_ids]} when is_list(session_ids) ->
        {:ok, sessions_by_id} = OmeglePhoenix.SessionManager.get_sessions(session_ids)

        Enum.each(session_ids, fn session_id ->
          if not Map.has_key?(sessions_by_id, session_id) do
            _ = OmeglePhoenix.SessionManager.cleanup_orphaned_session(session_id)

            :telemetry.execute(
              [:omegle_phoenix, :reaper, :orphaned_session],
              %{count: 1},
              %{session_id: session_id}
            )
          end
        end)

        %{state | session_cursor: next_cursor}

      _ ->
        %{state | session_cursor: "0"}
    end
  end

  defp reap_stale_queue_entries(state) do
    active_queue_keys = OmeglePhoenix.Matchmaker.queue_keys()

    new_queue_cursors =
      Enum.reduce(active_queue_keys, %{}, fn queue_key, acc ->
        cursor = Map.get(state.queue_cursors, queue_key, "0")

        next_cursor =
          case OmeglePhoenix.Redis.command([
                 "ZSCAN",
                 queue_key,
                 cursor,
                 "COUNT",
                 Integer.to_string(state.batch_size)
               ]) do
            {:ok, [updated_cursor, raw_entries]} when is_list(raw_entries) ->
              session_ids =
                raw_entries
                |> Enum.chunk_every(2)
                |> Enum.map(fn [session_id, _score] -> session_id end)

              {:ok, sessions_by_id} = OmeglePhoenix.SessionManager.get_sessions(session_ids)

              Enum.each(session_ids, fn session_id ->
                case Map.get(sessions_by_id, session_id) do
                  %{status: :waiting} ->
                    :ok

                  _ ->
                    _ = OmeglePhoenix.Matchmaker.leave_queue(session_id)

                    :telemetry.execute(
                      [:omegle_phoenix, :reaper, :queue_entry_removed],
                      %{count: 1},
                      %{session_id: session_id}
                    )
                end
              end)

              updated_cursor

            _ ->
              "0"
          end

        Map.put(acc, queue_key, next_cursor)
      end)

    %{state | queue_cursors: new_queue_cursors}
  end

  defp leader?(leader_token) do
    case OmeglePhoenix.Redis.command([
           "SET",
           @leader_key,
           leader_token,
           "PX",
           Integer.to_string(@leader_ttl_ms),
           "NX"
         ]) do
      {:ok, "OK"} ->
        true

      _ ->
        false
    end
  end

  defp leader_token do
    "#{Node.self()}:#{System.unique_integer([:positive, :monotonic])}"
  end

  defp release_leader(leader_token) do
    _ =
      OmeglePhoenix.Redis.command([
        "EVAL",
        @release_lock_script,
        "1",
        @leader_key,
        leader_token
      ])

    :ok
  end
end
