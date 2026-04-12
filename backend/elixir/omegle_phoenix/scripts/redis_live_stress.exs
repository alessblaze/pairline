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

defmodule RedisLiveStressHelpers do
  def wait_for_matched_pairs!(sessions, pair_count, timeout_ms \\ 10_000)

  def wait_for_matched_pairs!(_sessions, 0, _timeout_ms), do: []

  def wait_for_matched_pairs!(sessions, pair_count, timeout_ms) do
    attempts = max(div(timeout_ms, 50), 1)

    result =
      Enum.reduce_while(1..attempts, [], fn _, _acc ->
        pairs = matched_pairs(sessions)

        if length(pairs) >= pair_count do
          {:halt, Enum.take(pairs, pair_count)}
        else
          Process.sleep(50)
          {:cont, pairs}
        end
      end)

    if length(result) >= pair_count do
      Enum.take(result, pair_count)
    else
      raise("Timed out waiting for #{pair_count} matched pairs, found #{length(result)}")
    end
  end

  def wait_for_matchmaker_idle!(timeout_ms \\ 10_000) do
    attempts = max(div(timeout_ms, 50), 1)

    result =
      Enum.reduce_while(1..attempts, nil, fn _, _acc ->
        state = :sys.get_state(OmeglePhoenix.Matchmaker)

        if is_nil(state.local_match_batch_ref) and
             MapSet.size(state.pending_local_match_keys) == 0 do
          {:halt, :ok}
        else
          Process.sleep(50)
          {:cont, nil}
        end
      end)

    if result == :ok do
      :ok
    else
      raise("Timed out waiting for local match batch to become idle")
    end
  end

  def collect_async_errors(results) do
    Enum.flat_map(results, fn
      {:ok, :ok} -> []
      {:ok, {:error, error}} -> [error]
      {:exit, reason} -> [{:task_exit, reason}]
      other -> [{:unexpected_task_result, other}]
    end)
  end

  def active_session_ids! do
    case OmeglePhoenix.Redis.command(["SMEMBERS", OmeglePhoenix.RedisKeys.active_sessions_key()]) do
      {:ok, session_ids} when is_list(session_ids) -> session_ids
      {:error, reason} -> raise("Failed to list active sessions: #{inspect(reason)}")
      other -> raise("Unexpected active sessions response: #{inspect(other)}")
    end
  end

  def cleanup_session(session_id) do
    case OmeglePhoenix.SessionManager.delete_session(session_id) do
      :ok ->
        cleanup_orphan(session_id)

      {:error, :not_found} ->
        cleanup_orphan(session_id)

      {:error, reason} ->
        {:error, {:delete_session, session_id, reason}}
    end
  end

  defp cleanup_orphan(session_id) do
    case OmeglePhoenix.SessionManager.cleanup_orphaned_session(session_id) do
      :ok -> :ok
      {:error, :not_found} -> :ok
      {:error, reason} -> {:error, {:cleanup_orphaned_session, session_id, reason}}
    end
  end

  defp matched_pairs(sessions) do
    snapshots =
      Enum.reduce(sessions, %{}, fn %{id: id}, acc ->
        case OmeglePhoenix.SessionManager.get_session(id) do
          {:ok, session} -> Map.put(acc, id, session)
          _ -> acc
        end
      end)

    snapshots
    |> Enum.reduce([], fn {id, session}, acc ->
      partner_id = session.partner_id

      if session.status == :matched and is_binary(partner_id) and id < partner_id do
        case Map.get(snapshots, partner_id) do
          %{status: :matched, partner_id: ^id} -> [[id, partner_id] | acc]
          _ -> acc
        end
      else
        acc
      end
    end)
    |> Enum.sort()
  end
end

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
    id = Uniq.UUID.uuid4()
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
      _ = RedisLiveStressHelpers.cleanup_session(id)
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
    RedisLiveStressHelpers.collect_async_errors(create_results)

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
    RedisLiveStressHelpers.collect_async_errors(join_results)

  if join_errors != [], do: raise("Join queue failures: #{inspect(join_errors)}")

  {pair_us, matched_pairs} =
    :timer.tc(fn ->
      RedisLiveStressHelpers.wait_for_matched_pairs!(sessions, pair_count)
    end)

  RedisLiveStressHelpers.wait_for_matchmaker_idle!()

  disconnected_ids =
    matched_pairs
    |> Enum.take(disconnect_count)
    |> Enum.map(fn [id, _partner_id] -> id end)

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
    RedisLiveStressHelpers.collect_async_errors(disconnect_results)

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
    RedisLiveStressHelpers.collect_async_errors(leave_results)

  if leave_errors != [], do: raise("Leave queue failures: #{inspect(leave_errors)}")

  RedisLiveStressHelpers.wait_for_matchmaker_idle!()

  active_before_cleanup =
    case OmeglePhoenix.SessionManager.count_active_sessions() do
      {:ok, count} ->
        count

      {:error, reason} ->
        raise("Failed to count active sessions before cleanup: #{inspect(reason)}")
    end

  {cleanup_us, cleanup_results} =
    :timer.tc(fn ->
      sessions
      |> Task.async_stream(
        fn %{id: id} ->
          _ = OmeglePhoenix.Matchmaker.leave_queue(id)

          case RedisLiveStressHelpers.cleanup_session(id) do
            :ok -> :ok
            other -> {:error, {:cleanup_session, id, other}}
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
    RedisLiveStressHelpers.collect_async_errors(cleanup_results)

  if cleanup_errors != [], do: raise("Cleanup failures: #{inspect(cleanup_errors)}")

  final_active =
    Enum.reduce_while(1..50, nil, fn _, _acc ->
      case OmeglePhoenix.SessionManager.count_active_sessions() do
        {:ok, 0} ->
          {:halt, 0}

        {:ok, count} ->
          Process.sleep(100)
          {:cont, count}

        {:error, reason} ->
          raise("Failed to count active sessions during cleanup poll: #{inspect(reason)}")
      end
    end)

  if final_active != 0 do
    leftover_ids = RedisLiveStressHelpers.active_session_ids!()

    Enum.each(leftover_ids, fn session_id ->
      _ = RedisLiveStressHelpers.cleanup_session(session_id)
    end)

    retried_final_active =
      Enum.reduce_while(1..20, final_active, fn _, _acc ->
        case OmeglePhoenix.SessionManager.count_active_sessions() do
          {:ok, 0} ->
            {:halt, 0}

          {:ok, count} ->
            Process.sleep(100)
            {:cont, count}

          {:error, reason} ->
            raise("Failed to count active sessions during retry cleanup poll: #{inspect(reason)}")
        end
      end)

    if retried_final_active != 0 do
      final_leftover_ids = RedisLiveStressHelpers.active_session_ids!()

      raise(
        "Stress cleanup incomplete, active_sessions=#{inspect(retried_final_active)}, leftover_session_ids=#{inspect(final_leftover_ids)}"
      )
    end
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
