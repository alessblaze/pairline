# Pairline - Open Source Video Chat and Matchmaking
# Copyright (C) 2026 Albert Blasczykowski
# Aless Microsystems
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

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
      {:ok, acquired} ->
        started_at = System.monotonic_time()
        renewer = start_lock_renewer(acquired, lock_token)

        try do
          fun.()
        after
          stop_renewer(renewer)
          release_all(Enum.reverse(acquired), lock_token)

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

  defp acquire_all([], _token, acquired), do: {:ok, acquired}

  defp acquire_all([session_id | rest], token, acquired) do
    case lock_key(session_id) do
      {:ok, key} ->
        case OmeglePhoenix.Redis.command([
               "SET",
               key,
               token,
               "PX",
               Integer.to_string(@lock_ttl_ms),
               "NX"
             ]) do
          {:ok, "OK"} ->
            acquire_all(rest, token, acquired ++ [{session_id, key}])

          _ ->
            release_all(acquired, token)
            {:error, :locked}
        end

      {:skip, :no_route} ->
        acquire_all(rest, token, acquired)

      {:error, _reason} ->
        release_all(acquired, token)
        {:error, :locked}
    end
  end

  defp release_all(acquired, token) do
    Enum.each(acquired, fn {_session_id, key} -> release_lock_by_key(key, token) end)
  end

  defp start_lock_renewer(acquired, token) do
    parent = self()

    spawn(fn ->
      parent_ref = Process.monitor(parent)
      renew_loop(acquired, token, parent_ref)
    end)
  end

  defp renew_loop(acquired, token, parent_ref) do
    receive do
      :stop ->
        :ok

      {:DOWN, ^parent_ref, :process, _pid, _reason} ->
        :ok
    after
      max(div(@lock_ttl_ms, 2), 250) ->
        Enum.each(acquired, fn {_session_id, key} -> renew_lock_by_key(key, token) end)
        renew_loop(acquired, token, parent_ref)
    end
  end

  defp stop_renewer(pid) when is_pid(pid) do
    send(pid, :stop)
    :ok
  end

  defp renew_lock_by_key(key, token) do
    _ =
      OmeglePhoenix.Redis.command([
        "EVAL",
        @renew_script,
        "1",
        key,
        token,
        Integer.to_string(@lock_ttl_ms)
      ])

    :ok
  end

  defp release_lock_by_key(key, token) do
    _ =
      OmeglePhoenix.Redis.command([
        "EVAL",
        @unlock_script,
        "1",
        key,
        token
      ])

    :ok
  end

  defp lock_key(session_id) do
    case OmeglePhoenix.SessionManager.get_session_route(session_id) do
      {:ok, route} -> {:ok, OmeglePhoenix.RedisKeys.session_lock_key(session_id, route)}
      {:error, :not_found} -> {:skip, :no_route}
      {:error, :invalid_locator} -> {:skip, :no_route}
      {:error, reason} -> {:error, reason}
    end
  end
end
