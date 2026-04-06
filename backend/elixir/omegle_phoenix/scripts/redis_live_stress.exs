run_id = "stress:#{System.system_time(:millisecond)}:#{System.unique_integer([:positive])}"

session_count =
  System.get_env("STRESS_SESSION_COUNT", "100")
  |> String.to_integer()
  |> max(1)

concurrency =
  System.get_env("STRESS_CONCURRENCY", Integer.to_string(System.schedulers_online() * 2))
  |> String.to_integer()
  |> max(1)

pair_count =
  System.get_env("STRESS_PAIR_COUNT", "20")
  |> String.to_integer()
  |> max(0)
  |> min(div(session_count, 2))

leave_count =
  System.get_env("STRESS_LEAVE_COUNT", Integer.to_string(div(session_count, 3)))
  |> String.to_integer()
  |> max(0)
  |> min(session_count)

disconnect_count =
  System.get_env("STRESS_DISCONNECT_COUNT", Integer.to_string(div(pair_count, 2)))
  |> String.to_integer()
  |> max(0)
  |> min(pair_count)

IO.puts("Starting live Redis stress run")
IO.puts("  run_id=#{run_id}")
IO.puts("  session_count=#{session_count}")
IO.puts("  concurrency=#{concurrency}")
IO.puts("  pair_count=#{pair_count}")
IO.puts("  leave_count=#{leave_count}")
IO.puts("  disconnect_count=#{disconnect_count}")

{:ok, _} = Application.ensure_all_started(:omegle_phoenix)

sessions =
  Enum.map(1..session_count, fn index ->
    id = UUID.uuid4()
    ip = "198.51.100.#{rem(index, 200) + 1}"

    preferences = %{
      "mode" => "text",
      "interests" => Enum.join(["load", "redis", "group#{rem(index, 10)}"], ",")
    }

    %{id: id, ip: ip, preferences: preferences}
  end)

cleanup_sessions = fn ->
  sessions
  |> Task.async_stream(
    fn %{id: id, ip: ip} ->
      _ = OmeglePhoenix.Matchmaker.leave_queue(id)
      _ = OmeglePhoenix.SessionManager.delete_session(id)
      _ = OmeglePhoenix.Redis.command(["DEL", "ban:ip:#{ip}"])
      :ok
    end,
    max_concurrency: concurrency,
    ordered: false,
    timeout: 30_000,
    on_timeout: :kill_task
  )
  |> Stream.run()
end

