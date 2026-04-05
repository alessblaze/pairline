defmodule OmeglePhoenix.SessionLock do
  @moduledoc """
  Redis-backed lock helper for serializing operations on one or more session IDs.
  """

  @lock_ttl_ms 5_000
  @renew_script """
  if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('PEXPIRE', KEYS[1], ARGV[2])
  end
  return 0
  """
  @unlock_script """
  if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
  end
  return 0
  """

  def with_lock(nil, fun), do: fun.()

  def with_lock(session_id, fun) when is_binary(session_id) do
    with_locks([session_id], fun)
  end

  def with_locks(session_ids, fun) when is_list(session_ids) do
    ordered_ids =
      session_ids
      |> Enum.reject(&is_nil/1)
      |> Enum.uniq()
      |> Enum.sort()

    lock_token = "#{Node.self()}:#{System.unique_integer([:positive, :monotonic])}"

    case acquire_all(ordered_ids, lock_token, []) do
      :ok ->
        started_at = System.monotonic_time()
        renewer = start_lock_renewer(ordered_ids, lock_token)

        try do
          fun.()
        after
          stop_renewer(renewer)
          release_all(Enum.reverse(ordered_ids), lock_token)

          :telemetry.execute(
            [:omegle_phoenix, :session_lock, :acquired],
            %{duration: System.monotonic_time() - started_at},
            %{session_count: length(ordered_ids)}
          )
        end

      {:error, :locked} ->
        :telemetry.execute(
          [:omegle_phoenix, :session_lock, :contended],
          %{count: 1},
          %{session_count: length(ordered_ids)}
        )

        {:error, :locked}
    end
  end

  defp acquire_all([], _token, _acquired), do: :ok

  defp acquire_all([session_id | rest], token, acquired) do
    case OmeglePhoenix.Redis.command([
           "SET",
           lock_key(session_id),
           token,
           "PX",
           Integer.to_string(@lock_ttl_ms),
           "NX"
         ]) do
      {:ok, "OK"} ->
        acquire_all(rest, token, [session_id | acquired])

      _ ->
        release_all(acquired, token)
        {:error, :locked}
    end
  end

  defp release_all(session_ids, token) do
    Enum.each(session_ids, &release_lock(&1, token))
  end

  defp start_lock_renewer(session_ids, token) do
    parent = self()

    spawn(fn ->
      parent_ref = Process.monitor(parent)
      renew_loop(session_ids, token, parent_ref)
    end)
  end

  defp renew_loop(session_ids, token, parent_ref) do
    receive do
      :stop ->
        :ok

      {:DOWN, ^parent_ref, :process, _pid, _reason} ->
        :ok
    after
      max(div(@lock_ttl_ms, 2), 250) ->
        Enum.each(session_ids, &renew_lock(&1, token))
        renew_loop(session_ids, token, parent_ref)
    end
  end

  defp stop_renewer(pid) when is_pid(pid) do
    send(pid, :stop)
    :ok
  end

  defp renew_lock(session_id, token) do
    _ =
      OmeglePhoenix.Redis.command([
        "EVAL",
        @renew_script,
        "1",
        lock_key(session_id),
        token,
        Integer.to_string(@lock_ttl_ms)
      ])

    :ok
  end

  defp release_lock(session_id, token) do
    _ =
      OmeglePhoenix.Redis.command([
        "EVAL",
        @unlock_script,
        "1",
        lock_key(session_id),
        token
      ])

    :ok
  end

  defp lock_key(session_id) do
    case OmeglePhoenix.SessionManager.get_session_route(session_id) do
      {:ok, route} -> OmeglePhoenix.RedisKeys.session_lock_key(session_id, route)
      _ -> "session:lock:" <> session_id
    end
  end
end