try do
  {create_us, create_results} =
    :timer.tc(fn ->
      sessions
      |> Task.async_stream(
        fn %{id: id, ip: ip, preferences: preferences} ->
          case OmeglePhoenix.SessionManager.create_session(id, ip, preferences) do
            {:ok, _session} -> :ok
            other -> {:error, {:create_session, id, other}}
          end
        end,
        max_concurrency: concurrency,
        ordered: false,
        timeout: 30_000,
        on_timeout: :kill_task
      )
      |> Enum.to_list()
    end)

  create_errors =
    for {:ok, {:error, error}} <- create_results, do: error

  if create_errors != [], do: raise("Create session failures: #{inspect(create_errors)}")

  {join_us, join_results} =
    :timer.tc(fn ->
      sessions
      |> Task.async_stream(
        fn %{id: id, preferences: preferences} ->
          case OmeglePhoenix.Matchmaker.join_queue(id, preferences) do
            :ok -> :ok
            other -> {:error, {:join_queue, id, other}}
          end
        end,
        max_concurrency: concurrency,
        ordered: false,
        timeout: 30_000,
        on_timeout: :kill_task
      )
      |> Enum.to_list()
    end)

  join_errors =
    for {:ok, {:error, error}} <- join_results, do: error

  if join_errors != [], do: raise("Join queue failures: #{inspect(join_errors)}")

  paired_sessions =
    sessions
    |> Enum.take(pair_count * 2)
    |> Enum.chunk_every(2, 2, :discard)

  {pair_us, pair_results} =
    :timer.tc(fn ->
      paired_sessions
      |> Task.async_stream(
        fn [%{id: id_1}, %{id: id_2}] ->
          with {:ok, session_1} <- OmeglePhoenix.SessionManager.get_session(id_1),
               {:ok, session_2} <- OmeglePhoenix.SessionManager.get_session(id_2),
               {:ok, _updated_1, _updated_2, _common} <-
                 OmeglePhoenix.SessionManager.pair_sessions(session_1, session_2) do
            :ok
          else
            other -> {:error, {:pair_sessions, id_1, id_2, other}}
          end
        end,
        max_concurrency: concurrency,
        ordered: false,
        timeout: 30_000,
        on_timeout: :kill_task
      )
      |> Enum.to_list()
    end)

  pair_errors =
    for {:ok, {:error, error}} <- pair_results, do: error

  if pair_errors != [], do: raise("Pair session failures: #{inspect(pair_errors)}")

  disconnected_ids =
    paired_sessions
    |> Enum.take(disconnect_count)
    |> Enum.map(fn [%{id: id}, _other] -> id end)

  {disconnect_us, disconnect_results} =
    :timer.tc(fn ->
      disconnected_ids
      |> Task.async_stream(
        fn id ->
          case OmeglePhoenix.SessionManager.emergency_disconnect(id) do
            {:ok, _} -> :ok
            other -> {:error, {:disconnect, id, other}}
          end
        end,
        max_concurrency: concurrency,
        ordered: false,
        timeout: 30_000,
        on_timeout: :kill_task
      )
      |> Enum.to_list()
    end)

  disconnect_errors =
    for {:ok, {:error, error}} <- disconnect_results, do: error

  if disconnect_errors != [], do: raise("Disconnect failures: #{inspect(disconnect_errors)}")

  left_sessions = sessions |> Enum.take(leave_count)

  {leave_us, leave_results} =
    :timer.tc(fn ->
      left_sessions
      |> Task.async_stream(
        fn %{id: id} ->
          case OmeglePhoenix.Matchmaker.leave_queue(id) do
            :ok -> :ok
            other -> {:error, {:leave_queue, id, other}}
          end
        end,
        max_concurrency: concurrency,
        ordered: false,
        timeout: 30_000,
        on_timeout: :kill_task
      )
      |> Enum.to_list()
    end)

  leave_errors =
    for {:ok, {:error, error}} <- leave_results, do: error

  if leave_errors != [], do: raise("Leave queue failures: #{inspect(leave_errors)}")

  active_before_cleanup = OmeglePhoenix.SessionManager.count_active_sessions()

  {cleanup_us, cleanup_results} =
    :timer.tc(fn ->
      sessions
      |> Task.async_stream(
        fn %{id: id} ->
          _ = OmeglePhoenix.Matchmaker.leave_queue(id)

          case OmeglePhoenix.SessionManager.delete_session(id) do
            :ok -> :ok
            {:error, :not_found} -> :ok
            other -> {:error, {:delete_session, id, other}}
          end
        end,
        max_concurrency: concurrency,
        ordered: false,
        timeout: 30_000,
        on_timeout: :kill_task
      )
      |> Enum.to_list()
    end)

  cleanup_errors =
    for {:ok, {:error, error}} <- cleanup_results, do: error

  if cleanup_errors != [], do: raise("Cleanup failures: #{inspect(cleanup_errors)}")

  final_active =
    Enum.reduce_while(1..50, nil, fn _, _acc ->
      count = OmeglePhoenix.SessionManager.count_active_sessions()

      if count == 0 do
        {:halt, 0}
      else
        Process.sleep(100)
        {:cont, count}
      end
    end)

  if final_active != 0 do
    raise("Stress cleanup incomplete, active_sessions=#{inspect(final_active)}")
  end

  IO.puts("Stress run completed")
  IO.puts("  create_ms=#{div(create_us, 1000)}")
  IO.puts("  join_ms=#{div(join_us, 1000)}")
  IO.puts("  pair_ms=#{div(pair_us, 1000)}")
  IO.puts("  disconnect_ms=#{div(disconnect_us, 1000)}")
  IO.puts("  leave_ms=#{div(leave_us, 1000)}")
  IO.puts("  cleanup_ms=#{div(cleanup_us, 1000)}")
  IO.puts("  active_before_cleanup=#{active_before_cleanup}")
  IO.puts("  active_after_cleanup=0")
after
  cleanup_sessions.()
end
